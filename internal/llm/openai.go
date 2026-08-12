package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"strings"
)

// openAIClient speaks the /chat/completions dialect used by OpenAI and the
// large family of compatible servers (OpenRouter, LM Studio, vLLM, Ollama…).
type openAIClient struct {
	opts    Options
	vendor  string // openai|compat|azure|copilot
	copilot *copilotTokenSource
}

func (c *openAIClient) Kind() string { return "openai" }

// reasoningKind reports the static-catalogue key for this vendor. Only the
// direct OpenAI API has a documented per-model effort ladder; every other
// vendor (compat, Azure, Copilot, OpenRouter) is Auto-only unless the caller
// attaches a resolved capability explicitly.
func (c *openAIClient) reasoningKind() string {
	if c.vendor == "openai" {
		return "openai"
	}
	return "openai-compatible"
}

func (c *openAIClient) headers() map[string]string {
	if c.vendor == "copilot" {
		// Copilot needs a freshly-exchanged token plus editor headers.
		tok, err := c.copilot.token()
		if err != nil {
			return map[string]string{}
		}
		return copilotHeaders(tok)
	}
	h := map[string]string{}
	if c.opts.APIKey != "" {
		if c.vendor == "azure" {
			// Azure authenticates with an api-key header, not a bearer token.
			h["api-key"] = c.opts.APIKey
		} else {
			h["Authorization"] = "Bearer " + c.opts.APIKey
		}
	}
	if strings.Contains(c.opts.BaseURL, "openrouter.ai") {
		h["HTTP-Referer"] = "https://github.com/enowdev/antares"
		h["X-Title"] = "Antares"
	}
	return h
}

// endpoint builds a request URL. Azure routes by deployment name (the model) in
// the path and carries the api-version as a query parameter; everyone else
// appends the path to the base URL.
func (c *openAIClient) endpoint(path, model string) string {
	if c.vendor == "azure" {
		return fmt.Sprintf("%s/openai/deployments/%s%s?api-version=%s",
			c.opts.BaseURL, url.PathEscape(model), path, url.QueryEscape(c.opts.APIVersion))
	}
	return c.opts.BaseURL + path
}

type oaMessage struct {
	Role       string       `json:"role"`
	Content    any          `json:"content,omitempty"`
	Name       string       `json:"name,omitempty"`
	ToolCalls  []oaToolCall `json:"tool_calls,omitempty"`
	ToolCallID string       `json:"tool_call_id,omitempty"`
	Reasoning  string       `json:"reasoning,omitempty"`
}

type oaToolCall struct {
	Index    int    `json:"index,omitempty"`
	ID       string `json:"id,omitempty"`
	Type     string `json:"type,omitempty"`
	Function struct {
		Name      string `json:"name,omitempty"`
		Arguments string `json:"arguments,omitempty"`
	} `json:"function"`
}

type oaContentPart struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	ImageURL *struct {
		URL string `json:"url"`
	} `json:"image_url,omitempty"`
}

func toOAMessages(req Request) []oaMessage {
	out := make([]oaMessage, 0, len(req.Messages)+1)
	if strings.TrimSpace(req.System) != "" {
		out = append(out, oaMessage{Role: RoleSystem, Content: req.System})
	}
	for _, m := range req.Messages {
		om := oaMessage{Role: m.Role, Name: m.Name, ToolCallID: m.ToolCallID}
		if len(m.Parts) > 0 {
			parts := make([]oaContentPart, 0, len(m.Parts))
			if m.Content != "" {
				parts = append(parts, oaContentPart{Type: "text", Text: m.Content})
			}
			for _, p := range m.Parts {
				switch p.Type {
				case "text":
					parts = append(parts, oaContentPart{Type: "text", Text: p.Text})
				case "image":
					url := p.URL
					if url == "" && p.Data != "" {
						mime := p.MimeType
						if mime == "" {
							mime = "image/png"
						}
						url = "data:" + mime + ";base64," + p.Data
					}
					if url != "" {
						cp := oaContentPart{Type: "image_url"}
						cp.ImageURL = &struct {
							URL string `json:"url"`
						}{URL: url}
						parts = append(parts, cp)
					}
				}
			}
			om.Content = parts
		} else {
			om.Content = m.Content
		}
		for i, tc := range m.ToolCalls {
			var call oaToolCall
			call.Index = i
			call.ID = tc.ID
			call.Type = "function"
			call.Function.Name = tc.Name
			call.Function.Arguments = tc.Arguments
			om.ToolCalls = append(om.ToolCalls, call)
		}
		// Assistant turns that only carry tool calls need a non-nil content field
		// for strict servers.
		if om.Role == RoleAssistant && om.Content == "" && len(om.ToolCalls) > 0 {
			om.Content = nil
		}
		out = append(out, om)
	}
	return out
}

