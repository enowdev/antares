// Package config holds the layered Antares configuration: file defaults,
// YAML overrides from the profile config, then environment variables.
package config

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	xproxy "golang.org/x/net/proxy"
)

// Config is the complete runtime configuration tree.
type Config struct {
	Model         Model               `yaml:"model" json:"model"`
	Providers     map[string]Provider `yaml:"providers" json:"providers"`
	Database      Database            `yaml:"database" json:"database"`
	Server        Server              `yaml:"server" json:"server"`
	Agent         Agent               `yaml:"agent" json:"agent"`
	Tools         Tools               `yaml:"tools" json:"tools"`
	Terminal      Terminal            `yaml:"terminal" json:"terminal"`
	Compression   Compression         `yaml:"compression" json:"compression"`
	PromptCaching PromptCaching       `yaml:"prompt_caching" json:"prompt_caching"`
	Memory        Memory              `yaml:"memory" json:"memory"`
	RAG           RAG                 `yaml:"rag" json:"rag"`
	Skills        Skills              `yaml:"skills" json:"skills"`
	Plugins       Plugins             `yaml:"plugins" json:"plugins"`
	Roles         Roles               `yaml:"roles" json:"roles"`
	Security      Security            `yaml:"security" json:"security"`
	OSINT         OSINT               `yaml:"osint" json:"osint"`
	ImageGen      ImageGen            `yaml:"image_gen" json:"image_gen"`
	Cron          Cron                `yaml:"cron" json:"cron"`
	Autopilot     Autopilot           `yaml:"autopilot" json:"autopilot"`
	Gateway       Gateway             `yaml:"gateway" json:"gateway"`
	Delegation    Delegation          `yaml:"delegation" json:"delegation"`
	CodeExecution CodeExecution       `yaml:"code_execution" json:"code_execution"`
	Guardrails    Guardrails          `yaml:"tool_loop_guardrails" json:"tool_loop_guardrails"`
	SessionReset  SessionReset        `yaml:"session_reset" json:"session_reset"`
	Streaming     Streaming           `yaml:"streaming" json:"streaming"`
	Display       Display             `yaml:"display" json:"display"`
	Logging       Logging             `yaml:"logging" json:"logging"`
	MCP           MCP                 `yaml:"mcp" json:"mcp"`
	Proxies       Proxies             `yaml:"proxies" json:"proxies"`

	MaxConcurrentSessions int  `yaml:"max_concurrent_sessions" json:"max_concurrent_sessions"`
	GroupSessionsPerUser  bool `yaml:"group_sessions_per_user" json:"group_sessions_per_user"`

	Social Social `yaml:"social" json:"social"`
}

// Social configures the Social Media feature: IMAP mailbox, persistent browser,
// and autopilot toggle. Secrets (IMAP password, social account passwords) are
// NOT stored here; they live encrypted in SQLite.
type Social struct {
	Enabled         bool   `yaml:"enabled" json:"enabled"`
	IMAPHost        string `yaml:"imap_host" json:"imap_host"`
	IMAPPort        int    `yaml:"imap_port" json:"imap_port"`
	IMAPUsername    string `yaml:"imap_username" json:"imap_username"`
	BrowserEnabled  bool   `yaml:"browser_enabled" json:"browser_enabled"`
	AutopilotEnabled bool  `yaml:"autopilot_enabled" json:"autopilot_enabled"`
}

// Model selects which LLM answers by default.
type Model struct {
	Default   string `yaml:"default" json:"default"`
	Provider  string `yaml:"provider" json:"provider"`
	BaseURL   string `yaml:"base_url" json:"base_url"`
	APIKey    string `yaml:"api_key" json:"api_key"`
	Auxiliary string `yaml:"auxiliary" json:"auxiliary"`
	Vision    string `yaml:"vision" json:"vision"`
	Embedding string `yaml:"embedding" json:"embedding"`
	// TTS, STT, and Voice configure the voice tools. Empty uses sensible
	// defaults (tts-1 / whisper-1 / alloy) against an OpenAI-compatible provider.
	TTS   string `yaml:"tts" json:"tts"`
	STT   string `yaml:"stt" json:"stt"`
	Voice string `yaml:"voice" json:"voice"`

	Temperature      float64 `yaml:"temperature" json:"temperature"`
	TopP             float64 `yaml:"top_p" json:"top_p"`
	MaxTokens        int     `yaml:"max_tokens" json:"max_tokens"`
	ContextWindow    int     `yaml:"context_window" json:"context_window"`
	ReasoningEffort  string  `yaml:"reasoning_effort" json:"reasoning_effort"`
	ParallelToolCall bool    `yaml:"parallel_tool_calls" json:"parallel_tool_calls"`
	// MaxRetries is how many times a transient provider failure (429, 5xx,
	// dropped connection) is retried with backoff. Zero uses a safe default.
	MaxRetries int `yaml:"max_retries" json:"max_retries"`
	// Fallback lists models to try, in order, when the primary fails after its
	// retries — each a bare model id or "provider/model". A whole provider
	// outage or a decommissioned model then degrades instead of erroring.
	Fallback []string `yaml:"fallback" json:"fallback"`
	// Panel is the set of models /panel asks. Two or more, or the command has
	// nothing to compare.
	Panel []string `yaml:"panel" json:"panel"`
}

