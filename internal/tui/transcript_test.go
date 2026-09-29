package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/enowdev/antares/internal/agent"
	"github.com/enowdev/antares/internal/config"
)

func transcriptModel(w int) *Model {
	m := &Model{cfg: &config.Config{}, themeName: "antares", st: newStyles(themeByName("antares")), showReasoning: true}
	m.vp = viewport.New(w, 40)
	return m
}

// fixedClock makes thought and call durations deterministic.
func fixedClock(t *testing.T) func(time.Duration) {
	clock := time.Unix(1000, 0)
	prev := now
	now = func() time.Time { return clock }
	t.Cleanup(func() { now = prev })
	return func(d time.Duration) { clock = clock.Add(d) }
}

// withColour renders real escape sequences for the test, so widths and
// attributes are measured on what a terminal gets.
func withColour(t *testing.T) {
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(prev) })
}

func plainLines(m *Model) []string {
	return strings.Split(stripANSITest(m.renderBlocks()), "\n")
}

func call(m *Model, id, name, args, result string, isErr bool) {
	m.applyEvent(agent.Event{Type: agent.EventToolCall, ID: id, Name: name, Arguments: args})
	m.applyEvent(agent.Event{Type: agent.EventToolResult, ID: id, Content: result, IsError: isErr})
}

