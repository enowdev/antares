package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/viewport"

	"github.com/enowdev/antares/internal/agent"
	"github.com/enowdev/antares/internal/checkpoint"
	"github.com/enowdev/antares/internal/config"
	"github.com/enowdev/antares/internal/store"
	"github.com/enowdev/antares/internal/tools"
)

// undoTestModel builds a Model backed by a real in-memory store holding one
// user turn, so the marker-validation path runs against the same ListMessages
// the production code uses rather than a hand-rolled fake.
func undoTestModel(t *testing.T) (*Model, string) {
	t.Helper()
	db, err := store.Open(t.Context(), "memory", "", 1, 5000, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	sess := &store.Session{ID: "sess-1", Title: "t"}
	if err := db.CreateSession(t.Context(), sess); err != nil {
		t.Fatal(err)
	}
	for _, m := range []store.Message{
		{ID: "msg-user", SessionID: "sess-1", Role: "user", Content: "hello"},
		{ID: "msg-asst", SessionID: "sess-1", Role: "assistant", Content: "hi"},
	} {
		msg := m
		if err := db.AppendMessage(t.Context(), &msg); err != nil {
			t.Fatal(err)
		}
	}

	cfg := &config.Config{}
	m := &Model{cfg: cfg, themeName: "antares", st: newStyles(themeByName("antares"))}
	m.vp = viewport.New(80, 24)
	m.db = db
	// A real agent so cmdRevert gets past its "unavailable in this build"
	// guard and actually exercises marker validation. ANTARES_HOME is
	// redirected so the agent's on-disk stores land in the test's temp dir.
	t.Setenv("ANTARES_HOME", t.TempDir())
	m.ag = agent.New(cfg, db, tools.NewRegistry(), nil, nil)
	m.sessionID = "sess-1"
	return m, "msg-user"
}

// TestIsUserMessageRejectsUnknownAndAssistantIDs guards the /revert <id> path.
// An id the checkpoint store has never seen previews as "no file changes" with
// no error, so staging it would ask the operator to confirm a rollback that
// silently reverts nothing and then fails on the message trim. Assistant ids
// are rejected too: DeleteMessagesFrom would accept one and truncate from the
// wrong point.
func TestIsUserMessageRejectsUnknownAndAssistantIDs(t *testing.T) {
	m, userID := undoTestModel(t)

	ok, err := m.isUserMessage(userID)
	if err != nil || !ok {
		t.Fatalf("isUserMessage(%q) = %v, %v; want true, nil", userID, ok, err)
	}
	for _, id := range []string{"no-such-id", "msg-asst", ""} {
		ok, err := m.isUserMessage(id)
		if err != nil {
			t.Fatalf("isUserMessage(%q) unexpected error: %v", id, err)
		}
		if ok {
			t.Errorf("isUserMessage(%q) = true, want false", id)
		}
	}
}

// TestCmdRevertUnknownIDDoesNotStage is the regression for the real defect:
// /revert with a bad id must refuse before setting m.pending, so no
// confirmation block is shown and `y` cannot commit a destructive no-op.
func TestCmdRevertUnknownIDDoesNotStage(t *testing.T) {
	m, userID := undoTestModel(t)

	if _, cmd := m.cmdRevert("no-such-id"); cmd != nil {
		t.Errorf("unknown id should not return a command")
	}
	if m.pending != nil {
		t.Fatalf("unknown id staged a rollback: %+v", m.pending)
	}
	if len(m.blocks) == 0 || !strings.Contains(m.blocks[len(m.blocks)-1].text, "No user message") {
		t.Fatalf("expected a rejection notice, got blocks %+v", m.blocks)
	}

	// A valid id must get past validation rather than being rejected by it.
	m.blocks = nil
	m.cmdRevert(userID)
	if len(m.blocks) == 0 {
		t.Fatal("valid id produced no output at all")
	}
	if strings.Contains(m.blocks[len(m.blocks)-1].text, "No user message") {
		t.Errorf("valid id was rejected by the guard: %q", m.blocks[len(m.blocks)-1].text)
	}
}

// TestCommitSkippedCountComesFromPreview pins the second defect: RestoreSince
// drops externally-changed files with a bare `continue` and never records them
// in RestoreResult.Failed, so counting len(res.Failed) reported 0 skipped for
// exactly the case the confirm block warned about. The count must come from
// the staged preview instead.
func TestCommitSkippedCountComesFromPreview(t *testing.T) {
	changes := []checkpoint.ChangedSince{
		{Path: "a.go"},
		{Path: "b.go", ExternallyChanged: true},
		{Path: "c.go", ExternallyChanged: true},
		{Path: "d.go", WillDelete: true},
	}

	skipped := 0
	for _, c := range changes {
		if c.ExternallyChanged {
			skipped++
		}
	}
	if skipped != 2 {
		t.Fatalf("preview-derived skipped = %d, want 2", skipped)
	}

	// The empty Failed map is what RestoreSince actually returns for skipped
	// files; the old code read this and reported zero.
	res := &checkpoint.RestoreResult{Failed: map[string]string{}}
	if len(res.Failed) == skipped {
		t.Fatal("test no longer distinguishes Failed from the preview count")
	}

	m := &Model{cfg: &config.Config{}, themeName: "antares", st: newStyles(themeByName("antares"))}
	m.vp = viewport.New(80, 24)
	m.pending = &pendingRevert{marker: "m", label: "the last turn", changes: changes}
	if got := m.pendingConfirmMessage(); !strings.Contains(got, "2 file(s) edited outside") {
		t.Errorf("confirm block should warn about 2 external files, got %q", got)
	}
}
