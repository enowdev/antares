// Package tools implements the agent's callable tool surface and the toolset
// groupings that decide which tools a given platform exposes.
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
)

// Progress is an incremental update emitted while a tool runs.
type Progress struct {
	Tool    string `json:"tool"`
	Message string `json:"message"`
	Chunk   string `json:"chunk,omitempty"`
	Percent int    `json:"percent,omitempty"`
}

// Input carries everything a tool needs to run one invocation.
type Input struct {
	Args      json.RawMessage
	CallID    string
	SessionID string
	UserID    string
	Platform  string
	Workspace string
	// WriteRoots, when non-empty, marks this a project session: file writes are
	// confined to these directories (the project folder plus the antares
	// workspace), while reads are allowed anywhere on the machine so the agent
	// can read and copy from outside. Empty = an ordinary session, where reads
	// and writes are both confined to Workspace.
	WriteRoots []string
	// Emit reports progress; it is never nil.
	Emit func(Progress)
	// AskUser blocks the current turn to put questions to the person and returns
	// their answer as text. Nil where no one can answer (some gateways/cron), in
	// which case a blocking tool must fall back to yielding via its result.
	AskUser AskFunc
	// Deps exposes shared services (store, memory, rag, llm factory, sub-agent).
	Deps *Deps
}

// AskQuestion is one question the ask_user tool puts to the person.
type AskQuestion struct {
	Question    string   `json:"question"`
	Header      string   `json:"header,omitempty"`
	Options     []string `json:"options,omitempty"`
	MultiSelect bool     `json:"multiSelect,omitempty"`
}

// AskFunc pauses the turn, asks the questions, and returns the answer text. It
// unblocks with an error if the run is cancelled.
type AskFunc func(ctx context.Context, questions []AskQuestion) (string, error)

// Bind decodes the tool arguments into v.
func (in Input) Bind(v any) error {
	if len(in.Args) == 0 {
		return nil
	}
	dec := json.NewDecoder(strings.NewReader(string(in.Args)))
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("invalid arguments: %w", err)
	}
	return nil
}

// Result is the outcome of a tool invocation.
type Result struct {
	// Content is what the model sees.
	Content string `json:"content"`
	// Display is an optional richer rendering for the dashboard.
	Display string         `json:"display,omitempty"`
	IsError bool           `json:"is_error,omitempty"`
	Meta    map[string]any `json:"meta,omitempty"`
}

// Errorf builds a failed Result the model can read and recover from.
func Errorf(format string, args ...any) Result {
	return Result{Content: fmt.Sprintf(format, args...), IsError: true}
}

// Text builds a successful text Result.
func Text(s string) Result { return Result{Content: s} }

// Tool is one callable capability.
type Tool interface {
	Name() string
	Description() string
	Schema() map[string]any
	Execute(ctx context.Context, in Input) Result
}

// Approval reports whether a tool mutates state and should be gated.
type Approval interface {
	RequiresApproval() bool
}

// Registry holds the process-wide tool set.
type Registry struct {
	mu    sync.RWMutex
	tools map[string]Tool
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry { return &Registry{tools: map[string]Tool{}} }

// Register adds or replaces a tool.
func (r *Registry) Register(t Tool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tools[t.Name()] = t
}

// Unregister removes a tool by name. It is a no-op when the name is absent.
func (r *Registry) Unregister(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.tools, name)
}

// Replace removes oldNames and registers replacements while holding one lock,
// so concurrent tool resolution never observes a partially refreshed MCP set.
func (r *Registry) Replace(oldNames []string, replacements []Tool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, name := range oldNames {
		delete(r.tools, name)
	}
	for _, tool := range replacements {
		r.tools[tool.Name()] = tool
	}
}

// Get looks up a tool by name.
func (r *Registry) Get(name string) (Tool, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.tools[name]
	return t, ok
}

// Names returns all registered tool names, sorted.
func (r *Registry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.tools))
	for n := range r.tools {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// All returns every registered tool, sorted by name.
func (r *Registry) All() []Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Tool, 0, len(r.tools))
	for _, t := range r.tools {
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name() < out[j].Name() })
	return out
}