func (c *openAIClient) buildBody(req Request, stream bool) map[string]any {
	body := map[string]any{
		"model":    req.Model,
		"messages": toOAMessages(req),
	}
	if stream {
		body["stream"] = true
		body["stream_options"] = map[string]any{"include_usage": true}
	}
	if req.Temperature > 0 {
		body["temperature"] = req.Temperature
	}
	if req.TopP > 0 && req.TopP < 1 {
		body["top_p"] = req.TopP
	}
	if req.MaxTokens > 0 {
		body["max_tokens"] = req.MaxTokens
	}
	if len(req.StopSequences) > 0 {
		body["stop"] = req.StopSequences
	}
	if len(req.Tools) > 0 {
		tools := make([]map[string]any, 0, len(req.Tools))
		for _, t := range req.Tools {
			params := t.Parameters
			if params == nil {
				params = map[string]any{"type": "object", "properties": map[string]any{}}
			}
			tools = append(tools, map[string]any{
				"type": "function",
				"function": map[string]any{
					"name":        t.Name,
					"description": t.Description,
					"parameters":  params,
				},
			})
		}
		body["tools"] = tools
		if req.ToolChoice != "" && req.ToolChoice != "auto" {
			switch req.ToolChoice {
			case "none", "required":
				body["tool_choice"] = req.ToolChoice
			default:
				body["tool_choice"] = map[string]any{"type": "function", "function": map[string]any{"name": req.ToolChoice}}
			}
		}
		if !req.ParallelToolCalls {
			body["parallel_tool_calls"] = false
		}
	}
	if value, err := reasoningValue(req, c.reasoningKind(), c.opts.ProviderID, c.opts.BaseURL); err == nil && value != "" {
		// OpenAI uses reasoning_effort; OpenRouter accepts a reasoning object.
		// Every validated value is sent as-is, including a marked disable
		// value: omitting it would leave reasoning enabled at the model's
		// default instead of honoring the user's explicit Off choice.
		if strings.Contains(c.opts.BaseURL, "openrouter.ai") {
			body["reasoning"] = map[string]any{"effort": value}
		} else {
			body["reasoning_effort"] = value
		}
	}
	for k, v := range req.Extra {
		body[k] = v
	}
	return body
}

type oaResponse struct {
	ID      string `json:"id"`
	Model   string `json:"model"`
	Choices []struct {
		FinishReason string `json:"finish_reason"`
		Message      struct {
			Content          string       `json:"content"`
			Reasoning        string       `json:"reasoning"`
			ReasoningContent string       `json:"reasoning_content"`
			ToolCalls        []oaToolCall `json:"tool_calls"`
		} `json:"message"`
	} `json:"choices"`
	Usage *oaUsage `json:"usage"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error"`
}

type oaUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
	PromptDetails    *struct {
		CachedTokens int `json:"cached_tokens"`
	} `json:"prompt_tokens_details"`
	CompletionDetails *struct {
		ReasoningTokens int `json:"reasoning_tokens"`
	} `json:"completion_tokens_details"`
}

func (u *oaUsage) normalise() Usage {
	if u == nil {
		return Usage{}
	}
	out := Usage{
		InputTokens: u.PromptTokens, OutputTokens: u.CompletionTokens,
		// OpenAI prompt_tokens already includes cached tokens; the details field
		// is a subset used for billing, not additional context.
		ContextTokens: u.PromptTokens,
	}
	if u.PromptDetails != nil {
		out.CacheReadTokens = u.PromptDetails.CachedTokens
	}
	if u.CompletionDetails != nil {
		out.ReasoningTokens = u.CompletionDetails.ReasoningTokens
	}
	return out
}

