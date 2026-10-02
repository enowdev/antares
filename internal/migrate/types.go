// Package migrate brings a user's setup over from another personal-assistant
// agent (Hermes, OpenClaw, nanobot, …): providers and keys, persona files,
// memory, skills, MCP servers, schedules and chat channels. Chat history is
// deliberately not migrated.
//
// The flow is detect → plan → apply. Detect is cheap and read-only. Plan reads
// the source and describes every item it would bring over, without writing
// anything. Apply writes the items the user kept, after a backup. Contract:
// docs/plans/2026-10-03-migrate-contract.md.
package migrate

import "context"

// Category groups plan items; the UI shows one section per category and the
// CLI's --only flag filters by it.
type Category string

const (
	CatProvider  Category = "provider"  // an LLM provider with its key
	CatModel     Category = "model"     // the default model (+ fallbacks)
	CatSoul      Category = "soul"      // persona → ~/.antares/SOUL.md
	CatAgentsMD  Category = "agents_md" // global instructions → ~/.antares/AGENTS.md
	CatUserMD    Category = "user_md"   // facts about the user → ~/.antares/USER.md
	CatMemory    Category = "memory"    // one memory entry → store.Memory
	CatKnowledge Category = "knowledge" // a note/daily file → RAG collection
	CatSkill     Category = "skill"     // a SKILL.md folder → ~/.antares/skills/<name>/
	CatMCP       Category = "mcp"       // an MCP server → config mcp.servers
	CatCron      Category = "cron"      // a schedule → store.CronJob
	CatChannel   Category = "channel"   // a chat channel → config gateway.<platform>
	CatRole      Category = "role"      // a sub-agent/agent profile → ~/.antares/roles/<name>.md
)

// Status says whether an item can be applied as is.
type Status string

const (
	// StatusReady applies without further input.
	StatusReady Status = "ready"
	// StatusConflict means Antares already has something in this place (a
	// provider with this id, a skill with this name, a configured Telegram
	// bot, a non-default SOUL.md). Apply needs a Resolution for it.
	StatusConflict Status = "conflict"
	// StatusNeedsInput means a value is missing and the user must supply it
	// (Input), e.g. a key that could not be read or decrypted.
	StatusNeedsInput Status = "needs_input"
	// StatusUnsupported cannot be migrated (a channel Antares has no gateway
	// for, an OAuth login, a one-shot schedule). Shown with the reason; never
	// applied.
	StatusUnsupported Status = "unsupported"
)

// Resolution chooses what Apply does with a conflicting item.
type Resolution string

const (
	ResolveSkip    Resolution = "skip"    // keep what Antares has (default)
	ResolveReplace Resolution = "replace" // overwrite it
	ResolveRename  Resolution = "rename"  // add beside it under a new id/name (providers, skills, MCP, roles)
	ResolveAppend  Resolution = "append"  // text files only: add the imported text under a heading
)

// Source is one agent Antares can migrate from. Implementations live in
// source_<id>.go and register themselves in init via Register.
type Source interface {
	// ID is the stable identifier used in the API and CLI: "hermes",
	// "openclaw", "nanobot", "picoclaw", "cowagent", "qwenpaw", "letta",
	// "zeroclaw", "nanoclaw", "openhuman".
	ID() string
	// Name is the display name ("Hermes Agent").
	Name() string
	// Detect looks for the agent's data under the default locations (or at
	// root when non-empty) and returns what it found. It only stats and
	// reads small config files; it never decrypts or prompts.
	Detect(ctx context.Context, root string) (Detection, error)
	// Plan reads the source at det.Root and returns every item it would
	// migrate. It may decrypt secrets (which can trigger an OS keychain
	// prompt — that prompt is the user's consent); it never writes.
	Plan(ctx context.Context, det Detection, env Env) (Plan, error)
}

// Env is what a source may consult while planning: the current Antares state
// (to mark conflicts) without importing the server.
type Env interface {
	ProviderExists(id string) bool
	SkillExists(name string) bool
	MCPServerExists(name string) bool
	RoleExists(name string) bool
	// ChannelConfigured reports whether gateway.<platform> already has a
	// token set ("telegram", "discord", "slack", "matrix", "signal",
	// "whatsapp", "feishu").
	ChannelConfigured(platform string) bool
	// TextFileState returns "missing", "default" (the shipped placeholder)
	// or "custom" for SOUL.md / AGENTS.md / USER.md.
	TextFileState(cat Category) string
}

// Detection is one installation found on disk.
type Detection struct {
	Source  string `json:"source"` // Source.ID()
	Name    string `json:"name"`   // Source.Name()
	Root    string `json:"root"`   // the agent's home dir, e.g. ~/.hermes
	Version string `json:"version,omitempty"`
	// Profile distinguishes several installs of one agent (Hermes profiles,
	// OpenClaw extra agents); empty for the default.
	Profile string `json:"profile,omitempty"`
	// Running is true when the agent's process/service appears to be running
	// (its bots would fight Antares' bots over the same tokens).
	Running bool `json:"running"`
	// Summary is a short count line for the picker, e.g.
	// "2 providers · 14 skills · Telegram".
	Summary string `json:"summary"`
}

