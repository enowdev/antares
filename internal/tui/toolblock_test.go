package tui

import (
	"strings"
	"testing"

	"github.com/enowdev/antares/internal/agent"
	"github.com/enowdev/antares/internal/config"
	"github.com/enowdev/antares/internal/store"
)

// Tools in one batch are announced together and report in any order. A
// refused second call reporting first must mark that call, not the first one,
// or the refused tool shows a success tick and the other a cross.
func TestToolResultsMatchTheirCall(t *testing.T) {
	m := &Model{cfg: &config.Config{}, themeName: "antares", st: newStyles(themeByName("antares"))}
	m.applyEvent(agent.Event{Type: agent.EventToolCall, ID: "a", Name: "read_file", Arguments: `{"path":"x"}`})
	m.applyEvent(agent.Event{Type: agent.EventToolCall, ID: "b", Name: "write_file", Arguments: `{"path":"y"}`})
	m.applyEvent(agent.Event{Type: agent.EventToolProgress, ID: "a", Chunk: "reading"})
	m.applyEvent(agent.Event{Type: agent.EventToolResult, ID: "b", Content: "refused", IsError: true})

	byID := map[string]block{}
	for _, b := range m.blocks {
		if b.kind == blockTool {
			byID[b.callID] = b
		}
	}
	if len(byID) != 2 {
		t.Fatalf("want two tool blocks, got %d: %+v", len(byID), m.blocks)
	}
	if b := byID["b"]; !b.done || !b.isError || b.text != "refused" {
		t.Fatalf("refused call b = %+v, want done with an error", b)
	}
	if a := byID["a"]; a.done || a.text != "reading" {
		t.Fatalf("call a = %+v, want still running with its own progress", a)
	}

	m.applyEvent(agent.Event{Type: agent.EventToolResult, ID: "a", Content: "ok"})
	for _, b := range m.blocks {
		if b.callID == "a" && (!b.done || b.isError || b.text != "ok") {
			t.Fatalf("call a after its result = %+v", b)
		}
		if b.callID == "b" && (!b.isError || b.text != "refused") {
			t.Fatalf("call b was overwritten: %+v", b)
		}
	}
}

func TestBlocksFromMessagesCarriesToolArgs(t *testing.T) {
	msgs := []store.Message{
		{Role: store.RoleAssistant, ToolCalls: `[{"id":"c1","name":"read_file","arguments":"{\"path\":\"main.go\"}"}]`},
		{Role: store.RoleTool, ToolCallID: "c1", ToolName: "read_file", Content: "package main"},
	}
	bs := blocksFromMessages(msgs, false)
	if len(bs) != 1 || bs[0].kind != blockTool {
		t.Fatalf("blocks = %+v", bs)
	}
	if bs[0].name != "read_file" || !strings.Contains(bs[0].args, "main.go") {
		t.Fatalf("tool block lost its call: name=%q args=%q", bs[0].name, bs[0].args)
	}
}
