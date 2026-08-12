package cursor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"
)

func TestStreamRunReconnectsFromLastEventID(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		switch calls.Add(1) {
		case 1:
			_, _ = io.WriteString(w, "id: evt-1\nevent: assistant\ndata: {\"text\":\"hello\"}\n\n")
		default:
			if got := r.Header.Get("Last-Event-ID"); got != "evt-1" {
				t.Fatalf("Last-Event-ID = %q", got)
			}
			_, _ = io.WriteString(w,
				"id: evt-2\nevent: result\ndata: {\"runId\":\"run-one\",\"status\":\"FINISHED\",\"text\":\"done\"}\n\n"+
					"id: evt-3\nevent: done\ndata: {}\n\n")
		}
	}))
	defer srv.Close()

	client, _ := New(Options{BaseURL: srv.URL, APIKey: "synthetic-key", HTTPClient: srv.Client()})
	var events []StreamEvent
	run, err := client.StreamRun(context.Background(), "bc-agent", "run-one", func(e StreamEvent) error {
		events = append(events, e)
		return nil
	})
	if err != nil || run.Status != "FINISHED" || run.Result != "done" {
		t.Fatalf("StreamRun = %+v, %v", run, err)
	}
	if len(events) != 2 {
		t.Fatalf("events = %#v", events)
	}
}

func TestStreamRunReconnectsBeyondAttemptBudgetWhenEachDisconnectAdvancesEventID(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		attempt := calls.Add(1)
		if attempt > 1 {
			want := fmt.Sprintf("evt-%d", attempt-1)
			if got := r.Header.Get("Last-Event-ID"); got != want {
				t.Errorf("attempt %d Last-Event-ID = %q, want %q", attempt, got, want)
			}
		}
		if attempt <= 5 {
			_, _ = fmt.Fprintf(w,
				"id: evt-%d\nevent: assistant\ndata: {\"text\":\"progress\"}\n\n",
				attempt,
			)
			return
		}
		_, _ = io.WriteString(w,
			"id: evt-6\nevent: result\ndata: {\"runId\":\"run-one\",\"status\":\"FINISHED\",\"text\":\"done\"}\n\n"+
				"id: evt-7\nevent: done\ndata: {}\n\n")
	}))
	defer srv.Close()

	client, _ := New(Options{BaseURL: srv.URL, APIKey: "synthetic-key", HTTPClient: srv.Client()})
	var events []StreamEvent
	run, err := client.StreamRun(context.Background(), "bc-agent", "run-one", func(event StreamEvent) error {
		events = append(events, event)
		return nil
	})
	if err != nil || run.Status != "FINISHED" || run.Result != "done" {
		t.Fatalf("StreamRun = %+v, %v", run, err)
	}
	if calls.Load() != 6 {
		t.Fatalf("stream calls = %d, want 6", calls.Load())
	}
	if len(events) != 6 {
		t.Fatalf("events = %d, want five progress events and one result", len(events))
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// truncatedBody serves fixed SSE bytes and then fails, standing in for a
// connection dropped mid-stream.
type truncatedBody struct {
	data []byte
	err  error
	off  int
}

func (b *truncatedBody) Read(p []byte) (int, error) {
	if b.off < len(b.data) {
		n := copy(p, b.data[b.off:])
		b.off += n
		return n, nil
	}
	return 0, b.err
}

func (b *truncatedBody) Close() error { return nil }

func truncatedStreamClient(t *testing.T, respond func(attempt int32, r *http.Request) (string, error)) (*Client, *atomic.Int32) {
	t.Helper()
	var calls atomic.Int32
	client, err := New(Options{
		BaseURL: "https://api.cursor.invalid",
		APIKey:  "synthetic-key",
		HTTPClient: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			body, readErr := respond(calls.Add(1), r)
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
				Body:       &truncatedBody{data: []byte(body), err: readErr},
			}, nil
		})},
	})
	if err != nil {
		t.Fatal(err)
	}
	return client, &calls
}