// Provider is one configured LLM endpoint.
type Provider struct {
	Kind        string            `yaml:"kind" json:"kind"` // openai-compatible|anthropic|openai|gemini|custom
	BaseURL     string            `yaml:"base_url" json:"base_url"`
	APIKey      string            `yaml:"api_key" json:"api_key"`
	APIKeyEnv   string            `yaml:"api_key_env" json:"api_key_env"`
	Headers     map[string]string `yaml:"headers" json:"headers"`
	Models      []string          `yaml:"models" json:"models"`
	Enabled     bool              `yaml:"enabled" json:"enabled"`
	Label       string            `yaml:"label" json:"label"`
	TimeoutSecs int               `yaml:"timeout_seconds" json:"timeout_seconds"`
	APIVersion  string            `yaml:"api_version" json:"api_version"`
	Region      string            `yaml:"region" json:"region"`
	// ModelMeta carries metadata for models the provider's /models endpoint does
	// not describe — chiefly a manually added model's context window. Keyed by
	// model id. Empty for the common case where everything is auto-discovered.
	ModelMeta map[string]ModelMeta `yaml:"model_meta,omitempty" json:"model_meta,omitempty"`
}

// ModelMeta overrides or supplies model details the provider cannot report.
type ModelMeta struct {
	ContextWindow int `yaml:"context_window,omitempty" json:"context_window,omitempty"`
}

// Database picks the persistence driver.
type Database struct {
	Driver   string `yaml:"driver" json:"driver"` // sqlite|postgres|memory
	DSN      string `yaml:"dsn" json:"dsn"`
	MaxConns int    `yaml:"max_conns" json:"max_conns"`
	Busy     int    `yaml:"busy_timeout_ms" json:"busy_timeout_ms"`
	WAL      bool   `yaml:"wal" json:"wal"`
}

// Server configures the HTTP API + dashboard host.
type Server struct {
	Host         string `yaml:"host" json:"host"`
	Port         int    `yaml:"port" json:"port"`
	AuthToken    string `yaml:"auth_token" json:"auth_token"`
	AuthDisabled bool   `yaml:"auth_disabled" json:"auth_disabled"`
	// DashboardPasswordHash gates the web dashboard behind a login when set
	// (bcrypt hash of the password). Empty leaves the dashboard open. This is
	// web-only: the TUI, gateways, and any client presenting AuthToken as a
	// bearer bypass it.
	DashboardPasswordHash string   `yaml:"dashboard_password_hash" json:"dashboard_password_hash"`
	CORSOrigins           []string `yaml:"cors_origins" json:"cors_origins"`
	PublicURL             string   `yaml:"public_url" json:"public_url"`
	TrustProxy            bool     `yaml:"trust_proxy" json:"trust_proxy"`
}

// Agent tunes the conversation loop.
type Agent struct {
	MaxTurns          int      `yaml:"max_turns" json:"max_turns"`
	MaxToolCalls      int      `yaml:"max_tool_calls_per_turn" json:"max_tool_calls_per_turn"`
	Verbose           bool     `yaml:"verbose" json:"verbose"`
	ReasoningEffort   string   `yaml:"reasoning_effort" json:"reasoning_effort"`
	Personality       string   `yaml:"personality" json:"personality"`
	SystemPromptExtra string   `yaml:"system_prompt_extra" json:"system_prompt_extra"`
	Workspace         string   `yaml:"workspace" json:"workspace"`
	Timezone          string   `yaml:"timezone" json:"timezone"`
	Language          string   `yaml:"language" json:"language"`
	IdleTimeoutSecs   int      `yaml:"idle_timeout_seconds" json:"idle_timeout_seconds"`
	StopSequences     []string `yaml:"stop_sequences" json:"stop_sequences"`

	// RepeatLimit is how many identical tool calls are tolerated before the
	// agent is told to change approach. Zero uses the default.
	RepeatLimit int `yaml:"repeat_limit" json:"repeat_limit"`
	// VerifyReplies runs a cheap second model over a finished answer to catch
	// work that was described but not done.
	VerifyReplies bool `yaml:"verify_replies" json:"verify_replies"`
	// VerifyMax bounds how many times one turn can be sent back for more work.
	VerifyMax int `yaml:"verify_max" json:"verify_max"`
	// GoalMaxIterations bounds a standing goal so it cannot run forever.
	GoalMaxIterations int `yaml:"goal_max_iterations" json:"goal_max_iterations"`
	// WrapUntrustedOutput fences tool output that carries external content
	// (web pages, HTTP bodies, MCP results) so the model treats it as data and
	// not as instructions — a defence against prompt injection. On by default.
	WrapUntrustedOutput bool `yaml:"wrap_untrusted_output" json:"wrap_untrusted_output"`
	// SmartTitles names a new conversation with a short model-written title
	// instead of a truncation of the first message. On by default; it costs one
	// cheap auxiliary-model call per session.
	SmartTitles bool `yaml:"smart_titles" json:"smart_titles"`
}

