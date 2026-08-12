package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// anthropicClient speaks the Anthropic Messages API.
type anthropicClient struct{ opts Options }

func (c *anthropicClient) Kind() string { return "anthropic" }

func (c *anthropicClient) headers() map[string]string {
	h := map[string]string{
		"anthropic-version": "2023-06-01",
	}
	if c.opts.APIKey != "" {
		h["x-api-key"] = c.opts.APIKey
	}
	return h
}

type antContent struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`

	// tool_use
	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`

	// tool_result
	ToolUseID string `json:"tool_use_id,omitempty"`
	Content   any    `json:"content,omitempty"`
	IsError   bool   `json:"is_error,omitempty"`

	// image
	Source *antSource `json:"source,omitempty"`

	// thinking
	Thinking  string `json:"thinking,omitempty"`
	Signature string `json:"signature,omitempty"`

	CacheControl *antCacheControl `json:"cache_control,omitempty"`
}

type antCacheControl struct {
	Type string `json:"type"`
}

type antSource struct {
	Type      string `json:"type"`
	MediaType string `json:"media_type,omitempty"`
	Data      string `json:"data,omitempty"`
	URL       string `json:"url,omitempty"`
}

type antMessage struct {
	Role    string       `json:"role"`
	Content []antContent `json:"content"`
}

// toAnthropic converts neutral messages, merging tool results into user turns
// as the Messages API requires.
func toAnthropic(req Request) []antMessage {
	var out []antMessage
	appendMsg := func(role string, parts []antContent) {
		if len(parts) == 0 {
			return
		}
		if n := len(out); n > 0 && out[n-1].Role == role {
			out[n-1].Content = append(out[n-1].Content, parts...)
			return
		}
		out = append(out, antMessage{Role: role, Content: parts})
	}

	for _, m := range req.Messages {
		switch m.Role {
		case RoleSystem:
			// System content is hoisted into the top-level system field.
			continue
		case RoleTool:
			part := antContent{Type: "tool_result", ToolUseID: m.ToolCallID, Content: m.Content}
			appendMsg(RoleUser, []antContent{part})
		case RoleAssistant:
			var parts []antContent
			if strings.TrimSpace(m.Reasoning) != "" {
				parts = append(parts, antContent{Type: "text", Text: m.Reasoning})
			}
			if strings.TrimSpace(m.Content) != "" {
				parts = append(parts, antContent{Type: "text", Text: m.Content})
			}
			for _, tc := range m.ToolCalls {
				args := json.RawMessage(tc.Arguments)
				if strings.TrimSpace(tc.Arguments) == "" {
					args = json.RawMessage("{}")
				}
				parts = append(parts, antContent{Type: "tool_use", ID: tc.ID, Name: tc.Name, Input: args})
			}
			appendMsg(RoleAssistant, parts)
		default:
			var parts []antContent
			if strings.TrimSpace(m.Content) != "" {
				parts = append(parts, antContent{Type: "text", Text: m.Content})
			}
			for _, p := range m.Parts {
				switch p.Type {
				case "text":
					parts = append(parts, antContent{Type: "text", Text: p.Text})
				case "image":
					src := &antSource{Type: "base64", MediaType: p.MimeType, Data: p.Data}
					if p.URL != "" {
						src = &antSource{Type: "url", URL: p.URL}
					}
					if src.MediaType == "" && src.Type == "base64" {
						src.MediaType = "image/png"
					}
					parts = append(parts, antContent{Type: "image", Source: src})
				}
			}
			if m.CacheHint && len(parts) > 0 {
				parts[len(parts)-1].CacheControl = &antCacheControl{Type: "ephemeral"}
			}
			appendMsg(RoleUser, parts)
		}
	}
	if len(out) == 0 {
		out = append(out, antMessage{Role: RoleUser, Content: []antContent{{Type: "text", Text: "(empty)"}}})
	}
	return out
}

// anthropicSupportsAdaptiveThinking reports whether model belongs to the
// adaptive-thinking generation (Claude Sonnet 5 and later dated snapshots),
// which uses "thinking":{"type":"adaptive"} plus output_config.effort instead
// of a fixed token budget.
func anthropicSupportsAdaptiveThinking(model string) bool {
	return exactModelOrDatedSnapshot("claude-sonnet-5", model)
}