func TestStreamRunReconnectsAfterConnectionResetWithLastEventID(t *testing.T) {
	reset := &net.OpError{Op: "read", Net: "tcp", Err: errors.New("connection reset by peer")}
	client, calls := truncatedStreamClient(t, func(attempt int32, r *http.Request) (string, error) {
		if attempt == 1 {
			return "id: evt-1\nevent: assistant\ndata: {\"text\":\"hello\"}\n\n", reset
		}
		if got := r.Header.Get("Last-Event-ID"); got != "evt-1" {
			t.Errorf("reconnect Last-Event-ID = %q, want evt-1", got)
		}
		return "id: evt-2\nevent: result\ndata: {\"runId\":\"run-one\",\"status\":\"FINISHED\",\"text\":\"done\"}\n\n" +
			"id: evt-3\nevent: done\ndata: {}\n\n", io.EOF
	})

	run, err := client.StreamRun(context.Background(), "bc-agent", "run-one", func(StreamEvent) error { return nil })
	if err != nil || run == nil || run.Status != "FINISHED" || run.Result != "done" {
		t.Fatalf("StreamRun = %+v, %v; want reconnect after a connection reset", run, err)
	}
	if calls.Load() != 2 {
		t.Fatalf("stream calls = %d, want 2", calls.Load())
	}
}

func TestStreamRunTerminalResultWinsOverLaterReadError(t *testing.T) {
	client, calls := truncatedStreamClient(t, func(int32, *http.Request) (string, error) {
		return "id: evt-1\nevent: result\ndata: {\"runId\":\"run-one\",\"status\":\"FINISHED\",\"text\":\"done\"}\n\n",
			io.ErrUnexpectedEOF
	})

	run, err := client.StreamRun(context.Background(), "bc-agent", "run-one", func(StreamEvent) error { return nil })
	if err != nil || run == nil || run.Status != "FINISHED" || run.Result != "done" {
		t.Fatalf("StreamRun = %+v, %v; want the decoded result to outrank the read error", run, err)
	}
	if calls.Load() != 1 {
		t.Fatalf("stream calls = %d, want 1", calls.Load())
	}
}

// The same recovery must hold over a real connection, not just an injected
// read error: an aborted response truncates the chunked body mid-stream.
func TestStreamRunReconnectsAfterTruncatedResponse(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		if calls.Add(1) == 1 {
			_, _ = io.WriteString(w, "id: evt-1\nevent: assistant\ndata: {\"text\":\"hello\"}\n\n")
			w.(http.Flusher).Flush()
			panic(http.ErrAbortHandler)
		}
		if got := r.Header.Get("Last-Event-ID"); got != "evt-1" {
			t.Errorf("reconnect Last-Event-ID = %q, want evt-1", got)
		}
		_, _ = io.WriteString(w,
			"id: evt-2\nevent: result\ndata: {\"runId\":\"run-one\",\"status\":\"FINISHED\",\"text\":\"done\"}\n\n"+
				"id: evt-3\nevent: done\ndata: {}\n\n")
	}))
	defer srv.Close()

	client, _ := New(Options{BaseURL: srv.URL, APIKey: "synthetic-key", HTTPClient: srv.Client()})
	run, err := client.StreamRun(context.Background(), "bc-agent", "run-one", func(StreamEvent) error { return nil })
	if err != nil || run == nil || run.Status != "FINISHED" || run.Result != "done" {
		t.Fatalf("StreamRun = %+v, %v; want reconnect after a truncated response", run, err)
	}
	if calls.Load() != 2 {
		t.Fatalf("stream calls = %d, want 2", calls.Load())
	}
}

func TestStreamRunDoesNotRetryInvalidPayload(t *testing.T) {
	client, calls := truncatedStreamClient(t, func(int32, *http.Request) (string, error) {
		return "id: evt-1\nevent: result\ndata: {\"runId\":\n\n", io.EOF
	})

	run, err := client.StreamRun(context.Background(), "bc-agent", "run-one", func(StreamEvent) error { return nil })
	if err == nil {
		t.Fatalf("StreamRun = %+v, nil; want an immediate decode error", run)
	}
	if calls.Load() != 1 {
		t.Fatalf("stream calls = %d, want 1 (invalid payloads are not retried)", calls.Load())
	}
}