// Tools controls which toolsets are exposed to the model.
type Tools struct {
	Enabled        []string          `yaml:"enabled" json:"enabled"`
	Disabled       []string          `yaml:"disabled" json:"disabled"`
	Toolset        string            `yaml:"toolset" json:"toolset"`
	ApprovalMode   string            `yaml:"approval_mode" json:"approval_mode"` // auto|prompt|deny
	MaxOutputChars int               `yaml:"max_output_chars" json:"max_output_chars"`
	Timeouts       map[string]int    `yaml:"timeouts" json:"timeouts"`
	WebSearch      WebSearch         `yaml:"web_search" json:"web_search"`
	Browser        Browser           `yaml:"browser" json:"browser"`
	HTTP           HTTP              `yaml:"http" json:"http"`
	ComputerUse    bool              `yaml:"computer_use" json:"computer_use"`
	Platform       map[string]string `yaml:"platform_toolsets" json:"platform_toolsets"`
}

// HTTP configures the http_request tool, which calls HTTP APIs with a
// browser-identical TLS/HTTP2 fingerprint so requests are not rejected by
// bot-detection layers at the cryptographic level.
type HTTP struct {
	// Preset is the browser fingerprint to mimic, e.g. "chrome-131" (the
	// default), "chrome-131-windows", "chrome-133". Empty uses the default.
	Preset string `yaml:"preset" json:"preset"`
	// Proxy routes requests through a proxy, e.g. "http://host:3128" or
	// "socks5://host:1080".
	Proxy string `yaml:"proxy" json:"proxy"`
	// TimeoutSeconds bounds a single request; 0 uses the built-in default.
	TimeoutSeconds int `yaml:"timeout_seconds" json:"timeout_seconds"`
	// WrapTerminal puts curl/wget shims on the terminal's PATH so those
	// commands go through the fingerprinted client too, falling back to the
	// real binary for anything the shim cannot handle. On by default.
	WrapTerminal bool `yaml:"wrap_terminal" json:"wrap_terminal"`
}

// WebSearch configures the search backend used by the web_search tool.
// Browser configures the real-browser tool.
type Browser struct {
	Enabled bool `yaml:"enabled" json:"enabled"`
	// Executable overrides Chrome discovery; empty means look for one.
	Executable string `yaml:"executable" json:"executable"`
	// RemoteURL attaches to a Chrome already running with a debugging port,
	// e.g. http://127.0.0.1:9222, instead of starting one.
	RemoteURL string `yaml:"remote_url" json:"remote_url"`
	// UserDataDir keeps cookies and logins between runs.
	UserDataDir string `yaml:"user_data_dir" json:"user_data_dir"`
	// Headed shows the window; it needs a display to be of any use.
	Headed bool `yaml:"headed" json:"headed"`
	Width  int  `yaml:"width" json:"width"`
	Height int  `yaml:"height" json:"height"`
	// Stealth launches a source-patched anti-detection Chromium (downloaded and
	// verified on first use) instead of the system Chrome, so pages guarded by
	// bot-detection challenges — Cloudflare Turnstile and the like — load. The
	// binary is fetched once and cached; it carries its own upstream license.
	Stealth bool `yaml:"stealth" json:"stealth"`
	// Proxy routes the stealth browser through a proxy, e.g. "http://host:3128"
	// or "socks5://host:1080".
	Proxy string `yaml:"proxy" json:"proxy"`
	// Timezone and Locale spoof the stealth fingerprint, e.g. "America/New_York"
	// and "en-US".
	Timezone string `yaml:"timezone" json:"timezone"`
	Locale   string `yaml:"locale" json:"locale"`
}

type WebSearch struct {
	Provider   string `yaml:"provider" json:"provider"` // browser|brave|tavily|searxng|none
	APIKey     string `yaml:"api_key" json:"api_key"`
	BaseURL    string `yaml:"base_url" json:"base_url"`
	MaxResults int    `yaml:"max_results" json:"max_results"`
}

// Terminal configures the shell execution backend.
type Terminal struct {
	Backend         string   `yaml:"backend" json:"backend"` // local|docker|ssh
	CWD             string   `yaml:"cwd" json:"cwd"`
	Timeout         int      `yaml:"timeout" json:"timeout"`
	HomeMode        string   `yaml:"home_mode" json:"home_mode"`
	LifetimeSeconds int      `yaml:"lifetime_seconds" json:"lifetime_seconds"`
	Shell           string   `yaml:"shell" json:"shell"`
	BlockedCommands []string `yaml:"blocked_commands" json:"blocked_commands"`
	AllowNetwork    bool     `yaml:"allow_network" json:"allow_network"`
	// Sandbox confines what a command can reach: none, auto, bubblewrap, or
	// namespace. Auto uses the strongest thing this machine has.
	Sandbox string `yaml:"sandbox" json:"sandbox"`
	// SandboxHidden are paths kept out of the sandbox. Empty uses the default
	// list, which is the credential directories.
	SandboxHidden []string `yaml:"sandbox_hidden" json:"sandbox_hidden"`
	DockerImage   string   `yaml:"docker_image" json:"docker_image"`
	SSHHost       string   `yaml:"ssh_host" json:"ssh_host"`
}

