package llm

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
)

func TestGeminiThinkingConfigUsesLevelForGemini3(t *testing.T) {
	tc, ok := geminiThinkingConfig("gemini-3.6-flash-high", "high")
	if !ok || tc == nil {
		t.Fatal("expected thinkingConfig")
	}
	if tc["thinkingLevel"] != "HIGH" {
		t.Fatalf("thinkingLevel = %v, want HIGH", tc["thinkingLevel"])
	}
	if tc["includeThoughts"] != true {
		t.Fatalf("includeThoughts = %v, want true", tc["includeThoughts"])
	}
	if _, hasBudget := tc["thinkingBudget"]; hasBudget {
		t.Fatal("gemini-3 should not use thinkingBudget")
	}
}

func TestGeminiThinkingConfigUsesBudgetFor25(t *testing.T) {
	tc, ok := geminiThinkingConfig("gemini-2.5-flash", "medium")
	if !ok || tc == nil {
		t.Fatal("expected thinkingConfig")
	}
	if tc["thinkingBudget"] != 8192 {
		t.Fatalf("thinkingBudget = %v, want 8192", tc["thinkingBudget"])
	}
	if _, hasLevel := tc["thinkingLevel"]; hasLevel {
		t.Fatal("gemini-2.5 should not use thinkingLevel")
	}
}

func TestGemini3MinimalIsNotDisable(t *testing.T) {
	got, ok := geminiThinkingConfig("gemini-3.6-flash", "minimal")
	want := map[string]any{"thinkingLevel": "MINIMAL", "includeThoughts": true}
	if !ok || !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, ok=%v, want %#v, ok=true", got, ok, want)
	}
}

func TestGeminiThinkingConfigGemini3HasNoTrueOff(t *testing.T) {
	got, ok := geminiThinkingConfig("gemini-3.6-flash", "none")
	if !ok || got != nil {
		t.Fatalf("got %#v, ok=%v, want nil, ok=true (Gemini 3 has no true Off, only Minimal — that's a valid mapping, not a failure)", got, ok)
	}
}

// TestGeminiThinkingConfigMinimalUnmappableForLegacyBudgetModel guards the
// second half of geminiThinkingConfig's ok contract: "minimal" has no
// documented meaning for a pre-Gemini-3 thinkingBudget model, so it must be
// reported as unmappable (ok == false) rather than silently treated as a
// no-op, matching how an entirely unrecognised keyword is handled.
func TestGeminiThinkingConfigMinimalUnmappableForLegacyBudgetModel(t *testing.T) {
	if _, ok := geminiThinkingConfig("gemini-2.5-flash", "minimal"); ok {
		t.Fatal("got ok=true, want ok=false: legacy budget models have no minimal thinking level")
	}
}

// TestGeminiThinkingConfigUnrecognisedEffortIsUnmappable guards the general
// case behind the review finding: an effort value this switch has never
// heard of (as could be attached via a live/static ReasoningCapability that
// advertises more values than this local mapping knows) must report
// ok == false rather than silently mapping to no override.
func TestGeminiThinkingConfigUnrecognisedEffortIsUnmappable(t *testing.T) {
	if _, ok := geminiThinkingConfig("gemini-2.5-flash", "xhigh"); ok {
		t.Fatal("got ok=true, want ok=false for an unrecognised effort keyword")
	}
	if _, ok := geminiThinkingConfig("gemini-3.6-flash", "xhigh"); ok {
		t.Fatal("got ok=true, want ok=false for an unrecognised effort keyword on a Gemini 3 model")
	}
}

func TestGeminiLegacyBudgetZeroWhenOffSupported(t *testing.T) {
	cap, err := NewReasoningCapability(
		[]ReasoningValue{
			{Value: "none", Label: "Off", Kind: ReasoningValueDisable},
			{Value: "low", Label: "Low"},
			{Value: "medium", Label: "Medium"},
			{Value: "high", Label: "High"},
		},
		"medium", false, ReasoningCapabilityStatic,
	)
	if err != nil {
		t.Fatal(err)
	}
	c := &geminiClient{}
	body := c.buildBody(Request{
		Model: "gemini-2.5-flash", ReasoningEffort: "none", ReasoningCapability: cap,
		Messages: []Message{{Role: RoleUser, Content: "hi"}},
	})
	gen, _ := body["generationConfig"].(map[string]any)
	tc, _ := gen["thinkingConfig"].(map[string]any)
	want := map[string]any{"thinkingBudget": 0}
	if !reflect.DeepEqual(tc, want) {
		t.Fatalf("thinkingConfig = %#v, want %#v", tc, want)
	}
}