// Reconnects after read failures reuse the bounded no-progress budget rather
// than looping until the context expires.
func TestStreamRunTruncatedReadsRespectNoProgressBudget(t *testing.T) {
	var streamCalls, statusCalls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/stream"):
			streamCalls.Add(1)
			w.Header().Set("Content-Type", "text/event-stream")
			w.(http.Flusher).Flush()
			panic(http.ErrAbortHandler)
		case r.URL.Path == "/v1/agents/bc-agent/runs/run-one":
			statusCalls.Add(1)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": "run-one", "agentId": "bc-agent", "status": "FINISHED", "result": "done via status",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	client, _ := New(Options{BaseURL: srv.URL, APIKey: "synthetic-key", HTTPClient: srv.Client()})
	run, err := client.StreamRun(context.Background(), "bc-agent", "run-one", func(StreamEvent) error { return nil })
	if err != nil || run == nil || run.Result != "done via status" {
		t.Fatalf("StreamRun = %+v, %v", run, err)
	}
	if streamCalls.Load() != 4 || statusCalls.Load() != 1 {
		t.Fatalf("stream calls = %d, status calls = %d; want 4 and 1", streamCalls.Load(), statusCalls.Load())
	}
}

func TestStreamRunNoProgressCapFallsBackToTerminalRun(t *testing.T) {
	var streamCalls, statusCalls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/stream"):
			streamCalls.Add(1)
			w.Header().Set("Content-Type", "text/event-stream")
		case r.URL.Path == "/v1/agents/bc-agent/runs/run-one":
			statusCalls.Add(1)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": "run-one", "agentId": "bc-agent", "status": "FINISHED", "result": "done via status",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	client, _ := New(Options{BaseURL: srv.URL, APIKey: "synthetic-key", HTTPClient: srv.Client()})
	run, err := client.StreamRun(context.Background(), "bc-agent", "run-one", func(StreamEvent) error {
		return nil
	})
	if err != nil || run.Status != "FINISHED" || run.Result != "done via status" {
		t.Fatalf("StreamRun = %+v, %v", run, err)
	}
	if streamCalls.Load() != 4 || statusCalls.Load() != 1 {
		t.Fatalf("stream calls = %d, status calls = %d; want 4 and 1", streamCalls.Load(), statusCalls.Load())
	}
}

func TestStreamRunNoProgressFallbackDoesNotReturnActiveRun(t *testing.T) {
	var streamCalls, statusCalls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/stream"):
			streamCalls.Add(1)
			w.Header().Set("Content-Type", "text/event-stream")
		case r.URL.Path == "/v1/agents/bc-agent/runs/run-one":
			statusCalls.Add(1)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": "run-one", "agentId": "bc-agent", "status": "RUNNING",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	client, _ := New(Options{BaseURL: srv.URL, APIKey: "synthetic-key", HTTPClient: srv.Client()})
	run, err := client.StreamRun(ctx, "bc-agent", "run-one", func(StreamEvent) error {
		return nil
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("StreamRun = %+v, %v; want context deadline after active fallback", run, err)
	}
	if statusCalls.Load() == 0 {
		t.Fatal("no-progress cap never checked run status")
	}
	if got := streamCalls.Load(); got < 4 || got > 6 {
		t.Fatalf("stream calls = %d, want bounded reconnects after fallback", got)
	}
	if got := statusCalls.Load(); got != 1 {
		t.Fatalf("status calls = %d, want one bounded fallback check", got)
	}
}

// Cursor computes durationMs "once the run reaches FINISHED, ERROR,
// CANCELLED, or EXPIRED" — Cloud Agents API, "Get A Run"
// (https://cursor.com/docs/cloud-agent/api/endpoints).
func TestIsTerminalRunStatusCoversEveryCursorTerminalState(t *testing.T) {
	for _, tc := range []struct {
		status string
		want   bool
	}{
		{status: "FINISHED", want: true},
		{status: "ERROR", want: true},
		{status: "CANCELLED", want: true},
		{status: "EXPIRED", want: true},
		{status: " expired ", want: true},
		{status: "RUNNING"},
		{status: "CREATING"},
		{status: "PENDING"},
		{status: ""},
	} {
		if got := isTerminalRunStatus(tc.status); got != tc.want {
			t.Errorf("isTerminalRunStatus(%q) = %v, want %v", tc.status, got, tc.want)
		}
	}
}

func TestStreamRunNoProgressFallbackReturnsExpiredRun(t *testing.T) {
	var statusCalls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/stream"):
			w.Header().Set("Content-Type", "text/event-stream")
		case r.URL.Path == "/v1/agents/bc-agent/runs/run-one":
			statusCalls.Add(1)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": "run-one", "agentId": "bc-agent", "status": "EXPIRED",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	client, _ := New(Options{BaseURL: srv.URL, APIKey: "synthetic-key", HTTPClient: srv.Client()})
	run, err := client.StreamRun(ctx, "bc-agent", "run-one", func(StreamEvent) error { return nil })
	if err != nil || run == nil || run.Status != "EXPIRED" {
		t.Fatalf("StreamRun = %+v, %v; want the EXPIRED run returned instead of reconnecting", run, err)
	}
	if statusCalls.Load() != 1 {
		t.Fatalf("status calls = %d, want one bounded fallback check", statusCalls.Load())
	}
}

func TestStreamRunUsesDocumentedStreamEndpoint(t *testing.T) {
	var gotMethod, gotURI string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotURI = r.Method, r.RequestURI
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w,
			"id: evt-1\nevent: result\ndata: {\"runId\":\"run-one\",\"status\":\"FINISHED\",\"text\":\"done\"}\n\n"+
				"id: evt-2\nevent: done\ndata: {}\n\n")
	}))
	defer srv.Close()

	client, _ := New(Options{BaseURL: srv.URL, APIKey: "synthetic-key", HTTPClient: srv.Client()})
	if _, err := client.StreamRun(context.Background(), "bc-agent", "run-one", func(e StreamEvent) error { return nil }); err != nil {
		t.Fatalf("StreamRun error = %v", err)
	}
	// Cursor Cloud Agents API, "Stream A Run":
	// https://cursor.com/docs/cloud-agent/api/endpoints#stream-a-run
	if gotMethod != http.MethodGet {
		t.Fatalf("method = %q, want GET", gotMethod)
	}
	if want := "/v1/agents/bc-agent/runs/run-one/stream"; gotURI != want {
		t.Fatalf("request URI = %q, want %q", gotURI, want)
	}
}