// Compression controls automatic context compaction.
type Compression struct {
	Enabled                bool    `yaml:"enabled" json:"enabled"`
	ProgressNotices        bool    `yaml:"progress_notices" json:"progress_notices"`
	Threshold              float64 `yaml:"threshold" json:"threshold"`
	TargetRatio            float64 `yaml:"target_ratio" json:"target_ratio"`
	ProtectLastN           int     `yaml:"protect_last_n" json:"protect_last_n"`
	ProtectFirstN          int     `yaml:"protect_first_n" json:"protect_first_n"`
	MinTailUserMessages    int     `yaml:"min_tail_user_messages" json:"min_tail_user_messages"`
	MaxAttempts            int     `yaml:"max_attempts" json:"max_attempts"`
	IdleCompactAfterSecs   int     `yaml:"idle_compact_after_seconds" json:"idle_compact_after_seconds"`
	ProactivePruneTokens   int     `yaml:"proactive_prune_tokens" json:"proactive_prune_tokens"`
	ProactivePruneMinChars int     `yaml:"proactive_prune_min_result_chars" json:"proactive_prune_min_result_chars"`
}

// PromptCaching mirrors provider-side cache controls.
type PromptCaching struct {
	Enabled  bool   `yaml:"enabled" json:"enabled"`
	CacheTTL string `yaml:"cache_ttl" json:"cache_ttl"`
}

// Memory is the agent-curated long-term memory.
type Memory struct {
	Enabled            bool `yaml:"memory_enabled" json:"memory_enabled"`
	UserProfileEnabled bool `yaml:"user_profile_enabled" json:"user_profile_enabled"`
	MemoryCharLimit    int  `yaml:"memory_char_limit" json:"memory_char_limit"`
	UserCharLimit      int  `yaml:"user_char_limit" json:"user_char_limit"`
	NudgeInterval      int  `yaml:"nudge_interval" json:"nudge_interval"`
	FlushMinTurns      int  `yaml:"flush_min_turns" json:"flush_min_turns"`
	SearchLimit        int  `yaml:"search_limit" json:"search_limit"`
}

// RAG is the native retrieval store: embeds chunks with the configured model,
// keeps vectors in the Antares database, and layers rerank + dedup on top. There
// is no external backend anymore — it is all in-process.
type RAG struct {
	Enabled bool `yaml:"enabled" json:"enabled"`

	// Embedding + vector store settings.
	EmbedProvider string `yaml:"embed_provider" json:"embed_provider"`
	EmbedModel    string `yaml:"embed_model" json:"embed_model"`
	EmbedBaseURL  string `yaml:"embed_base_url" json:"embed_base_url"`
	EmbedAPIKey   string `yaml:"embed_api_key" json:"embed_api_key"`
	ChunkSize     int    `yaml:"chunk_size" json:"chunk_size"`
	ChunkOverlap  int    `yaml:"chunk_overlap" json:"chunk_overlap"`
	TopK          int    `yaml:"top_k" json:"top_k"`
	Hybrid        bool   `yaml:"hybrid" json:"hybrid"`
	// Recall is how many candidates to pull before rerank/dedup narrow to TopK.
	Recall int `yaml:"recall" json:"recall"`

	// Reranking, applied to the recalled candidates. Mode: llm | api | off.
	RerankMode   string `yaml:"rerank_mode" json:"rerank_mode"`
	RerankURL    string `yaml:"rerank_url" json:"rerank_url"`
	RerankModel  string `yaml:"rerank_model" json:"rerank_model"`
	RerankAPIKey string `yaml:"rerank_api_key" json:"rerank_api_key"`
	// Compress collapses near-duplicate results (exact content) before TopK.
	Compress bool `yaml:"compress" json:"compress"`

	// AutoContext feeds every chat turn: index the conversation and surface
	// relevant memory/docs into the prompt automatically.
	AutoContext bool `yaml:"auto_context" json:"auto_context"`
}

// ImageGen configures text-to-image generation.
type ImageGen struct {
	Enabled  bool   `yaml:"enabled" json:"enabled"`
	Provider string `yaml:"provider" json:"provider"` // a configured provider id, or "openai"
	Model    string `yaml:"model" json:"model"`
	BaseURL  string `yaml:"base_url" json:"base_url"`
	APIKey   string `yaml:"api_key" json:"api_key"`
	Size     string `yaml:"size" json:"size"`
}

// Security holds settings for the authorized-security roles.
//
// Deprecated: scope-gating was removed — the scope_check tool and /scope
// command are gone, and security tools no longer refuse targets. The
// fields remain so existing config.yaml files keep loading, and Scope is
// still surfaced in engagement views as a "declared targets" list for the
// user's own bookkeeping. RequireScope has no effect.
type Security struct {
	// Scope is a list of declared targets, surfaced in engagement views.
	// No longer enforced.
	Scope []string `yaml:"scope" json:"scope"`
	// RequireScope is ignored. Kept for backward compatibility.
	RequireScope bool `yaml:"require_scope" json:"require_scope"`
}

// OSINT holds optional credentials that unlock deeper open-source lookups. All
// OSINT tools work keyless by default; these only enable extras.
type OSINT struct {
	// GoogleCookie is a full Cookie header from a logged-in Google session,
	// used by osint_google to resolve an email/gaia id to its public Google
	// profile (GHunt-style). Optional and ToS-sensitive: supplying your own
	// session cookie is your decision. Empty disables the tool with a hint.
	GoogleCookie string `yaml:"google_cookie" json:"google_cookie"`
	// GoogleAuthUser selects which account in a multi-account cookie session the
	// Google lookups act as — the /u/<N>/ index. 0 is the default account.
	GoogleAuthUser int `yaml:"google_authuser" json:"google_authuser"`
}

