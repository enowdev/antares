package tools

import (
	"context"
	"testing"
)

func TestRegistryReplacePreservesUnrelatedTools(t *testing.T) {
	r := NewRegistry()
	r.Register(namedTestTool("native"))
	r.Register(namedTestTool("mcp__old"))

	r.Replace([]string{"mcp__old"}, []Tool{namedTestTool("mcp__new")})
	if _, ok := r.Get("native"); !ok {
		t.Fatal("Replace removed an unrelated native tool")
	}
	if _, ok := r.Get("mcp__old"); ok {
		t.Fatal("Replace retained an old tool")
	}
	if _, ok := r.Get("mcp__new"); !ok {
		t.Fatal("Replace did not add the replacement tool")
	}
}

type namedTestTool string

func (t namedTestTool) Name() string                        { return string(t) }
func (namedTestTool) Description() string                   { return "test" }
func (namedTestTool) Schema() map[string]any                { return map[string]any{} }
func (namedTestTool) Execute(context.Context, Input) Result { return Text("ok") }
