package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/enowdev/antares/internal/commands"
	"github.com/enowdev/antares/internal/store"
)

func TestParseAskArgs(t *testing.T) {
	o, err := parseAskArgs([]string{"--session", "ses_1", "-m", "gpt", "--effort=high", "what", "is", "--role", "coder", "-a", "a.png", "--attach=b.pdf", "-q", "this"})
	if err != nil {
		t.Fatal(err)
	}
	want := askOptions{Session: "ses_1", Model: "gpt", Effort: "high", Role: "coder",
		Attach: []string{"a.png", "b.pdf"}, Quiet: true, Prompt: "what is this"}
	if !reflect.DeepEqual(o, want) {
		t.Fatalf("got %+v\nwant %+v", o, want)
	}

	o, err = parseAskArgs([]string{"--new", "--", "--not-a-flag", "-q"})
	if err != nil || !o.New || o.Prompt != "--not-a-flag -q" || o.Quiet {
		t.Fatalf("-- should end flags: %+v %v", o, err)
	}
	if _, err := parseAskArgs([]string{"--new", "--session", "x"}); err == nil {
		t.Fatal("--new with --session should fail")
	}
	if _, err := parseAskArgs([]string{"--model"}); err == nil {
		t.Fatal("a flag missing its value should fail")
	}
	if _, err := parseAskArgs([]string{"--bogus"}); err == nil {
		t.Fatal("an unknown flag should fail")
	}
	if _, err := parseAskArgs([]string{"-h"}); err != errAskHelp {
		t.Fatalf("-h: %v", err)
	}
}

func TestCombinePrompt(t *testing.T) {
	cases := [][3]string{
		{"", "  piped\n", "piped"},
		{"arg", "", "arg"},
		{"review", "diff body\n", "review\n\ndiff body"},
	}
	for _, c := range cases {
		if got := combinePrompt(c[0], c[1]); got != c[2] {
			t.Errorf("combinePrompt(%q,%q) = %q, want %q", c[0], c[1], got, c[2])
		}
	}
}

func TestParseLogsArgs(t *testing.T) {
	o, err := parseLogsArgs(nil)
	if err != nil || o.Lines != 50 || o.Follow {
		t.Fatalf("defaults: %+v %v", o, err)
	}
	o, err = parseLogsArgs([]string{"-f", "-n", "10"})
	if err != nil || o.Lines != 10 || !o.Follow {
		t.Fatalf("-f -n 10: %+v %v", o, err)
	}
	o, err = parseLogsArgs([]string{"-n5"})
	if err != nil || o.Lines != 5 {
		t.Fatalf("-n5: %+v %v", o, err)
	}
	o, err = parseLogsArgs([]string{"--lines=0", "--path"})
	if err != nil || o.Lines != 0 || !o.Path {
		t.Fatalf("--lines=0: %+v %v", o, err)
	}
	for _, bad := range [][]string{{"-n"}, {"-n", "x"}, {"-n", "-3"}, {"--wat"}} {
		if _, err := parseLogsArgs(bad); err == nil {
			t.Errorf("%v should fail", bad)
		}
	}
}

func TestLastLines(t *testing.T) {
	var b strings.Builder
	for i := 1; i <= 5000; i++ {
		b.WriteString("line ")
		b.WriteString(strings.Repeat("x", i%40))
		b.WriteString("\n")
	}
	data := []byte(b.String())
	all := strings.Split(strings.TrimSuffix(b.String(), "\n"), "\n")

	for _, n := range []int{0, 1, 3, 4999, 5000, 9000} {
		got, err := lastLines(bytes.NewReader(data), int64(len(data)), n)
		if err != nil {
			t.Fatal(err)
		}
		want := ""
		if n > 0 {
			k := n
			if k > len(all) {
				k = len(all)
			}
			want = strings.Join(all[len(all)-k:], "\n") + "\n"
		}
		if string(got) != want {
			t.Errorf("n=%d: got %d bytes, want %d", n, len(got), len(want))
		}
	}

	// No trailing newline: the last partial line counts as a line.
	got, _ := lastLines(strings.NewReader("a\nb\nc"), 5, 2)
	if string(got) != "b\nc" {
		t.Fatalf("partial last line: %q", got)
	}
}