// Resolve expands a toolset name plus explicit enable/disable lists into the
// concrete tools handed to the model.
func (r *Registry) Resolve(toolset string, enabled, disabled []string) []Tool {
	want := map[string]bool{}
	for _, n := range ExpandToolset(toolset) {
		want[n] = true
	}
	for _, n := range enabled {
		n = strings.TrimSpace(n)
		if n == "" {
			continue
		}
		if strings.HasPrefix(n, "@") {
			for _, m := range ExpandToolset(strings.TrimPrefix(n, "@")) {
				want[m] = true
			}
			continue
		}
		want[n] = true
	}
	for _, n := range disabled {
		n = strings.TrimSpace(n)
		if strings.HasPrefix(n, "@") {
			for _, m := range ExpandToolset(strings.TrimPrefix(n, "@")) {
				delete(want, m)
			}
			continue
		}
		delete(want, n)
	}
	// process is terminal's job-control companion. Custom roles that explicitly
	// enable terminal should not silently lose the only safe way to observe or
	// cancel an unknown-duration command. An explicit process disable still wins.
	if want["terminal"] && !contains(disabled, "process") {
		want["process"] = true
	}

	r.mu.RLock()
	defer r.mu.RUnlock()

	// Tools borrowed from MCP servers are opt-out rather than opt-in: if a
	// server is configured, its tools are meant to be reachable.
	for name := range r.tools {
		if strings.HasPrefix(name, MCPPrefix) && !contains(disabled, name) {
			want[name] = true
		}
	}

	out := make([]Tool, 0, len(want))
	for name := range want {
		if t, ok := r.tools[name]; ok {
			out = append(out, t)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name() < out[j].Name() })
	return out
}

