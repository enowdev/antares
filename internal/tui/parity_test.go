package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/enowdev/antares/internal/agent"
	"github.com/enowdev/antares/internal/commands"
	"github.com/enowdev/antares/internal/config"
	"github.com/enowdev/antares/internal/llm"
	"github.com/enowdev/antares/internal/store"
	"github.com/enowdev/antares/internal/tools"
)

// parityModel is a headless Model with a real in-memory store and agent, so
// commands run through the same registry and store calls production does.
func parityModel(t *testing.T) *Model {
	t.Helper()
	t.Setenv("ANTARES_HOME", t.TempDir())
	db, err := store.Open(t.Context(), "memory", "", 1, 5000, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	cfg := &config.Config{}
	m := &Model{cfg: cfg, db: db, themeName: "antares", st: newStyles(themeByName("antares"))}
	m.vp = viewport.New(80, 24)
	m.ta = textarea.New()
	m.ag = agent.New(cfg, db, tools.NewRegistry(), nil, nil)
	return m
}

func lastBlock(m *Model) block {
	if len(m.blocks) == 0 {
		return block{}
	}
	return m.blocks[len(m.blocks)-1]
}

// TestPaletteUnion checks the palette is native + registry with no name
// listed twice, and that a native command shadows its registry namesake.
func TestPaletteUnion(t *testing.T) {
	list := paletteCommands()
	seen := map[string]bool{}
	for _, c := range list {
		if seen[c.Name] {
			t.Fatalf("/%s listed twice", c.Name)
		}
		seen[c.Name] = true
	}
	for _, want := range []string{"theme", "revert", "effort", "attach", "search", "delete", "answer", // native
		"skills", "memory", "goal", "steer", "fork", "export", "usage", "roles", "role", "new", "resume", "copy"} { // registry
		if !seen[want] {
			t.Errorf("palette is missing /%s", want)
		}
	}

	overlap := 0
	for _, s := range commands.Catalogue(commands.SurfaceTUI) {
		if _, ok := nativeCommand(s.Name); ok {
			overlap++
		}
	}
	if want := len(native) + len(commands.Catalogue(commands.SurfaceTUI)) - overlap; len(list) != want {
		t.Errorf("palette has %d commands, want %d", len(list), want)
	}

	for _, c := range list {
		if c.Name == "undo" && c.Run == nil {
			t.Error("/undo must stay native: it restores files, the registry's only trims messages")
		}
		if c.Name == "memory" && c.Run != nil {
			t.Error("/memory should dispatch through the registry")
		}
	}
}

func TestPaletteWindow(t *testing.T) {
	cases := []struct{ n, sel, rows, lo, hi int }{
		{5, 0, 10, 0, 5},
		{60, 0, 10, 0, 10},
		{60, 30, 10, 25, 35},
		{60, 59, 10, 50, 60},
	}
	for _, c := range cases {
		lo, hi := paletteWindow(c.n, c.sel, c.rows)
		if lo != c.lo || hi != c.hi {
			t.Errorf("paletteWindow(%d,%d,%d) = %d,%d; want %d,%d", c.n, c.sel, c.rows, lo, hi, c.lo, c.hi)
		}
	}
}

// TestRegistryDispatch runs a registry command end to end through the Model:
// runCommand hands back a command, its message lands in Update, and the
// markdown output is shown as a system block.
func TestRegistryDispatch(t *testing.T) {
	m := parityModel(t)
	if err := m.db.PutMemory(t.Context(), &store.Memory{Scope: "global", Key: "editor", Content: "uses helix"}); err != nil {
		t.Fatal(err)
	}
	_, cmd := m.runCommand("/memory")
	if cmd == nil {
		t.Fatal("/memory returned no command")
	}
	msg, ok := cmd().(cmdResultMsg)
	if !ok {
		t.Fatalf("expected cmdResultMsg, got %T", cmd())
	}
	m.Update(msg)
	got := lastBlock(m)
	if got.kind != blockSystem || !got.markdown || !strings.Contains(got.text, "uses helix") {
		t.Fatalf("unexpected block %+v", got)
	}

	// An unknown command is answered in place, without a round trip.
	if _, cmd := m.runCommand("/nope"); cmd != nil {
		t.Error("unknown command should not dispatch")
	}
	if !strings.Contains(lastBlock(m).text, "Unknown command /nope") {
		t.Errorf("unknown command message missing: %q", lastBlock(m).text)
	}
}

// TestApplyAction covers the action→effect mapping.
func TestApplyAction(t *testing.T) {
	m := parityModel(t)
	m.sessionID, m.title, m.role, m.projectDir = "ses_1", "t", "coder", "/tmp"
	m.attachments = []attachment{{name: "a.txt"}}
	m.blocks = []block{{kind: blockUser, text: "hi"}}

	if quit, _ := m.applyAction(commands.Action{Kind: "clear"}); quit || m.sessionID != "ses_1" || len(m.blocks) != 0 {
		t.Errorf("clear should wipe the screen and keep the session: %q %d", m.sessionID, len(m.blocks))
	}
	m.applyAction(commands.Action{Kind: "new"})
	if m.sessionID != "" || m.title != "" || m.role != "" || m.projectDir != "" || m.attachments != nil {
		t.Errorf("new should reset session state: %+v", m)
	}
	if quit, _ := m.applyAction(commands.Action{Kind: "quit"}); !quit {
		t.Error("quit should quit")
	}
	m.applyAction(commands.Action{Kind: "role-changed", Value: "researcher"})
	if m.role != "researcher" {
		t.Errorf("role-changed not applied: %q", m.role)
	}
	m.applyAction(commands.Action{Kind: "setup"})
	if !strings.Contains(lastBlock(m).text, "antares setup") {
		t.Errorf("setup should point at `antares setup`: %q", lastBlock(m).text)
	}
	m.applyAction(commands.Action{Kind: "copy"})
	if !strings.Contains(lastBlock(m).text, "Nothing to copy") {
		t.Errorf("copy with no reply: %q", lastBlock(m).text)
	}
	m.applyAction(commands.Action{Kind: "retry"})
	if !strings.Contains(lastBlock(m).text, "Nothing to resend") {
		t.Errorf("retry with no message: %q", lastBlock(m).text)
	}
	if _, cmd := m.applyAction(commands.Action{Kind: "resume", Value: "ses_x"}); cmd == nil {
		t.Error("resume should load the session")
	}
	n := len(m.blocks)
	if quit, cmd := m.applyAction(commands.Action{Kind: "from-the-future"}); quit || cmd != nil || len(m.blocks) != n {
		t.Error("an unknown action must be ignored")
	}
}

// TestResumeLoadsTranscript drives resume from the registry through to the
// rebuilt transcript, including the session's stored role.
func TestResumeLoadsTranscript(t *testing.T) {
	m := parityModel(t)
	ctx := t.Context()
	sess := &store.Session{ID: "ses_resume_1", Title: "Earlier", Meta: map[string]any{"project_dir": "/src/app"}}
	if err := m.db.CreateSession(ctx, sess); err != nil {
		t.Fatal(err)
	}
	for _, msg := range []store.Message{
		{ID: "m1", SessionID: sess.ID, Role: store.RoleUser, Content: "question"},
		{ID: "m2", SessionID: sess.ID, Role: store.RoleUser, Content: "injected", Hidden: true},
		{ID: "m3", SessionID: sess.ID, Role: store.RoleAssistant, Content: "answer"},
	} {
		msg := msg
		if err := m.db.AppendMessage(ctx, &msg); err != nil {
			t.Fatal(err)
		}
	}
	_ = m.db.SetKV(ctx, "role:"+sess.ID, "researcher")

	_, cmd := m.runCommand("/resume ses_resume")
	res := cmd().(cmdResultMsg)
	_, load := m.applyCommandResult(res)
	if load == nil {
		t.Fatal("resume produced no load")
	}
	m.Update(load())
	if m.sessionID != sess.ID || m.title != "Earlier" || m.role != "researcher" || m.projectDir != "/src/app" {
		t.Fatalf("session not adopted: id=%q title=%q role=%q project=%q", m.sessionID, m.title, m.role, m.projectDir)
	}
	// The transcript skips the hidden message; the command's own output
	// follows it rather than being wiped by the reload.
	if len(m.blocks) != 3 || m.blocks[0].text != "question" || m.blocks[1].text != "answer" ||
		!strings.Contains(m.blocks[2].text, "Resuming") {
		t.Fatalf("unexpected transcript after resume: %+v", m.blocks)
	}
}

// TestSessionChangedKeepsOutput guards the reload a session-changed action
// triggers: rebuilding the transcript from the store must not wipe the
// command's own output ("Renamed to …").
func TestSessionChangedKeepsOutput(t *testing.T) {
	m := parityModel(t)
	if err := m.db.CreateSession(t.Context(), &store.Session{ID: "ses_t", Title: "Old"}); err != nil {
		t.Fatal(err)
	}
	m.sessionID = "ses_t"
	_, cmd := m.runCommand("/title New name")
	_, load := m.applyCommandResult(cmd().(cmdResultMsg))
	if load == nil {
		t.Fatal("session-changed should reload the session")
	}
	m.Update(load())
	if m.title != "New name" {
		t.Errorf("title = %q", m.title)
	}
	if b := lastBlock(m); !strings.Contains(b.text, "Renamed to New name") {
		t.Errorf("output lost across the reload: %+v", m.blocks)
	}
}

// TestBatchedRunesAreText guards input that arrives several runes at once: a
// message whose runes spell "up" must be typed, not read as the cursor key.
func TestBatchedRunesAreText(t *testing.T) {
	m := parityModel(t)
	m.ta.Focus()
	for _, chunk := range []string{"/goal wrap ", "up", " now"} {
		m.onKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(chunk)})
	}
	if got := m.ta.Value(); got != "/goal wrap up now" {
		t.Errorf("composer = %q, want the text as typed", got)
	}
}

