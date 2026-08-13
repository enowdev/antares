package server

import (
	"context"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/enowdev/antares/internal/agent"
)

// liveRun is an append-only log of one turn's events that any number of SSE
// clients can replay and then follow. It lets a turn outlive the HTTP request
// that started it: the run is driven on a background context and publishes here,
// so a client that navigates away and comes back can reattach and keep watching
// instead of losing the turn.
const (
	// maxLiveEvents bounds how many of a turn's most recent original events are
	// retained for replay. Older text and reasoning are folded into a replay
	// checkpoint so a cursor behind the window can still reconstruct the turn.
	maxLiveEvents = 4000

	// Cursor partials are bounded to the same limit before publication. Applying
	// the limit generically keeps an ordinary run's replay checkpoint bounded too.
	maxLiveReplaySnapshotRunes = maxCursorPartialRunes

	liveReplayTrimmedToolsNotice = "Earlier live tool progress was trimmed from this replay."
)

type liveRunKind uint8

const (
	liveRunOrdinary liveRunKind = iota
	liveRunCursorDirect
	liveRunCursorRecovery
)

type liveRun struct {
	mu     sync.Mutex
	events []agent.Event
	base   int // absolute index of events[0] (count of events already trimmed)
	// replayText and replayReasoning contain the canonical partials immediately
	// before events[0]. They are emitted after a synthetic reset when a follower's
	// cursor predates base. Their rune counts avoid repeatedly scanning snapshots.
	replayText           []byte
	replayTextRunes      int
	replayReasoning      []byte
	replayReasoningRunes int
	replayToolsTrimmed   bool
	done                 bool
	kind                 liveRunKind
	detached             bool
	stop                 context.CancelFunc
	updated              chan struct{} // closed on every change; replaced under the lock
}

func newLiveRun() *liveRun { return &liveRun{updated: make(chan struct{})} }

func newCursorLiveRun(kind liveRunKind) *liveRun {
	return &liveRun{kind: kind, updated: make(chan struct{})}
}

func (lr *liveRun) signal() {
	close(lr.updated)
	lr.updated = make(chan struct{})
}

// publish appends an event and wakes every follower.
func (lr *liveRun) publish(e agent.Event) {
	lr.mu.Lock()
	lr.events = append(lr.events, e)
	// Fold trimmed events into the checkpoint before releasing their storage.
	// base continues to count only original events; synthetic checkpoint frames
	// therefore do not disturb an existing follower's absolute cursor.
	if over := len(lr.events) - maxLiveEvents; over > 0 {
		for _, dropped := range lr.events[:over] {
			lr.foldReplayEvent(dropped)
		}
		retained := copy(lr.events, lr.events[over:])
		clear(lr.events[retained:])
		lr.events = lr.events[:retained]
		lr.base += over
	}
	lr.signal()
	lr.mu.Unlock()
}

func (lr *liveRun) foldReplayEvent(event agent.Event) {
	switch event.Type {
	case agent.EventReset:
		lr.replayText = nil
		lr.replayTextRunes = 0
		lr.replayReasoning = nil
		lr.replayReasoningRunes = 0
	case agent.EventText:
		lr.replayText, lr.replayTextRunes = appendLiveReplaySnapshot(
			lr.replayText, lr.replayTextRunes, event.Delta,
		)
	case agent.EventReasoning:
		lr.replayReasoning, lr.replayReasoningRunes = appendLiveReplaySnapshot(
			lr.replayReasoning, lr.replayReasoningRunes, event.Delta,
		)
	case agent.EventToolCall, agent.EventToolProgress, agent.EventToolResult:
		// Tool payloads are live-only. Retain only the fact that an evicted card
		// existed, never its arguments, chunks, or result.
		lr.replayToolsTrimmed = true
	}
}

func appendLiveReplaySnapshot(snapshot []byte, runes int, delta string) ([]byte, int) {
	remaining := maxLiveReplaySnapshotRunes - runes
	if remaining <= 0 || delta == "" {
		return snapshot, runes
	}
	deltaRunes := utf8.RuneCountInString(delta)
	if deltaRunes <= remaining {
		return append(snapshot, delta...), runes + deltaRunes
	}
	end := len(delta)
	seen := 0
	for index := range delta {
		if seen == remaining {
			end = index
			break
		}
		seen++
	}
	return append(snapshot, delta[:end]...), runes + seen
}

func (lr *liveRun) replayAnchor() []agent.Event {
	anchor := make([]agent.Event, 0, 4)
	anchor = append(anchor, agent.Event{Type: agent.EventReset})
	if lr.replayToolsTrimmed {
		anchor = append(anchor, agent.Event{
			Type: agent.EventNotice, Message: liveReplayTrimmedToolsNotice,
		})
	}
	if len(lr.replayReasoning) > 0 {
		anchor = append(anchor, agent.Event{
			Type: agent.EventReasoning, Delta: string(lr.replayReasoning),
		})
	}
	if len(lr.replayText) > 0 {
		anchor = append(anchor, agent.Event{
			Type: agent.EventText, Delta: string(lr.replayText),
		})
	}
	return anchor
}

// finish marks the run complete so followers return once caught up.
func (lr *liveRun) finish() {
	lr.mu.Lock()
	if !lr.done {
		lr.done = true
		lr.stop = nil
		lr.signal()
	}
	lr.mu.Unlock()
}

