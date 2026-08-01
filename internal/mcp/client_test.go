package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"testing"
	"time"
)

// TestStdioRoundTrip runs this test binary as a fake MCP server (see
// TestHelperServer) and exercises the full handshake, tools/list, and
// tools/call path over stdio.
func TestStdioRoundTrip(t *testing.T) {
	client, err := Connect(context.Background(), "fake", ServerConfig{
		Transport: "stdio",
		Command:   os.Args[0],
		Args:      []string{"-test.run=TestHelperServer"},
		Env:       map[string]string{"ANTARES_MCP_HELPER": "1"},
	})
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer client.Close()

	tools := client.Tools()
	if len(tools) != 1 || tools[0].Name != "echo" {
		t.Fatalf("tools = %+v, want one tool named echo", tools)
	}
	if tools[0].Description == "" {
		t.Error("tool description was not carried through")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	res, err := client.Call(ctx, "echo", map[string]any{"text": "halo"})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected tool error: %s", res.Text)
	}
	if res.Text != "echo: halo" {
		t.Fatalf("text = %q, want %q", res.Text, "echo: halo")
	}
}

func TestStdioReportsToolErrors(t *testing.T) {
	client, err := Connect(context.Background(), "fake", ServerConfig{
		Transport: "stdio",
		Command:   os.Args[0],
		Args:      []string{"-test.run=TestHelperServer"},
		Env:       map[string]string{"ANTARES_MCP_HELPER": "1"},
	})
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer client.Close()

	res, err := client.Call(context.Background(), "boom", nil)
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected the unknown tool to be reported as an error")
	}
}

func TestEmptyToolListEncodesAsArray(t *testing.T) {
	client := &Client{}
	got := client.Tools()
	if got == nil {
		t.Fatal("Tools() returned nil, want a non-nil empty slice")
	}
	payload, err := json.Marshal(struct {
		Tools []ToolDef `json:"tools"`
	}{Tools: got})
	if err != nil {
		t.Fatal(err)
	}
	if string(payload) != `{"tools":[]}` {
		t.Fatalf("empty tool payload = %s, want tools encoded as []", payload)
	}
}

func TestUnknownTransport(t *testing.T) {
	if _, err := Connect(context.Background(), "x", ServerConfig{Transport: "carrier-pigeon"}); err == nil {
		t.Fatal("expected an unknown transport to fail")
	}
}

func TestToolNameNamespacing(t *testing.T) {
	got := mcpToolName("my server", "read/file")
	if got != "mcp__my_server__read_file" {
		t.Fatalf("mcpToolName = %q", got)
	}
}

// TestHelperServer is not a real test: when ANTARES_MCP_HELPER is set it acts
// as a minimal MCP server speaking newline-delimited JSON-RPC on stdio.
func TestHelperServer(t *testing.T) {
	if os.Getenv("ANTARES_MCP_HELPER") != "1" {
		t.Skip("helper process")
	}

	reply := func(id *int64, result any) {
		out := map[string]any{"jsonrpc": "2.0", "id": id, "result": result}
		b, _ := json.Marshal(out)
		os.Stdout.Write(append(b, '\n'))
	}

	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 0, 64*1024), 4<<20)
	for sc.Scan() {
		var req struct {
			ID     *int64          `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if err := json.Unmarshal(sc.Bytes(), &req); err != nil {
			continue
		}
		switch req.Method {
		case "initialize":
			reply(req.ID, map[string]any{
				"protocolVersion": protocolVersion,
				"capabilities":    map[string]any{"tools": map[string]any{}},
				"serverInfo":      map[string]any{"name": "fake-server", "version": "0.0.1"},
			})
		case "notifications/initialized":
			// no response for notifications
		case "tools/list":
			reply(req.ID, map[string]any{
				"tools": []map[string]any{{
					"name":        "echo",
					"description": "Echo the supplied text back.",
					"inputSchema": map[string]any{
						"type":       "object",
						"properties": map[string]any{"text": map[string]any{"type": "string"}},
					},
				}},
			})
		case "tools/call":
			var p struct {
				Name      string         `json:"name"`
				Arguments map[string]any `json:"arguments"`
			}
			_ = json.Unmarshal(req.Params, &p)
			if p.Name != "echo" {
				reply(req.ID, map[string]any{
					"isError": true,
					"content": []map[string]any{{"type": "text", "text": "unknown tool " + p.Name}},
				})
				continue
			}
			text, _ := p.Arguments["text"].(string)
			reply(req.ID, map[string]any{
				"content": []map[string]any{{"type": "text", "text": "echo: " + text}},
			})
		}
	}
}

var _ = exec.Command
