package agent

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/enowdev/antares/internal/config"
	"github.com/enowdev/antares/internal/cursor"
	"github.com/enowdev/antares/internal/cursorrun"
	"github.com/enowdev/antares/internal/llm"
	"github.com/enowdev/antares/internal/store"
	"github.com/enowdev/antares/internal/tools"
)

type agentCursorRunnerStub struct {
	invalidations atomic.Int32
}

func (*agentCursorRunnerStub) Catalog(context.Context, bool) (*cursor.ModelCatalog, error) {
	return nil, errors.New("unexpected Catalog call")
}

func (f *agentCursorRunnerStub) InvalidateCatalog() {
	f.invalidations.Add(1)
}

func (*agentCursorRunnerStub) ValidateModel(
	context.Context,
	*cursor.ModelSelection,
	cursorrun.SelectionPolicy,
) (*cursor.ModelSelection, error) {
	return nil, errors.New("unexpected ValidateModel call")
}

func (*agentCursorRunnerStub) CreateAgent(
	context.Context,
	cursor.CreateAgentRequest,
) (*cursor.CreateAgentResponse, error) {
	return nil, errors.New("unexpected CreateAgent call")
}

func (*agentCursorRunnerStub) CreateRun(
	context.Context,
	string,
	cursor.CreateRunRequest,
) (*cursor.Run, error) {
	return nil, errors.New("unexpected CreateRun call")
}

func (*agentCursorRunnerStub) GetAgent(context.Context, string) (*cursor.Agent, error) {
	return nil, errors.New("unexpected GetAgent call")
}

func (*agentCursorRunnerStub) GetRun(context.Context, string, string) (*cursor.Run, error) {
	return nil, errors.New("unexpected GetRun call")
}

func (*agentCursorRunnerStub) CancelRun(context.Context, string, string) error {
	return errors.New("unexpected CancelRun call")
}

func (*agentCursorRunnerStub) StreamRun(
	context.Context,
	string,
	string,
	string,
	func() error,
	func(cursor.StreamEvent) error,
) (*cursor.Run, error) {
	return nil, errors.New("unexpected StreamRun call")
}

func (*agentCursorRunnerStub) Progress(cursor.StreamEvent) cursorrun.Progress {
	return cursorrun.Progress{}
}

type cursorDependencyProbeTool struct {
	want cursorrun.Runner
	seen atomic.Bool
}

func (*cursorDependencyProbeTool) Name() string        { return "cursor_dependency_probe" }
func (*cursorDependencyProbeTool) Description() string { return "test Cursor dependency injection" }
func (*cursorDependencyProbeTool) Schema() map[string]any {
	return map[string]any{"type": "object"}
}

func (t *cursorDependencyProbeTool) Execute(_ context.Context, in tools.Input) tools.Result {
	if in.Deps != nil && in.Deps.Cursor == t.want {
		t.seen.Store(true)
		return tools.Result{Content: "ok"}
	}
	return tools.Errorf("wrong Cursor runner dependency")
}

func TestAgentInjectsSharedCursorRunnerIntoToolDependencies(t *testing.T) {
	runner := &agentCursorRunnerStub{}
	a := New(config.Default(), nil, tools.NewRegistry(), nil, nil)
	a.SetCursorRunner(runner)
	probe := &cursorDependencyProbeTool{want: runner}

	outcomes := a.executeTools(
		context.Background(),
		[]llm.ToolCall{{ID: "call-one", Name: probe.Name(), Arguments: `{}`}},
		map[string]tools.Tool{probe.Name(): probe},
		Request{},
		&store.Session{ID: "ses-one", Workspace: t.TempDir()},
		noEmit,
	)

	if len(outcomes) != 1 || outcomes[0].isError || !probe.seen.Load() {
		t.Fatalf("probe outcome = %+v, runner seen = %v", outcomes, probe.seen.Load())
	}
}

func TestAgentConfigReloadInvalidatesSharedCursorRunner(t *testing.T) {
	runner := &agentCursorRunnerStub{}
	a := New(config.Default(), nil, tools.NewRegistry(), nil, nil)
	a.SetCursorRunner(runner)

	next := config.Default()
	a.SetConfig(next)

	if got := runner.invalidations.Load(); got != 1 {
		t.Fatalf("runner invalidations = %d, want 1", got)
	}
}