func TestGeminiRejectsUnsupportedOffBeforeRequest(t *testing.T) {
	cap, err := NewReasoningCapability(
		[]ReasoningValue{{Value: "low", Label: "Low"}, {Value: "high", Label: "High"}},
		"low", false, ReasoningCapabilityStatic,
	)
	if err != nil {
		t.Fatal(err)
	}
	c := &geminiClient{}
	_, err = c.Chat(context.Background(), Request{
		Model: "gemini-2.5-flash", ReasoningEffort: "none", ReasoningCapability: cap,
	})
	var unsupported *UnsupportedReasoningEffortError
	if !errors.As(err, &unsupported) {
		t.Fatalf("err = %v, want UnsupportedReasoningEffortError", err)
	}
}

// TestGeminiUnmappableLegacyEffortFailsBeforeRequest guards a silent-drop
// bug: an effort can pass validation against an *attached* capability (e.g. a
// live capability advertising a value this local geminiThinkingConfig switch
// does not recognise for the model) yet have no mapping at all.
// geminiThinkingConfig previously returned nil in that case and buildBody
// just omitted thinkingConfig, silently downgrading the turn to Auto. Chat
// must now fail before any request is sent.
func TestGeminiUnmappableLegacyEffortFailsBeforeRequest(t *testing.T) {
	cap, err := NewReasoningCapability(
		[]ReasoningValue{{Value: "low", Label: "Low"}, {Value: "xhigh", Label: "Extra High"}},
		"low", false, ReasoningCapabilityStatic,
	)
	if err != nil {
		t.Fatal(err)
	}

	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	c := &geminiClient{opts: Options{BaseURL: srv.URL, HTTPClient: srv.Client()}}
	_, err = c.Chat(context.Background(), Request{
		Model: "gemini-2.5-flash", ReasoningEffort: "xhigh", ReasoningCapability: cap,
	})
	var unsupported *UnsupportedReasoningEffortError
	if !errors.As(err, &unsupported) {
		t.Fatalf("err = %v, want UnsupportedReasoningEffortError", err)
	}
	if got := atomic.LoadInt32(&hits); got != 0 {
		t.Fatalf("requests sent = %d, want 0", got)
	}

	// buildBody alone (bypassing Chat) must also stay silent-safe: it must
	// not fabricate a thinkingConfig it cannot actually honor.
	body := (&geminiClient{}).buildBody(Request{
		Model: "gemini-2.5-flash", ReasoningEffort: "xhigh", ReasoningCapability: cap,
		Messages: []Message{{Role: RoleUser, Content: "hi"}},
	})
	gen, _ := body["generationConfig"].(map[string]any)
	if gen != nil {
		if _, ok := gen["thinkingConfig"]; ok {
			t.Fatalf("buildBody emitted a thinkingConfig for an unmappable effort: %#v", gen["thinkingConfig"])
		}
	}
}

func TestToGeminiPreservesThoughtSignatureOnFunctionCall(t *testing.T) {
	req := Request{
		Messages: []Message{{
			Role: RoleAssistant,
			ToolCalls: []ToolCall{{
				ID:               "abc123",
				Name:             "get_weather",
				Arguments:        `{"city":"Jakarta"}`,
				ThoughtSignature: "sig-from-model",
			}},
		}},
	}
	contents := toGemini(req)
	if len(contents) != 1 || contents[0].Role != "model" {
		t.Fatalf("contents = %+v", contents)
	}
	if len(contents[0].Parts) != 1 {
		t.Fatalf("parts = %+v", contents[0].Parts)
	}
	p := contents[0].Parts[0]
	if p.FunctionCall == nil || p.FunctionCall.Name != "get_weather" {
		t.Fatalf("functionCall = %+v", p.FunctionCall)
	}
	if p.FunctionCall.ID != "abc123" {
		t.Fatalf("functionCall.id = %q", p.FunctionCall.ID)
	}
	if p.ThoughtSignature != "sig-from-model" {
		t.Fatalf("thoughtSignature = %q, want sig-from-model", p.ThoughtSignature)
	}
}

func TestToGeminiInjectsDummySignatureWhenMissing(t *testing.T) {
	req := Request{
		Messages: []Message{{
			Role: RoleAssistant,
			ToolCalls: []ToolCall{{
				ID:        "x",
				Name:      "read_file",
				Arguments: `{"path":"a.go"}`,
			}},
		}},
	}
	contents := toGemini(req)
	sig := contents[0].Parts[0].ThoughtSignature
	if sig != geminiDummyThoughtSignature {
		t.Fatalf("thoughtSignature = %q, want dummy %q", sig, geminiDummyThoughtSignature)
	}
}

