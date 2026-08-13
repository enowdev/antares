package server

import (
	"context"
	"strings"
	"sync"

	"github.com/enowdev/antares/internal/agent"
)

// liveRun is an append-only log of one turn's events that any number of SSE
// clients can replay and then follow. It lets a turn outlive the HTTP request
// that started it: the run is driven on a background context and publishes here,
// so a client that navigates away and comes back can reattach and keep watching
// instead of losing the turn.
// maxLiveEvents bounds how many of a turn's most recent events are retained for
// replay. A long-horizon turn can emit tens of thousands of tiny events (text
// deltas, tool progress); keeping them all pins memory and lengthens every
// reconnect replay. We keep a trailing window instead — a reconnecting client
// still has the persisted history for anything older, so it loses nothing.
const maxLiveEvents = 4000

type liveRun struct {
	mu      sync.Mutex
	events  []agent.Event
	base    int // absolute index of events[0] (count of events already trimmed)
	done    bool
	stop    context.CancelFunc
	updated chan struct{} // closed on every change; replaced under the lock
}

func newLiveRun() *liveRun { return &liveRun{updated: make(chan struct{})} }

func (lr *liveRun) signal() {
	close(lr.updated)
	lr.updated = make(chan struct{})
}

// publish appends an event and wakes every follower.
func (lr *liveRun) publish(e agent.Event) {
	lr.mu.Lock()
	lr.events = append(lr.events, e)
	// Trim the oldest events once the window is exceeded, tracking how many were
	// dropped in base so follow()'s absolute cursor keeps mapping correctly.
	if over := len(lr.events) - maxLiveEvents; over > 0 {
		lr.events = append(lr.events[:0], lr.events[over:]...)
		lr.base += over
	}
	lr.signal()
	lr.mu.Unlock()
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

// setStop attaches a local watcher cancellation hook. Ordinary chat runs do
// not set one; direct Cursor runs use it so /api/chat/interrupt can stop local
// observation without cancelling the remote run.
func (lr *liveRun) setStop(stop context.CancelFunc) {
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

// stopWatching invokes the direct run's local cancellation hook at most once.
func (lr *liveRun) stopWatching() bool {
	lr.mu.Lock()
	stop := lr.stop
	lr.stop = nil
	lr.mu.Unlock()
	if stop == nil {
		return false
	}
	stop()
	return true
}

// follow replays events from cursor, then blocks for new ones until the run
// finishes or ctx is cancelled (the client disconnected). send stops the follow
// early by returning an error.
func (lr *liveRun) follow(ctx context.Context, cursor int, send func(agent.Event, int) error) error {
	i := cursor // absolute event index
	for {
		lr.mu.Lock()
		// If the cursor points at events already trimmed, fast-forward to the
		// oldest retained event rather than reading a negative slice index.
		if i < lr.base {
			i = lr.base
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
