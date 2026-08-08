package agent

import (
	"context"
	"testing"

	"github.com/enowdev/antares/internal/llm"
	"github.com/enowdev/antares/internal/tools"
)

type stubTool struct{ name string }

func (s stubTool) Name() string           { return s.name }
func (s stubTool) Description() string    { return s.name }
func (s stubTool) Schema() map[string]any { return map[string]any{} }
func (s stubTool) Execute(context.Context, tools.Input) tools.Result {
	return tools.Text("ok")
}

func TestSanitizeToolsRenamesWebSearchForAntigravity(t *testing.T) {
	ws := stubTool{name: "web_search"}
	specs := []llm.Tool{
		{Name: "web_search", Description: "search"},
		{Name: "read_file", Description: "read"},
	}
	byName := map[string]tools.Tool{"web_search": ws, "read_file": stubTool{name: "read_file"}}

	out, mapped := sanitizeToolsForProvider(specs, byName, "antigravity", "http://localhost:8080/antigravity/v1")
	if out[0].Name != "search_web" {
		t.Fatalf("wire name = %q, want search_web", out[0].Name)
	}
	if out[1].Name != "read_file" {
		t.Fatalf("read_file must stay")
	}
	if _, ok := mapped["search_web"]; !ok {
		t.Fatal("search_web must map back to a tool")
	}
	if mapped["search_web"].Name() != "web_search" {
		t.Fatalf("alias should resolve to web_search tool, got %q", mapped["search_web"].Name())
	}
}

func TestSanitizeToolsLeavesOthersAlone(t *testing.T) {
	specs := []llm.Tool{{Name: "web_search"}}
	byName := map[string]tools.Tool{"web_search": stubTool{name: "web_search"}}
	out, _ := sanitizeToolsForProvider(specs, byName, "openai", "https://api.openai.com/v1")
	if out[0].Name != "web_search" {
		t.Fatalf("non-antigravity must keep web_search, got %q", out[0].Name)
	}
}

func TestIsAntigravityRoute(t *testing.T) {
	if !isAntigravityRoute("antigravity", "http://x") {
		t.Fatal("provider id")
	}
	if !isAntigravityRoute("gemini", "http://localhost:8080/antigravity/v1beta") {
		t.Fatal("base url")
	}
	if isAntigravityRoute("custom", "http://localhost:8080/v1") {
		t.Fatal("codebuddy must not match")
	}
}
