package llm

import (
	"context"
	"errors"
)

// FallbackEntry is one client and the model to ask it for.
type FallbackEntry struct {
	Client              Client
	Model               string
	ReasoningCapability *ReasoningCapability
}

// fallbackClient tries each entry in order, moving to the next when one fails
// hard (after its own retries). It is the layer above retry: retry rides out a
// blip on one provider, fallback survives that provider being gone entirely.
type fallbackClient struct {
	entries []FallbackEntry
}

// NewFallback wraps an ordered list of clients. The first is primary. With one
// entry it returns that client unwrapped, so there is no overhead when no
// fallback is configured.
func NewFallback(entries []FallbackEntry) Client {
	if len(entries) == 1 {
		return entries[0].Client
	}
	return &fallbackClient{entries: entries}
}

func (c *fallbackClient) Kind() string {
	if len(c.entries) > 0 {
		return c.entries[0].Client.Kind()
	}
	return ""
}

func (c *fallbackClient) Chat(ctx context.Context, req Request) (*Response, error) {
	var lastErr error
	for i, e := range c.entries {
		r := fallbackRequest(req, e, i > 0)
		resp, err := e.Client.Chat(ctx, r)
		if err == nil {
			return resp, nil
		}
		if ctx.Err() != nil {
			return nil, err // the caller cancelled; do not keep trying
		}
		lastErr = err
	}
	return nil, lastErr
}

// Stream falls back only while nothing has been emitted; once tokens have
// reached the caller, switching providers would replay a partial answer.
func (c *fallbackClient) Stream(ctx context.Context, req Request, emit func(Event) error) (*Response, error) {
	var lastErr error
	for i, e := range c.entries {
		emitted := false
		wrapped := func(ev Event) error {
			emitted = true
			return emit(ev)
		}
		r := fallbackRequest(req, e, i > 0)
		resp, err := e.Client.Stream(ctx, r, wrapped)
		if err == nil {
			return resp, nil
		}
		if ctx.Err() != nil || emitted || i == len(c.entries)-1 {
			return resp, err
		}
		lastErr = err
	}
	return nil, lastErr
}

func fallbackRequest(req Request, entry FallbackEntry, isFallback bool) Request {
	req.Model = entry.Model
	req.ReasoningCapability = entry.ReasoningCapability
	if isFallback && ValidateReasoningEffort(entry.Model, entry.ReasoningCapability, req.ReasoningEffort) != nil {
		req.ReasoningEffort = ""
	}
	return req
}

// Models and Embed use the primary only — a fallback for enumeration or
// embeddings would silently change the vector space.
func (c *fallbackClient) Models(ctx context.Context) ([]ModelInfo, error) {
	if len(c.entries) == 0 {
		return nil, errors.New("no client")
	}
	return c.entries[0].Client.Models(ctx)
}

func (c *fallbackClient) Embed(ctx context.Context, model string, inputs []string) ([][]float32, error) {
	if len(c.entries) == 0 {
		return nil, errors.New("no client")
	}
	return c.entries[0].Client.Embed(ctx, model, inputs)
}
