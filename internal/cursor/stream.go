package cursor

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// errStreamDone signals that the SSE stream ended cleanly with a "done"
// event. It is an internal sentinel, never returned to callers of StreamRun.
var errStreamDone = errors.New("Cursor stream done")

const maxSSELineBytes = 1 << 20 // 1 MiB

// emitError wraps an error returned by the caller-supplied emit callback so
// StreamRun can recognize it and return immediately without reconnecting.
type emitError struct{ err error }

func (e *emitError) Error() string { return e.err.Error() }
func (e *emitError) Unwrap() error { return e.err }

// transportError marks a connection-level failure — a dropped, reset, or
// truncated stream — which StreamRun recovers from by reconnecting with
// Last-Event-ID. Protocol failures (oversized lines, undecodable payloads,
// API errors, emit failures) are never wrapped in it and stay immediate.
type transportError struct{ err error }

func (e *transportError) Error() string { return "cursor: stream transport: " + e.err.Error() }
func (e *transportError) Unwrap() error { return e.err }

// sanitizeStreamError holds an in-band SSE error to the same contract as a
// REST failure. Emit failures belong to the caller and pass through
// untouched, so the caller's own error identity survives.
func (c *Client) sanitizeStreamError(err error) error {
	var emErr *emitError
	if errors.As(err, &emErr) {
		return err
	}
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		safe := *apiErr
		c.sanitizeAPIError(&safe)
		return &safe
	}
	return err
}

func isRetryableStreamError(err error) bool {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var transport *transportError
	return errors.As(err, &transport)
}

// parseSSE reads Server-Sent Events from r until EOF, a "done" event, or an
// error. It returns the most recent non-empty event id seen (for use as a
// Last-Event-ID header on reconnect) and, if a "result" event was decoded,
// the terminal Run it describes.
func parseSSE(r io.Reader, emit func(StreamEvent) error) (lastID string, terminal *Run, err error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), maxSSELineBytes)

	var (
		recID   string
		recType string
		recData []string
	)

	flush := func() error {
		if recID == "" && recType == "" && len(recData) == 0 {
			return nil
		}
		if recID != "" {
			lastID = recID
		}
		eventName := recType
		if eventName == "" {
			eventName = "message"
		}
		raw := json.RawMessage(strings.Join(recData, "\n"))
		out := StreamEvent{ID: recID, Type: eventName, Raw: raw}

		var decodeErr error
		switch eventName {
		case "assistant", "thinking":
			var payload struct {
				Text string `json:"text"`
			}
			decodeErr = json.Unmarshal(raw, &payload)
			out.Text = payload.Text
		case "status":
			var payload struct {
				RunID  string `json:"runId"`
				Status string `json:"status"`
			}
			decodeErr = json.Unmarshal(raw, &payload)
			out.RunID = payload.RunID
			out.Status = payload.Status
		case "tool_call":
			// Cursor Cloud Agents API, "Stream A Run" — tool call payloads
			// (https://cursor.com/docs/cloud-agent/api/endpoints#stream-a-run):
			// { callId, name, status, args?, result?, truncated?: {args?, result?} }
			var payload struct {
				CallID    string          `json:"callId"`
				Name      string          `json:"name"`
				Status    string          `json:"status"`
				Args      json.RawMessage `json:"args,omitempty"`
				Result    json.RawMessage `json:"result,omitempty"`
				Truncated struct {
					Args   bool `json:"args"`
					Result bool `json:"result"`
				} `json:"truncated"`
			}
			decodeErr = json.Unmarshal(raw, &payload)
			out.CallID = payload.CallID
			out.ToolName = payload.Name
			out.Status = payload.Status
			out.ToolArgs = payload.Args
			out.ToolResult = payload.Result
			out.ArgsTruncated = payload.Truncated.Args
			out.ResultTruncated = payload.Truncated.Result
		case "result":
			var payload struct {
				RunID      string    `json:"runId"`
				Status     string    `json:"status"`
				Text       string    `json:"text"`
				DurationMS int64     `json:"durationMs"`
				Git        *GitState `json:"git,omitempty"`
			}
			decodeErr = json.Unmarshal(raw, &payload)
			if decodeErr == nil {
				terminal = &Run{
					ID:         payload.RunID,
					Status:     payload.Status,
					Result:     payload.Text,
					DurationMS: payload.DurationMS,
					Git:        payload.Git,
				}
				out.RunID = payload.RunID
				out.Status = payload.Status
				out.Text = payload.Text
			}
		case "error":
			var payload struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			}
			decodeErr = json.Unmarshal(raw, &payload)
			if decodeErr == nil {
				return &APIError{Code: payload.Code, Message: payload.Message}
			}
		case "done":
			return errStreamDone
		case "heartbeat", "interaction_update":
			return nil
		}
		if decodeErr != nil {
			return fmt.Errorf("cursor: decode %s event: %w", eventName, decodeErr)
		}
		if emitErr := emit(out); emitErr != nil {
			return &emitError{err: emitErr}
		}
		return nil
	}

	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case line == "":
			if ferr := flush(); ferr != nil {
				return lastID, terminal, ferr
			}
			recID, recType, recData = "", "", nil
		case strings.HasPrefix(line, ":"):
			// SSE comment line, typically used as a keep-alive ping.
		case strings.HasPrefix(line, "id:"):
			recID = strings.TrimPrefix(strings.TrimPrefix(line, "id:"), " ")
		case strings.HasPrefix(line, "event:"):
			recType = strings.TrimPrefix(strings.TrimPrefix(line, "event:"), " ")
		case strings.HasPrefix(line, "data:"):
			recData = append(recData, strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
		}
	}
	if serr := scanner.Err(); serr != nil {
		// An oversized line is a protocol violation that would recur on every
		// reconnect; anything else here is the connection failing under us.
		if errors.Is(serr, bufio.ErrTooLong) {
			return lastID, terminal, fmt.Errorf("cursor: sse scan: %w", serr)
		}
		return lastID, terminal, &transportError{err: serr}
	}
	if recID != "" || recType != "" || len(recData) != 0 {
		if ferr := flush(); ferr != nil {
			return lastID, terminal, ferr
		}
	}
	return lastID, terminal, nil
}

