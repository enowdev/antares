// Package llm normalises several vendor chat APIs behind one streaming
// interface: OpenAI-compatible, OpenAI, Anthropic, Gemini, and custom endpoints.
package llm

import (
	"context"
	"encoding/json"
)

// Roles used in Message.
const (
	RoleSystem    = "system"
	RoleUser      = "user"
	RoleAssistant = "assistant"
	RoleTool      = "tool"
)

// Part is one element of a multimodal message.
type Part struct {
	Type     string `json:"type"` // text|image|file
	Text     string `json:"text,omitempty"`
	MimeType string `json:"mime_type,omitempty"`
	// Data is base64 without a data: prefix; URL is used when the source is remote.
	Data string `json:"data,omitempty"`
	URL  string `json:"url,omitempty"`
}

// ToolCall is a model request to invoke a tool.
type ToolCall struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"` // raw JSON
	// ThoughtSignature is Gemini's encrypted reasoning state that must be
	// echoed back on the same functionCall part in the next request.
	// Without it, multi-turn tool use returns HTTP 400 ("Function call is
	// missing a thought_signature"). Official SDKs and Gemini CLI preserve
	// this; when absent, clients inject "skip_thought_signature_validator".
	// See https://ai.google.dev/gemini-api/docs/thinking#signatures
	ThoughtSignature string `json:"thought_signature,omitempty"`
}

// Message is one conversation element in provider-neutral form.
type Message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content,omitempty"`
	Parts      []Part     `json:"parts,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	Name       string     `json:"name,omitempty"`
	Reasoning  string     `json:"reasoning,omitempty"`
	// ThoughtSignature is Gemini part-level metadata for assistant text
	// (non-tool) turns. Echoed on the text part when re-sending history.
	ThoughtSignature string `json:"thought_signature,omitempty"`
	// CacheHint marks a prefix boundary for providers that support prompt caching.
	CacheHint bool `json:"cache_hint,omitempty"`
}

// ToolName is the function name a tool result belongs to. Providers that key
// results by name rather than call id (Gemini) rely on it.
func (m Message) ToolName() string { return m.Name }

// Tool is a function the model may call.
type Tool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

// Request is a normalised completion request.
type Request struct {
	Model             string
	System            string
	Messages          []Message
	Tools             []Tool
	ToolChoice        string // auto|none|required|<tool name>
	Temperature       float64
	TopP              float64
	MaxTokens         int
	StopSequences     []string
	ReasoningEffort   string // none|low|medium|high
	ParallelToolCalls bool
	PromptCache       bool
	Extra             map[string]any
}

// Usage reports token accounting for one call.
//
// ContextTokens is the provider-normalised size of the prompt that occupied the
// model context for this call. It is deliberately separate from cache billing
// fields: OpenAI-compatible and Gemini APIs report cached tokens as a subset of
// InputTokens, while Anthropic reports them alongside InputTokens. Consumers
// must not infer context size by blindly adding cache fields.
type Usage struct {
	InputTokens      int     `json:"input_tokens"`
	OutputTokens     int     `json:"output_tokens"`
	CacheReadTokens  int     `json:"cache_read_tokens"`
	CacheWriteTokens int     `json:"cache_write_tokens"`
	ReasoningTokens  int     `json:"reasoning_tokens"`
	ContextTokens    int     `json:"context_tokens"`
	Cost             float64 `json:"cost"`
}

// ContextSize returns the provider-normalised prompt size for context-window
// accounting. Adapters with cache counters whose semantics differ set
// ContextTokens explicitly. The conservative fallback treats InputTokens as the
// complete prompt so an unknown adapter cannot double-count a cache subset.
func (u Usage) ContextSize() int {
	if u.ContextTokens > 0 {
		return u.ContextTokens
	}
	return u.InputTokens
}

// Response is the final result of a completion.
type Response struct {
	Content      string     `json:"content"`
	Reasoning    string     `json:"reasoning,omitempty"`
	ToolCalls    []ToolCall `json:"tool_calls,omitempty"`
	// ThoughtSignature is Gemini part-level metadata for the final text turn.
	// Agent history must copy this onto the assistant Message for multi-turn
	// continuity when the model is not making tool calls.
	ThoughtSignature string `json:"thought_signature,omitempty"`
	FinishReason     string `json:"finish_reason"`
	Model            string `json:"model"`
	Usage            Usage  `json:"usage"`
	Raw              json.RawMessage
}

// EventType enumerates streaming callbacks.
type EventType string

const (
	EventText          EventType = "text"
	EventReasoning     EventType = "reasoning"
	EventToolCallStart EventType = "tool_call_start"
	EventToolCallDelta EventType = "tool_call_delta"
	EventToolCallEnd   EventType = "tool_call_end"
	EventUsage         EventType = "usage"
	EventDone          EventType = "done"
	EventError         EventType = "error"
)

// Event is one incremental streaming update.
type Event struct {
	Type       EventType `json:"type"`
	Delta      string    `json:"delta,omitempty"`
	Index      int       `json:"index,omitempty"`
	ToolCallID string    `json:"tool_call_id,omitempty"`
	ToolName   string    `json:"tool_name,omitempty"`
	Usage      *Usage    `json:"usage,omitempty"`
	Err        string    `json:"error,omitempty"`
}

// ModelInfo describes one model offered by a provider.
type ModelInfo struct {
	ID            string  `json:"id"`
	Name          string  `json:"name"`
	Provider      string  `json:"provider"`
	ContextWindow int     `json:"context_window"`
	MaxOutput     int     `json:"max_output"`
	InputCost     float64 `json:"input_cost"`  // USD per 1M tokens
	OutputCost    float64 `json:"output_cost"` // USD per 1M tokens
	Vision        bool    `json:"vision"`
	Tools         bool    `json:"tools"`
	Reasoning     bool    `json:"reasoning"`
}

// Client is a provider adapter.
type Client interface {
	// Kind reports the adapter family (openai, anthropic, gemini, ...).
	Kind() string
	// Chat runs a non-streaming completion.
	Chat(ctx context.Context, req Request) (*Response, error)
	// Stream runs a completion, invoking emit for each incremental event. The
	// final Response is returned once the stream completes.
	Stream(ctx context.Context, req Request, emit func(Event) error) (*Response, error)
	// Models lists available models; providers that cannot enumerate return nil.
	Models(ctx context.Context) ([]ModelInfo, error)
	// Embed produces embedding vectors, or ErrUnsupported.
	Embed(ctx context.Context, model string, inputs []string) ([][]float32, error)
}

// AudioClient is an optional capability: text-to-speech and speech-to-text.
// Adapters that support it (the OpenAI family) implement it; callers type-assert.
type AudioClient interface {
	// Speak synthesises speech from text, returning the audio bytes and the file
	// extension for them (e.g. "mp3").
	Speak(ctx context.Context, model, voice, format, text string) ([]byte, string, error)
	// Transcribe turns speech audio into text.
	Transcribe(ctx context.Context, model, filename string, audio []byte) (string, error)
}