// Proxies is a global store of named proxy endpoints — nothing more. It does
// not decide when a proxy is used; a tool or the agent chooses an entry by id
// (or label) when it wants one. Think of it as shared proxy storage the app
// draws from on demand.
type Proxies struct {
	// Entries are the saved proxies, each with a stable ID.
	Entries []ProxyEntry `yaml:"entries" json:"entries"`
}

// Find returns the dial URL of the entry matching ref (an id or a label,
// case-insensitive), or "" when nothing matches. This is how a tool resolves an
// agent-supplied proxy choice to something dial-ready.
func (p Proxies) Find(ref string) string {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return ""
	}
	for _, e := range p.Entries {
		if e.ID == ref || strings.EqualFold(e.Label, ref) {
			return e.ProxyURL()
		}
	}
	return ""
}

// First returns the dial URL of the first stored proxy, or "" when none are
// saved. Tools that want to route through a proxy by default (rather than wait
// for the agent to pick one) use this — the store is small and unordered, so
// "first" is simply "any saved proxy".
func (p Proxies) First() string {
	for _, e := range p.Entries {
		if u := e.ProxyURL(); u != "" {
			return u
		}
	}
	return ""
}

// ProxyEntry is one saved proxy. URL is the full endpoint; the convenience
// fields (Scheme/Host/Port/Username/Password) let the dashboard edit parts
// without re-typing the whole URL, and ProxyURL() composes them when URL is
// empty.
type ProxyEntry struct {
	ID       string `yaml:"id" json:"id"`
	Label    string `yaml:"label" json:"label"`
	URL      string `yaml:"url" json:"url"`
	Scheme   string `yaml:"scheme" json:"scheme"` // http|https|socks5 (default http)
	Host     string `yaml:"host" json:"host"`
	Port     int    `yaml:"port" json:"port"`
	Username string `yaml:"username" json:"username"`
	Password string `yaml:"password" json:"password"`
}

// ProxyURL returns the entry as a dial-ready URL, e.g.
// "http://user:pass@host:port". It prefers an explicit URL, else composes one
// from the parts. Empty if the entry has neither.
func (p ProxyEntry) ProxyURL() string {
	if u := strings.TrimSpace(p.URL); u != "" {
		if !strings.Contains(u, "://") {
			u = "http://" + u
		}
		return u
	}
	host := strings.TrimSpace(p.Host)
	if host == "" {
		return ""
	}
	scheme := strings.TrimSpace(p.Scheme)
	if scheme == "" {
		scheme = "http"
	}
	auth := ""
	if p.Username != "" {
		auth = url.QueryEscape(p.Username)
		if p.Password != "" {
			auth += ":" + url.QueryEscape(p.Password)
		}
		auth += "@"
	}
	hostport := host
	if p.Port > 0 {
		hostport = fmt.Sprintf("%s:%d", host, p.Port)
	}
	return fmt.Sprintf("%s://%s%s", scheme, auth, hostport)
}

// ParseProxyLine reads one proxy in any of the common shapes and returns a
// populated entry (Scheme/Host/Port/Username/Password). Supported forms:
//
//	scheme://user:pass@host:port      scheme://host:port
//	user:pass@host:port               host:port
//	host:port:user:pass               host:port:user
//	label = <any of the above>        (an optional "name = …" prefix)
//
// The default scheme is http. It errors only when it cannot find a host:port.
func ParseProxyLine(line string) (ProxyEntry, error) {
	raw := strings.TrimSpace(line)
	var label string
	// Optional "Label = value" or "Label: scheme://…" prefix. Only split on the
	// first " = " / ": " that is clearly a label, i.e. before any "://".
	if i := strings.Index(raw, "="); i > 0 && (strings.Index(raw, "://") == -1 || i < strings.Index(raw, "://")) {
		label = strings.TrimSpace(raw[:i])
		raw = strings.TrimSpace(raw[i+1:])
	}
	if raw == "" {
		return ProxyEntry{}, fmt.Errorf("empty proxy line")
	}

	scheme := "http"
	if i := strings.Index(raw, "://"); i >= 0 {
		scheme = strings.ToLower(raw[:i])
		raw = raw[i+3:]
	}

	var user, pass, host string
	var port int

	if strings.Contains(raw, "@") {
		// [user[:pass]]@host:port
		at := strings.LastIndex(raw, "@")
		cred := raw[:at]
		hp := raw[at+1:]
		user, pass = splitFirst(cred, ":")
		var perr error
		host, port, perr = splitHostPort(hp)
		if perr != nil {
			return ProxyEntry{}, perr
		}
	} else {
		parts := strings.Split(raw, ":")
		switch len(parts) {
		case 2: // host:port
			host = parts[0]
			port = atoiSafe(parts[1])
		case 3: // host:port:user
			host, port = parts[0], atoiSafe(parts[1])
			user = parts[2]
		case 4: // host:port:user:pass
			host, port = parts[0], atoiSafe(parts[1])
			user, pass = parts[2], parts[3]
		default:
			return ProxyEntry{}, fmt.Errorf("unrecognised proxy format: %q", line)
		}
	}

	host = strings.TrimSpace(host)
	if host == "" || port <= 0 {
		return ProxyEntry{}, fmt.Errorf("proxy needs host:port: %q", line)
	}
	if label == "" {
		label = fmt.Sprintf("%s:%d", host, port)
	}
	return ProxyEntry{
		Label: label, Scheme: scheme, Host: host, Port: port,
		Username: strings.TrimSpace(user), Password: pass,
	}, nil
}