// streamOnce opens a single SSE connection for a run and parses it until the
// connection closes, a terminal result arrives, or an error occurs.
//
// done reports whether the connection ended in a way that should stop
// reconnect attempts (a "done" event was observed). terminal is non-nil only
// when a "result" event was decoded. err is nil for a clean disconnect that
// callers should retry (no terminal, not done).
func (c *Client) streamOnce(
	ctx context.Context,
	agentID, runID, lastID string,
	emit func(StreamEvent) error,
) (nextID string, terminal *Run, done bool, err error) {
	// Documented endpoint: GET /v1/agents/{id}/runs/{runId}/stream — Cursor
	// Cloud Agents API, "Stream A Run"
	// (https://cursor.com/docs/cloud-agent/api/endpoints#stream-a-run).
	path := "/v1/agents/" + url.PathEscape(agentID) + "/runs/" + url.PathEscape(runID) + "/stream"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return lastID, nil, false, err
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Accept", "text/event-stream")
	if lastID != "" {
		req.Header.Set("Last-Event-ID", lastID)
	}

	// Cursor streams rely on periodic heartbeats and can legitimately run
	// far longer than the client's ordinary metadata/lifecycle timeout;
	// lifetime control belongs to ctx instead.
	streamHTTP := *c.http
	streamHTTP.Timeout = 0

	resp, err := streamHTTP.Do(req)
	if err != nil {
		return lastID, nil, false, &transportError{err: err}
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return lastID, nil, false, c.decodeAPIError(resp)
	}

	id, terminal, err := parseSSE(resp.Body, emit)
	if id != "" {
		lastID = id
	}
	if errors.Is(err, errStreamDone) {
		return lastID, terminal, true, nil
	}
	if err != nil {
		// A result decoded on this connection is the run's real outcome; a
		// transport failure arriving after it must not discard it.
		if terminal != nil && isRetryableStreamError(err) {
			return lastID, terminal, false, nil
		}
		return lastID, terminal, false, c.sanitizeStreamError(err)
	}
	return lastID, terminal, false, nil
}

// StreamOptions configures StreamRunWithOptions. LastEventID lets a caller
// resume a stream from a token persisted before this process started,
// instead of always replaying from the beginning of the run.
//
// OnReset is invoked at most once per call, before Antares discards the
// current Last-Event-ID following a 400 invalid_last_event_id response or a
// 410 stream_expired response. It lets a caller drop its own persisted copy
// of that token so a dead resume point is never reused on a later call. An
// error returned from OnReset aborts the stream immediately.
type StreamOptions struct {
	LastEventID string
	OnReset     func() error
}

