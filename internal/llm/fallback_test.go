package llm

import (
	"context"
	"testing"
)

func TestFallbackTriesNextOnFailure(t *testing.T) {
	primary := &fakeClient{failN: 10, failErr: &apiError{Status: 401}} // hard fail
	backup := &fakeClient{failN: 0}                                    // succeeds
	c := NewFallback([]FallbackEntry{
		{Client: primary, Model: "m1"},
		{Client: backup, Model: "m2"},
	})
	resp, err := c.Chat(context.Background(), Request{})
	if err != nil || resp.Content != "ok" {
		t.Fatalf("expected fallback to succeed, got %v %v", resp, err)
	}
	if primary.calls != 1 || backup.calls != 1 {
		t.Fatalf("expected both tried once, got primary=%d backup=%d", primary.calls, backup.calls)
	}
}

func TestFallbackSingleEntryUnwrapped(t *testing.T) {
	only := &fakeClient{}
	c := NewFallback([]FallbackEntry{{Client: only, Model: "m"}})
	if c != only {
		t.Fatal("a single entry should return the client unwrapped")
	}
}

func TestFallbackOverridesModel(t *testing.T) {
	rec := &modelRecorder{}
	c := NewFallback([]FallbackEntry{
		{Client: &fakeClient{failN: 10, failErr: &apiError{Status: 500}}, Model: "m1"},
		{Client: rec, Model: "m2"},
	})
	_, _ = c.Chat(context.Background(), Request{Model: "original"})
	if rec.gotModel != "m2" {
		t.Fatalf("fallback should set the entry's model, got %q", rec.gotModel)
	}
}

func TestFallbackReplacesPrimaryReasoningCapability(t *testing.T) {
	primaryCapability, err := NewReasoningCapability(
		[]ReasoningValue{{Value: "high", Label: "HIGH"}},
		"high", false, ReasoningCapabilityLive,
	)
	if err != nil {
		t.Fatal(err)
	}
	fallbackCapability, err := NewReasoningCapability(
		[]ReasoningValue{{Value: "low", Label: "LOW"}},
		"low", false, ReasoningCapabilityLive,
	)
	if err != nil {
		t.Fatal(err)
	}

	rec := &modelRecorder{}
	c := NewFallback([]FallbackEntry{
		{
			Client:              &fakeClient{failN: 10, failErr: &apiError{Status: 500}},
			Model:               "primary",
			ReasoningCapability: primaryCapability,
		},
		{
			Client:              rec,
			Model:               "fallback",
			ReasoningCapability: fallbackCapability,
		},
	})
	_, err = c.Chat(context.Background(), Request{
		Model:               "original",
		ReasoningEffort:     "high",
		ReasoningCapability: primaryCapability,
	})
	if err != nil {
		t.Fatal(err)
	}
	if rec.gotModel != "fallback" {
		t.Fatalf("model = %q, want fallback", rec.gotModel)
	}
	if rec.gotCapability != fallbackCapability {
		t.Fatalf("capability = %#v, want fallback entry capability %#v", rec.gotCapability, fallbackCapability)
	}
	if rec.gotEffort != "" {
		t.Fatalf("reasoning effort = %q, want Auto for unsupported legacy value", rec.gotEffort)
	}
}

type modelRecorder struct {
	gotModel      string
	gotEffort     string
	gotCapability *ReasoningCapability
}

func (m *modelRecorder) Kind() string { return "rec" }
func (m *modelRecorder) Chat(ctx context.Context, req Request) (*Response, error) {
	m.gotModel = req.Model
	m.gotEffort = req.ReasoningEffort
	m.gotCapability = req.ReasoningCapability
	return &Response{Content: "ok"}, nil
}
func (m *modelRecorder) Stream(ctx context.Context, req Request, emit func(Event) error) (*Response, error) {
	m.gotModel = req.Model
	m.gotEffort = req.ReasoningEffort
	m.gotCapability = req.ReasoningCapability
	return &Response{Content: "ok"}, nil
}
func (m *modelRecorder) Models(context.Context) ([]ModelInfo, error) { return nil, nil }
func (m *modelRecorder) Embed(context.Context, string, []string) ([][]float32, error) {
	return nil, nil
}