func TestParseGeminiPartsCapturesSignature(t *testing.T) {
	parts := []gemPart{
		{
			ThoughtSignature: "fc-sig",
			FunctionCall:     &gemFuncCall{Name: "search_web", Args: json.RawMessage(`{"q":"x"}`), ID: "call1"},
		},
		{Text: "done", ThoughtSignature: "text-sig"},
	}
	content, _, calls, textSig := parseGeminiParts(parts)
	if content != "done" {
		t.Fatalf("content = %q", content)
	}
	if len(calls) != 1 || calls[0].ThoughtSignature != "fc-sig" {
		t.Fatalf("calls = %+v", calls)
	}
	if calls[0].ID != "call1" {
		t.Fatalf("id = %q", calls[0].ID)
	}
	if textSig != "text-sig" {
		t.Fatalf("textSig = %q", textSig)
	}
}

func TestBuildBodyGemini3HighThinking(t *testing.T) {
	cap, err := NewReasoningCapability(
		[]ReasoningValue{{Value: "low", Label: "Low"}, {Value: "high", Label: "High"}},
		"low", false, ReasoningCapabilityStatic,
	)
	if err != nil {
		t.Fatal(err)
	}
	c := &geminiClient{}
	body := c.buildBody(Request{
		Model:               "gemini-3.6-flash-high",
		ReasoningEffort:     "high",
		ReasoningCapability: cap,
		Messages:            []Message{{Role: RoleUser, Content: "hi"}},
	})
	gen, _ := body["generationConfig"].(map[string]any)
	tc, _ := gen["thinkingConfig"].(map[string]any)
	raw, _ := json.Marshal(tc)
	if !strings.Contains(string(raw), `"thinkingLevel":"HIGH"`) {
		t.Fatalf("thinkingConfig = %s", raw)
	}
}

func TestNormalizeGeminiBaseURLMatchesCLI(t *testing.T) {
	cases := map[string]string{
		"http://127.0.0.1:8080/antigravity":                "http://127.0.0.1:8080/antigravity/v1beta",
		"http://127.0.0.1:8080/antigravity/":               "http://127.0.0.1:8080/antigravity/v1beta",
		"http://127.0.0.1:8080/antigravity/v1beta":         "http://127.0.0.1:8080/antigravity/v1beta",
		"https://generativelanguage.googleapis.com/v1beta": "https://generativelanguage.googleapis.com/v1beta",
		"http://localhost:8080/v1":                         "http://localhost:8080/v1",
	}
	for in, want := range cases {
		if got := normalizeGeminiBaseURL(in); got != want {
			t.Fatalf("normalizeGeminiBaseURL(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestGeminiStickySystemAnchorAndHeaders(t *testing.T) {
	sid := "sess-test-123"
	anchor := geminiStickySystemAnchor(sid)
	if !strings.Contains(anchor, "/.gemini/tmp/") {
		t.Fatalf("anchor missing tmp path: %q", anchor)
	}
	hash := geminiStickyTmpHash(sid)
	if len(hash) != 64 {
		t.Fatalf("tmp hash len = %d, want 64", len(hash))
	}
	if !strings.Contains(anchor, hash) {
		t.Fatalf("anchor missing hash: %q", anchor)
	}

	h := withGeminiGatewayStickyHeaders(nil, "http://127.0.0.1:8080/antigravity/v1beta", sid)
	if h["x-gemini-api-privileged-user-id"] == "" {
		t.Fatal("missing privileged-user-id header")
	}
	if h["session_id"] != sid {
		t.Fatalf("session_id = %q", h["session_id"])
	}
	// Official Google base should not get sticky headers.
	h2 := withGeminiGatewayStickyHeaders(nil, "https://generativelanguage.googleapis.com/v1beta", sid)
	if len(h2) != 0 {
		t.Fatalf("official API must not inject sticky headers: %v", h2)
	}

	c := &geminiClient{opts: Options{
		BaseURL:   "http://127.0.0.1:8080/antigravity/v1beta",
		SessionID: sid,
	}}
	body := c.buildBody(Request{
		System:   "You are helpful.",
		Messages: []Message{{Role: RoleUser, Content: "hi"}},
	})
	sys, _ := body["systemInstruction"].(gemContent)
	if len(sys.Parts) < 2 {
		t.Fatalf("system parts = %+v, want sticky + user system", sys.Parts)
	}
	if !strings.Contains(sys.Parts[0].Text, "/.gemini/tmp/"+hash) {
		t.Fatalf("first system part = %q", sys.Parts[0].Text)
	}
	if sys.Parts[1].Text != "You are helpful." {
		t.Fatalf("second system part = %q", sys.Parts[1].Text)
	}
}
