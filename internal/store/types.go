// Package store persists Antares state behind a driver-agnostic interface so
// the same code runs on SQLite (single node) or Postgres (shared/multi node).
package store

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// Meta is a free-form JSON blob attached to most records.
type Meta map[string]any

// Value serialises Meta for the SQL driver.
func (m Meta) Value() (any, error) {
	if m == nil {
		return "{}", nil
	}
	b, err := json.Marshal(m)
	return string(b), err
}

// Scan deserialises Meta from the SQL driver.
func (m *Meta) Scan(src any) error {
	if src == nil {
		*m = Meta{}
		return nil
	}
	var b []byte
	switch v := src.(type) {
	case []byte:
		b = v
	case string:
		b = []byte(v)
	default:
		*m = Meta{}
		return nil
	}
	if len(b) == 0 {
		*m = Meta{}
		return nil
	}
	return json.Unmarshal(b, m)
}

// Session is one conversation thread.
type Session struct {
	ID           string    `json:"id"`
	Title        string    `json:"title"`
	Platform     string    `json:"platform"`
	ChannelID    string    `json:"channel_id"`
	UserID       string    `json:"user_id"`
	Model        string    `json:"model"`
	Provider     string    `json:"provider"`
	Workspace    string    `json:"workspace"`
	MessageCount int       `json:"message_count"`
	TokensIn     int64     `json:"tokens_in"`
	TokensOut    int64     `json:"tokens_out"`
	Cost         float64   `json:"cost"`
	Archived     bool      `json:"archived"`
	Pinned       bool      `json:"pinned"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
	Meta         Meta      `json:"meta"`
}

// Role values for Message.
const (
	RoleSystem    = "system"
	RoleUser      = "user"
	RoleAssistant = "assistant"
	RoleTool      = "tool"
)

// Message is one turn element inside a session.
type Message struct {
	ID          string    `json:"id"`
	SessionID   string    `json:"session_id"`
	Seq         int64     `json:"seq"`
	Role        string    `json:"role"`
	Content     string    `json:"content"`
	Reasoning   string    `json:"reasoning,omitempty"`
	ToolCalls   string    `json:"tool_calls,omitempty"` // JSON array
	ToolCallID  string    `json:"tool_call_id,omitempty"`
	ToolName    string    `json:"tool_name,omitempty"`
	Attachments string    `json:"attachments,omitempty"` // JSON array
	Model       string    `json:"model,omitempty"`
	TokensIn    int       `json:"tokens_in"`
	TokensOut   int       `json:"tokens_out"`
	Hidden      bool      `json:"hidden"`
	Compacted   bool      `json:"compacted"`
	CreatedAt   time.Time `json:"created_at"`
	Meta        Meta      `json:"meta"`
}

// Memory is one agent-curated long-term fact.
type Memory struct {
	ID        string    `json:"id"`
	Scope     string    `json:"scope"` // global|user|session|project
	ScopeKey  string    `json:"scope_key"`
	Key       string    `json:"key"`
	Content   string    `json:"content"`
	Tags      string    `json:"tags"` // JSON array
	Source    string    `json:"source"`
	Pinned    bool      `json:"pinned"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// VPSHost is a saved server the user can monitor and manage over SSH. The
// secret fields (Password, PrivateKey, Passphrase) are stored encrypted at
// rest; the store encrypts on Put and decrypts on read, so callers see plain
// values but the DB never holds them in the clear.
type VPSHost struct {
	ID         string `json:"id"`
	Label      string `json:"label"`
	Host       string `json:"host"`
	Port       int    `json:"port"`
	Username   string `json:"username"`
	AuthMethod string `json:"auth_method"` // password|key
	Password   string `json:"password,omitempty"`
	PrivateKey string `json:"private_key,omitempty"`
	Passphrase string `json:"passphrase,omitempty"`
	// HostKey is the server's SSH public key, pinned on first connect (TOFU).
	// A later connection presenting a different key is refused. Not secret.
	HostKey   string    `json:"host_key,omitempty"`
	FolderID  string    `json:"folder_id"`
	SortOrder int64     `json:"sort_order"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// VPSFolder groups saved servers in an arbitrarily deep adjacency tree. An
// empty ParentID is the root; folder and host ordering are independent.
type VPSFolder struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	ParentID  string    `json:"parent_id"`
	SortOrder int64     `json:"sort_order"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Addr is the host:port to dial.
func (v VPSHost) Addr() string {
	p := v.Port
	if p == 0 {
		p = 22
	}
	return fmt.Sprintf("%s:%d", v.Host, p)
}

// Chunk is one embedded text span in the builtin RAG index.
type Chunk struct {
	ID         string    `json:"id"`
	Collection string    `json:"collection"`
	DocID      string    `json:"doc_id"`
	Path       string    `json:"path"`
	Index      int       `json:"index"`
	Content    string    `json:"content"`
	Embedding  []float32 `json:"-"`
	Meta       Meta      `json:"meta"`
	CreatedAt  time.Time `json:"created_at"`
}

// CronJob is a scheduled natural-language task.
type CronJob struct {
	ID        string     `json:"id"`
	Name      string     `json:"name"`
	Schedule  string     `json:"schedule"`
	Prompt    string     `json:"prompt"`
	Enabled   bool       `json:"enabled"`
	Target    string     `json:"target"`
	Timezone  string     `json:"timezone"`
	LastRun   *time.Time `json:"last_run"`
	NextRun   *time.Time `json:"next_run"`
	LastState string     `json:"last_state"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
	Meta      Meta       `json:"meta"`
}

// CronRun records one execution of a CronJob.
type CronRun struct {
	ID         string     `json:"id"`
	JobID      string     `json:"job_id"`
	Status     string     `json:"status"` // running|ok|error|skipped
	Output     string     `json:"output"`
	Error      string     `json:"error"`
	SessionID  string     `json:"session_id"`
	StartedAt  time.Time  `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at"`
}

// Usage is a per-call accounting record powering the analytics page.
type Usage struct {
	ID        string    `json:"id"`
	SessionID string    `json:"session_id"`
	Provider  string    `json:"provider"`
	Model     string    `json:"model"`
	TokensIn  int       `json:"tokens_in"`
	TokensOut int       `json:"tokens_out"`
	CacheRead int       `json:"cache_read"`
	Cost      float64   `json:"cost"`
	Source    string    `json:"source"`
	CreatedAt time.Time `json:"created_at"`
}

// Pairing is an approved external identity (Telegram/Discord user).
type Pairing struct {
	ID          string    `json:"id"`
	Platform    string    `json:"platform"`
	ExternalID  string    `json:"external_id"`
	DisplayName string    `json:"display_name"`
	Status      string    `json:"status"` // pending|approved|revoked
	Code        string    `json:"code"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// SessionFilter narrows a session listing.
type SessionFilter struct {
	Platform string
	// ExcludePlatforms hides sessions from these platforms — used to keep
	// sub-agent sessions ("subagent", "background") out of the human's list.
	ExcludePlatforms []string
	UserID           string
	Query            string
	Archived         *bool
	Limit            int
	Offset           int
	OrderBy          string // updated_at|created_at|message_count
}

// SearchHit is a full-text match inside a session.
type SearchHit struct {
	SessionID    string    `json:"session_id"`
	SessionTitle string    `json:"session_title"`
	MessageID    string    `json:"message_id"`
	Role         string    `json:"role"`
	Snippet      string    `json:"snippet"`
	CreatedAt    time.Time `json:"created_at"`
	Score        float64   `json:"score"`
}

// Stats summarises the database for the dashboard header.
type Stats struct {
	Sessions      int64   `json:"sessions"`
	Messages      int64   `json:"messages"`
	Memories      int64   `json:"memories"`
	CronJobs      int64   `json:"cron_jobs"`
	TokensIn      int64   `json:"tokens_in"`
	TokensOut     int64   `json:"tokens_out"`
	Cost          float64 `json:"cost"`
	EmptySessions int64   `json:"empty_sessions"`
	SizeBytes     int64   `json:"size_bytes"`
}

// UsagePoint is one bucket in the analytics timeseries.
type UsagePoint struct {
	Bucket    string  `json:"bucket"`
	TokensIn  int64   `json:"tokens_in"`
	TokensOut int64   `json:"tokens_out"`
	Cost      float64 `json:"cost"`
	Calls     int64   `json:"calls"`
}

// ModelUsage aggregates spend per model.
type ModelUsage struct {
	Model     string  `json:"model"`
	Provider  string  `json:"provider"`
	Calls     int64   `json:"calls"`
	TokensIn  int64   `json:"tokens_in"`
	TokensOut int64   `json:"tokens_out"`
	Cost      float64 `json:"cost"`
}

// Store is the persistence contract every driver implements.
type Store interface {
	Driver() string
	Close() error
	Ping(ctx context.Context) error
	Stats(ctx context.Context) (Stats, error)

	CreateSession(ctx context.Context, s *Session) error
	GetSession(ctx context.Context, id string) (*Session, error)
	UpdateSession(ctx context.Context, s *Session) error
	// SetSessionTitle updates only the title (and updated_at), leaving meta and
	// the message counters untouched — so a title save from a stale in-memory
	// struct cannot clobber a tool's concurrent meta write.
	SetSessionTitle(ctx context.Context, id, title string) error
	// TouchSession bumps only updated_at.
	TouchSession(ctx context.Context, id string) error
	ListSessions(ctx context.Context, f SessionFilter) ([]Session, int64, error)
	DeleteSession(ctx context.Context, id string) error
	DeleteSessions(ctx context.Context, ids []string) (int64, error)
	DeleteEmptySessions(ctx context.Context) (int64, error)
	CountEmptySessions(ctx context.Context) (int64, error)
	PruneSessions(ctx context.Context, olderThan time.Time) (int64, error)

	AppendMessage(ctx context.Context, m *Message) error
	ListMessages(ctx context.Context, sessionID string, limit, offset int) ([]Message, error)
	DeleteMessage(ctx context.Context, id string) error
	// DeleteMessagesFrom removes a message and everything after it in its session,
	// then recomputes the session tallies. Powers "edit message".
	DeleteMessagesFrom(ctx context.Context, sessionID, fromID string) error
	SearchMessages(ctx context.Context, query string, limit int) ([]SearchHit, error)

	PutMemory(ctx context.Context, m *Memory) error
	ListMemories(ctx context.Context, scope, scopeKey string, limit int) ([]Memory, error)
	SearchMemories(ctx context.Context, query string, limit int) ([]Memory, error)
	DeleteMemory(ctx context.Context, id string) error
	ClearMemories(ctx context.Context, scope, scopeKey string) (int64, error)

	PutChunks(ctx context.Context, chunks []Chunk) error
	SearchChunks(ctx context.Context, collection string, embedding []float32, text string, topK int, hybrid bool) ([]Chunk, []float64, error)
	DeleteCollection(ctx context.Context, collection string) (int64, error)
	ListCollections(ctx context.Context) ([]string, error)

	PutVPSHost(ctx context.Context, v *VPSHost) error
	GetVPSHost(ctx context.Context, id string) (*VPSHost, error)
	ListVPSHosts(ctx context.Context) ([]VPSHost, error)
	DeleteVPSHost(ctx context.Context, id string) error
	SetVPSHostKey(ctx context.Context, id, hostKey string) error
	CreateVPSFolder(ctx context.Context, f *VPSFolder) error
	RenameVPSFolder(ctx context.Context, id, name string) error
	ListVPSFolders(ctx context.Context) ([]VPSFolder, error)
	MoveVPSFolder(ctx context.Context, id, parentID string, index int) error
	MoveVPSHost(ctx context.Context, id, folderID string, index int) error
	DeleteVPSFolder(ctx context.Context, id string) error

	PutCronJob(ctx context.Context, j *CronJob) error
	GetCronJob(ctx context.Context, id string) (*CronJob, error)
	ListCronJobs(ctx context.Context) ([]CronJob, error)
	DeleteCronJob(ctx context.Context, id string) error
	PutCronRun(ctx context.Context, r *CronRun) error
	ListCronRuns(ctx context.Context, jobID string, limit int) ([]CronRun, error)

	RecordUsage(ctx context.Context, u *Usage) error
	UsageSeries(ctx context.Context, since time.Time, bucket string) ([]UsagePoint, error)
	UsageByModel(ctx context.Context, since time.Time) ([]ModelUsage, error)

	PutPairing(ctx context.Context, p *Pairing) error
	GetPairing(ctx context.Context, platform, externalID string) (*Pairing, error)
	ListPairings(ctx context.Context) ([]Pairing, error)
	DeletePairing(ctx context.Context, id string) error

	GetKV(ctx context.Context, key string) (string, error)
	SetKV(ctx context.Context, key, value string) error
	DeleteKV(ctx context.Context, key string) error
	ListKV(ctx context.Context, prefix string) (map[string]string, error)
}