func TestStreamRunParsesMultilineDataToolCallAndIgnoresHeartbeat(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w,
			": ping\n\n"+
				"event: heartbeat\ndata: {}\n\n"+
				"id: evt-1\nevent: tool_call\ndata: {\"name\":\"grep\",\"status\":\"running\"}\n\n"+
				"id: evt-2\nevent: assistant\ndata: {\"text\":\ndata: \"hello world\"}\n\n"+
				"id: evt-3\nevent: result\ndata: {\"runId\":\"run-one\",\"status\":\"FINISHED\",\"text\":\"ok\"}\n\n"+
				"id: evt-4\nevent: done\ndata: {}\n\n")
	}))
	defer srv.Close()

	client, _ := New(Options{BaseURL: srv.URL, APIKey: "synthetic-key", HTTPClient: srv.Client()})
	var events []StreamEvent
	run, err := client.StreamRun(context.Background(), "bc-agent", "run-one", func(e StreamEvent) error {
		events = append(events, e)
		return nil
	})
	if err != nil || run.Status != "FINISHED" || run.Result != "ok" {
		t.Fatalf("StreamRun = %+v, %v", run, err)
	}
	if len(events) != 3 {
		t.Fatalf("events = %#v, want 3 (heartbeat and done must be ignored)", events)
	}
	if events[0].Type != "tool_call" || events[0].ToolName != "grep" || events[0].Status != "running" {
		t.Fatalf("tool_call event = %+v", events[0])
	}
	if events[1].Type != "assistant" || events[1].Text != "hello world" {
		t.Fatalf("assistant event (multiline data) = %+v", events[1])
	}
}

// The tool layer redacts again, but the client contract must on its own keep
// the configured key out of stream errors and keep them bounded.
func TestStreamRunSanitizesInBandSSEError(t *testing.T) {
	const key = "synthetic-key"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "id: evt-1\nevent: error\ndata: {\"code\":\"rejected "+key+
			"\",\"message\":\"upstream refused "+key+" \xff\xfe "+strings.Repeat("padding ", 400)+"\"}\n\n")
	}))
	defer srv.Close()

	client, _ := New(Options{BaseURL: srv.URL, APIKey: key, HTTPClient: srv.Client()})
	_, err := client.StreamRun(context.Background(), "bc-agent", "run-one", func(StreamEvent) error { return nil })
	if err == nil {
		t.Fatal("StreamRun accepted an SSE error event")
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %v (%T), want *APIError", err, err)
	}
	if strings.Contains(apiErr.Message, key) || strings.Contains(apiErr.Code, key) ||
		strings.Contains(err.Error(), key) {
		t.Fatalf("stream error leaked the API key: code=%q message=%q", apiErr.Code, apiErr.Message)
	}
	if got := utf8.RuneCountInString(apiErr.Message); got > 240 {
		t.Fatalf("stream error message = %d runes, want the bounded API-error policy", got)
	}
	if !utf8.ValidString(apiErr.Message) || !utf8.ValidString(apiErr.Code) {
		t.Fatalf("stream error was not normalized to valid UTF-8: code=%q message=%q", apiErr.Code, apiErr.Message)
	}
}

