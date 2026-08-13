// Package approval provides explicit, instance-owned operation approval gates.
package approval

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Operation is the immutable human-facing description of work awaiting
// approval.
type Operation struct {
	SessionID string
	Tool      string
	Arguments string
	Message   string
	Reason    string
}

// ErrTimeout distinguishes the gate's own deadline from a deadline inherited
// through the caller's context.
var ErrTimeout = fmt.Errorf("approval timed out: %w", context.DeadlineExceeded)

// Request is one pending operation approval.
type Request struct {
	ID        string    `json:"id"`
	SessionID string    `json:"session_id"`
	Tool      string    `json:"tool"`
	Arguments string    `json:"arguments"`
	Message   string    `json:"message,omitempty"`
	Reason    string    `json:"reason,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

type decision struct {
	allow bool
	err   error
}

type pendingRequest struct {
	request  Request
	done     chan struct{}
	decision decision
}

// Gate owns the approval requests for one agent instance.
type Gate struct {
	mu      sync.Mutex
	pending map[string]*pendingRequest
	timeout time.Duration
}

// NewGate constructs an empty operation approval gate.
func NewGate(timeout time.Duration) *Gate {
	return &Gate{
		pending: make(map[string]*pendingRequest),
		timeout: timeout,
	}
}

// Await publishes an immutable operation and blocks until it is allowed,
// denied, timed out, or cancelled. The first terminal outcome wins.
func (g *Gate) Await(ctx context.Context, op Operation, emit func(Request) error) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}

	op = cloneOperation(op)
	pending := &pendingRequest{done: make(chan struct{})}

	g.mu.Lock()
	for {
		pending.request = Request{
			ID:        newRequestID(),
			SessionID: op.SessionID,
			Tool:      op.Tool,
			Arguments: op.Arguments,
			Message:   op.Message,
			Reason:    op.Reason,
			CreatedAt: time.Now(),
		}
		if _, exists := g.pending[pending.request.ID]; !exists {
			break
		}
	}
	g.pending[pending.request.ID] = pending
	g.mu.Unlock()

	timer := time.NewTimer(g.timeout)
	defer timer.Stop()

	if emit != nil {
		if err := emit(cloneRequest(pending.request)); err != nil {
			return g.finishOrAwait(pending, decision{err: err})
		}
	}

	select {
	case <-pending.done:
		return pending.decision.allow, pending.decision.err
	case <-timer.C:
		return g.finishOrAwait(pending, decision{err: ErrTimeout})
	case <-ctx.Done():
		return g.finishOrAwait(pending, decision{err: ctx.Err()})
	}
}

// Pending lists immutable request snapshots, oldest first.
func (g *Gate) Pending() []Request {
	g.mu.Lock()
	defer g.mu.Unlock()

	out := make([]Request, 0, len(g.pending))
	for _, pending := range g.pending {
		out = append(out, cloneRequest(pending.request))
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].ID < out[j].ID
		}
		return out[i].CreatedAt.Before(out[j].CreatedAt)
	})
	return out
}

// Resolve allows or denies a request. It reports false when another terminal
// outcome already removed the request or the ID was never pending.
func (g *Gate) Resolve(id string, allow bool) bool {
	g.mu.Lock()
	defer g.mu.Unlock()

	pending, ok := g.pending[id]
	if !ok {
		return false
	}
	g.finishLocked(pending, decision{allow: allow})
	return true
}

func (g *Gate) finishOrAwait(pending *pendingRequest, result decision) (bool, error) {
	g.mu.Lock()
	current, ok := g.pending[pending.request.ID]
	if ok && current == pending {
		g.finishLocked(pending, result)
		g.mu.Unlock()
		return result.allow, result.err
	}
	g.mu.Unlock()

	<-pending.done
	return pending.decision.allow, pending.decision.err
}

func (g *Gate) finishLocked(pending *pendingRequest, result decision) {
	delete(g.pending, pending.request.ID)
	pending.decision = result
	close(pending.done)
}

func cloneOperation(op Operation) Operation {
	return Operation{
		SessionID: strings.Clone(op.SessionID),
		Tool:      strings.Clone(op.Tool),
		Arguments: strings.Clone(op.Arguments),
		Message:   strings.Clone(op.Message),
		Reason:    strings.Clone(op.Reason),
	}
}

func cloneRequest(req Request) Request {
	return Request{
		ID:        strings.Clone(req.ID),
		SessionID: strings.Clone(req.SessionID),
		Tool:      strings.Clone(req.Tool),
		Arguments: strings.Clone(req.Arguments),
		Message:   strings.Clone(req.Message),
		Reason:    strings.Clone(req.Reason),
		CreatedAt: req.CreatedAt,
	}
}

var fallbackRequestID atomic.Uint64

func newRequestID() string {
	var random [10]byte
	if _, err := rand.Read(random[:]); err == nil {
		return "apr_" + hex.EncodeToString(random[:])
	}
	return fmt.Sprintf("apr_%020d", fallbackRequestID.Add(1))
}