// Plan is everything a source would bring over.
type Plan struct {
	Detection Detection `json:"detection"`
	Items     []Item    `json:"items"`
	// Warnings that are not about one item (e.g. "Hermes is running — stop
	// it before importing its Telegram token").
	Warnings []string `json:"warnings,omitempty"`
}

// Item is one thing to migrate.
type Item struct {
	// ID is stable for a given source state: "<category>:<key>", e.g.
	// "provider:openrouter", "skill:web-research", "memory:7".
	ID       string   `json:"id"`
	Category Category `json:"category"`
	// Title is the one-line label ("OpenRouter", "Telegram bot @mybot").
	Title string `json:"title"`
	// Detail is an optional second line ("3 models · key from .env").
	Detail string `json:"detail,omitempty"`
	Status Status `json:"status"`
	// Reason explains a conflict, a needed input or why it is unsupported.
	Reason string `json:"reason,omitempty"`
	// Input, for StatusNeedsInput, names the field the user must fill
	// ("api_key"); the UI renders a password field for keys.
	Input string `json:"input,omitempty"`
	// Secret marks an item carrying a credential; the API never returns the
	// value, only that one exists.
	Secret bool `json:"secret,omitempty"`
	// Default selection in the UI: true for ready items, false otherwise.
	Selected bool `json:"selected"`

	// Payload is the data Apply writes; one of these is set, by category.
	// Never serialised to clients (secrets live here).
	Payload Payload `json:"-"`
}

// Payload carries the concrete values for an item.
type Payload struct {
	Provider  *ProviderPayload
	Model     *ModelPayload
	Text      *TextPayload // soul, agents_md, user_md
	Memory    *MemoryPayload
	Knowledge *KnowledgePayload
	Skill     *SkillPayload
	MCP       *MCPPayload
	Cron      *CronPayload
	Channel   *ChannelPayload
	Role      *RolePayload
}

type ProviderPayload struct {
	ID      string // proposed Antares provider id
	Label   string
	Kind    string // an Antares provider kind (openai-compatible, anthropic, gemini, codex, openai, …)
	BaseURL string
	APIKey  string
	Headers map[string]string
	Models  []string
}

type ModelPayload struct {
	Provider string // Antares provider id (after rename resolution)
	Model    string
	Fallback []string
}

type TextPayload struct {
	Content string
	// From names the source file(s) for the heading used on append.
	From string
}

type MemoryPayload struct {
	Scope   string // "global" or "user"
	Key     string
	Content string
	Tags    []string
}

type KnowledgePayload struct {
	Path    string // source path, for the RAG doc id and meta
	Content string
}

type SkillPayload struct {
	Name string
	Dir  string // the source SKILL.md folder, copied whole
}

type MCPPayload struct {
	Name      string
	Transport string // "stdio" or "http"
	Command   string
	Args      []string
	Env       map[string]string
	URL       string
	Headers   map[string]string
}

type CronPayload struct {
	Name     string
	Schedule string // already converted to an Antares schedule (cron, @every, @daily…)
	Prompt   string
	Timezone string
	Enabled  bool
}

type ChannelPayload struct {
	Platform string // telegram | discord | slack | matrix | signal | whatsapp | feishu
	// Fields holds the gateway.<platform> keys to set, by their YAML name
	// (bot_token, app_token, allowed_users, allowed_chats, …).
	Fields map[string]any
}

type RolePayload struct {
	Name    string
	Title   string
	Summary string
	Prompt  string
	Model   string
}

// Choice is what the user decided for one item.
type Choice struct {
	ID         string     `json:"id"`
	Resolution Resolution `json:"resolution,omitempty"` // for conflicts
	// Input supplies a needs_input value (e.g. the API key).
	Input string `json:"input,omitempty"`
}

// Report is the outcome of Apply.
type Report struct {
	Source  string        `json:"source"`
	Backup  string        `json:"backup"` // backup dir, for undo
	Applied []string      `json:"applied"`
	Skipped []string      `json:"skipped"`
	Failed  []ItemFailure `json:"failed,omitempty"`
	// NeedsRestart is true when the gateway or MCP changed and a reload is
	// needed for them to take effect (the server applies it itself).
	NeedsRestart bool `json:"needs_restart"`
}

type ItemFailure struct {
	ID    string `json:"id"`
	Error string `json:"error"`
}

var registry []Source

// Register adds a source; called from each source file's init.
func Register(s Source) { registry = append(registry, s) }

// Sources returns the registered sources in registration order.
func Sources() []Source { return append([]Source(nil), registry...) }

// Lookup finds a source by id.
func Lookup(id string) (Source, bool) {
	for _, s := range registry {
		if s.ID() == id {
			return s, true
		}
	}
	return nil, false
}