func TestStreamRunContextCancellationReturnsImmediately(t *testing.T) {
	block := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.(http.Flusher).Flush()
		<-block
	}))
	defer func() {
		close(block)
		srv.Close()
	}()

	client, _ := New(Options{BaseURL: srv.URL, APIKey: "synthetic-key", HTTPClient: srv.Client()})
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	_, err := client.StreamRun(ctx, "bc-agent", "run-one", func(e StreamEvent) error { return nil })
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("StreamRun took too long to honor cancellation: %v", elapsed)
	}
}

func TestStreamRunOversizedLineReturnsExplicitError(t *testing.T) {
	huge := strings.Repeat("a", 2<<20)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "event: assistant\ndata: "+huge+"\n\n")
	}))
	defer srv.Close()

	client, _ := New(Options{BaseURL: srv.URL, APIKey: "synthetic-key", HTTPClient: srv.Client()})
	_, err := client.StreamRun(context.Background(), "bc-agent", "run-one", func(e StreamEvent) error { return nil })
	if err == nil {
		t.Fatal("expected explicit error for oversized SSE line, got nil (possible silent partial success)")
	}
}

func TestStreamRun410FallsBackToGetRun(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/stream"):
			w.WriteHeader(http.StatusGone)
			_ = json.NewEncoder(w).Encode(map[string]any{"code": "stream_expired", "message": "stream expired"})
		case r.URL.Path == "/v1/agents/bc-agent/runs/run-one":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": "run-one", "agentId": "bc-agent", "status": "FINISHED", "result": "done",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	client, _ := New(Options{BaseURL: srv.URL, APIKey: "synthetic-key", HTTPClient: srv.Client()})
	run, err := client.StreamRun(context.Background(), "bc-agent", "run-one", func(e StreamEvent) error { return nil })
	if err != nil || run.Status != "FINISHED" || run.Result != "done" {
		t.Fatalf("StreamRun = %+v, %v", run, err)
	}
}

func TestStreamRunEndsWithDoneButNoResultFallsBackToGetRun(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/stream"):
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, "id: evt-1\nevent: done\ndata: {}\n\n")
		case r.URL.Path == "/v1/agents/bc-agent/runs/run-one":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": "run-one", "agentId": "bc-agent", "status": "FINISHED", "result": "done via fallback",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	client, _ := New(Options{BaseURL: srv.URL, APIKey: "synthetic-key", HTTPClient: srv.Client()})
	run, err := client.StreamRun(context.Background(), "bc-agent", "run-one", func(e StreamEvent) error { return nil })
	if err != nil || run.Result != "done via fallback" {
		t.Fatalf("StreamRun = %+v, %v", run, err)
	}
}

func TestStreamRunResetsOnceAfterInvalidLastEventID(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		switch calls.Add(1) {
		case 1:
			_, _ = io.WriteString(w, "id: evt-1\nevent: assistant\ndata: {\"text\":\"hi\"}\n\n")
		case 2:
			if got := r.Header.Get("Last-Event-ID"); got != "evt-1" {
				t.Fatalf("Last-Event-ID = %q, want evt-1", got)
			}
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]any{"code": "invalid_last_event_id", "message": "unknown event id"})
		case 3:
			if got := r.Header.Get("Last-Event-ID"); got != "" {
				t.Fatalf("Last-Event-ID = %q, want reset to empty", got)
			}
			_, _ = io.WriteString(w,
				"id: evt-2\nevent: result\ndata: {\"runId\":\"run-one\",\"status\":\"FINISHED\",\"text\":\"done\"}\n\n"+
					"id: evt-3\nevent: done\ndata: {}\n\n")
		default:
			t.Fatalf("unexpected extra call %d", calls.Load())
		}
	}))
	defer srv.Close()

	client, _ := New(Options{BaseURL: srv.URL, APIKey: "synthetic-key", HTTPClient: srv.Client()})
	run, err := client.StreamRun(context.Background(), "bc-agent", "run-one", func(e StreamEvent) error { return nil })
	if err != nil || run.Status != "FINISHED" || run.Result != "done" {
		t.Fatalf("StreamRun = %+v, %v", run, err)
	}
	if calls.Load() != 3 {
		t.Fatalf("calls = %d, want 3", calls.Load())
	}
}