// anthropicLegacyThinkingBudgets lists pre-adaptive extended-thinking model
// families and the token budgets published for their effort ladder.
// https://docs.anthropic.com/en/docs/build-with-claude/extended-thinking
var anthropicLegacyThinkingBudgets = []struct {
	family  string
	budgets map[string]int
}{
	{family: "claude-3-7-sonnet", budgets: map[string]int{"low": 2048, "medium": 8192, "high": 16384}},
}

// anthropicLegacyBudget resolves the fixed token budget for a catalogued
// legacy model and effort value. It never guesses a budget for an
// unrecognised model or an effort value outside that model's documented
// ladder.
func anthropicLegacyBudget(model, effort string) (int, bool) {
	for _, entry := range anthropicLegacyThinkingBudgets {
		if !exactModelOrDatedSnapshot(entry.family, model) {
			continue
		}
		budget, ok := entry.budgets[effort]
		return budget, ok
	}
	return 0, false
}

func (c *anthropicClient) buildBody(req Request, stream bool) map[string]any {
	maxTokens := req.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 8192
	}
	body := map[string]any{
		"model":      req.Model,
		"max_tokens": maxTokens,
		"messages":   toAnthropic(req),
	}
	if s := strings.TrimSpace(req.System); s != "" {
		sys := []map[string]any{{"type": "text", "text": s}}
		if req.PromptCache {
			sys[0]["cache_control"] = map[string]any{"type": "ephemeral"}
		}
		body["system"] = sys
	}
	if stream {
		body["stream"] = true
	}
	if req.Temperature > 0 {
		body["temperature"] = req.Temperature
	}
	if req.TopP > 0 && req.TopP < 1 {
		body["top_p"] = req.TopP
	}
	if len(req.StopSequences) > 0 {
		body["stop_sequences"] = req.StopSequences
	}
	if len(req.Tools) > 0 {
		tools := make([]map[string]any, 0, len(req.Tools))
		for i, t := range req.Tools {
			schema := t.Parameters
			if schema == nil {
				schema = map[string]any{"type": "object", "properties": map[string]any{}}
			}
			entry := map[string]any{"name": t.Name, "description": t.Description, "input_schema": schema}
			// Cache the tool block so long tool lists are not re-billed each turn.
			if req.PromptCache && i == len(req.Tools)-1 {
				entry["cache_control"] = map[string]any{"type": "ephemeral"}
			}
			tools = append(tools, entry)
		}
		body["tools"] = tools
		switch req.ToolChoice {
		case "", "auto":
		case "none":
			body["tool_choice"] = map[string]any{"type": "none"}
		case "required":
			body["tool_choice"] = map[string]any{"type": "any"}
		default:
			body["tool_choice"] = map[string]any{"type": "tool", "name": req.ToolChoice}
		}
		if !req.ParallelToolCalls {
			if tc, ok := body["tool_choice"].(map[string]any); ok {
				tc["disable_parallel_tool_use"] = true
			} else {
				body["tool_choice"] = map[string]any{"type": "auto", "disable_parallel_tool_use": true}
			}
		}
	}
	if value, err := reasoningValue(req, "anthropic", c.opts.ProviderID, c.opts.BaseURL); err == nil && value != "" {
		capability := resolvedReasoningCapability(req, "anthropic", c.opts.ProviderID, c.opts.BaseURL)
		switch {
		case anthropicSupportsAdaptiveThinking(req.Model):
			if disable := reasoningDisableValue(capability); disable != "" && value == disable {
				body["thinking"] = map[string]any{"type": "disabled"}
			} else {
				body["thinking"] = map[string]any{"type": "adaptive"}
				body["output_config"] = map[string]any{"effort": value}
			}
		default:
			// Pre-adaptive models only understand fixed token budgets, and
			// only for the catalogued legacy families; an unrecognised model
			// gets no thinking override rather than a guessed budget.
			if budget, ok := anthropicLegacyBudget(req.Model, value); ok {
				body["thinking"] = map[string]any{"type": "enabled", "budget_tokens": budget}
			}
		}
	}
	if th, ok := body["thinking"].(map[string]any); ok {
		// Thinking (adaptive or fixed-budget) is incompatible with
		// temperature/top_p sampling controls.
		delete(body, "top_p")
		delete(body, "temperature")
		if _, hasBudget := th["budget_tokens"]; hasBudget && maxTokens < 16384 {
			// Fixed-budget thinking requires headroom beyond the budget.
			body["max_tokens"] = 16384
		}
	}
	for k, v := range req.Extra {
		body[k] = v
	}
	return body
}

