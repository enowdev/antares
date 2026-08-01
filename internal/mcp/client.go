// Package mcp speaks the Model Context Protocol, letting Antares borrow tools
// from external servers over stdio or streamable HTTP.
package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/enowdev/antares/internal/version"
)

// protocolVersion is the revision Antares implements.
const protocolVersion = "2024-11-05"

// ToolDef is a tool advertised by an MCP server.
type ToolDef struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

// content is one element of a tool result.
type content struct {
	Type string `json:"type"`
	Text string `json:"text"`
	// Non-text content is summarised rather than inlined.
	MimeType string `json:"mimeType,omitempty"`
	Data     string `json:"data,omitempty"`
}

// CallResult is the outcome of calling an MCP tool.
type CallResult struct {
	Text    string
	IsError bool
}

// rpcRequest is a JSON-RPC 2.0 request.
type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int64  `json:"id,omitempty"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

// rpcResponse is a JSON-RPC 2.0 response.
type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *int64          `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   *rpcError       `json:"error"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

func (e *rpcError) Error() string {
	return fmt.Sprintf("mcp error %d: %s", e.Code, e.Message)
}

// transport carries JSON-RPC frames to one server.
type transport interface {
	send(ctx context.Context, req rpcRequest) (*rpcResponse, error)
	notify(ctx context.Context, req rpcRequest) error
	Close() error
}

// Client is a connection to one MCP server.
type Client struct {
	name      string
	transport transport

	mu       sync.RWMutex
	tools    []ToolDef
	seq      int64
	srvName  string
	toolsOK  bool
	toolsErr string
}

// Connect starts a server and performs the initialise handshake.
func Connect(ctx context.Context, name string, cfg ServerConfig) (*Client, error) {
	var (
		tr  transport
		err error
	)
	switch strings.ToLower(strings.TrimSpace(cfg.Transport)) {
	case "", "stdio":
		tr, err = newStdioTransport(cfg)
	case "http", "sse", "streamable-http":
		tr, err = newHTTPTransport(cfg)
	default:
		return nil, fmt.Errorf("unknown MCP transport %q", cfg.Transport)
	}
	if err != nil {
		return nil, err
	}

	c := &Client{name: name, transport: tr}
	if err := c.initialize(ctx); err != nil {
		tr.Close()
		return nil, err
	}
	if err := c.refreshTools(ctx); err != nil {
		c.mu.Lock()
		c.tools = []ToolDef{}
		c.toolsOK = false
		c.toolsErr = err.Error()
		c.mu.Unlock()
		slog.Warn("mcp: cannot list tools", "server", name, "error", err)
	}
	return c, nil
}

// ServerConfig describes how to reach one MCP server.
type ServerConfig struct {
	Transport string
	Command   string
	Args      []string
	Env       map[string]string
	URL       string
	Headers   map[string]string
}

func (c *Client) nextID() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.seq++
	return c.seq
}

func (c *Client) initialize(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	resp, err := c.transport.send(ctx, rpcRequest{
		JSONRPC: "2.0", ID: c.nextID(), Method: "initialize",
		Params: map[string]any{
			"protocolVersion": protocolVersion,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"clientInfo":      map[string]any{"name": version.Display, "version": version.Version},
		},
	})
	if err != nil {
		return fmt.Errorf("initialize: %w", err)
	}
	if resp.Error != nil {
		return fmt.Errorf("initialize: %w", resp.Error)
	}

	var info struct {
		ServerInfo struct {
			Name    string `json:"name"`
			Version string `json:"version"`
		} `json:"serverInfo"`
	}
	_ = json.Unmarshal(resp.Result, &info)
	c.mu.Lock()
	c.srvName = info.ServerInfo.Name
	c.mu.Unlock()

	// The spec requires this notification before any other request.
	if err := c.transport.notify(ctx, rpcRequest{
		JSONRPC: "2.0", Method: "notifications/initialized",
	}); err != nil {
		return fmt.Errorf("initialized notification: %w", err)
	}
	slog.Info("mcp server connected", "server", c.name, "implementation", info.ServerInfo.Name)
	return nil
}

// refreshTools reloads the server's tool list.
func (c *Client) refreshTools(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	resp, err := c.transport.send(ctx, rpcRequest{
		JSONRPC: "2.0", ID: c.nextID(), Method: "tools/list",
	})
	if err != nil {
		return err
	}
	if resp.Error != nil {
		return resp.Error
	}
	var out struct {
		Tools []ToolDef `json:"tools"`
	}
	if err := json.Unmarshal(resp.Result, &out); err != nil {
		return err
	}
	c.mu.Lock()
	c.tools = append([]ToolDef{}, out.Tools...)
	c.toolsOK = true
	c.toolsErr = ""
	c.mu.Unlock()
	return nil
}

// ToolState returns whether tool discovery reached the backing application and
// the last discovery error. A proxy can initialize successfully while its
// backing application (for example IDA Pro) is still offline.
func (c *Client) ToolState() (bool, string) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.toolsOK, c.toolsErr
}

// Tools returns the cached tool list.
func (c *Client) Tools() []ToolDef {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return append([]ToolDef{}, c.tools...)
}

// Name returns the configured server name.
func (c *Client) Name() string { return c.name }

// Call invokes one tool and flattens its content into text.
func (c *Client) Call(ctx context.Context, tool string, args map[string]any) (*CallResult, error) {
	if args == nil {
		args = map[string]any{}
	}
	resp, err := c.transport.send(ctx, rpcRequest{
		JSONRPC: "2.0", ID: c.nextID(), Method: "tools/call",
		Params: map[string]any{"name": tool, "arguments": args},
	})
	if err != nil {
		return nil, err
	}
	if resp.Error != nil {
		return &CallResult{Text: resp.Error.Message, IsError: true}, nil
	}

	var out struct {
		Content []content `json:"content"`
		IsError bool      `json:"isError"`
	}
	if err := json.Unmarshal(resp.Result, &out); err != nil {
		return nil, fmt.Errorf("decode tool result: %w", err)
	}

	var b strings.Builder
	for _, item := range out.Content {
		switch item.Type {
		case "text":
			b.WriteString(item.Text)
			b.WriteString("\n")
		case "image":
			fmt.Fprintf(&b, "[image: %s, %d bytes base64]\n", item.MimeType, len(item.Data))
		case "resource":
			fmt.Fprintf(&b, "[resource: %s]\n", item.MimeType)
		}
	}
	text := strings.TrimSpace(b.String())
	if text == "" {
		text = "(no content returned)"
	}
	return &CallResult{Text: text, IsError: out.IsError}, nil
}

// ResourceDef describes an MCP resource a server exposes.
type ResourceDef struct {
	URI         string `json:"uri"`
	Name        string `json:"name"`
	Description string `json:"description"`
	MimeType    string `json:"mimeType"`
}

// ListResources returns the resources a server offers. A server that does not
// implement resources answers with an error, which is reported as none.
func (c *Client) ListResources(ctx context.Context) ([]ResourceDef, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	resp, err := c.transport.send(ctx, rpcRequest{
		JSONRPC: "2.0", ID: c.nextID(), Method: "resources/list",
	})
	if err != nil {
		return nil, err
	}
	if resp.Error != nil {
		return nil, resp.Error
	}
	var out struct {
		Resources []ResourceDef `json:"resources"`
	}
	if err := json.Unmarshal(resp.Result, &out); err != nil {
		return nil, err
	}
	return out.Resources, nil
}

// ReadResource fetches one resource's contents by URI and flattens them to text.
func (c *Client) ReadResource(ctx context.Context, uri string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	resp, err := c.transport.send(ctx, rpcRequest{
		JSONRPC: "2.0", ID: c.nextID(), Method: "resources/read",
		Params: map[string]any{"uri": uri},
	})
	if err != nil {
		return "", err
	}
	if resp.Error != nil {
		return "", resp.Error
	}
	var out struct {
		Contents []struct {
			URI      string `json:"uri"`
			MimeType string `json:"mimeType"`
			Text     string `json:"text"`
			Blob     string `json:"blob"`
		} `json:"contents"`
	}
	if err := json.Unmarshal(resp.Result, &out); err != nil {
		return "", err
	}
	var b strings.Builder
	for _, part := range out.Contents {
		if part.Text != "" {
			b.WriteString(part.Text)
			b.WriteString("\n")
		} else if part.Blob != "" {
			fmt.Fprintf(&b, "[binary resource: %s, %d bytes base64]\n", part.MimeType, len(part.Blob))
		}
	}
	text := strings.TrimSpace(b.String())
	if text == "" {
		text = "(resource is empty)"
	}
	return text, nil
}

// Close shuts down the connection.
func (c *Client) Close() error { return c.transport.Close() }

// ---- stdio transport ---------------------------------------------------------

// stdioTransport runs the server as a child process and exchanges
// newline-delimited JSON-RPC frames over its pipes.
type stdioTransport struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout *bufio.Reader

	mu     sync.Mutex
	closed bool
}

func newStdioTransport(cfg ServerConfig) (transport, error) {
	if strings.TrimSpace(cfg.Command) == "" {
		return nil, fmt.Errorf("stdio transport needs a command")
	}
	cmd := exec.Command(cfg.Command, cfg.Args...)
	cmd.Env = os.Environ()
	for k, v := range cfg.Env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	// Server logs go to stderr; surface them at debug level rather than dropping.
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start %s: %w", cfg.Command, err)
	}
	go func() {
		sc := bufio.NewScanner(stderr)
		for sc.Scan() {
			slog.Debug("mcp server stderr", "command", cfg.Command, "line", sc.Text())
		}
	}()

	return &stdioTransport{cmd: cmd, stdin: stdin, stdout: bufio.NewReaderSize(stdout, 1<<20)}, nil
}

func (t *stdioTransport) send(ctx context.Context, req rpcRequest) (*rpcResponse, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return nil, fmt.Errorf("mcp connection closed")
	}
	if err := t.writeFrame(req); err != nil {
		return nil, err
	}

	// Skip any notification the server interleaves before our response.
	type result struct {
		resp *rpcResponse
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		for {
			line, err := t.stdout.ReadBytes('\n')
			if err != nil {
				ch <- result{err: err}
				return
			}
			line = bytes.TrimSpace(line)
			if len(line) == 0 {
				continue
			}
			var resp rpcResponse
			if err := json.Unmarshal(line, &resp); err != nil {
				continue
			}
			if resp.ID == nil {
				continue // notification
			}
			ch <- result{resp: &resp}
			return
		}
	}()

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case r := <-ch:
		return r.resp, r.err
	}
}

func (t *stdioTransport) notify(_ context.Context, req rpcRequest) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.writeFrame(req)
}

func (t *stdioTransport) writeFrame(req rpcRequest) error {
	b, err := json.Marshal(req)
	if err != nil {
		return err
	}
	if _, err := t.stdin.Write(append(b, '\n')); err != nil {
		return err
	}
	return nil
}

func (t *stdioTransport) Close() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return nil
	}
	t.closed = true
	_ = t.stdin.Close()
	if t.cmd.Process != nil {
		_ = t.cmd.Process.Kill()
	}
	return nil
}

// ---- http transport ----------------------------------------------------------

// httpTransport posts JSON-RPC frames to a streamable-HTTP MCP endpoint.
type httpTransport struct {
	url       string
	headers   map[string]string
	client    *http.Client
	sessionID string
	mu        sync.Mutex
}

func newHTTPTransport(cfg ServerConfig) (transport, error) {
	if strings.TrimSpace(cfg.URL) == "" {
		return nil, fmt.Errorf("http transport needs a url")
	}
	return &httpTransport{
		url: cfg.URL, headers: cfg.Headers,
		client: &http.Client{Timeout: 120 * time.Second},
	}, nil
}

func (t *httpTransport) post(ctx context.Context, req rpcRequest) (*http.Response, error) {
	b, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, "POST", t.url, bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json, text/event-stream")
	httpReq.Header.Set("User-Agent", version.UserAgent())
	for k, v := range t.headers {
		httpReq.Header.Set(k, v)
	}
	t.mu.Lock()
	if t.sessionID != "" {
		httpReq.Header.Set("Mcp-Session-Id", t.sessionID)
	}
	t.mu.Unlock()

	resp, err := t.client.Do(httpReq)
	if err != nil {
		return nil, err
	}
	if sid := resp.Header.Get("Mcp-Session-Id"); sid != "" {
		t.mu.Lock()
		t.sessionID = sid
		t.mu.Unlock()
	}
	return resp, nil
}

func (t *httpTransport) send(ctx context.Context, req rpcRequest) (*rpcResponse, error) {
	resp, err := t.post(ctx, req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("mcp http %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	// Servers may answer as plain JSON or as a one-shot SSE stream.
	if strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream") {
		for _, line := range strings.Split(string(body), "\n") {
			line = strings.TrimSpace(line)
			if !strings.HasPrefix(line, "data:") {
				continue
			}
			payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			var out rpcResponse
			if err := json.Unmarshal([]byte(payload), &out); err == nil && out.ID != nil {
				return &out, nil
			}
		}
		return nil, fmt.Errorf("no JSON-RPC response in the event stream")
	}

	var out rpcResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return &out, nil
}

func (t *httpTransport) notify(ctx context.Context, req rpcRequest) error {
	resp, err := t.post(ctx, req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	return nil
}

func (t *httpTransport) Close() error { return nil }