func TestStreamRunReturnsEmitErrorImmediately(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "id: evt-1\nevent: assistant\ndata: {\"text\":\"hi\"}\n\n")
	}))
	defer srv.Close()

	client, _ := New(Options{BaseURL: srv.URL, APIKey: "synthetic-key", HTTPClient: srv.Client()})
	wantErr := errors.New("synthetic emit failure")
	_, err := client.StreamRun(context.Background(), "bc-agent", "run-one", func(e StreamEvent) error {
		return wantErr
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("err = %v, want %v", err, wantErr)
	}
	if calls.Load() != 1 {
		t.Fatalf("calls = %d, want 1 (no retry after emit error)", calls.Load())
	}
}

// StreamRunWithOptions is the real implementation; StreamRun must remain a
// thin wrapper that calls it with an empty StreamOptions.
func TestStreamRunWithOptionsUsesSuppliedLastEventID(t *testing.T) {
	var gotHeader string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeader = r.Header.Get("Last-Event-ID")
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w,
			"id: evt-9\nevent: result\ndata: {\"runId\":\"run-one\",\"status\":\"FINISHED\",\"text\":\"done\"}\n\n"+
				"id: evt-10\nevent: done\ndata: {}\n\n")
	}))
	defer srv.Close()

	client, _ := New(Options{BaseURL: srv.URL, APIKey: "synthetic-key", HTTPClient: srv.Client()})
	run, err := client.StreamRunWithOptions(context.Background(), "bc-agent", "run-one",
		StreamOptions{LastEventID: "evt-8"},
		func(StreamEvent) error { return nil })
	if err != nil || run.Status != "FINISHED" {
		t.Fatalf("StreamRunWithOptions = %+v, %v", run, err)
	}
	if gotHeader != "evt-8" {
		t.Fatalf("initial Last-Event-ID = %q, want evt-8", gotHeader)
	}
}

// A stale caller-supplied resume token must trigger OnReset before Antares
// replays the run with a cleared token, so a persisted copy of the token
// (e.g. across process restarts) does not keep resurrecting a dead cursor.
func TestStreamRunWithOptionsInvalidLastEventIDCallsOnResetBeforeReplay(t *testing.T) {
	var calls atomic.Int32
	order := make(chan string, 4)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		switch calls.Add(1) {
		case 1:
			if got := r.Header.Get("Last-Event-ID"); got != "stale-evt" {
				t.Errorf("first Last-Event-ID = %q, want stale-evt", got)
			}
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]any{"code": "invalid_last_event_id", "message": "unknown event id"})
		case 2:
			order <- "reconnect"
			if got := r.Header.Get("Last-Event-ID"); got != "" {
				t.Errorf("reconnect Last-Event-ID = %q, want reset to empty", got)
			}
			_, _ = io.WriteString(w,
				"id: evt-1\nevent: result\ndata: {\"runId\":\"run-one\",\"status\":\"FINISHED\",\"text\":\"done\"}\n\n"+
					"id: evt-2\nevent: done\ndata: {}\n\n")
		default:
			t.Errorf("unexpected extra call %d", calls.Load())
		}
	}))
	defer srv.Close()

	var resetCalls atomic.Int32
	client, _ := New(Options{BaseURL: srv.URL, APIKey: "synthetic-key", HTTPClient: srv.Client()})
	run, err := client.StreamRunWithOptions(context.Background(), "bc-agent", "run-one",
		StreamOptions{
			LastEventID: "stale-evt",
			OnReset: func() error {
				resetCalls.Add(1)
				order <- "reset"
				return nil
			},
		},
		func(StreamEvent) error { return nil })
	if err != nil || run.Status != "FINISHED" || run.Result != "done" {
		t.Fatalf("StreamRunWithOptions = %+v, %v", run, err)
	}
	if resetCalls.Load() != 1 {
		t.Fatalf("OnReset calls = %d, want exactly 1", resetCalls.Load())
	}
	close(order)
	var got []string
	for v := range order {
		got = append(got, v)
	}
	if want := []string{"reset", "reconnect"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("call order = %v, want %v (OnReset must run before replay)", got, want)
	}
}

