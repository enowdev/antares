package cursor

import "encoding/json"

type Me struct {
	APIKeyName    string `json:"apiKeyName"`
	CreatedAt     string `json:"createdAt"`
	UserID        int64  `json:"userId,omitempty"`
	UserEmail     string `json:"userEmail,omitempty"`
	UserFirstName string `json:"userFirstName,omitempty"`
	UserLastName  string `json:"userLastName,omitempty"`
}

type ModelParameterValue struct {
	Value       string `json:"value"`
	DisplayName string `json:"displayName,omitempty"`
}

type ModelParameter struct {
	ID          string                `json:"id"`
	DisplayName string                `json:"displayName,omitempty"`
	Values      []ModelParameterValue `json:"values"`
}

type ModelParameterSelection struct {
	ID    string `json:"id"`
	Value string `json:"value"`
}

type ModelVariant struct {
	Params      []ModelParameterSelection `json:"params"`
	DisplayName string                    `json:"displayName"`
	Description string                    `json:"description,omitempty"`
	IsDefault   bool                      `json:"isDefault,omitempty"`
}

type Model struct {
	ID          string           `json:"id"`
	DisplayName string           `json:"displayName"`
	Description string           `json:"description,omitempty"`
	Aliases     []string         `json:"aliases,omitempty"`
	Parameters  []ModelParameter `json:"parameters,omitempty"`
	Variants    []ModelVariant   `json:"variants,omitempty"`
}

type ModelCatalog struct {
	Items []Model `json:"items"`
}

type StreamEvent struct {
	ID     string
	Type   string
	Status string
	Text   string
	RunID  string
	Raw    json.RawMessage

	// Tool call fields, populated only for Type == "tool_call". CallID
	// identifies one tool invocation across its "running" and "completed"
	// updates. ToolArgs/ToolResult are the raw JSON values Cursor sent;
	// either is nil when Cursor omitted it (not yet available, or dropped
	// because it exceeded the stream's inline size limit, in which case the
	// matching *Truncated flag is set).
	ToolName        string
	CallID          string
	ToolArgs        json.RawMessage
	ToolResult      json.RawMessage
	ArgsTruncated   bool
	ResultTruncated bool
}

// PromptImage is an image attachment on a prompt. Exactly one of Data (a
// base64-encoded payload) or URL should be set, per the Cursor Cloud Agents
// API; MimeType is required alongside Data.
type PromptImage struct {
	Data     string `json:"data,omitempty"`
	URL      string `json:"url,omitempty"`
	MimeType string `json:"mimeType,omitempty"`
}

type Prompt struct {
	Text   string        `json:"text"`
	Images []PromptImage `json:"images,omitempty"`
}

type ModelSelection struct {
	ID     string                    `json:"id"`
	Params []ModelParameterSelection `json:"params,omitempty"`
}

type Repository struct {
	URL         string `json:"url"`
	StartingRef string `json:"startingRef,omitempty"`
	PRURL       string `json:"prUrl,omitempty"`
}

type CreateAgentRequest struct {
	Prompt              Prompt          `json:"prompt"`
	Model               *ModelSelection `json:"model,omitempty"`
	Name                string          `json:"name,omitempty"`
	Repos               []Repository    `json:"repos,omitempty"`
	WorkOnCurrentBranch bool            `json:"workOnCurrentBranch,omitempty"`
	AutoCreatePR        bool            `json:"autoCreatePR,omitempty"`
	SkipReviewerRequest bool            `json:"skipReviewerRequest,omitempty"`
	Mode                string          `json:"mode,omitempty"`
}

type CreateRunRequest struct {
	Prompt Prompt `json:"prompt"`
	Mode   string `json:"mode,omitempty"`
}

type GitBranch struct {
	RepoURL string `json:"repoUrl"`
	Branch  string `json:"branch,omitempty"`
	PRURL   string `json:"prUrl,omitempty"`
}

type GitState struct {
	Branches []GitBranch `json:"branches"`
}

type Agent struct {
	ID          string       `json:"id"`
	Name        string       `json:"name"`
	Status      string       `json:"status"`
	URL         string       `json:"url"`
	LatestRunID string       `json:"latestRunId"`
	Git         *GitState    `json:"git,omitempty"`
	Repos       []Repository `json:"repos,omitempty"`
}

type Run struct {
	ID         string    `json:"id"`
	AgentID    string    `json:"agentId"`
	Status     string    `json:"status"`
	CreatedAt  string    `json:"createdAt"`
	UpdatedAt  string    `json:"updatedAt"`
	DurationMS int64     `json:"durationMs,omitempty"`
	Result     string    `json:"result,omitempty"`
	Git        *GitState `json:"git,omitempty"`
}

type CreateAgentResponse struct {
	Agent Agent `json:"agent"`
	Run   Run   `json:"run"`
}

type CreateRunResponse struct {
	Run Run `json:"run"`
}