func splitFirst(s, sep string) (a, b string) {
	if i := strings.Index(s, sep); i >= 0 {
		return s[:i], s[i+len(sep):]
	}
	return s, ""
}

func splitHostPort(hp string) (string, int, error) {
	host, portStr := splitFirst(hp, ":")
	port := atoiSafe(portStr)
	if strings.TrimSpace(host) == "" || port <= 0 {
		return "", 0, fmt.Errorf("expected host:port, got %q", hp)
	}
	return host, port, nil
}

func atoiSafe(s string) int {
	n := 0
	for _, r := range strings.TrimSpace(s) {
		if r < '0' || r > '9' {
			return 0
		}
		n = n*10 + int(r-'0')
	}
	return n
}

// ProxyHTTPClient builds an *http.Client that dials through the given proxy URL.
// http/https proxies use the standard transport proxy; socks5/socks5h use a
// x/net SOCKS dialer. It is the one place that turns a proxy URL into a client,
// shared by the tools and the server's proxy-test endpoint. An empty proxyURL
// yields a plain client with the given timeout.
func ProxyHTTPClient(proxyURL string, timeout time.Duration) (*http.Client, error) {
	proxyURL = strings.TrimSpace(proxyURL)
	if proxyURL == "" {
		return &http.Client{Timeout: timeout}, nil
	}
	u, err := url.Parse(proxyURL)
	if err != nil {
		return nil, err
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https", "":
		if u.Scheme == "" {
			u.Scheme = "http"
		}
		return &http.Client{
			Timeout:   timeout,
			Transport: &http.Transport{Proxy: http.ProxyURL(u)},
		}, nil
	case "socks5", "socks5h":
		var auth *xproxy.Auth
		if u.User != nil {
			pw, _ := u.User.Password()
			auth = &xproxy.Auth{User: u.User.Username(), Password: pw}
		}
		d, err := xproxy.SOCKS5("tcp", u.Host, auth, xproxy.Direct)
		if err != nil {
			return nil, err
		}
		tr := &http.Transport{}
		if dc, ok := d.(xproxy.ContextDialer); ok {
			tr.DialContext = dc.DialContext
		} else {
			tr.DialContext = func(_ context.Context, network, addr string) (net.Conn, error) {
				return d.Dial(network, addr)
			}
		}
		return &http.Client{Timeout: timeout, Transport: tr}, nil
	default:
		return nil, fmt.Errorf("unsupported proxy scheme %q", u.Scheme)
	}
}

// Roles configures the specialist agent roles.
type Roles struct {
	Dirs []string `yaml:"dirs" json:"dirs"`
}

// Plugins configures external programs that hook into the agent.
type Plugins struct {
	Enabled bool     `yaml:"enabled" json:"enabled"`
	Dirs    []string `yaml:"dirs" json:"dirs"`
}

// Skills configures the self-improving skill library.
type Skills struct {
	Enabled               bool     `yaml:"enabled" json:"enabled"`
	Dirs                  []string `yaml:"dirs" json:"dirs"`
	CreationNudgeInterval int      `yaml:"creation_nudge_interval" json:"creation_nudge_interval"`
	AutoCreate            bool     `yaml:"auto_create" json:"auto_create"`
	HubSources            []string `yaml:"hub_sources" json:"hub_sources"`
}

// Cron configures the built-in scheduler.
// Autopilot configures the unattended work-queue runner.
type Autopilot struct {
	// VerifyCommand runs in each card's workspace to check the work (e.g.
	// "go build ./... && go test ./..."). Empty skips verification.
	VerifyCommand string `yaml:"verify_command" json:"verify_command"`
	// BaseBranch is the PR base when autopilot runs with --pr. Default "main".
	BaseBranch string `yaml:"base_branch" json:"base_branch"`
}

type Cron struct {
	Enabled       bool   `yaml:"enabled" json:"enabled"`
	Timezone      string `yaml:"timezone" json:"timezone"`
	MaxConcurrent int    `yaml:"max_concurrent" json:"max_concurrent"`
	HistoryLimit  int    `yaml:"history_limit" json:"history_limit"`
}

// Gateway holds messaging platform credentials.
type Gateway struct {
	Enabled  bool     `yaml:"enabled" json:"enabled"`
	Telegram Telegram `yaml:"telegram" json:"telegram"`
	Discord  Discord  `yaml:"discord" json:"discord"`
	Slack    Slack    `yaml:"slack" json:"slack"`
	Matrix   Matrix   `yaml:"matrix" json:"matrix"`
	Signal   Signal   `yaml:"signal" json:"signal"`
	WhatsApp WhatsApp `yaml:"whatsapp" json:"whatsapp"`
	Feishu   Feishu   `yaml:"feishu" json:"feishu"`
	// Bindings route a specific server/channel to a specific agent, model, and
	// policy. When any binding exists for a platform, that platform answers ONLY
	// in bound channels — an unbound channel is ignored (a strict allowlist).
	Bindings []Binding `yaml:"bindings" json:"bindings"`
}