type antResponse struct {
	ID      string       `json:"id"`
	Model   string       `json:"model"`
	Content []antContent `json:"content"`
	StopRsn string       `json:"stop_reason"`
	Usage   *struct {
		InputTokens              int `json:"input_tokens"`
		OutputTokens             int `json:"output_tokens"`
		CacheReadInputTokens     int `json:"cache_read_input_tokens"`
		CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
	} `json:"usage"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

func (c *anthropicClient) Chat(ctx context.Context, req Request) (*Response, error) {
	if _, err := reasoningValue(req, "anthropic", c.opts.ProviderID, c.opts.BaseURL); err != nil {
		return nil, err
	}
	var raw antResponse
	if err := c.opts.doJSON(ctx, "POST", c.opts.BaseURL+"/messages", c.buildBody(req, false), c.headers(), &raw); err != nil {
		return nil, err
	}
	return c.fromResponse(&raw)
}

// fromResponse converts a decoded Anthropic messages response into the neutral
// shape. It is shared with the Bedrock adapter, which returns the same body.
func (c *anthropicClient) fromResponse(raw *antResponse) (*Response, error) {
	if raw.Error != nil {
		return nil, fmt.Errorf("provider error: %s", raw.Error.Message)
	}
	resp := &Response{Model: raw.Model, FinishReason: raw.StopRsn}
	var text, thinking strings.Builder
	for _, blk := range raw.Content {
		switch blk.Type {
		case "text":
			text.WriteString(blk.Text)
		case "thinking":
			thinking.WriteString(blk.Thinking)
		case "tool_use":
			args := string(blk.Input)
			if strings.TrimSpace(args) == "" {
				args = "{}"
			}
			resp.ToolCalls = append(resp.ToolCalls, ToolCall{ID: blk.ID, Name: blk.Name, Arguments: args})
		}
	}
	resp.Content, resp.Reasoning = text.String(), thinking.String()
	if raw.Usage != nil {
		resp.Usage = Usage{
			InputTokens: raw.Usage.InputTokens, OutputTokens: raw.Usage.OutputTokens,
			CacheReadTokens: raw.Usage.CacheReadInputTokens, CacheWriteTokens: raw.Usage.CacheCreationInputTokens,
			// Anthropic reports cache reads/creation alongside uncached input.
			ContextTokens: raw.Usage.InputTokens + raw.Usage.CacheReadInputTokens + raw.Usage.CacheCreationInputTokens,
		}
	}
	return resp, nil
}

func (c *anthropicClient) Stream(ctx context.Context, req Request, emit func(Event) error) (*Response, error) {
	if _, err := reasoningValue(req, "anthropic", c.opts.ProviderID, c.opts.BaseURL); err != nil {
		return nil, err
	}
	httpResp, err := c.opts.doStream(ctx, "POST", c.opts.BaseURL+"/messages", c.buildBody(req, true), c.headers())
	if err != nil {
		return nil, err
	}
	defer httpResp.Body.Close()

	var (
		text      strings.Builder
		thinking  strings.Builder
		acc       = newToolCallAccumulator()
		usage     Usage
		finish    string
		model     = req.Model
		blockKind = map[int]string{}
	)

	err = sseLines(httpResp.Body, func(evName, data string) error {
		var ev struct {
			Type  string `json:"type"`
			Index int    `json:"index"`
			Delta struct {
				Type        string `json:"type"`
				Text        string `json:"text"`
				Thinking    string `json:"thinking"`
				PartialJSON string `json:"partial_json"`
				StopReason  string `json:"stop_reason"`
			} `json:"delta"`
			ContentBlock *antContent `json:"content_block"`
			Message      *struct {
				Model string `json:"model"`
				Usage *struct {
					InputTokens              int `json:"input_tokens"`
					OutputTokens             int `json:"output_tokens"`
					CacheReadInputTokens     int `json:"cache_read_input_tokens"`
					CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
				} `json:"usage"`
			} `json:"message"`
			Usage *struct {
				InputTokens          int `json:"input_tokens"`
				OutputTokens         int `json:"output_tokens"`
				CacheReadInputTokens int `json:"cache_read_input_tokens"`
			} `json:"usage"`
			Error *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal([]byte(data), &ev); err != nil {
			return nil
		}
		kind := ev.Type
		if kind == "" {
			kind = evName
		}
		switch kind {
		case "error":
			if ev.Error != nil {
				return fmt.Errorf("provider error: %s", ev.Error.Message)
			}
		case "message_start":
			if ev.Message != nil {
				if ev.Message.Model != "" {
					model = ev.Message.Model
				}
				if u := ev.Message.Usage; u != nil {
					usage.InputTokens = u.InputTokens
					usage.CacheReadTokens = u.CacheReadInputTokens
					usage.CacheWriteTokens = u.CacheCreationInputTokens
					usage.ContextTokens = usage.InputTokens + usage.CacheReadTokens + usage.CacheWriteTokens
				}
			}
		case "content_block_start":
			if ev.ContentBlock == nil {
				return nil
			}
			blockKind[ev.Index] = ev.ContentBlock.Type
			if ev.ContentBlock.Type == "tool_use" {
				call := acc.ensure(ev.Index)
				call.ID = ev.ContentBlock.ID
				call.Name = ev.ContentBlock.Name
				return emit(Event{Type: EventToolCallStart, Index: ev.Index, ToolCallID: call.ID, ToolName: call.Name})
			}
		case "content_block_delta":
			switch ev.Delta.Type {
			case "text_delta":
				text.WriteString(ev.Delta.Text)
				return emit(Event{Type: EventText, Delta: ev.Delta.Text})
			case "thinking_delta":
				thinking.WriteString(ev.Delta.Thinking)
				return emit(Event{Type: EventReasoning, Delta: ev.Delta.Thinking})
			case "input_json_delta":
				call := acc.ensure(ev.Index)
				call.Arguments += ev.Delta.PartialJSON
				return emit(Event{Type: EventToolCallDelta, Index: ev.Index, ToolCallID: call.ID, ToolName: call.Name, Delta: ev.Delta.PartialJSON})
			}
		case "message_delta":
			if ev.Delta.StopReason != "" {
				finish = ev.Delta.StopReason
			}
			if ev.Usage != nil {
				usage.OutputTokens = ev.Usage.OutputTokens
				// Real Anthropic puts input_tokens in message_start; some
				// Anthropic-compatible providers (z.ai/GLM) only send it here.
				// Take it when message_start didn't, so input is never lost.
				if ev.Usage.InputTokens > 0 {
					usage.InputTokens = ev.Usage.InputTokens
				}
				if ev.Usage.CacheReadInputTokens > 0 {
					usage.CacheReadTokens = ev.Usage.CacheReadInputTokens
				}
				usage.ContextTokens = usage.InputTokens + usage.CacheReadTokens + usage.CacheWriteTokens
				u := usage
				return emit(Event{Type: EventUsage, Usage: &u})
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	calls := acc.result()
	for i, call := range calls {
		if err := emit(Event{Type: EventToolCallEnd, Index: i, ToolCallID: call.ID, ToolName: call.Name, Delta: call.Arguments}); err != nil {
			return nil, err
		}
	}
	return &Response{
		Content: text.String(), Reasoning: thinking.String(), ToolCalls: calls,
		FinishReason: finish, Model: model, Usage: usage,
	}, nil
}

func (c *anthropicClient) Models(ctx context.Context) ([]ModelInfo, error) {
	var raw struct {
		Data []struct {
			ID          string `json:"id"`
			DisplayName string `json:"display_name"`
		} `json:"data"`
	}
	if err := c.opts.doJSON(ctx, "GET", c.opts.BaseURL+"/models?limit=200", nil, c.headers(), &raw); err != nil {
		return nil, err
	}
	out := make([]ModelInfo, 0, len(raw.Data))
	for _, m := range raw.Data {
		out = append(out, ModelInfo{
			ID: m.ID, Name: firstNonEmpty(m.DisplayName, m.ID), Provider: c.opts.ProviderID,
			ContextWindow: 200000, MaxOutput: 64000, Vision: true, Tools: true, Reasoning: true,
		})
	}
	return out, nil
}

func (c *anthropicClient) Embed(context.Context, string, []string) ([][]float32, error) {
	return nil, ErrUnsupported
}