// Toolsets are named groupings mirroring the platform toolset config.
var Toolsets = map[string][]string{
	"minimal": {"read_file", "list_files", "grep", "todo"},
	"coding": {
		"read_file", "read_document", "write_file", "edit_file", "list_files", "glob", "grep",
		"terminal", "process", "todo", "board", "project_info", "set_soul", "skill", "delegate_task", "task", "list_roles", "diagnostics", "http_request", "ask_user", "schedule",
	},
	"research": {
		"read_file", "read_document", "web_search", "web_fetch", "http_request", "browser", "grep", "todo", "memory",
		"session_search", "rag_search", "skill", "view_image", "report_finding", "add_intel", "methodology_status",
		"delegate_task", "task", "list_roles",
	},
	"browser": {"browser", "web_search", "web_fetch", "http_request", "read_file", "write_file", "todo", "report_finding", "add_intel", "methodology_status"},
	"security": {
		"read_file", "write_file", "list_files", "glob", "grep", "terminal", "process",
		"web_search", "web_fetch", "http_request", "browser", "todo", "skill",
		"report_finding", "triage_finding", "add_intel", "methodology_status",
		"delegate_task", "task", "list_roles", "diagnostics", "ask_user", "vps_run", "vps_upload", "vps_download",
		"osint_dns", "osint_dorks", "osint_whois", "osint_ip", "osint_username", "osint_github", "osint_email", "osint_email_full", "list_proxies", "osint_breach", "osint_shodan", "osint_reputation", "osint_crypto", "osint_domain", "osint_phone", "osint_scrape", "osint_paste", "osint_footprint", "osint_pivot", "osint_google", "osint_dorks_live", "check_dependencies", "re_info", "re_strings", "re_analyze", "re_decompile", "solve_captcha", "intercept",
		"attack_script", "awshook", "azurehook", "kubehook", "winhook", "machook", "cipipe", "ebpf",
		"hackbrowser",
	},
	"osint": {
		"osint_dns", "osint_dorks", "osint_whois", "osint_ip", "osint_username", "osint_github", "osint_email", "osint_email_full", "list_proxies", "osint_breach", "osint_shodan", "osint_reputation", "osint_crypto", "osint_domain", "osint_phone", "osint_scrape", "osint_paste", "osint_footprint", "osint_pivot", "osint_google", "osint_dorks_live", "check_dependencies", "re_info", "re_strings", "re_analyze", "re_decompile", "solve_captcha", "intercept",
		"web_search", "web_fetch", "http_request", "read_file", "todo", "add_intel", "report_finding",
	},
	"reverse": {
		"re_info", "re_strings", "re_analyze", "re_decompile", "check_dependencies",
		"read_file", "list_files", "glob", "grep", "terminal", "process", "todo", "report_finding",
	},
	"vibecoder": {
		"web_fetch", "http_request", "browser", "web_search", "terminal", "process",
		"read_file", "write_file", "list_files", "glob", "grep", "check_dependencies",
		"todo", "report_finding", "add_intel", "methodology_status", "skill", "rag_search",
	},
	"intercept": {
		"intercept", "browser", "http_request", "web_fetch", "solve_captcha",
		"read_file", "write_file", "todo", "report_finding", "add_intel",
	},
	"social": {
		"read_file", "write_file", "edit_file", "list_files", "glob", "grep",
		"terminal", "process", "web_search", "web_fetch", "http_request", "browser",
		"todo", "memory", "skill", "delegate_task", "task", "list_roles",
		"email_read", "temp_mail", "social_browser", "social_account", "ask_user", "schedule", "rag_search", "rag_index",
	},
	"default": {
		"read_file", "read_document", "write_file", "edit_file", "list_files", "glob", "grep",
		"terminal", "process", "web_search", "web_fetch", "http_request", "browser", "todo", "board", "project_info", "set_soul", "memory", "list_proxies", "vps_run", "vps_upload", "vps_download",
		"session_search", "rag_search", "rag_index", "skill", "delegate_task", "task", "list_roles", "image_generate", "view_image", "speak", "transcribe", "computer", "diagnostics", "ask_user", "schedule",
		"osint_dns", "osint_dorks", "osint_whois", "osint_ip", "osint_username", "osint_github", "osint_email", "osint_email_full", "osint_breach", "osint_shodan", "osint_reputation", "osint_crypto", "osint_domain", "osint_phone", "osint_scrape", "osint_paste", "osint_footprint", "osint_pivot", "osint_google", "osint_dorks_live", "check_dependencies", "re_info", "re_strings", "re_analyze", "re_decompile", "solve_captcha", "intercept",
		"email_read", "temp_mail", "social_browser", "social_account",
	},
	"all": nil, // resolved dynamically to every registered tool
}

// ExpandToolset returns the tool names in a named toolset.
func ExpandToolset(name string) []string {
	name = strings.TrimSpace(name)
	if name == "" {
		name = "default"
	}
	if name == "all" {
		return globalRegistry.Names()
	}
	if set, ok := Toolsets[name]; ok {
		return set
	}
	return Toolsets["default"]
}

// ToolsetNames lists the available toolsets, sorted.
func ToolsetNames() []string {
	out := make([]string, 0, len(Toolsets))
	for k := range Toolsets {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// MCPPrefix namespaces tools imported from MCP servers.
const MCPPrefix = "mcp__"

func contains(list []string, want string) bool {
	for _, v := range list {
		if strings.TrimSpace(v) == want {
			return true
		}
	}
	return false
}

var globalRegistry = NewRegistry()

// Default returns the process-wide registry.
func Default() *Registry { return globalRegistry }

// schema is a small helper for building JSON Schema objects.
func schema(props map[string]any, required ...string) map[string]any {
	if props == nil {
		props = map[string]any{}
	}
	out := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		out["required"] = required
	} else {
		out["required"] = []string{}
	}
	return out
}

func prop(typ, desc string) map[string]any {
	return map[string]any{"type": typ, "description": desc}
}

func propEnum(desc string, values ...string) map[string]any {
	return map[string]any{"type": "string", "description": desc, "enum": values}
}

func propDefault(typ, desc string, def any) map[string]any {
	return map[string]any{"type": typ, "description": desc, "default": def}
}