// Binding is a per-scope routing and policy rule for a messaging channel. The
// most specific match wins: a rule naming both guild and channel beats one
// naming only the guild, which beats a platform-wide rule.
type Binding struct {
	ID        string `yaml:"id" json:"id"`
	Platform  string `yaml:"platform" json:"platform"`   // discord|telegram
	GuildID   string `yaml:"guild_id" json:"guild_id"`   // discord server; empty = any
	ChannelID string `yaml:"channel_id" json:"channel_id"` // channel/chat id; empty = any in guild
	Label     string `yaml:"label" json:"label"`         // human label for the UI
	Enabled   bool   `yaml:"enabled" json:"enabled"`

	// Policy applied when this binding matches.
	Role         string   `yaml:"role" json:"role"`                   // agent/role name; empty = default
	Model        string   `yaml:"model" json:"model"`                 // model override; empty = default
	Toolset      string   `yaml:"toolset" json:"toolset"`             // toolset override; empty = role/default
	AllowedUsers []string `yaml:"allowed_users" json:"allowed_users"` // platform user ids; empty = anyone paired
	// AllowedRoles gates a Discord server binding by the sender's server roles:
	// the bot answers only members holding one of these role ids. Empty means
	// NO ONE in the server is served by this binding (strict — a server binding
	// with no roles is off). Ignored for DMs and Telegram.
	AllowedRoles []string `yaml:"allowed_roles" json:"allowed_roles"`
	ReplyMode    string   `yaml:"reply_mode" json:"reply_mode"` // mention|always (groups only)
	PromptPrefix string   `yaml:"prompt_prefix" json:"prompt_prefix"` // appended to the system prompt
	// RelevanceFilter, when set, gates messages: a cheap model call decides
	// whether the message fits the criteria before the full agent runs. Empty
	// answers everything.
	RelevanceFilter string `yaml:"relevance_filter" json:"relevance_filter"`
}

// Signal talks to a signal-cli REST API daemon (bbernhard/signal-cli-rest-api),
// which the user runs. It polls for messages and posts to send.
type Signal struct {
	Enabled        bool     `yaml:"enabled" json:"enabled"`
	APIURL         string   `yaml:"api_url" json:"api_url"` // http://localhost:8080
	Number         string   `yaml:"number" json:"number"`   // the bot's own +number
	AllowedUsers   []string `yaml:"allowed_users" json:"allowed_users"`
	RequirePairing bool     `yaml:"require_pairing" json:"require_pairing"`
}

// WhatsApp uses the Meta Cloud API: a webhook receives messages, the Graph API
// sends them. The adapter runs its own listener on ListenAddr.
type WhatsApp struct {
	Enabled       bool     `yaml:"enabled" json:"enabled"`
	Token         string   `yaml:"token" json:"token"`                     // Graph API access token
	PhoneNumberID string   `yaml:"phone_number_id" json:"phone_number_id"` // for sending
	VerifyToken   string   `yaml:"verify_token" json:"verify_token"`       // webhook challenge secret
	ListenAddr    string   `yaml:"listen_addr" json:"listen_addr"`         // :8090
	Path          string   `yaml:"path" json:"path"`                       // /webhook
	AllowedUsers  []string `yaml:"allowed_users" json:"allowed_users"`
}

// Feishu (Lark) uses a webhook for events and a tenant token for sending. The
// adapter runs its own listener on ListenAddr.
type Feishu struct {
	Enabled      bool     `yaml:"enabled" json:"enabled"`
	AppID        string   `yaml:"app_id" json:"app_id"`
	AppSecret    string   `yaml:"app_secret" json:"app_secret"`
	VerifyToken  string   `yaml:"verify_token" json:"verify_token"`
	ListenAddr   string   `yaml:"listen_addr" json:"listen_addr"` // :8091
	Path         string   `yaml:"path" json:"path"`               // /webhook
	AllowedUsers []string `yaml:"allowed_users" json:"allowed_users"`
	AllowedChats []string `yaml:"allowed_chats" json:"allowed_chats"`
}

// Slack bot settings. Uses Socket Mode, so no public URL is needed: an
// app-level token (xapp-…) opens the socket and a bot token (xoxb-…) sends.
type Slack struct {
	Enabled         bool     `yaml:"enabled" json:"enabled"`
	AppToken        string   `yaml:"app_token" json:"app_token"`
	BotToken        string   `yaml:"bot_token" json:"bot_token"`
	AllowedUsers    []string `yaml:"allowed_users" json:"allowed_users"`
	AllowedChannels []string `yaml:"allowed_channels" json:"allowed_channels"`
	RequirePairing  bool     `yaml:"require_pairing" json:"require_pairing"`
	StreamEdits     bool     `yaml:"stream_edits" json:"stream_edits"`
}

// Matrix bot settings. Connects to a homeserver with an access token and
// long-polls /sync.
type Matrix struct {
	Enabled        bool     `yaml:"enabled" json:"enabled"`
	Homeserver     string   `yaml:"homeserver" json:"homeserver"` // https://matrix.org
	AccessToken    string   `yaml:"access_token" json:"access_token"`
	UserID         string   `yaml:"user_id" json:"user_id"` // @bot:matrix.org
	AllowedUsers   []string `yaml:"allowed_users" json:"allowed_users"`
	AllowedRooms   []string `yaml:"allowed_rooms" json:"allowed_rooms"`
	RequirePairing bool     `yaml:"require_pairing" json:"require_pairing"`
}