func (lr *liveRun) beginCursorApproval(stop context.CancelFunc) {
	lr.mu.Lock()
	if lr.done {
		lr.mu.Unlock()
		if stop != nil {
			stop()
		}
		return
	}
	lr.stop = stop
	lr.mu.Unlock()
}

// beginCursorCreate switches from cancellable approval to a non-cancellable
// mutation. False means local Stop won before the POST boundary.
func (lr *liveRun) beginCursorCreate() bool {
	lr.mu.Lock()
	defer lr.mu.Unlock()
	if lr.done || lr.detached {
		return false
	}
	lr.stop = nil
	return true
}

// beginCursorWatch installs the watcher cancellation only after returned IDs
// are durable. A Stop during create records detachment and makes this return
// false without ever cancelling the non-idempotent create request.
func (lr *liveRun) beginCursorWatch(stop context.CancelFunc) bool {
	lr.mu.Lock()
	if lr.done || lr.detached {
		lr.mu.Unlock()
		if stop != nil {
			stop()
		}
		return false
	}
	lr.stop = stop
	lr.mu.Unlock()
	return true
}

func (lr *liveRun) runKind() liveRunKind {
	lr.mu.Lock()
	defer lr.mu.Unlock()
	return lr.kind
}

func (lr *liveRun) isCursor() bool {
	kind := lr.runKind()
	return kind == liveRunCursorDirect || kind == liveRunCursorRecovery
}

// stopWatching cancels approval before a POST, records detachment while a
// create POST is in flight, and cancels only an established local watcher.
func (lr *liveRun) stopWatching() bool {
	lr.mu.Lock()
	if lr.done || (lr.kind != liveRunCursorDirect && lr.kind != liveRunCursorRecovery) {
		lr.mu.Unlock()
		return false
	}
	lr.detached = true
	stop := lr.stop
	lr.stop = nil
	lr.mu.Unlock()
	if stop != nil {
		stop()
	}
	return true
}

// follow replays events from cursor, then blocks for new ones until the run
// finishes or ctx is cancelled (the client disconnected). send stops the follow
// early by returning an error.
func (lr *liveRun) follow(ctx context.Context, cursor int, send func(agent.Event, int) error) error {
	i := cursor // absolute event index
follow:
	for {
		lr.mu.Lock()
		// A stale cursor needs a reset plus the canonical state at the retention
		// boundary before trailing events can be applied. Completing the anchor
		// advances to base, the next original event index.
		if i < lr.base {
			i = lr.base
			anchor := lr.replayAnchor()
			lr.mu.Unlock()
			for index, event := range anchor {
				next := i
				// Until the final checkpoint frame is delivered, report a cursor
				// behind base so a mid-anchor reconnect repeats the whole reset.
				if index < len(anchor)-1 {
					next = i - 1
				}
				if err := send(event, next); err != nil {
					return err
				}
			}
			continue
		}
		for i-lr.base < len(lr.events) {
			e := lr.events[i-lr.base]
			i++
			// A reconnect may have thousands of token-sized deltas waiting. Collapse
			// adjacent text/reasoning events while the backlog is already available;
			// the returned cursor still advances past every original event.
			if e.Type == agent.EventText || e.Type == agent.EventReasoning {
				var merged strings.Builder
				for i-lr.base < len(lr.events) {
					next := lr.events[i-lr.base]
					if next.Type != e.Type {
						break
					}
					if merged.Len() == 0 {
						merged.Grow(len(e.Delta) + len(next.Delta))
						merged.WriteString(e.Delta)
					}
					merged.WriteString(next.Delta)
					i++
				}
				if merged.Len() > 0 {
					e.Delta = merged.String()
				}
			}
			lr.mu.Unlock()
			if err := send(e, i); err != nil {
				return err
			}
			lr.mu.Lock()
			// Publishing can compact the window while send is in progress.
			// Restart at the outer loop so the newer checkpoint is emitted.
			if i < lr.base {
				lr.mu.Unlock()
				continue follow
			}
		}
		if lr.done {
			lr.mu.Unlock()
			return nil
		}
		wait := lr.updated
		lr.mu.Unlock()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-wait:
		}
	}
}

// liveHub tracks the in-flight run per session so a reconnecting client can find
// it. A run is registered while it streams and removed once it completes (by
// then its result is persisted, so a returning client hydrates it instead).
type liveHub struct {
	mu   sync.Mutex
	runs map[string]*liveRun
}

func newLiveHub() *liveHub { return &liveHub{runs: make(map[string]*liveRun)} }

func (h *liveHub) put(session string, lr *liveRun) {
	if session == "" {
		return
	}
	h.mu.Lock()
	h.runs[session] = lr
	h.mu.Unlock()
}

// putIfAbsent atomically reserves a session for one live turn or recovery
// watcher. It is the paid-run concurrency gate and must be acquired before an
// approval is published.
func (h *liveHub) putIfAbsent(session string, lr *liveRun) bool {
	if session == "" || lr == nil {
		return false
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, exists := h.runs[session]; exists {
		return false
	}
	h.runs[session] = lr
	return true
}

func (h *liveHub) get(session string) *liveRun {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.runs[session]
}

func (h *liveHub) remove(session string, lr *liveRun) {
	h.mu.Lock()
	// Only remove if it is still the same run (a newer turn may have replaced it).
	if h.runs[session] == lr {
		delete(h.runs, session)
	}
	h.mu.Unlock()
}