// A run whose retention window elapsed (410) is a signal the resume token is
// dead too, even though Antares does not retry the stream itself: OnReset
// must still fire before the GetRun fallback so a persisted token is
// dropped instead of being reused on the next call.
func TestStreamRunWithOptions410CallsOnResetBeforeGetRunFallback(t *testing.T) {
	var streamCalls, statusCalls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/stream"):
			streamCalls.Add(1)
			w.WriteHeader(http.StatusGone)
			_ = json.NewEncoder(w).Encode(map[string]any{"code": "stream_expired", "message": "stream expired"})
		case r.URL.Path == "/v1/agents/bc-agent/runs/run-one":
			statusCalls.Add(1)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": "run-one", "agentId": "bc-agent", "status": "FINISHED", "result": "done",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	var resetCalls atomic.Int32
	client, _ := New(Options{BaseURL: srv.URL, APIKey: "synthetic-key", HTTPClient: srv.Client()})
	run, err := client.StreamRunWithOptions(context.Background(), "bc-agent", "run-one",
		StreamOptions{
			LastEventID: "evt-old",
			OnReset: func() error {
				resetCalls.Add(1)
				return nil
			},
		},
		func(StreamEvent) error { return nil })
	if err != nil || run.Status != "FINISHED" || run.Result != "done" {
		t.Fatalf("StreamRunWithOptions = %+v, %v", run, err)
	}
	if resetCalls.Load() != 1 {
		t.Fatalf("OnReset calls = %d, want exactly 1", resetCalls.Load())
	}
	if streamCalls.Load() != 1 || statusCalls.Load() != 1 {
		t.Fatalf("stream calls = %d, status calls = %d; want 1 and 1 (no stream retry after 410)",
			streamCalls.Load(), statusCalls.Load())
	}
}

// An OnReset failure must abort the stream immediately instead of
// proceeding to reconnect with a cleared token the caller could not record.
func TestStreamRunWithOptionsAbortsWhenOnResetFails(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		calls.Add(1)
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]any{"code": "invalid_last_event_id", "message": "unknown event id"})
	}))
	defer srv.Close()

	wantErr := errors.New("synthetic persistence failure")
	client, _ := New(Options{BaseURL: srv.URL, APIKey: "synthetic-key", HTTPClient: srv.Client()})
	_, err := client.StreamRunWithOptions(context.Background(), "bc-agent", "run-one",
		StreamOptions{
			LastEventID: "stale-evt",
			OnReset:     func() error { return wantErr },
		},
		func(StreamEvent) error { return nil })
	if !errors.Is(err, wantErr) {
		t.Fatalf("err = %v, want %v", err, wantErr)
	}
	if calls.Load() != 1 {
		t.Fatalf("stream calls = %d, want 1 (no reconnect after OnReset failure)", calls.Load())
	}
}

// The terminal-result-wins guarantee must hold through the new entry point
// directly, not only through the StreamRun compatibility wrapper.
func TestStreamRunWithOptionsTerminalResultWinsOverLaterReadError(t *testing.T) {
	client, calls := truncatedStreamClient(t, func(int32, *http.Request) (string, error) {
		return "id: evt-1\nevent: result\ndata: {\"runId\":\"run-one\",\"status\":\"FINISHED\",\"text\":\"done\"}\n\n",
			io.ErrUnexpectedEOF
	})

	run, err := client.StreamRunWithOptions(context.Background(), "bc-agent", "run-one", StreamOptions{},
		func(StreamEvent) error { return nil })
	if err != nil || run == nil || run.Status != "FINISHED" || run.Result != "done" {
		t.Fatalf("StreamRunWithOptions = %+v, %v; want the decoded result to outrank the read error", run, err)
	}
	if calls.Load() != 1 {
		t.Fatalf("stream calls = %d, want 1", calls.Load())
	}
}