// StreamRun streams a run's events, transparently reconnecting on ordinary
// disconnects while preserving Last-Event-ID. It is a compatibility wrapper
// over StreamRunWithOptions with no caller-supplied resume token.
func (c *Client) StreamRun(
	ctx context.Context,
	agentID, runID string,
	emit func(StreamEvent) error,
) (*Run, error) {
	return c.StreamRunWithOptions(ctx, agentID, runID, StreamOptions{}, emit)
}

// StreamRunWithOptions streams a run's events, transparently reconnecting on
// ordinary disconnects while preserving Last-Event-ID. The retry budget
// applies only to consecutive disconnects that make no event-ID progress.
// It returns the run's terminal state once a "result" event is decoded, or
// once the stream ends and GetRun confirms completion.
func (c *Client) StreamRunWithOptions(
	ctx context.Context,
	agentID, runID string,
	options StreamOptions,
	emit func(StreamEvent) error,
) (*Run, error) {
	if strings.TrimSpace(agentID) == "" {
		return nil, fmt.Errorf("cursor: agentID is required")
	}
	if strings.TrimSpace(runID) == "" {
		return nil, fmt.Errorf("cursor: runID is required")
	}

	backoffs := [3]time.Duration{250 * time.Millisecond, 500 * time.Millisecond, 1 * time.Second}
	const maxNoProgress = 4

	lastID := options.LastEventID
	var resetUsed bool
	resetOnce := func() error {
		if resetUsed {
			return nil
		}
		resetUsed = true
		if options.OnReset == nil {
			return nil
		}
		return options.OnReset()
	}
	noProgress := 0
	var retryDelay time.Duration

	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if retryDelay > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(retryDelay):
			}
		}
		retryDelay = 0

		previousID := lastID
		nextID, terminal, done, err := c.streamOnce(ctx, agentID, runID, lastID, emit)
		if nextID != "" {
			lastID = nextID
		}
		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return nil, ctxErr
			}
			if IsStatus(err, http.StatusGone) {
				// Unlike a transport read error racing a decoded "result"
				// event (where the terminal value already in hand wins),
				// GetRun has not run yet here: nothing terminal exists to
				// prefer. OnReset is a persistence invariant, not a
				// best-effort notification, so a failure aborts immediately
				// instead of calling GetRun, which would let a stale or
				// unrecorded resume token be finalized or replayed
				// inconsistently.
				if rerr := resetOnce(); rerr != nil {
					return nil, rerr
				}
				return c.GetRun(ctx, agentID, runID)
			}
			var apiErr *APIError
			if errors.As(err, &apiErr) && apiErr.Status == http.StatusBadRequest &&
				apiErr.Code == "invalid_last_event_id" && !resetUsed {
				if rerr := resetOnce(); rerr != nil {
					return nil, rerr
				}
				lastID = ""
				continue
			}
			var emErr *emitError
			if errors.As(err, &emErr) {
				return nil, emErr.err
			}
			if !isRetryableStreamError(err) {
				return nil, err
			}
			// A dropped connection is an ordinary disconnect: reuse the
			// bounded reconnect accounting below instead of failing the run.
		}
		if terminal != nil {
			return terminal, nil
		}
		if done {
			// Stream ended with "done" but no "result" event; confirm the
			// final state via the metadata API instead of guessing.
			return c.GetRun(ctx, agentID, runID)
		}

		if nextID != "" && nextID != previousID {
			noProgress = 0
			retryDelay = backoffs[0]
			continue
		}

		noProgress++
		if noProgress >= maxNoProgress {
			current, err := c.GetRun(ctx, agentID, runID)
			if err != nil {
				return nil, err
			}
			if current != nil && isTerminalRunStatus(current.Status) {
				return current, nil
			}
			// A live run is not a successful stream result. Keep reconnecting
			// with capped backoff until the stream advances or ctx expires.
			noProgress = 0
			retryDelay = backoffs[len(backoffs)-1]
			continue
		}
		retryDelay = backoffs[min(noProgress-1, len(backoffs)-1)]
	}
}

// isTerminalRunStatus reports whether a run has stopped for good. Cursor
// computes a run's duration once it reaches any of these states — Cloud
// Agents API (https://cursor.com/docs/cloud-agent/api/endpoints) — so an
// EXPIRED run will never produce further stream events.
func isTerminalRunStatus(status string) bool {
	switch strings.ToUpper(strings.TrimSpace(status)) {
	case "FINISHED", "ERROR", "CANCELLED", "EXPIRED":
		return true
	default:
		return false
	}
}
