package llm

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
)

// geminiClient speaks Google's generateContent API, either the public
// Generative Language endpoint (api-key auth) or Vertex AI on GCP (OAuth
// service-account auth, project/location URLs).
type geminiClient struct {
	opts    Options
	vertex  bool
	project string
	region  string
	tokens  *gcpTokenSource
}

func (c *geminiClient) Kind() string {
	if c.vertex {
		return "vertex"
	}
	return "gemini"
}

func (c *geminiClient) headers() map[string]string {
	h := map[string]string{}
	if c.vertex {
		if tok, err := c.tokens.token(); err == nil && tok != "" {
			h["Authorization"] = "Bearer " + tok
		}
		return h
	}
	if c.opts.APIKey != "" {
		h["x-goog-api-key"] = c.opts.APIKey
	}
	return h
}

// normalizeGeminiBaseURL makes Antares accept the same base as Gemini CLI.
// CLI sets GOOGLE_GEMINI_BASE_URL to the gateway root (…/antigravity) and the
// SDK appends /v1beta. Antares previously required …/antigravity/v1beta in config.
// Without /v1beta, Sub2API returns 404 for /antigravity/models/….
func normalizeGeminiBaseURL(base string) string {
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	if base == "" {
		return base
	}
	lower := strings.ToLower(base)
	// Already a full v1beta endpoint (official or gateway).
	if strings.HasSuffix(lower, "/v1beta") || strings.Contains(lower, "/v1beta/") {
		return base
	}
	// Gemini CLI style: http://127.0.0.1:8080/antigravity
	if strings.HasSuffix(lower, "/antigravity") || strings.Contains(lower, "/antigravity/") {
		if !strings.Contains(lower, "/v1beta") {
			return base + "/v1beta"
		}
	}
	return base
}

// geminiGatewaySticky reports whether this base is a reverse-proxy Antigravity
// (or similar) Gemini route that benefits from CLI-compatible sticky signals.
func geminiGatewaySticky(base string) bool {
	return strings.Contains(strings.ToLower(base), "antigravity")
}

// withGeminiGatewayStickyHeaders adds Gemini-CLI-compatible sticky headers so
// multi-account gateways pin the same upstream account across agent turns
// (needed for implicit cache + thoughtSignature continuity).
func withGeminiGatewayStickyHeaders(in map[string]string, base, sessionID string) map[string]string {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" || !geminiGatewaySticky(base) {
		return in
	}
	out := make(map[string]string, len(in)+3)
	for k, v := range in {
		out[k] = v
	}
	// Sub2API extractGeminiCLISessionHash prefers this header + body tmp path.
	if _, ok := out["x-gemini-api-privileged-user-id"]; !ok {
		out["x-gemini-api-privileged-user-id"] = geminiStickyUserID(sessionID)
	}
	// Usage-log correlation (does not drive Gemini sticky alone, but harmless).
	if _, ok := out["session_id"]; !ok {
		out["session_id"] = sessionID
	}
	return out
}

