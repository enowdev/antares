package tools

import (
	"context"
	"time"

	"github.com/enowdev/antares/internal/board"
	"github.com/enowdev/antares/internal/config"
	"github.com/enowdev/antares/internal/cursorrun"
	"github.com/enowdev/antares/internal/engagement"
	"github.com/enowdev/antares/internal/findings"
	"github.com/enowdev/antares/internal/store"
)

// RAGResult is one retrieved passage.
type RAGResult struct {
	Content string  `json:"content"`
	Path    string  `json:"path,omitempty"`
	DocID   string  `json:"doc_id,omitempty"`
	Score   float64 `json:"score"`
}

// RAGProvider is the native retrieval backend (vectors in the Antares DB).
type RAGProvider interface {
	Name() string
	Search(ctx context.Context, collection, query string, topK int) ([]RAGResult, error)
	Index(ctx context.Context, collection string, docs []RAGDoc) (int, error)
	Collections(ctx context.Context) ([]string, error)
	Delete(ctx context.Context, collection string) error
}

// RAGDoc is one document submitted for indexing.
type RAGDoc struct {
	ID      string         `json:"id"`
	Path    string         `json:"path"`
	Content string         `json:"content"`
	Meta    map[string]any `json:"meta,omitempty"`
}

// SubAgent runs a nested agent turn for delegate_task.
type SubAgent func(ctx context.Context, req SubAgentRequest) (string, error)

// SubAgentRequest describes a delegated workstream.
type SubAgentRequest struct {
	Prompt      string
	SystemExtra string
	Toolset     string
	Model       string
	// Role names a specialist to run this workstream as. It sets the prompt,
	// toolset, and model unless those are given explicitly.
	Role string
	// Isolate runs the sub-agent in its own git worktree, so parallel
	// sub-agents editing the same repository do not conflict.
	Isolate bool
	// Workspace overrides the sub-agent's working directory.
	Workspace  string
	MaxTurns   int
	ParentID   string
	OnProgress func(Progress)
}

// BackgroundTasks runs sub-agents asynchronously: Start returns a handle
// immediately and the work continues in the background, to be polled later. It
// is how one turn dispatches several long workstreams without blocking on each.
type BackgroundTasks interface {
	Start(req SubAgentRequest) string
	Status(id string) (TaskInfo, bool)
	List(parent string) []TaskInfo
	Stop(id string) bool
	// Send continues a finished task with a follow-up, running another turn on
	// its session and returning the new reply.
	Send(id, message string) (string, error)
}

// TaskInfo is a snapshot of a background task.
type TaskInfo struct {
	ID        string    `json:"id"`
	Role      string    `json:"role"`
	Task      string    `json:"task"`
	Status    string    `json:"status"` // running|done|error|stopped
	Output    string    `json:"output"`
	Error     string    `json:"error"`
	StartedAt time.Time `json:"started_at"`
	EndedAt   time.Time `json:"ended_at"`
}

// SkillLibrary is the subset of the skills manager that tools need.
type SkillLibrary interface {
	List() []SkillInfo
	Search(query string, limit int) []SkillInfo
	// SearchFiltered ranks matches and narrows by security metadata; empty
	// filter fields are ignored.
	SearchFiltered(query, cwe, tech, category string, limit int) []SkillInfo
	// Chains resolves a skill's follow-on techniques (chains_with).
	Chains(name string) []SkillInfo
	Read(name string) (SkillInfo, string, bool)
	Write(name, description, body string, tags []string) error
	MarkUsed(name string)
}

// SkillInfo describes one stored skill.
type SkillInfo struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Tags        []string `json:"tags"`
	Triggers    []string `json:"triggers"`
	Enabled     bool     `json:"enabled"`
	// Security-library metadata, empty for everyday skills.
	CWEIDs     []string `json:"cwe_ids,omitempty"`
	TechStack  []string `json:"tech_stack,omitempty"`
	OWASPID    string   `json:"owasp_id,omitempty"`
	ChainsWith []string `json:"chains_with,omitempty"`
}

// Deps bundles the services tools may reach for.
type Deps struct {
	Config *config.Config
	Store  store.Store
	RAG    RAGProvider
	Sub    SubAgent
	Skills SkillLibrary
	// Tasks runs sub-agents in the background. It may be nil.
	Tasks BackgroundTasks
	// Shell owns persistent terminal sessions keyed by session id.
	Shell *ShellManager
	// Checkpoint saves a copy of a file about to be changed. It may be nil,
	// in which case nothing is kept and an edit cannot be undone.
	Checkpoint func(sessionID, path, tool string)
	// RecordResult notes the sha256 of what a tool just wrote to a file, so a
	// later rollback can tell session-produced content from an outside edit. Nil
	// when checkpoints are off.
	RecordResult func(sessionID, path, resultHash string)
	// Roles lists the specialist roles available for delegation. It may be nil.
	Roles func() []RoleInfo
	// Vision describes an image using a vision model. It may be nil.
	Vision func(ctx context.Context, data []byte, mime, question string) (string, error)
	// Speak synthesises speech from text, returning the audio and its extension.
	// It may be nil.
	Speak func(ctx context.Context, text, voice string) (audio []byte, ext string, err error)
	// Transcribe turns speech audio into text. It may be nil.
	Transcribe func(ctx context.Context, filename string, audio []byte) (string, error)
	// Board is the Kanban board store. It may be nil.
	Board *board.Board
	// Findings is the security engagement ledger. It may be nil.
	Findings FindingStore
	// Intel is the engagement's fact ledger and methodology. It may be nil.
	Intel IntelStore
	// SocialBrowser is the persistent stealth Chromium for social media. Nil
	// when the social feature is not configured.
	SocialBrowser SocialBrowserManager
	// Cursor owns the shared Cursor catalogue and remote-run lifecycle. It may
	// be nil in runtimes that do not wire the Cursor integration.
	Cursor cursorrun.Runner
}

// SocialBrowserManager is the minimal interface the social_browser tool needs
// from the socialbrowser.Manager.
type SocialBrowserManager interface {
	Status() (string, string)
	Start(ctx context.Context) error
	Stop()
}

// RoleInfo is the subset of a role the tools need.
type RoleInfo struct {
	Name     string
	Summary  string
	Category string
	Danger   bool
	// Subrole is true for a specialist reached only through its master role.
	Subrole bool
	// Parent names the master role a subrole belongs to.
	Parent string
}

// FindingStore is the subset of the findings store the tools need.
type FindingStore interface {
	Add(sessionID string, f findings.Finding) (findings.Finding, error)
	Triage(sessionID, id string, status findings.Status, duplicateOf string) (findings.Finding, bool, error)
	List(sessionID string) ([]findings.Finding, error)
}

// IntelStore is the subset of the engagement store the tools need.
type IntelStore interface {
	Add(sessionID string, in engagement.Intel) (engagement.Intel, bool, error)
	State(sessionID string, hasScope, hasReport bool) ([]engagement.PhaseState, error)
	List(sessionID string) ([]engagement.Intel, error)
}