func (c *openAIClient) Chat(ctx context.Context, req Request) (*Response, error) {
	if _, err := reasoningValue(req, c.reasoningKind(), c.opts.ProviderID, c.opts.BaseURL); err != nil {
		return nil, err
	}
	var raw oaResponse
	if err := c.opts.doJSON(ctx, "POST", c.endpoint("/chat/completions", req.Model), c.buildBody(req, false), c.headers(), &raw); err != nil {
		return nil, err
	}
	if raw.Error != nil {
		return nil, fmt.Errorf("provider error: %s", raw.Error.Message)
	}
	if len(raw.Choices) == 0 {
		return nil, fmt.Errorf("provider returned no choices")
	}
	ch := raw.Choices[0]
	resp := &Response{
		Content:      ch.Message.Content,
		Reasoning:    firstNonEmpty(ch.Message.Reasoning, ch.Message.ReasoningContent),
		FinishReason: ch.FinishReason,
		Model:        raw.Model,
		Usage:        raw.Usage.normalise(),
	}
	for _, tc := range ch.Message.ToolCalls {
		args := tc.Function.Arguments
		if strings.TrimSpace(args) == "" {
			args = "{}"
		}
		resp.ToolCalls = append(resp.ToolCalls, ToolCall{ID: tc.ID, Name: tc.Function.Name, Arguments: args})
	}
	return resp, nil
}

