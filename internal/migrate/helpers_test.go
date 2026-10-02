package migrate

// Shared test helpers for every source's tests: a configurable fake Env and a
// golden-file check. Run `go test ./internal/migrate -update` to rewrite
// goldens.

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var updateGolden = flag.Bool("update", false, "rewrite golden files")

// FakeEnv is an Env with fixed answers. Zero value: nothing exists, every text
// file is "missing", RAG on, no default model.
type FakeEnv struct {
	Providers    map[string]bool
	SameProvider map[string]bool // ProviderMatches answers true for these ids
	Skills       map[string]bool
	MCP          map[string]bool
	Roles        map[string]bool
	Channels     map[string]bool
	Text         map[Category]string
	DefaultModel bool
	RAGOff       bool
}

func (e FakeEnv) ProviderExists(id string) bool          { return e.Providers[id] }
func (e FakeEnv) SkillExists(name string) bool           { return e.Skills[name] }
func (e FakeEnv) MCPServerExists(name string) bool       { return e.MCP[name] }
func (e FakeEnv) RoleExists(name string) bool            { return e.Roles[name] }
func (e FakeEnv) ChannelConfigured(platform string) bool { return e.Channels[platform] }
func (e FakeEnv) TextFileState(cat Category) string {
	if s := e.Text[cat]; s != "" {
		return s
	}
	return "missing"
}
func (e FakeEnv) ProviderMatches(id, _, _ string) bool { return e.SameProvider[id] }
func (e FakeEnv) HasDefaultModel() bool                { return e.DefaultModel }
func (e FakeEnv) RAGEnabled() bool                     { return !e.RAGOff }

// CheckGolden compares got with testdata/golden/<name>; -update rewrites it.
// Absolute paths under the test's temp/testdata dirs should be made relative
// by the caller before calling.
func CheckGolden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", "golden", name)
	if *updateGolden {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("missing golden %s (run with -update): %v", path, err)
	}
	if string(want) != got {
		t.Fatalf("golden %s mismatch (run with -update to accept)\n--- got ---\n%s\n--- want ---\n%s", path, got, want)
	}
}

// AssertNoSecrets fails when any of secrets appears in s.
func AssertNoSecrets(t *testing.T, where, s string, secrets ...string) {
	t.Helper()
	for _, sec := range secrets {
		if sec != "" && strings.Contains(s, sec) {
			t.Fatalf("%s leaks a secret ending %q", where, sec[len(sec)-4:])
		}
	}
}

// noRunning disables real process detection for the duration of a test.
func noRunning(t *testing.T) {
	t.Helper()
	old := RunningCheck
	oldPID := PIDAlive
	RunningCheck = func(_, _ []string) bool { return false }
	PIDAlive = func(int) bool { return false }
	t.Cleanup(func() { RunningCheck, PIDAlive = old, oldPID })
}