func TestFollowFileSeesAppendsAndTruncation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "daemon.log")
	if err := os.WriteFile(path, []byte("old\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	var out syncBuffer
	done := make(chan error, 1)
	go func() { done <- followFile(ctx, path, 4, &out, 10*time.Millisecond) }()

	appendTo := func(s string) {
		f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = f.WriteString(s)
		f.Close()
	}
	waitFor := func(want string) {
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			if out.String() == want {
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
		t.Fatalf("followed output %q, want %q", out.String(), want)
	}
	appendTo("new 1\n")
	waitFor("new 1\n")
	if err := os.WriteFile(path, []byte("r\n"), 0o600); err != nil { // truncate + rewrite
		t.Fatal(err)
	}
	waitFor("new 1\nr\n")
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestPlainMarkdown(t *testing.T) {
	in := strings.Join([]string{
		"**Usage, last 7 day(s)**",
		"",
		"- `gpt_4o__mini` — 1.2k in / _300_ out · $0.0100",
		"## Heading ##",
		"See [the docs](https://example.com) and snake_case_name stays.",
		"```go",
		"x := **not bold**",
		"```",
		"| Role | Score |",
		"|---|---|",
		"| coder | 9.5 |",
		"| researcher | 10.0 |",
	}, "\n")
	want := strings.Join([]string{
		"Usage, last 7 day(s)",
		"",
		"- gpt_4o__mini — 1.2k in / 300 out · $0.0100",
		"Heading",
		"See the docs (https://example.com) and snake_case_name stays.",
		"x := **not bold**",
		"Role        Score",
		"----------  -----",
		"coder       9.5",
		"researcher  10.0",
	}, "\n") + "\n"
	if got := plainMarkdown(in); got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestTrailingPadAndBackground(t *testing.T) {
	in := "\x1b[1mTotal\x1b[0m\x1b[38;5;252m 5 in\x1b[0m\x1b[38;5;252m \x1b[0m\x1b[38;5;252m \x1b[0m   "
	if got := trailingPad.ReplaceAllString(in, ""); got != "\x1b[1mTotal\x1b[0m\x1b[38;5;252m 5 in" {
		t.Fatalf("%q", got)
	}
	for v, want := range map[string]bool{"": false, "15;0": false, "0;15": true, "0;default;7": true} {
		if lightBackground(v) != want {
			t.Errorf("lightBackground(%q) != %v", v, want)
		}
	}
}

func TestSplitFlags(t *testing.T) {
	flags, words, err := splitFlags([]string{"ses_1", "--json", "-o", "out.md", "--limit=3", "--", "--x"},
		[]string{"--json"}, []string{"-o", "--limit"})
	if err != nil {
		t.Fatal(err)
	}
	if flags["--json"] != "true" || flags["-o"] != "out.md" || flags["--limit"] != "3" {
		t.Fatalf("flags %v", flags)
	}
	if !reflect.DeepEqual(words, []string{"ses_1", "--x"}) {
		t.Fatalf("words %v", words)
	}
	if _, _, err := splitFlags([]string{"--nope"}, nil, nil); err == nil {
		t.Fatal("unknown flag should fail")
	}
	if _, _, err := splitFlags([]string{"-o"}, nil, []string{"-o"}); err == nil {
		t.Fatal("missing value should fail")
	}
}

func TestMatchSessionPrefix(t *testing.T) {
	list := []store.Session{{ID: "ses_abc1"}, {ID: "ses_abc2"}, {ID: "ses_def"}}
	if id, err := matchSessionPrefix(list, "ses_d"); err != nil || id != "ses_def" {
		t.Fatalf("unique prefix: %q %v", id, err)
	}
	if _, err := matchSessionPrefix(list, "ses_abc"); err == nil {
		t.Fatal("ambiguous prefix should fail")
	}
	if _, err := matchSessionPrefix(list, "zzz"); err == nil {
		t.Fatal("no match should fail")
	}
}

func TestSessionMarkdown(t *testing.T) {
	sess := &store.Session{ID: "ses_1", Title: "Plan", Model: "m", CreatedAt: time.Date(2026, 1, 2, 3, 4, 0, 0, time.UTC)}
	msgs := []store.Message{
		{Role: store.RoleUser, Content: "hi"},
		{Role: store.RoleAssistant, Content: ""},
		{Role: store.RoleTool, ToolName: "read_file", Content: "line1\nline2"},
		{Role: store.RoleUser, Content: "secret context", Hidden: true},
		{Role: store.RoleAssistant, Content: "hello"},
	}
	got := sessionMarkdown(sess, msgs)
	for _, want := range []string{"# Plan", "## You\n\nhi", "> `read_file` → line1 line2", "## Antares\n\nhello"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "secret context") {
		t.Error("hidden message leaked into the transcript")
	}
}

func TestEditorCommand(t *testing.T) {
	if got := editorCommand("code -w", "nano", "darwin"); got != "code -w" {
		t.Fatal(got)
	}
	if got := editorCommand(" ", "nano", "linux"); got != "nano" {
		t.Fatal(got)
	}
	if got := editorCommand("", "", "windows"); got != "notepad" {
		t.Fatal(got)
	}
	if got := editorCommand("", "", "linux"); got != "vi" {
		t.Fatal(got)
	}
}

func TestPassthroughTable(t *testing.T) {
	for name := range passthrough {
		spec, ok := commands.Lookup(name)
		if !ok {
			t.Errorf("passthrough %q is not a registry command", name)
			continue
		}
		if spec.Client {
			t.Errorf("passthrough %q is a client-side command", name)
		}
	}
	if passthroughNeeds("mcp", "search github") != needStore || passthroughNeeds("mcp", "") != needRuntime {
		t.Error("mcp catalogue verbs should not need the runtime")
	}
	if passthroughNeeds("skills", "") != needSkills {
		t.Error("skills should load only the skill library")
	}
}

func TestPassthroughRejectsConversationCommands(t *testing.T) {
	handled, err := runPassthrough("undo", nil)
	if !handled || err == nil || !strings.Contains(err.Error(), "inside a conversation") {
		t.Fatalf("undo: handled=%v err=%v", handled, err)
	}
	if handled, _ := runPassthrough("definitely-not-a-command", nil); handled {
		t.Fatal("unknown names must fall through to the usage error")
	}
}

func TestShellHints(t *testing.T) {
	in := "Find more with `/skills search <words>`. Resume with `/resume <id>`, set `/model <id>`, see `/usage 30`."
	want := "Find more with `antares skills search <words>`. Resume with `/resume <id>`, set `antares model <id>`, see `antares usage 30`."
	if got := shellHints(in); got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
}

func TestAttachName(t *testing.T) {
	if got := attachName("/tmp/My Report (final).pdf"); got != "My_Report_final_.pdf" {
		t.Fatal(got)
	}
	if got := attachName("..."); got != "attachment" {
		t.Fatal(got)
	}
}

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}