// Telegram bot settings.
type Telegram struct {
	Enabled        bool     `yaml:"enabled" json:"enabled"`
	BotToken       string   `yaml:"bot_token" json:"bot_token"`
	AllowedUsers   []string `yaml:"allowed_users" json:"allowed_users"`
	AllowedChats   []string `yaml:"allowed_chats" json:"allowed_chats"`
	RequirePairing bool     `yaml:"require_pairing" json:"require_pairing"`
	StreamEdits    bool     `yaml:"stream_edits" json:"stream_edits"`
}

// Discord bot settings.
type Discord struct {
	Enabled        bool     `yaml:"enabled" json:"enabled"`
	BotToken       string   `yaml:"bot_token" json:"bot_token"`
	AllowedUsers   []string `yaml:"allowed_users" json:"allowed_users"`
	AllowedGuilds  []string `yaml:"allowed_guilds" json:"allowed_guilds"`
	RequirePairing bool     `yaml:"require_pairing" json:"require_pairing"`
	Intents        int      `yaml:"intents" json:"intents"`
	// ReplyStyle is how the bot renders answers: "embed" (coloured card, the
	// default) or "plain" (a normal message). Empty means embed.
	ReplyStyle string `yaml:"reply_style" json:"reply_style"`
}

// Delegation bounds subagent recursion.
type Delegation struct {
	Enabled       bool `yaml:"enabled" json:"enabled"`
	MaxIterations int  `yaml:"max_iterations" json:"max_iterations"`
	MaxDepth      int  `yaml:"max_depth" json:"max_depth"`
	MaxParallel   int  `yaml:"max_parallel" json:"max_parallel"`
	// Subprocess runs each top-level sub-agent as its own antares process
	// instead of in-process, so a crashing sub-agent cannot take the parent
	// down. Findings, intel, and sessions are file-backed, so state still
	// flows. Off by default.
	Subprocess bool `yaml:"subprocess" json:"subprocess"`
}

// CodeExecution bounds the sandboxed code tool.
type CodeExecution struct {
	Enabled      bool `yaml:"enabled" json:"enabled"`
	Timeout      int  `yaml:"timeout" json:"timeout"`
	MaxToolCalls int  `yaml:"max_tool_calls" json:"max_tool_calls"`
}

// Guardrails detects and stops runaway tool loops.
type Guardrails struct {
	WarningsEnabled bool `yaml:"warnings_enabled" json:"warnings_enabled"`
	HardStopEnabled bool `yaml:"hard_stop_enabled" json:"hard_stop_enabled"`
	WarnAfter       int  `yaml:"warn_after" json:"warn_after"`
	HardStopAfter   int  `yaml:"hard_stop_after" json:"hard_stop_after"`
}

// SessionReset controls automatic session rotation.
type SessionReset struct {
	Mode        string `yaml:"mode" json:"mode"` // never|idle|daily
	IdleMinutes int    `yaml:"idle_minutes" json:"idle_minutes"`
	AtHour      int    `yaml:"at_hour" json:"at_hour"`
}

// Streaming toggles incremental responses.
type Streaming struct {
	Enabled bool `yaml:"enabled" json:"enabled"`
}

// Display holds UI preferences shared by the dashboard.
type Display struct {
	Compact          bool   `yaml:"compact" json:"compact"`
	ToolProgress     bool   `yaml:"tool_progress" json:"tool_progress"`
	ShowReasoning    bool   `yaml:"show_reasoning" json:"show_reasoning"`
	// MaxLiveReasoningChars caps how much of a streaming reasoning trace the
	// dashboard keeps in React state (trailing window). High-effort models can
	// emit hundreds of KB per turn; unbounded string growth freezes the tab.
	// 0 means unlimited. The full text is still persisted server-side and is
	// restored on hydrate after the turn completes.
	MaxLiveReasoningChars int    `yaml:"max_live_reasoning_chars" json:"max_live_reasoning_chars"`
	Theme                 string `yaml:"theme" json:"theme"`
	Skin                  string `yaml:"skin" json:"skin"`
	Language              string `yaml:"language" json:"language"`
	BellOnComplete        bool   `yaml:"bell_on_complete" json:"bell_on_complete"`
	InterimAssistant      bool   `yaml:"interim_assistant_messages" json:"interim_assistant_messages"`
}

// Logging controls log level and sinks.
type Logging struct {
	Level string `yaml:"level" json:"level"`
	File  string `yaml:"file" json:"file"`
	JSON  bool   `yaml:"json" json:"json"`
}

// MCP holds Model Context Protocol server registrations.
type MCP struct {
	Enabled bool                 `yaml:"enabled" json:"enabled"`
	Servers map[string]MCPServer `yaml:"servers" json:"servers"`
}

// MCPServer is one registered MCP server (stdio or http transport).
type MCPServer struct {
	Transport string            `yaml:"transport" json:"transport"` // stdio|http
	Command   string            `yaml:"command" json:"command"`
	Args      []string          `yaml:"args" json:"args"`
	Env       map[string]string `yaml:"env" json:"env"`
	URL       string            `yaml:"url" json:"url"`
	Headers   map[string]string `yaml:"headers" json:"headers"`
	Enabled   bool              `yaml:"enabled" json:"enabled"`
}