func TestResolveAskAnswer(t *testing.T) {
	one := &pendingAsk{questions: []agent.AskQuestion{{Question: "Which?", Options: []string{"red", "green", "blue"}}}}
	multi := &pendingAsk{questions: []agent.AskQuestion{{Question: "Which?", Options: []string{"red", "green", "blue"}, MultiSelect: true}}}
	cases := []struct {
		a        *pendingAsk
		in, want string
	}{
		{one, "2", "green"},
		{one, " 3 ", "blue"},
		{one, "9", "9"},
		{one, "purple", "purple"},
		{one, "1,3", "1,3"}, // several picks only for a multi-select
		{multi, "1,3", "red, blue"},
		{&pendingAsk{questions: []agent.AskQuestion{{Question: "Name?"}}}, "2", "2"},
	}
	for _, c := range cases {
		if got := resolveAskAnswer(c.a, c.in); got != c.want {
			t.Errorf("resolveAskAnswer(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestAskStaging walks the ask state machine through the key handler: the
// event stages it, Esc puts it aside, /answer re-shows it, and Enter answers.
func TestAskStaging(t *testing.T) {
	m := parityModel(t)
	m.busy = true
	m.applyEvent(agent.Event{Type: agent.EventAsk, ID: "ask_1",
		Content: `{"id":"ask_1","questions":[{"question":"Deploy where?","options":["staging","prod"]}]}`})
	if m.ask == nil || m.ask.id != "ask_1" || !strings.Contains(lastBlock(m).text, "1) staging") {
		t.Fatalf("ask not staged: %+v / %q", m.ask, lastBlock(m).text)
	}

	m.onKey(tea.KeyMsg{Type: tea.KeyEsc})
	if m.ask == nil || !m.ask.aside || !strings.Contains(lastBlock(m).text, "/answer") {
		t.Fatalf("Esc should put the question aside and say how to answer: %q", lastBlock(m).text)
	}

	// Aside, plain text is a normal message again — and busy blocks it.
	m.ta.SetValue("hello")
	m.onKey(tea.KeyMsg{Type: tea.KeyEnter})
	if m.ask == nil {
		t.Fatal("text typed while the question is aside must not answer it")
	}

	m.ta.SetValue("/answer")
	m.onKey(tea.KeyMsg{Type: tea.KeyEnter})
	if m.ask == nil || m.ask.aside {
		t.Fatal("/answer with no text should bring the question back")
	}

	m.ta.SetValue("2")
	m.onKey(tea.KeyMsg{Type: tea.KeyEnter})
	if m.ask != nil {
		t.Fatal("Enter should answer the question")
	}
	// The user's answer is shown, then — since nothing is really waiting in
	// this test — the notice that the question is gone.
	if b := m.blocks[len(m.blocks)-2]; b.kind != blockUser || b.text != "prod" {
		t.Errorf("answer block = %+v, want the picked option", b)
	}
	if !strings.Contains(lastBlock(m).text, "no longer waiting") {
		t.Errorf("expected the stale-ask notice, got %q", lastBlock(m).text)
	}
}

// TestApprovalQueue checks several approvals are decided oldest first and
// that y / n / Esc each settle one.
func TestApprovalQueue(t *testing.T) {
	m := parityModel(t)
	m.busy = true
	for _, id := range []string{"apr_1", "apr_2", "apr_3"} {
		m.applyEvent(agent.Event{Type: agent.EventApproval, ID: id, Name: "terminal",
			Arguments: `{"command":"rm -rf build"}`, Content: `{"reason":"it deletes a whole directory tree"}`})
	}
	if len(m.approvals) != 3 {
		t.Fatalf("want 3 queued, got %d", len(m.approvals))
	}
	prompt := lastBlock(m).text
	if !strings.Contains(prompt, "Allow terminal?") || !strings.Contains(prompt, "rm -rf build") ||
		!strings.Contains(prompt, "whole directory tree") {
		t.Errorf("prompt should name the tool, the command and the risk: %q", prompt)
	}
	// Only the head is shown until it is decided.
	if n := strings.Count(blocksText(m), "Allow terminal?"); n != 1 {
		t.Errorf("only the first request should be on screen, saw %d", n)
	}

	m.onKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	if len(m.approvals) != 2 || m.approvals[0].id != "apr_2" {
		t.Fatalf("y should settle apr_1: %+v", m.approvals)
	}
	m.onKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	m.onKey(tea.KeyMsg{Type: tea.KeyEsc})
	if len(m.approvals) != 0 {
		t.Fatalf("n and Esc should settle the rest: %+v", m.approvals)
	}
	if m.ta.Value() != "" {
		t.Errorf("approval keys must not reach the composer: %q", m.ta.Value())
	}

	// A finished turn drops whatever is still staged.
	m.queueApproval(pendingApproval{id: "apr_4", tool: "write_file"})
	m.Update(doneMsg{})
	if len(m.approvals) != 0 || m.ask != nil {
		t.Error("doneMsg should clear staged pauses")
	}
}

func blocksText(m *Model) string {
	var b strings.Builder
	for _, bl := range m.blocks {
		b.WriteString(bl.text + "\n")
	}
	return b.String()
}

func TestBuildMessage(t *testing.T) {
	img := llm.Part{Type: "image", MimeType: "image/png", Data: "AAAA"}
	atts := []attachment{
		{name: "shot.png", image: true, part: img},
		{name: "spec.pdf", path: filepath.Join("misc", "spec.pdf")},
		{name: "notes.txt", path: filepath.Join("misc", "notes.txt")},
	}
	msg, images := buildMessage("summarise", atts)
	want := "summarise\n\nAttached file(s) — read each with the read_document tool before answering:\n" +
		"- spec.pdf (path: misc/spec.pdf)\n- notes.txt (path: misc/notes.txt)"
	if msg != want {
		t.Errorf("message =\n%q\nwant\n%q", msg, want)
	}
	if len(images) != 1 || images[0].MimeType != "image/png" {
		t.Errorf("images = %+v", images)
	}
	if msg, _ := buildMessage("", atts[1:2]); !strings.HasPrefix(msg, "Attached file(s)") {
		t.Errorf("attachment-only message should be the note alone: %q", msg)
	}
	if msg, images := buildMessage("plain", nil); msg != "plain" || images != nil {
		t.Errorf("no attachments should pass through: %q %v", msg, images)
	}
}

// TestPrepareAttachment checks images become inline parts and documents are
// copied under the uploads directory where read_document looks.
func TestPrepareAttachment(t *testing.T) {
	t.Setenv("ANTARES_HOME", t.TempDir())
	dir := t.TempDir()
	png := filepath.Join(dir, "shot.png")
	// A 1x1 PNG header is enough for the extension check.
	if err := os.WriteFile(png, []byte("\x89PNG\r\n\x1a\nrest"), 0o644); err != nil {
		t.Fatal(err)
	}
	doc := filepath.Join(dir, "my notes.txt")
	if err := os.WriteFile(doc, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}

	a, err := prepareAttachment(png, "")
	if err != nil || !a.image || a.part.MimeType != "image/png" || a.part.Data == "" {
		t.Fatalf("image attachment = %+v, %v", a, err)
	}
	d, err := prepareAttachment(doc, "ses_1")
	if err != nil || d.image {
		t.Fatalf("document attachment = %+v, %v", d, err)
	}
	if d.name != "my_notes.txt" || d.path != filepath.Join("ses_1", "my_notes.txt") {
		t.Errorf("document named %q at %q", d.name, d.path)
	}
	if b, err := os.ReadFile(filepath.Join(tools.UploadsDir(), d.path)); err != nil || string(b) != "hello" {
		t.Errorf("document not copied into uploads: %v", err)
	}
	if _, err := prepareAttachment(filepath.Join(dir, "missing"), ""); err == nil {
		t.Error("a missing file should fail")
	}
}

// TestTurnRequestOptions checks the per-turn options reach the request, and
// that a project binding is only sent for a new session.
func TestTurnRequestOptions(t *testing.T) {
	m := parityModel(t)
	m.role, m.effort, m.projectDir = "coder", "high", "/src/app"
	req := m.turnRequest("go")
	if req.Role != "coder" || req.ReasoningEffort != "high" || req.ProjectDir != "/src/app" || req.Platform != "tui" {
		t.Errorf("new-session request = %+v", req)
	}
	m.sessionID = "ses_1"
	if req := m.turnRequest("go"); req.ProjectDir != "" || req.SessionID != "ses_1" {
		t.Errorf("an existing session must not re-send the binding: %+v", req)
	}
}

// TestProjectAndEffort covers the /project and /effort guards.
func TestProjectAndEffort(t *testing.T) {
	m := parityModel(t)
	dir := t.TempDir()
	m.cmdProject(dir)
	if m.projectDir != dir {
		t.Fatalf("project = %q, want %q", m.projectDir, dir)
	}
	m.sessionID = "ses_1"
	m.cmdProject(t.TempDir())
	if m.projectDir != dir || !strings.Contains(lastBlock(m).text, "/new") {
		t.Errorf("an existing session must keep its binding: %q", lastBlock(m).text)
	}

	m.cmdEffort("high")
	if m.effort != "high" {
		t.Errorf("effort = %q", m.effort)
	}
	m.cmdEffort("ludicrous")
	if m.effort != "high" || !strings.Contains(lastBlock(m).text, "auto") {
		t.Errorf("an unknown level must be refused: %q", lastBlock(m).text)
	}
	m.cmdEffort("auto")
	if m.effort != "" {
		t.Errorf("auto should clear the override, got %q", m.effort)
	}
	m.cmdEffort("")
	if !m.picker.active || len(m.picker.items) < 2 {
		t.Error("/effort with no argument should open the picker")
	}
}

// TestDeleteConfirm stages /delete and checks only "y" commits it.
func TestDeleteConfirm(t *testing.T) {
	m := parityModel(t)
	if err := m.db.CreateSession(t.Context(), &store.Session{ID: "ses_del", Title: "Doomed"}); err != nil {
		t.Fatal(err)
	}
	m.sessionID, m.title = "ses_del", "Doomed"

	m.cmdDelete("")
	m.onKey(tea.KeyMsg{Type: tea.KeyEsc})
	if m.confirm != nil || m.sessionID != "ses_del" || !strings.Contains(lastBlock(m).text, "cancelled") {
		t.Fatal("Esc should cancel the delete")
	}

	m.cmdDelete("")
	_, cmd := m.onKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	if cmd == nil {
		t.Fatal("y should run the delete")
	}
	m.Update(cmd())
	if m.sessionID != "" {
		t.Errorf("after delete the TUI should be on a new session, still %q", m.sessionID)
	}
	if s, err := m.db.GetSession(t.Context(), "ses_del"); err == nil && s != nil {
		t.Error("session still in the store")
	}
}
