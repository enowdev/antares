package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// codexClient speaks OpenAI's Responses API (/v1/responses), the dialect the
// Codex CLI and the reasoning models use. It is a different shape from
// chat/completions — messages become an input array, the system prompt is
// "instructions", and outputs are typed items — so it is its own adapter rather
// than a vendor flag. Streaming is emit-once (the SSE event grammar differs);
// the answer is identical.
type codexClient struct{ opts Options }

func (c *codexClient) Kind() string { return "openai" }

func (c *codexClient) headers() map[string]string {
	h := map[string]string{}
	if c.opts.APIKey != "" {
		h["Authorization"] = "Bearer " + c.opts.APIKey
	}
	return h
}

func (c *codexClient) buildBody(req Request, stream bool) map[string]any {
	body := map[string]any{
		"model": req.Model,
		"input": toResponsesInput(req),
	}
	if s := strings.TrimSpace(req.System); s != "" {
		body["instructions"] = s
	}
	if stream {
		body["stream"] = true
	}
	if req.MaxTokens > 0 {
		body["max_output_tokens"] = req.MaxTokens
	}
	if req.Temperature > 0 {
		body["temperature"] = req.Temperature
	}
	if value, err := reasoningValue(req, "codex", c.opts.ProviderID, c.opts.BaseURL); err == nil && value != "" {
		body["reasoning"] = map[string]any{"effort": value}
	}
	if len(req.Tools) > 0 {
		tools := make([]map[string]any, 0, len(req.Tools))
		for _, t := range req.Tools {
			params := t.Parameters
			if params == nil {
				params = map[string]any{"type": "object", "properties": map[string]any{}}
			}
			tools = append(tools, map[string]any{
				"type": "function", "name": t.Name, "description": t.Description, "parameters": params,
			})
		}
		body["tools"] = tools
	}
	for k, v := range req.Extra {
		body[k] = v
	}
	return body
}

// toResponsesInput maps the neutral messages onto the Responses input array.
func toResponsesInput(req Request) []map[string]any {
	var input []map[string]any
	for _, m := range req.Messages {
		switch m.Role {
		case RoleTool:
			input = append(input, map[string]any{
				"type": "function_call_output", "call_id": m.ToolCallID, "output": m.Content,
			})
		case RoleAssistant:
			if m.Content != "" {
				input = append(input, map[string]any{
					"role":    "assistant",
					"content": []map[string]any{{"type": "output_text", "text": m.Content}},
				})
			}
			for _, tc := range m.ToolCalls {
				args := tc.Arguments
				if strings.TrimSpace(args) == "" {
					args = "{}"
				}
				input = append(input, map[string]any{
					"type": "function_call", "call_id": tc.ID, "name": tc.Name, "arguments": args,
				})
			}
		default: // user / system-as-user
			parts := []map[string]any{}
			if m.Content != "" {
				parts = append(parts, map[string]any{"type": "input_text", "text": m.Content})
			}
			for _, p := range m.Parts {
				if p.Type == "image" {
					u := p.URL
					if u == "" && p.Data != "" {
						mime := p.MimeType
						if mime == "" {
							mime = "image/png"
						}
						u = "data:" + mime + ";base64," + p.Data
					}
					if u != "" {
						parts = append(parts, map[string]any{"type": "input_image", "image_url": u})
					}
				}
			}
			if len(parts) == 0 {
				parts = append(parts, map[string]any{"type": "input_text", "text": m.Content})
			}
			input = append(input, map[string]any{"role": "user", "content": parts})
		}
	}
	return input
}

type responsesReply struct {
	Output []struct {
		Type    string `json:"type"`
		Role    string `json:"role"`
		Name    string `json:"name"`
		CallID  string `json:"call_id"`
		Args    string `json:"arguments"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		Summary []struct {
			Text string `json:"text"`
		} `json:"summary"`
	} `json:"output"`
	Usage *struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

func (c *codexClient) Chat(ctx context.Context, req Request) (*Response, error) {
	if _, err := reasoningValue(req, "codex", c.opts.ProviderID, c.opts.BaseURL); err != nil {
		return nil, err
	}
	var raw responsesReply
	if err := c.opts.doJSON(ctx, "POST", c.opts.BaseURL+"/responses", c.buildBody(req, false), c.headers(), &raw); err != nil {
		return nil, err
	}
	return raw.toResponse()
}

func (r *responsesReply) toResponse() (*Response, error) {
	if r.Error != nil {
		return nil, fmt.Errorf("provider error: %s", r.Error.Message)
	}
	resp := &Response{}
	var text, reasoning strings.Builder
	for _, item := range r.Output {
		switch item.Type {
		case "message":
			for _, part := range item.Content {
				if part.Type == "output_text" {
					text.WriteString(part.Text)
				}
			}
		case "reasoning":
			for _, s := range item.Summary {
				reasoning.WriteString(s.Text)
			}
		case "function_call":
			args := item.Args
			if strings.TrimSpace(args) == "" {
				args = "{}"
			}
			resp.ToolCalls = append(resp.ToolCalls, ToolCall{ID: item.CallID, Name: item.Name, Arguments: args})
		}
	}
	resp.Content, resp.Reasoning = text.String(), reasoning.String()
	if r.Usage != nil {
		resp.Usage = Usage{
			InputTokens: r.Usage.InputTokens, OutputTokens: r.Usage.OutputTokens,
			ContextTokens: r.Usage.InputTokens,
		}
	}
	return resp, nil
}

// Stream emits the whole answer once (the Responses SSE grammar is not decoded).
func (c *codexClient) Stream(ctx context.Context, req Request, emit func(Event) error) (*Response, error) {
	resp, err := c.Chat(ctx, req)
	if err != nil {
		return nil, err
	}
	if resp.Reasoning != "" {
		_ = emit(Event{Type: EventReasoning, Delta: resp.Reasoning})
	}
	if resp.Content != "" {
		_ = emit(Event{Type: EventText, Delta: resp.Content})
	}
	return resp, nil
}

func (c *codexClient) Models(ctx context.Context) ([]ModelInfo, error) {
	// The Responses backend shares the models list with the OpenAI adapter.
	oc := &openAIClient{opts: c.opts, vendor: "openai"}
	return oc.Models(ctx)
}

func (c *codexClient) Embed(ctx context.Context, model string, inputs []string) ([][]float32, error) {
	oc := &openAIClient{opts: c.opts, vendor: "openai"}
	return oc.Embed(ctx, model, inputs)
}

// codexJSON is a tiny helper used by tests to inspect the built body.
func (c *codexClient) bodyJSON(req Request) string {
	b, _ := json.Marshal(c.buildBody(req, false))
	return string(b)
}