// Full tool-call identity, args, result, and truncation flags must survive
// decoding exactly as Cursor documents the tool_call envelope, and status
// events must carry their run id too.
func TestCompleteToolCallEventDecodesIDArgsResultAndTruncation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w,
			"event: status\ndata: {\"runId\":\"run-one\",\"status\":\"RUNNING\"}\n\n"+
				"id: evt-1\nevent: tool_call\ndata: {\"callId\":\"call-1\",\"name\":\"read_file\",\"status\":\"running\","+
				"\"args\":{\"path\":\"README.md\"}}\n\n"+
				"id: evt-2\nevent: tool_call\ndata: {\"callId\":\"call-1\",\"name\":\"read_file\",\"status\":\"completed\","+
				"\"args\":{\"path\":\"README.md\"},\"result\":{\"content\":\"# Project\"},"+
				"\"truncated\":{\"args\":true,\"result\":true}}\n\n"+
				"id: evt-3\nevent: result\ndata: {\"runId\":\"run-one\",\"status\":\"FINISHED\",\"text\":\"done\"}\n\n"+
				"id: evt-4\nevent: done\ndata: {}\n\n")
	}))
	defer srv.Close()

	client, _ := New(Options{BaseURL: srv.URL, APIKey: "synthetic-key", HTTPClient: srv.Client()})
	var events []StreamEvent
	run, err := client.StreamRun(context.Background(), "bc-agent", "run-one", func(e StreamEvent) error {
		events = append(events, e)
		return nil
	})
	if err != nil || run.Status != "FINISHED" {
		t.Fatalf("StreamRun = %+v, %v", run, err)
	}
	if len(events) != 4 {
		t.Fatalf("events = %#v, want status, two tool_call events, and result", events)
	}

	status := events[0]
	if status.Type != "status" || status.RunID != "run-one" || status.Status != "RUNNING" {
		t.Fatalf("status event = %+v", status)
	}

	running := events[1]
	if running.CallID != "call-1" || running.ToolName != "read_file" || running.Status != "running" {
		t.Fatalf("running tool_call = %+v", running)
	}
	if string(running.ToolArgs) != `{"path":"README.md"}` {
		t.Fatalf("running args = %s, want {\"path\":\"README.md\"}", running.ToolArgs)
	}
	if running.ToolResult != nil {
		t.Fatalf("running result = %s, want nil (tool still running)", running.ToolResult)
	}
	if running.ArgsTruncated || running.ResultTruncated {
		t.Fatalf("running truncation = args=%v result=%v, want both false", running.ArgsTruncated, running.ResultTruncated)
	}

	completed := events[2]
	if completed.CallID != "call-1" || completed.ToolName != "read_file" || completed.Status != "completed" {
		t.Fatalf("completed tool_call = %+v", completed)
	}
	if string(completed.ToolArgs) != `{"path":"README.md"}` {
		t.Fatalf("completed args = %s", completed.ToolArgs)
	}
	if string(completed.ToolResult) != `{"content":"# Project"}` {
		t.Fatalf("completed result = %s", completed.ToolResult)
	}
	if !completed.ArgsTruncated || !completed.ResultTruncated {
		t.Fatalf("completed truncation = args=%v result=%v, want both true", completed.ArgsTruncated, completed.ResultTruncated)
	}

	resultEvt := events[3]
	if resultEvt.Type != "result" || resultEvt.RunID != "run-one" {
		t.Fatalf("result event = %+v", resultEvt)
	}
}

func TestStreamRunIgnoresClientTimeoutDuringStream(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "id: evt-1\nevent: assistant\ndata: {\"text\":\"hi\"}\n\n")
		w.(http.Flusher).Flush()
		time.Sleep(120 * time.Millisecond)
		_, _ = io.WriteString(w,
			"id: evt-2\nevent: result\ndata: {\"runId\":\"run-one\",\"status\":\"FINISHED\",\"text\":\"done\"}\n\n"+
				"id: evt-3\nevent: done\ndata: {}\n\n")
	}))
	defer srv.Close()

	client, _ := New(Options{BaseURL: srv.URL, APIKey: "synthetic-key", HTTPClient: &http.Client{Timeout: 50 * time.Millisecond}})
	run, err := client.StreamRun(context.Background(), "bc-agent", "run-one", func(e StreamEvent) error { return nil })
	if err != nil || run.Status != "FINISHED" {
		t.Fatalf("StreamRun = %+v, %v (client timeout should not apply mid-stream)", run, err)
	}
}