// geminiStickyUserID is a stable UUID-shaped id derived from the Antares session.
func geminiStickyUserID(sessionID string) string {
	sum := sha256.Sum256([]byte("antares-gemini-sticky:" + sessionID))
	// Format first 16 bytes as UUID v4-ish (version/variant bits fixed).
	b := sum[:16]
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// geminiStickyTmpHash is a 64-hex digest matching Sub2API's
// /\.gemini\/tmp\/([A-Fa-f0-9]{64})/ sticky extractor.
func geminiStickyTmpHash(sessionID string) string {
	sum := sha256.Sum256([]byte("antares-gemini-tmp:" + sessionID))
	return hex.EncodeToString(sum[:])
}

// geminiStickySystemAnchor is injected into systemInstruction so the gateway
// can hash a stable sticky key even as conversation contents grow each turn.
func geminiStickySystemAnchor(sessionID string) string {
	if strings.TrimSpace(sessionID) == "" {
		return ""
	}
	// Shape mirrors Gemini CLI project temp path that Sub2API keys sticky on.
	return "The project's temporary directory is: /.gemini/tmp/" + geminiStickyTmpHash(sessionID)
}

// geminiDummyThoughtSignature is what the official Gemini CLI injects when a
// functionCall part is missing thoughtSignature. Required for multi-turn tool
// use on Gemini 3 / thinking models (HTTP 400 otherwise).
// See packages/core historyHardening.js SYNTHETIC_THOUGHT_SIGNATURE.
const geminiDummyThoughtSignature = "skip_thought_signature_validator"

type gemPart struct {
	Text             string         `json:"text,omitempty"`
	Thought          bool           `json:"thought,omitempty"`
	ThoughtSignature string         `json:"thoughtSignature,omitempty"`
	InlineData       *gemInlineData `json:"inlineData,omitempty"`
	FunctionCall     *gemFuncCall   `json:"functionCall,omitempty"`
	FunctionResponse *gemFuncResult `json:"functionResponse,omitempty"`
}

type gemInlineData struct {
	MimeType string `json:"mimeType"`
	Data     string `json:"data"`
}

type gemFuncCall struct {
	Name string          `json:"name"`
	Args json.RawMessage `json:"args,omitempty"`
	ID   string          `json:"id,omitempty"`
}

type gemFuncResult struct {
	Name     string `json:"name"`
	ID       string `json:"id,omitempty"`
	Response any    `json:"response"`
}

type gemContent struct {
	Role  string    `json:"role,omitempty"` // user|model
	Parts []gemPart `json:"parts"`
}

func toGemini(req Request) []gemContent {
	var out []gemContent
	// Tool calls are keyed by id upstream but by name in Gemini; keep a map so
	// tool results can be matched back to their call.
	callName := map[string]string{}

	appendMsg := func(role string, parts []gemPart) {
		if len(parts) == 0 {
			return
		}
		if n := len(out); n > 0 && out[n-1].Role == role {
			out[n-1].Parts = append(out[n-1].Parts, parts...)
			return
		}
		out = append(out, gemContent{Role: role, Parts: parts})
	}

	for _, m := range req.Messages {
		switch m.Role {
		case RoleSystem:
			continue
		case RoleAssistant:
			var parts []gemPart
			if strings.TrimSpace(m.Content) != "" {
				p := gemPart{Text: m.Content}
				// Text-only turns may carry a part-level thought signature.
				if m.ThoughtSignature != "" && len(m.ToolCalls) == 0 {
					p.ThoughtSignature = m.ThoughtSignature
				}
				parts = append(parts, p)
			}
			for i, tc := range m.ToolCalls {
				callName[tc.ID] = tc.Name
				args := json.RawMessage(tc.Arguments)
				if strings.TrimSpace(tc.Arguments) == "" {
					args = json.RawMessage("{}")
				}
				sig := strings.TrimSpace(tc.ThoughtSignature)
				if sig == "" && i == 0 {
					sig = strings.TrimSpace(m.ThoughtSignature)
				}
				if sig == "" {
					// Gemini 3 rejects functionCall parts without a signature.
					sig = geminiDummyThoughtSignature
				}
				parts = append(parts, gemPart{
					ThoughtSignature: sig,
					FunctionCall:     &gemFuncCall{Name: tc.Name, Args: args, ID: tc.ID},
				})
			}
			appendMsg("model", parts)
		case RoleTool:
			name := m.ToolName()
			if n, ok := callName[m.ToolCallID]; ok && n != "" {
				name = n
			}
			appendMsg("user", []gemPart{{FunctionResponse: &gemFuncResult{
				Name:     name,
				ID:       m.ToolCallID,
				Response: map[string]any{"result": m.Content},
			}}})
		default:
			var parts []gemPart
			if strings.TrimSpace(m.Content) != "" {
				parts = append(parts, gemPart{Text: m.Content})
			}
			for _, p := range m.Parts {
				switch p.Type {
				case "text":
					parts = append(parts, gemPart{Text: p.Text})
				case "image":
					if p.Data != "" {
						mime := p.MimeType
						if mime == "" {
							mime = "image/png"
						}
						parts = append(parts, gemPart{InlineData: &gemInlineData{MimeType: mime, Data: p.Data}})
					}
				}
			}
			appendMsg("user", parts)
		}
	}
	return out
}

// sanitizeSchema strips JSON Schema keywords Gemini rejects.
func sanitizeSchema(in map[string]any) map[string]any {
	if in == nil {
		return map[string]any{"type": "object", "properties": map[string]any{}}
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		switch k {
		case "additionalProperties", "$schema", "$id", "default", "examples", "title", "exclusiveMinimum", "exclusiveMaximum":
			continue
		}
		switch tv := v.(type) {
		case map[string]any:
			out[k] = sanitizeSchema(tv)
		case []any:
			arr := make([]any, 0, len(tv))
			for _, item := range tv {
				if m, ok := item.(map[string]any); ok {
					arr = append(arr, sanitizeSchema(m))
					continue
				}
				arr = append(arr, item)
			}
			out[k] = arr
		default:
			out[k] = v
		}
	}
	if _, ok := out["type"]; !ok {
		if _, hasProps := out["properties"]; hasProps {
			out["type"] = "object"
		}
	}
	return out
}

func (c *geminiClient) buildBody(req Request) map[string]any {
	body := map[string]any{"contents": toGemini(req)}
	sysParts := make([]gemPart, 0, 2)
	if anchor := geminiStickySystemAnchor(c.opts.SessionID); anchor != "" && geminiGatewaySticky(c.opts.BaseURL) {
		// Keep sticky fingerprint in systemInstruction (stable across agent turns).
		sysParts = append(sysParts, gemPart{Text: anchor})
	}
	if s := strings.TrimSpace(req.System); s != "" {
		sysParts = append(sysParts, gemPart{Text: s})
	}
	if len(sysParts) > 0 {
		body["systemInstruction"] = gemContent{Parts: sysParts}
	}
	gen := map[string]any{}
	if req.Temperature > 0 {
		gen["temperature"] = req.Temperature
	}
	if req.TopP > 0 && req.TopP < 1 {
		gen["topP"] = req.TopP
	}
	if req.MaxTokens > 0 {
		gen["maxOutputTokens"] = req.MaxTokens
	}
	if len(req.StopSequences) > 0 {
		gen["stopSequences"] = req.StopSequences
	}
	if value, err := reasoningValue(req, c.Kind(), c.opts.ProviderID, c.opts.BaseURL); err == nil {
		if tc := geminiThinkingConfig(req.Model, value); tc != nil {
			gen["thinkingConfig"] = tc
		}
	}
	if len(gen) > 0 {
		body["generationConfig"] = gen
	}
	if len(req.Tools) > 0 {
		decls := make([]map[string]any, 0, len(req.Tools))
		for _, t := range req.Tools {
			decls = append(decls, map[string]any{
				"name":        t.Name,
				"description": t.Description,
				"parameters":  sanitizeSchema(t.Parameters),
			})
		}
		body["tools"] = []map[string]any{{"functionDeclarations": decls}}
		mode := "AUTO"
		switch req.ToolChoice {
		case "none":
			mode = "NONE"
		case "required":
			mode = "ANY"
		}
		body["toolConfig"] = map[string]any{"functionCallingConfig": map[string]any{"mode": mode}}
	}
	for k, v := range req.Extra {
		body[k] = v
	}
	return body
}

type gemResponse struct {
	Candidates []struct {
		Content      gemContent `json:"content"`
		FinishReason string     `json:"finishReason"`
	} `json:"candidates"`
	UsageMetadata *struct {
		PromptTokenCount        int `json:"promptTokenCount"`
		CandidatesTokenCount    int `json:"candidatesTokenCount"`
		CachedContentTokenCount int `json:"cachedContentTokenCount"`
		ThoughtsTokenCount      int `json:"thoughtsTokenCount"`
	} `json:"usageMetadata"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

func (c *geminiClient) endpoint(model, method string, stream bool) string {
	model = strings.TrimPrefix(model, "models/")
	if c.vertex {
		u := fmt.Sprintf("%s/v1/projects/%s/locations/%s/publishers/google/models/%s:%s",
			c.opts.BaseURL, c.project, c.region, url.PathEscape(model), method)
		if stream {
			u += "?alt=sse"
		}
		return u
	}
	u := c.opts.BaseURL + "/models/" + url.PathEscape(model) + ":" + method
	if stream {
		u += "?alt=sse"
	}
	return u
}

// geminiThinkingConfig builds generationConfig.thinkingConfig for the model.
// Gemini 3 series prefer thinkingLevel (MINIMAL/LOW/MEDIUM/HIGH); 2.5 series
// use thinkingBudget token counts. includeThoughts requests thought summaries
// when the endpoint exposes them (not all reverse proxies return thought text).
//
// Minimal is a real, distinct thinking level for Gemini 3 — not an Off
// synonym. Gemini 3 has no true Off; its static capability never advertises
// "none", so the "none" case below only guards a stray legacy value.
func geminiThinkingConfig(model, effort string) map[string]any {
	e := strings.ToLower(strings.TrimSpace(effort))
	if e == "" {
		return nil
	}
	useLevel := geminiModelUsesThinkingLevel(model)
	switch e {
	case "none":
		if useLevel {
			return nil
		}
		return map[string]any{"thinkingBudget": 0}
	case "minimal":
		if !useLevel {
			return nil
		}
		return map[string]any{"thinkingLevel": "MINIMAL", "includeThoughts": true}
	case "low":
		if useLevel {
			return map[string]any{"thinkingLevel": "LOW", "includeThoughts": true}
		}
		return map[string]any{"thinkingBudget": 2048, "includeThoughts": true}
	case "medium":
		if useLevel {
			return map[string]any{"thinkingLevel": "MEDIUM", "includeThoughts": true}
		}
		return map[string]any{"thinkingBudget": 8192, "includeThoughts": true}
	case "high":
		if useLevel {
			return map[string]any{"thinkingLevel": "HIGH", "includeThoughts": true}
		}
		return map[string]any{"thinkingBudget": 24576, "includeThoughts": true}
	default:
		return nil
	}
}

func geminiModelUsesThinkingLevel(model string) bool {
	m := strings.ToLower(strings.TrimPrefix(model, "models/"))
	// Gemini 3.x model ids (and antigravity aliases like gemini-3.6-flash-high).
	return strings.HasPrefix(m, "gemini-3") || strings.Contains(m, "gemini-3.")
}

// parseGeminiParts folds candidate parts into content, reasoning, tool calls,
// and preserves thoughtSignature on tool calls / final text.
func parseGeminiParts(parts []gemPart) (content, reasoning string, calls []ToolCall, textSig string) {
	var text, thought strings.Builder
	for i, p := range parts {
		switch {
		case p.FunctionCall != nil:
			args := string(p.FunctionCall.Args)
			if strings.TrimSpace(args) == "" {
				args = "{}"
			}
			id := p.FunctionCall.ID
			if id == "" {
				id = fmt.Sprintf("call_%d_%s", i, p.FunctionCall.Name)
			}
			calls = append(calls, ToolCall{
				ID:               id,
				Name:             p.FunctionCall.Name,
				Arguments:        args,
				ThoughtSignature: p.ThoughtSignature,
			})
		case p.Thought:
			thought.WriteString(p.Text)
			if p.ThoughtSignature != "" && textSig == "" {
				textSig = p.ThoughtSignature
			}
		default:
			if p.Text != "" {
				text.WriteString(p.Text)
			}
			// Final answer parts often carry thoughtSignature without thought:true.
			if p.ThoughtSignature != "" {
				textSig = p.ThoughtSignature
			}
		}
	}
	return text.String(), thought.String(), calls, textSig
}

func (c *geminiClient) Chat(ctx context.Context, req Request) (*Response, error) {
	if _, err := reasoningValue(req, c.Kind(), c.opts.ProviderID, c.opts.BaseURL); err != nil {
		return nil, err
	}
	// Prefer stream collection: some Gemini-compatible reverse proxies aggregate
	// non-stream generateContent by keeping only the final STOP chunk, which is
	// often empty text after a functionCall chunk. streamGenerateContent preserves
	// functionCall + thoughtSignature parts (official multi-turn tool contract).
	if len(req.Tools) > 0 {
		return c.Stream(ctx, req, func(Event) error { return nil })
	}

	var raw gemResponse
	if err := c.opts.doJSON(ctx, "POST", c.endpoint(req.Model, "generateContent", false), c.buildBody(req), c.headers(), &raw); err != nil {
		return nil, err
	}
	if raw.Error != nil {
		return nil, fmt.Errorf("provider error: %s", raw.Error.Message)
	}
	resp := &Response{Model: req.Model}
	if len(raw.Candidates) > 0 {
		cand := raw.Candidates[0]
		resp.FinishReason = strings.ToLower(cand.FinishReason)
		resp.Content, resp.Reasoning, resp.ToolCalls, resp.ThoughtSignature = parseGeminiParts(cand.Content.Parts)
	}
	if u := raw.UsageMetadata; u != nil {
		resp.Usage = Usage{
			InputTokens: u.PromptTokenCount, OutputTokens: u.CandidatesTokenCount,
			CacheReadTokens: u.CachedContentTokenCount, ReasoningTokens: u.ThoughtsTokenCount,
			// Gemini promptTokenCount includes cachedContentTokenCount.
			ContextTokens: u.PromptTokenCount,
		}
	}
	return resp, nil
}

func (c *geminiClient) Stream(ctx context.Context, req Request, emit func(Event) error) (*Response, error) {
	if _, err := reasoningValue(req, c.Kind(), c.opts.ProviderID, c.opts.BaseURL); err != nil {
		return nil, err
	}
	httpResp, err := c.opts.doStream(ctx, "POST", c.endpoint(req.Model, "streamGenerateContent", true), c.buildBody(req), c.headers())
	if err != nil {
		return nil, err
	}
	defer httpResp.Body.Close()

	var (
		text    strings.Builder
		thought strings.Builder
		calls   []ToolCall
		usage   Usage
		finish  string
		textSig string
		callSeq int
		emitted = map[string]bool{}
	)

	err = sseLines(httpResp.Body, func(_, data string) error {
		var raw gemResponse
		if err := json.Unmarshal([]byte(data), &raw); err != nil {
			return nil
		}
		if raw.Error != nil {
			return fmt.Errorf("provider error: %s", raw.Error.Message)
		}
		if u := raw.UsageMetadata; u != nil {
			usage = Usage{
				InputTokens: u.PromptTokenCount, OutputTokens: u.CandidatesTokenCount,
				CacheReadTokens: u.CachedContentTokenCount, ReasoningTokens: u.ThoughtsTokenCount,
				// Gemini promptTokenCount includes cachedContentTokenCount.
				ContextTokens: u.PromptTokenCount,
			}
			cp := usage
			if err := emit(Event{Type: EventUsage, Usage: &cp}); err != nil {
				return err
			}
		}
		for _, cand := range raw.Candidates {
			if cand.FinishReason != "" {
				finish = strings.ToLower(cand.FinishReason)
			}
			for _, p := range cand.Content.Parts {
				switch {
				case p.FunctionCall != nil:
					args := string(p.FunctionCall.Args)
					if strings.TrimSpace(args) == "" {
						args = "{}"
					}
					id := p.FunctionCall.ID
					if id == "" {
						id = fmt.Sprintf("call_%d_%s", callSeq, p.FunctionCall.Name)
					}
					callSeq++
					// Dedupe by provider id or name+args when id missing.
					dedupeKey := id
					if emitted[dedupeKey] {
						continue
					}
					emitted[dedupeKey] = true
					idx := len(calls)
					calls = append(calls, ToolCall{
						ID: id, Name: p.FunctionCall.Name, Arguments: args,
						ThoughtSignature: p.ThoughtSignature,
					})
					if err := emit(Event{Type: EventToolCallStart, Index: idx, ToolCallID: id, ToolName: p.FunctionCall.Name}); err != nil {
						return err
					}
					if err := emit(Event{Type: EventToolCallDelta, Index: idx, ToolCallID: id, ToolName: p.FunctionCall.Name, Delta: args}); err != nil {
						return err
					}
				case p.Thought:
					if p.Text != "" {
						thought.WriteString(p.Text)
						if err := emit(Event{Type: EventReasoning, Delta: p.Text}); err != nil {
							return err
						}
					}
					if p.ThoughtSignature != "" {
						textSig = p.ThoughtSignature
					}
				case p.Text != "":
					text.WriteString(p.Text)
					if err := emit(Event{Type: EventText, Delta: p.Text}); err != nil {
						return err
					}
					if p.ThoughtSignature != "" {
						textSig = p.ThoughtSignature
					}
				default:
					// Signature-only or empty-text finish chunks still carry
					// thoughtSignature that must survive into history.
					if p.ThoughtSignature != "" {
						textSig = p.ThoughtSignature
					}
				}
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	// If a functionCall arrived without a signature, inject the CLI dummy so
	// the next turn does not 400. Prefer part-level sig already stored.
	for i := range calls {
		if strings.TrimSpace(calls[i].ThoughtSignature) == "" {
			if textSig != "" {
				calls[i].ThoughtSignature = textSig
			} else {
				calls[i].ThoughtSignature = geminiDummyThoughtSignature
			}
		}
	}
	for i, call := range calls {
		if err := emit(Event{Type: EventToolCallEnd, Index: i, ToolCallID: call.ID, ToolName: call.Name, Delta: call.Arguments}); err != nil {
			return nil, err
		}
	}
	return &Response{
		Content: text.String(), Reasoning: thought.String(), ToolCalls: calls,
		ThoughtSignature: textSig, FinishReason: finish, Model: req.Model, Usage: usage,
	}, nil
}

func (c *geminiClient) Models(ctx context.Context) ([]ModelInfo, error) {
	if c.vertex {
		// Vertex lists publisher models through a different API; users name the
		// model directly.
		return nil, nil
	}
	var raw struct {
		Models []struct {
			Name             string   `json:"name"`
			DisplayName      string   `json:"displayName"`
			InputTokenLimit  int      `json:"inputTokenLimit"`
			OutputTokenLimit int      `json:"outputTokenLimit"`
			Methods          []string `json:"supportedGenerationMethods"`
		} `json:"models"`
	}
	if err := c.opts.doJSON(ctx, "GET", c.opts.BaseURL+"/models?pageSize=200", nil, c.headers(), &raw); err != nil {
		return nil, err
	}
	out := make([]ModelInfo, 0, len(raw.Models))
	for _, m := range raw.Models {
		supported := false
		for _, meth := range m.Methods {
			if meth == "generateContent" {
				supported = true
			}
		}
		if !supported {
			continue
		}
		id := strings.TrimPrefix(m.Name, "models/")
		out = append(out, ModelInfo{
			ID: id, Name: firstNonEmpty(m.DisplayName, id), Provider: c.opts.ProviderID,
			ContextWindow: m.InputTokenLimit, MaxOutput: m.OutputTokenLimit,
			Vision: true, Tools: true, Reasoning: strings.Contains(id, "2.5") || strings.Contains(id, "3"),
		})
	}
	return out, nil
}

func (c *geminiClient) Embed(ctx context.Context, model string, inputs []string) ([][]float32, error) {
	if model == "" {
		model = "text-embedding-004"
	}
	reqs := make([]map[string]any, 0, len(inputs))
	for _, in := range inputs {
		reqs = append(reqs, map[string]any{
			"model":   "models/" + strings.TrimPrefix(model, "models/"),
			"content": map[string]any{"parts": []map[string]any{{"text": in}}},
		})
	}
	var raw struct {
		Embeddings []struct {
			Values []float32 `json:"values"`
		} `json:"embeddings"`
	}
	body := map[string]any{"requests": reqs}
	if err := c.opts.doJSON(ctx, "POST", c.endpoint(model, "batchEmbedContents", false), body, c.headers(), &raw); err != nil {
		return nil, err
	}
	out := make([][]float32, 0, len(raw.Embeddings))
	for _, e := range raw.Embeddings {
		out = append(out, e.Values)
	}
	return out, nil
}
