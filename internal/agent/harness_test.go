package agent

import (
	"context"
	"testing"

	"github.com/enowdev/antares/internal/llm"
)

func TestRepeatTrackerTripsOnIdenticalCalls(t *testing.T) {
	r := newRepeatTracker(3)
	call := llm.ToolCall{Name: "read_file", Arguments: `{"path":"a.txt"}`}

	if got := r.record([]llm.ToolCall{call}); len(got) != 0 {
		t.Fatalf("first call tripped: %v", got)
	}
	if got := r.record([]llm.ToolCall{call}); len(got) != 0 {
		t.Fatalf("second call tripped: %v", got)
	}
	got := r.record([]llm.ToolCall{call})
	if len(got) != 1 || got[0] != "read_file" {
		t.Fatalf("third identical call should trip, got %v", got)
	}
	// It must fire once, not on every call after the limit, or the history
	// fills with the same nudge.
	if again := r.record([]llm.ToolCall{call}); len(again) != 0 {
		t.Fatalf("the nudge repeated: %v", again)
	}
}

func TestRepeatTrackerIgnoresDifferentArguments(t *testing.T) {
	r := newRepeatTracker(2)
	for _, path := range []string{"a", "b", "c", "d"} {
		got := r.record([]llm.ToolCall{{Name: "read_file", Arguments: `{"path":"` + path + `"}`}})
		if len(got) != 0 {
			t.Fatalf("reading %q tripped the guard: %v", path, got)
		}
	}
}

func TestRepeatTrackerNormalisesArguments(t *testing.T) {
	r := newRepeatTracker(2)
	// Same call, re-serialised with different key order and spacing.
	r.record([]llm.ToolCall{{Name: "grep", Arguments: `{"pattern":"x","path":"."}`}})
	got := r.record([]llm.ToolCall{{Name: "grep", Arguments: `{ "path": ".", "pattern": "x" }`}})
	if len(got) != 1 {
		t.Fatalf("a re-serialised identical call was not recognised: %v", got)
	}
}

func TestRepeatTrackerExceeded(t *testing.T) {
	r := newRepeatTracker(2)
	call := llm.ToolCall{Name: "terminal", Arguments: `{"command":"ls"}`}
	for i := 0; i < 3; i++ {
		r.record([]llm.ToolCall{call})
		if r.exceeded() {
			t.Fatalf("gave up after %d calls, too early", i+1)
		}
	}
	r.record([]llm.ToolCall{call})
	if !r.exceeded() {
		t.Fatal("expected the run to be abandoned after twice the limit")
	}
}

func TestRepeatTrackerAllowsManagedProcessPolling(t *testing.T) {
	r := newRepeatTracker(2)
	call := llm.ToolCall{Name: "process", Arguments: `{"action":"wait","process_id":"proc_123","timeout":30}`}
	for i := 0; i < 20; i++ {
		if got := r.record([]llm.ToolCall{call}); len(got) != 0 {
			t.Fatalf("managed process wait was treated as a loop after %d calls: %v", i+1, got)
		}
	}
	if r.exceeded() {
		t.Fatal("managed process polling tripped the repeat guard")
	}
}

func TestRepeatTrackerStillTracksProcessKill(t *testing.T) {
	r := newRepeatTracker(2)
	call := llm.ToolCall{Name: "process", Arguments: `{"action":"kill","process_id":"proc_123"}`}
	if got := r.record([]llm.ToolCall{call}); len(got) != 0 {
		t.Fatal(got)
	}
	if got := r.record([]llm.ToolCall{call}); len(got) != 1 || got[0] != "process" {
		t.Fatalf("repeated process kill was not tracked: %v", got)
	}
}

func TestSteeringRequiresARunningSession(t *testing.T) {
	a := &Agent{active: map[string]context.CancelFunc{}}
	if a.Steer("nope", "do this instead") {
		t.Fatal("steering a session that is not running should report false")
	}

	a.active["s1"] = func() {}
	if !a.Steer("s1", "do this instead") {
		t.Fatal("steering a running session should be accepted")
	}
	if a.Steer("s1", "   ") {
		t.Fatal("an empty note should be rejected")
	}

	notes := drainSteering("s1")
	if len(notes) != 1 || notes[0] != "do this instead" {
		t.Fatalf("got %v", notes)
	}
	// Draining takes them, so a later turn does not replay old instructions.
	if again := drainSteering("s1"); len(again) != 0 {
		t.Fatalf("notes were replayed: %v", again)
	}
}

func TestExtractJSON(t *testing.T) {
	cases := map[string]string{
		`{"complete":true}`:                                 `{"complete":true}`,
		"```json\n{\"complete\":false}\n```":                `{"complete":false}`,
		"Here is my verdict:\n{\"complete\": true}\nThanks": `{"complete": true}`,
		"no json here":                                      "no json here",
	}
	for in, want := range cases {
		if got := extractJSON(in); got != want {
			t.Errorf("extractJSON(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNormaliseArgsToleratesGarbage(t *testing.T) {
	// A model sometimes emits arguments that are not valid JSON at all; the
	// guard must still fingerprint them rather than panic.
	if got := normaliseArgs(`{"broken":`); got != `{"broken":` {
		t.Fatalf("got %q", got)
	}
}

func TestRepeatKeyWriteFileSamePathDifferentContent(t *testing.T) {
	r := newRepeatTracker(2)
	// Same path, different content — the old full-args fingerprint would not
	// trip. With the path-aware key, repeated writes to the same file are
	// recognised as a stuck loop.
	path := `{"path":"config.yaml","content":"v1"}`
	r.record([]llm.ToolCall{{Name: "write_file", Arguments: path}})
	path2 := `{"path":"config.yaml","content":"v2"}`
	got := r.record([]llm.ToolCall{{Name: "write_file", Arguments: path2}})
	if len(got) != 1 || got[0] != "write_file" {
		t.Fatalf("same-path different-content write should trip on 2nd call, got %v", got)
	}
}

func TestRepeatKeyEditFileSamePathDifferentContent(t *testing.T) {
	r := newRepeatTracker(2)
	r.record([]llm.ToolCall{{Name: "edit_file", Arguments: `{"path":"main.go","old_string":"a","new_string":"b"}`}})
	got := r.record([]llm.ToolCall{{Name: "edit_file", Arguments: `{"path":"main.go","old_string":"c","new_string":"d"}`}})
	if len(got) != 1 || got[0] != "edit_file" {
		t.Fatalf("same-path different-content edit should trip on 2nd call, got %v", got)
	}
}

func TestRepeatKeyVpsUploadSameRemotePath(t *testing.T) {
	r := newRepeatTracker(2)
	r.record([]llm.ToolCall{{Name: "vps_upload", Arguments: `{"remote_path":"/tmp/x","local_path":"/a/b"}`}})
	got := r.record([]llm.ToolCall{{Name: "vps_upload", Arguments: `{"remote_path":"/tmp/x","local_path":"/c/d"}`}})
	if len(got) != 1 || got[0] != "vps_upload" {
		t.Fatalf("same-remote-path different-local vps_upload should trip, got %v", got)
	}
}

func TestRepeatKeyWriteFileDifferentPathDoesNotTrip(t *testing.T) {
	r := newRepeatTracker(2)
	r.record([]llm.ToolCall{{Name: "write_file", Arguments: `{"path":"a.txt","content":"x"}`}})
	got := r.record([]llm.ToolCall{{Name: "write_file", Arguments: `{"path":"b.txt","content":"x"}`}})
	if len(got) != 0 {
		t.Fatalf("different paths should not trip: %v", got)
	}
}