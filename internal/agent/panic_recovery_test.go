package agent

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/enowdev/antares/internal/config"
	"github.com/enowdev/antares/internal/llm"
	"github.com/enowdev/antares/internal/store"
	"github.com/enowdev/antares/internal/tools"
)

// panicTool is a mock tool that always panics, to test recovery.
type panicTool struct{}

func (panicTool) Name() string           { return "panic_tool" }
func (panicTool) Description() string    { return "a tool that panics" }
func (panicTool) Schema() map[string]any { return nil }
func (panicTool) Execute(_ context.Context, _ tools.Input) tools.Result {
	panic("simulated tool panic")
}

// okTool is a mock tool that returns a normal result.
type okTool struct{}

func (okTool) Name() string           { return "ok_tool" }
func (okTool) Description() string    { return "a tool that succeeds" }
func (okTool) Schema() map[string]any { return nil }
func (okTool) Execute(_ context.Context, _ tools.Input) tools.Result {
	return tools.Text("ok")
}

// TestExecuteToolsRecoversFromPanic verifies that a panicking tool in the
// parallel execution path does not deadlock wg.Wait(), and that the panic
// is surfaced as an error tool result.
func TestExecuteToolsRecoversFromPanic(t *testing.T) {
	cfg := config.Default()
	cfg.Model.ParallelToolCall = true

	a := &Agent{cfg: cfg}

	byName := map[string]tools.Tool{
		"panic_tool": panicTool{},
		"ok_tool":    okTool{},
	}

	calls := []llm.ToolCall{
		{ID: "call_1", Name: "ok_tool", Arguments: "{}"},
		{ID: "call_2", Name: "panic_tool", Arguments: "{}"},
	}

	sess := &store.Session{ID: "test_session"}

	var events []Event
	emit := func(e Event) error {
		events = append(events, e)
		return nil
	}

	// This would deadlock without the recover() wrapper.
	done := make(chan struct{})
	go func() {
		defer close(done)
		results := a.executeTools(context.Background(), calls, byName, Request{}, sess, emit)
		if len(results) != 2 {
			t.Errorf("expected 2 results, got %d", len(results))
		}
		if !results[1].isError {
			t.Error("panic_tool result should be an error")
		}
		if results[0].isError {
			t.Error("ok_tool result should not be an error")
		}
	}()

	select {
	case <-done:
		// success — no deadlock
	case <-time.After(10 * time.Second):
		t.Fatal("executeTools deadlocked on panicking tool")
	}

	_ = events // suppress unused warning
	_ = sync.Once{}
}

// TestExecuteToolsSerialRecoversFromPanic verifies the serial path also
// recovers from a panicking tool without killing the turn.
func TestExecuteToolsSerialRecoversFromPanic(t *testing.T) {
	cfg := config.Default()
	cfg.Model.ParallelToolCall = false

	a := &Agent{cfg: cfg}

	byName := map[string]tools.Tool{
		"panic_tool": panicTool{},
	}

	calls := []llm.ToolCall{
		{ID: "call_1", Name: "panic_tool", Arguments: "{}"},
	}

	sess := &store.Session{ID: "test_session"}

	emit := func(e Event) error { return nil }

	results := a.executeTools(context.Background(), calls, byName, Request{}, sess, emit)
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if !results[0].isError {
		t.Fatal("serial panic_tool result should be an error")
	}
	if results[0].message.Content == "" {
		t.Fatal("serial panic_tool should have error content")
	}
}