func TestMarkerAndTextColumns(t *testing.T) {
	m := transcriptModel(60)
	m.blocks = []block{
		{kind: blockUser, text: "hello there"},
		{kind: blockAssistant, text: "An answer.", done: true},
		{kind: blockSystem, text: "a notice from a command"},
	}
	got := plainLines(m)
	want := []string{"▌ hello there", "", "  An answer.", "", "  a notice from a command"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("columns:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestUserMessageWrapsUnderItsMarker(t *testing.T) {
	m := transcriptModel(20)
	m.blocks = []block{{kind: blockUser, text: "one two three four five six seven"}}
	for _, l := range plainLines(m) {
		if !strings.HasPrefix(l, "▌ ") {
			t.Fatalf("row %q lost the marker", l)
		}
	}
}

func TestClosedToolRowColumns(t *testing.T) {
	const w = 70
	m := transcriptModel(w)
	call(m, "1", "write_file", `{"path":"docs/grid.md","content":"a\nb\nc\n"}`, "Created docs/grid.md (6 bytes, 3 lines)", false)
	call(m, "2", "todo_nothing", `{}`, "", false)
	lines := plainLines(m)
	if len(lines) != 2 {
		t.Fatalf("want two packed rows, got %q", lines)
	}
	row := lines[0]
	if lipgloss.Width(row) != w {
		t.Fatalf("row width %d, want %d: %q", lipgloss.Width(row), w, row)
	}
	if !strings.HasPrefix(row, "✓ write       docs/grid.md") {
		t.Fatalf("marker/verb/target columns wrong: %q", row)
	}
	if !strings.HasSuffix(row, "new · 3 lines ▸") {
		t.Fatalf("metric not flush right before the chevron: %q", row)
	}
	// A row with nothing to open keeps the chevron column, blank.
	empty := lines[1]
	if lipgloss.Width(empty) != w || !strings.HasSuffix(empty, "  ") {
		t.Fatalf("chevron column not kept blank: %q", empty)
	}
	if []rune(row)[len([]rune(row))-1] != '▸' {
		t.Fatal("chevron is not on the last column")
	}
}

func TestOpenedFrameAndFolding(t *testing.T) {
	const w = 60
	m := transcriptModel(w)
	var out strings.Builder
	out.WriteString("Exit code 2\n\n")
	for i := 0; i < 20; i++ {
		fmt.Fprintf(&out, "line %d\n", i)
	}
	call(m, "1", "terminal", `{"command":"make build"}`, out.String(), true)
	lines := plainLines(m)
	top := lines[0]
	if !strings.HasPrefix(top, "╭─ ✗ shell  make build ─") || !strings.HasSuffix(top, "20 lines · exit 2 ▾ ─╮") {
		t.Fatalf("frame top: %q", top)
	}
	body := lines[1 : len(lines)-1]
	if len(body) != bodyPreview+1 {
		t.Fatalf("body rows = %d, want %d folded", len(body), bodyPreview+1)
	}
	for _, l := range lines {
		if lipgloss.Width(l) != w {
			t.Fatalf("frame row width %d != %d: %q", lipgloss.Width(l), w, l)
		}
	}
	for _, l := range body {
		if !strings.HasPrefix(l, "│ ") || !strings.HasSuffix(l, " │") {
			t.Fatalf("body row not walled: %q", l)
		}
	}
	if !strings.HasPrefix(body[0], "│ line 0") {
		t.Fatalf("body not on the text column: %q", body[0])
	}
	if !strings.Contains(body[len(body)-1], "┈ 8 more lines") {
		t.Fatalf("fold row: %q", body[len(body)-1])
	}
	bottom := lines[len(lines)-1]
	if !strings.HasPrefix(bottom, "╰─") || !strings.HasSuffix(bottom, "─╯") {
		t.Fatalf("frame bottom: %q", bottom)
	}
}

func TestShortBodyDoesNotFold(t *testing.T) {
	m := transcriptModel(60)
	call(m, "1", "read_file", `{"path":"a.go"}`, strings.Repeat("x\n", bodyPreview+1), false)
	m.blocks[0].open = true
	if s := m.renderBlocks(); strings.Contains(s, "more lines") {
		t.Fatalf("a body one row over the preview folded:\n%s", stripANSITest(s))
	}
}

func TestFailureOpensAndCtrlOToggles(t *testing.T) {
	m := transcriptModel(60)
	call(m, "1", "read_file", `{"path":"a.go"}`, "package a", false)
	call(m, "2", "read_file", `{"path":"b.go"}`, "no such file", true)
	if m.blocks[0].open || !m.blocks[1].open {
		t.Fatalf("defaults: ok closed, failed open; got %v %v", m.blocks[0].open, m.blocks[1].open)
	}
	m.toggleOpen()
	if !m.blocks[0].open || !m.blocks[1].open {
		t.Fatal("Ctrl+O with a closed row should open everything")
	}
	m.toggleOpen()
	if m.blocks[0].open || m.blocks[1].open {
		t.Fatal("Ctrl+O with everything open should close everything")
	}
}

func TestEditRowShowsDiffCounts(t *testing.T) {
	m := transcriptModel(70)
	call(m, "1", "edit_file", `{"path":"x.go","old_string":"a\nb\nc","new_string":"a\nB\nc\nd"}`, "Edited x.go", false)
	lines := plainLines(m)
	if !strings.HasSuffix(lines[0], "+2 -1 ▸") {
		t.Fatalf("edit metric: %q", lines[0])
	}
	m.blocks[0].open = true
	lines = plainLines(m)
	got := strings.Join(lines, "\n")
	for _, want := range []string{"│   a", "│ - b", "│ + B", "│ + d"} {
		if !strings.Contains(got, want) {
			t.Fatalf("diff missing %q:\n%s", want, got)
		}
	}
	if strings.Index(got, "- b") > strings.Index(got, "+ B") {
		t.Fatal("a replacement should read deletion first")
	}
}

func TestReadOnlyRunCollapses(t *testing.T) {
	m := transcriptModel(80)
	call(m, "1", "read_file", `{"path":"a.go"}`, "a", false)
	m.applyEvent(agent.Event{Type: agent.EventReasoning, Delta: "Now grep."})
	call(m, "2", "grep", `{"pattern":"x"}`, "a.go:1:x", false)
	call(m, "3", "read_file", `{"path":"b.go"}`, "b", false)
	lines := plainLines(m)
	if len(lines) != 1 {
		t.Fatalf("a run of reads should be one row, got:\n%s", strings.Join(lines, "\n"))
	}
	if !strings.HasPrefix(lines[0], "✓ 3 calls     read ×2 · grep") || !strings.HasSuffix(lines[0], "▸") {
		t.Fatalf("group row: %q", lines[0])
	}
	// Opened, the run's calls (and the thought) sit under its row.
	m.blocks[0].groupOpen = true
	lines = plainLines(m)
	if len(lines) != 5 || !strings.HasSuffix(lines[0], "▾") || !strings.HasPrefix(lines[1], "  ✓ read") || !strings.HasPrefix(lines[2], "  ✻ Thought") {
		t.Fatalf("opened run:\n%s", strings.Join(lines, "\n"))
	}
}

func TestRunBreakers(t *testing.T) {
	cases := map[string]func(m *Model){
		"write": func(m *Model) {
			call(m, "w", "write_file", `{"path":"c.go","content":"x"}`, "Created c.go", false)
		},
		"failure": func(m *Model) {
			call(m, "f", "read_file", `{"path":"nope"}`, "no such file", true)
		},
		"running": func(m *Model) {
			m.applyEvent(agent.Event{Type: agent.EventToolCall, ID: "r", Name: "grep", Arguments: `{"pattern":"y"}`})
		},
		"text": func(m *Model) {
			m.applyEvent(agent.Event{Type: agent.EventText, Delta: "Found it."})
			m.closeStreaming()
		},
		"non-zero exit": func(m *Model) {
			call(m, "t", "terminal", `{"command":"false"}`, "Exit code 1\n\n", true)
		},
	}
	for name, breaker := range cases {
		t.Run(name, func(t *testing.T) {
			m := transcriptModel(80)
			call(m, "1", "read_file", `{"path":"a.go"}`, "a", false)
			breaker(m)
			call(m, "2", "read_file", `{"path":"b.go"}`, "b", false)
			for _, r := range groupRoles(m.blocks) {
				if r.kind != roleAlone {
					t.Fatalf("a %s between two reads must not make a run: %+v", name, groupRoles(m.blocks))
				}
			}
		})
	}
}

func TestThinkingRowStates(t *testing.T) {
	withColour(t)
	advance := fixedClock(t)
	m := transcriptModel(60)
	m.applyEvent(agent.Event{Type: agent.EventReasoning, Delta: "Check the file first. But wait"})
	lines := plainLines(m)
	if len(lines) != 1 || !strings.HasPrefix(lines[0], "✻ Thinking… Check the file first ") {
		t.Fatalf("live thought: %q", lines)
	}
	if lipgloss.Width(lines[0]) != 60 || !strings.HasSuffix(lines[0], "▸") {
		t.Fatalf("live thought row: %q", lines[0])
	}
	advance(6 * time.Second)
	m.applyEvent(agent.Event{Type: agent.EventText, Delta: "Done."})
	lines = plainLines(m)
	if !strings.HasPrefix(lines[0], "✻ Thought for 6s") {
		t.Fatalf("finished thought: %q", lines[0])
	}
	if lines[1] != "" || lines[2] != "  Done." {
		t.Fatalf("a blank row should separate the list from the reply: %q", lines)
	}
	// Opened: framed, not italic.
	m.blocks[0].open = true
	lines = plainLines(m)
	if !strings.HasPrefix(lines[0], "╭─ ✻ Thought for 6s ─") || !strings.HasPrefix(lines[1], "│ Check the file first.") {
		t.Fatalf("opened thought:\n%s", strings.Join(lines, "\n"))
	}
	if !strings.Contains(m.renderBlocks(), "\x1b[") {
		t.Fatal("expected styled output")
	}
	if strings.Contains(m.renderBlocks(), ";3m") || strings.Contains(m.renderBlocks(), "[3m") || strings.Contains(m.renderBlocks(), "[3;") {
		t.Fatal("thought text should not be italic")
	}
	// Reasoning off: nothing at all.
	m.showReasoning = false
	lines = plainLines(m)
	if lines[0] != "  Done." {
		t.Fatalf("reasoning off still shows the thought: %q", lines)
	}
}

func TestNoticesAndErrors(t *testing.T) {
	m := transcriptModel(60)
	m.applyEvent(agent.Event{Type: agent.EventNotice, Message: "provider glitch — retrying"})
	m.applyEvent(agent.Event{Type: agent.EventNotice, Message: "provider glitch — retrying"})
	m.applyEvent(agent.Event{Type: agent.EventError, Err: "boom"})
	got := strings.Join(plainLines(m), "\n")
	want := "↻ retry 2\n  provider glitch — retrying\n\n✗ error\n  boom"
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestNoLineExceedsWidth(t *testing.T) {
	withColour(t)
	advance := fixedClock(t)
	for w := 20; w <= 120; w++ {
		m := transcriptModel(w)
		m.blocks = append(m.blocks, block{kind: blockUser, text: "a very long user message with a path/that/is/really/long/and/keeps/going.go and 中文字符 words"})
		m.applyEvent(agent.Event{Type: agent.EventReasoning, Delta: "Thinking about a rather long sentence that should be trimmed on narrow rows."})
		advance(3 * time.Second)
		call(m, "1", "read_file", `{"path":"internal/some/deeply/nested/path/file_with_a_long_name.go"}`, "x", false)
		call(m, "2", "grep", `{"pattern":"a pattern that is long","path":"internal"}`, "a\nb", false)
		call(m, "3", "edit_file", `{"path":"internal/x.go","old_string":"a","new_string":"`+strings.Repeat("b", 200)+`"}`, "ok", false)
		call(m, "4", "terminal", `{"command":"go test ./... && go vet ./... && echo done"}`, "Exit code 1\n\n"+strings.Repeat("output line that is long enough to cut\n", 30), true)
		call(m, "5", "mcp_server_with_a_very_long_tool_name", `{"query":"q"}`, "{}", false)
		m.applyEvent(agent.Event{Type: agent.EventText, Delta: "# Title\n\nParagraph with `code` and **bold** and a link [here](http://x).\n\n- item one that wraps around the row\n  - nested\n\n1. first\n\n> quote that is long enough to wrap onto another row\n\n```python\ndef f():\n    return 'a very long string literal that must soft wrap inside the block'\n```\n\n| Agent | Role | Cost |\n|---|:---:|---:|\n| router | Reads the request and picks who handles it | $0.001 |\n\n---\n\nsupercalifragilisticexpialidocious_and_more_and_more_and_more"})
		m.closeStreaming()
		m.applyEvent(agent.Event{Type: agent.EventNotice, Message: "tool-call limit reached with 2 task(s) still open — continuing (1/3)"})
		m.applyEvent(agent.Event{Type: agent.EventError, Err: "provider returned 503: service unavailable, try again later"})
		m.pushOutput("**Commands**\n\n- `/help` — show help")
		for pass := 0; pass < 2; pass++ { // second pass reads the cache
			if pass == 1 {
				m.toggleOpen()
			}
			for _, l := range strings.Split(m.renderBlocks(), "\n") {
				if lw := lipgloss.Width(l); lw > w {
					t.Fatalf("width %d: row is %d wide: %q", w, lw, stripANSITest(l))
				}
			}
		}
	}
}

func TestSettledBlocksAreCached(t *testing.T) {
	m := transcriptModel(60)
	m.blocks = []block{{kind: blockAssistant, text: "settled", done: true}}
	m.renderBlocks()
	if len(m.cache) != 1 {
		t.Fatalf("settled block not cached: %d entries", len(m.cache))
	}
	m.applyEvent(agent.Event{Type: agent.EventText, Delta: "streaming"})
	m.renderBlocks()
	m.renderBlocks()
	if len(m.cache) != 1 {
		t.Fatalf("a streaming block should not fill the cache: %d entries", len(m.cache))
	}
}