func (c *openAIClient) Stream(ctx context.Context, req Request, emit func(Event) error) (*Response, error) {
	if _, err := reasoningValue(req, c.reasoningKind(), c.opts.ProviderID, c.opts.BaseURL); err != nil {
		return nil, err
	}
	httpResp, err := c.opts.doStream(ctx, "POST", c.endpoint("/chat/completions", req.Model), c.buildBody(req, true), c.headers())
	if err != nil {
		return nil, err
	}
	defer httpResp.Body.Close()

	var (
		text      strings.Builder
		reasoning strings.Builder
		acc       = newToolCallAccumulator()
		usage     Usage
		finish    string
		model     = req.Model
		started   = map[int]bool{}
	)

	err = sseLines(httpResp.Body, func(_, data string) error {
		if data == "[DONE]" {
			return io.EOF
		}
		var chunk struct {
			Model   string `json:"model"`
			Choices []struct {
				Delta struct {
					Content          string       `json:"content"`
					Reasoning        string       `json:"reasoning"`
					ReasoningContent string       `json:"reasoning_content"`
					ToolCalls        []oaToolCall `json:"tool_calls"`
				} `json:"delta"`
				FinishReason string `json:"finish_reason"`
			} `json:"choices"`
			Usage *oaUsage `json:"usage"`
			Error *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			return nil // tolerate keepalives and non-JSON noise
		}
		if chunk.Error != nil {
			return fmt.Errorf("provider error: %s", chunk.Error.Message)
		}
		if chunk.Model != "" {
			model = chunk.Model
		}
		if chunk.Usage != nil {
			usage = chunk.Usage.normalise()
			if err := emit(Event{Type: EventUsage, Usage: &usage}); err != nil {
				return err
			}
		}
		for _, ch := range chunk.Choices {
			if ch.FinishReason != "" {
				finish = ch.FinishReason
			}
			if r := firstNonEmpty(ch.Delta.Reasoning, ch.Delta.ReasoningContent); r != "" {
				reasoning.WriteString(r)
				if err := emit(Event{Type: EventReasoning, Delta: r}); err != nil {
					return err
				}
			}
			if ch.Delta.Content != "" {
				text.WriteString(ch.Delta.Content)
				if err := emit(Event{Type: EventText, Delta: ch.Delta.Content}); err != nil {
					return err
				}
			}
			for _, tc := range ch.Delta.ToolCalls {
				call := acc.ensure(tc.Index)
				if tc.ID != "" {
					call.ID = tc.ID
				}
				if tc.Function.Name != "" {
					call.Name += tc.Function.Name
				}
				if !started[tc.Index] && call.Name != "" {
					started[tc.Index] = true
					if err := emit(Event{Type: EventToolCallStart, Index: tc.Index, ToolCallID: call.ID, ToolName: call.Name}); err != nil {
						return err
					}
				}
				if tc.Function.Arguments != "" {
					call.Arguments += tc.Function.Arguments
					if err := emit(Event{Type: EventToolCallDelta, Index: tc.Index, ToolCallID: call.ID, ToolName: call.Name, Delta: tc.Function.Arguments}); err != nil {
						return err
					}
				}
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
	if finish == "" && len(calls) > 0 {
		finish = "tool_calls"
	}
	return &Response{
		Content:      text.String(),
		Reasoning:    reasoning.String(),
		ToolCalls:    calls,
		FinishReason: finish,
		Model:        model,
		Usage:        usage,
	}, nil
}

func (c *openAIClient) Models(ctx context.Context) ([]ModelInfo, error) {
	if c.vendor == "azure" {
		// Azure lists deployments through a management API, not /models; the
		// user configures deployment names directly.
		return nil, nil
	}
	var raw struct {
		Data []struct {
			ID            string `json:"id"`
			Name          string `json:"name"`
			ContextLength int    `json:"context_length"`
			TopProvider   *struct {
				MaxCompletionTokens int `json:"max_completion_tokens"`
			} `json:"top_provider"`
			Pricing *struct {
				Prompt     string `json:"prompt"`
				Completion string `json:"completion"`
			} `json:"pricing"`
			Architecture *struct {
				InputModalities []string `json:"input_modalities"`
			} `json:"architecture"`
			Reasoning *openRouterReasoningMetadata `json:"reasoning"`
		} `json:"data"`
	}
	if err := c.opts.doJSON(ctx, "GET", c.opts.BaseURL+"/models", nil, c.headers(), &raw); err != nil {
		return nil, err
	}
	out := make([]ModelInfo, 0, len(raw.Data))
	for _, m := range raw.Data {
		info := ModelInfo{
			ID: m.ID, Name: firstNonEmpty(m.Name, m.ID),
			Provider: c.opts.ProviderID, ContextWindow: m.ContextLength, Tools: true,
		}
		if m.TopProvider != nil {
			info.MaxOutput = m.TopProvider.MaxCompletionTokens
		}
		if m.Pricing != nil {
			info.InputCost = parsePricePerMillion(m.Pricing.Prompt)
			info.OutputCost = parsePricePerMillion(m.Pricing.Completion)
		}
		if m.Architecture != nil {
			for _, mod := range m.Architecture.InputModalities {
				if mod == "image" {
					info.Vision = true
				}
			}
		}
		if capability := openRouterReasoningCapability(m.Reasoning); capability != nil {
			info = info.WithReasoningCapability(capability)
		}
		out = append(out, info)
	}
	return out, nil
}

func (c *openAIClient) Embed(ctx context.Context, model string, inputs []string) ([][]float32, error) {
	if model == "" {
		return nil, fmt.Errorf("embedding model is required")
	}
	var raw struct {
		Data []struct {
			Index     int       `json:"index"`
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
	}
	body := map[string]any{"model": model, "input": inputs}
	if err := c.opts.doJSON(ctx, "POST", c.endpoint("/embeddings", model), body, c.headers(), &raw); err != nil {
		return nil, err
	}
	out := make([][]float32, len(inputs))
	for _, d := range raw.Data {
		if d.Index >= 0 && d.Index < len(out) {
			out[d.Index] = d.Embedding
		}
	}
	return out, nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// parsePricePerMillion converts OpenRouter's per-token price strings to USD/1M.
func parsePricePerMillion(s string) float64 {
	if s == "" {
		return 0
	}
	var f float64
	if _, err := fmt.Sscanf(s, "%g", &f); err != nil {
		return 0
	}
	return f * 1_000_000
}
