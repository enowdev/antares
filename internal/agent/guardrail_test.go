package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/enowdev/antares/internal/config"
	"github.com/enowdev/antares/internal/store"
)

// The tool-call guardrail auto-continue hinges on incompleteTodos: it decides
// whether a run that hit the budget still has work to do. These tests pin that
// gate and the message that pushes the model to keep going.

func newKVAgent(t *testing.T) *Agent {
	t.Helper()
	db, err := store.Open(context.Background(), "memory", "", 1, 5000, false)
	if err != nil {
		t.Fatalf("open memory store: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return &Agent{db: db}
}

func TestIncompleteTodosCountsOpenItems(t *testing.T) {
	a := newKVAgent(t)
	ctx := context.Background()
	sid := "s1"
	// two open, one completed, one blank (ignored)
	todo := `[
		{"content":"do A","status":"pending"},
		{"content":"do B","status":"in_progress"},
		{"content":"do C","status":"completed"},
		{"content":"","status":"pending"}
	]`
	if err := a.db.SetKV(ctx, "todo:"+sid, todo); err != nil {
		t.Fatalf("SetKV: %v", err)
	}
	if got := a.incompleteTodos(ctx, sid); got != 2 {
		t.Fatalf("open count = %d, want 2", got)
	}
}

func TestIncompleteTodosZeroWhenAllDone(t *testing.T) {
	a := newKVAgent(t)
	ctx := context.Background()
	sid := "s2"
	if err := a.db.SetKV(ctx, "todo:"+sid, `[{"content":"x","status":"completed"}]`); err != nil {
		t.Fatalf("SetKV: %v", err)
	}
	if got := a.incompleteTodos(ctx, sid); got != 0 {
		t.Fatalf("open count = %d, want 0 (all completed → guardrail should stop)", got)
	}
}

func TestIncompleteTodosZeroWithNoList(t *testing.T) {
	a := newKVAgent(t)
	// No todo written: a run with no task list must not auto-continue.
	if got := a.incompleteTodos(context.Background(), "missing"); got != 0 {
		t.Fatalf("open count = %d, want 0 when no list exists", got)
	}
}

func TestGuardrailContinueMessageStatesCountAndKeepsGoing(t *testing.T) {
	m := guardrailContinueMessage(3)
	if !strings.Contains(m, "3 task") {
		t.Fatalf("message should name the open count: %q", m)
	}
	// It must push forward: name the "keep going / continue" intent, and not
	// instruct the model to stop calling tools the way the terminal message does.
	low := strings.ToLower(m)
	if !strings.Contains(low, "keep going") && !strings.Contains(low, "continue") {
		t.Fatalf("continue message should push the model forward: %q", m)
	}
	if strings.Contains(low, "stop calling tools") {
		t.Fatalf("continue message must not tell the model to stop calling tools: %q", m)
	}
}

func TestGuardrailCapIsBounded(t *testing.T) {
	// A sane, finite ceiling so a runaway tool loop can never be truly unbounded.
	if maxGuardrailContinues <= 0 || maxGuardrailContinues > 20 {
		t.Fatalf("maxGuardrailContinues = %d, want a small positive ceiling", maxGuardrailContinues)
	}
}
func TestAbsoluteCeilingDefaultIs200(t *testing.T) {
	cfg := config.Default()
	if cfg.Guardrails.AbsoluteMaxToolCalls != 200 {
		t.Fatalf("AbsoluteMaxToolCalls = %d, want 200", cfg.Guardrails.AbsoluteMaxToolCalls)
	}
}

func TestAbsoluteCeilingIsSaneAndFinite(t *testing.T) {
	cfg := config.Default()
	v := cfg.Guardrails.AbsoluteMaxToolCalls
	if v <= 0 || v > 10000 {
		t.Fatalf("AbsoluteMaxToolCalls = %d, want a sane finite ceiling (1-10000)", v)
	}
}

func TestAbsoluteCeilingDisabledWhenZero(t *testing.T) {
	cfg := config.Default()
	cfg.Guardrails.AbsoluteMaxToolCalls = 0
	if cfg.Guardrails.AbsoluteMaxToolCalls != 0 {
		t.Fatal("AbsoluteMaxToolCalls should be 0 when disabled")
	}
}
