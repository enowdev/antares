# Adaptive Reasoning and Direct Cursor Mode Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make reasoning controls match each provider/model and let users select and run exact Cursor Cloud Agent model variants directly from the web composer.

**Architecture:** Chat models remain behind `llm.Client`, while Cursor remains an agent-capability provider with a dedicated SSE execution path. A shared reasoning-capability contract drives chat UI and adapter validation; a shared Cursor run service drives both tools and direct web runs. The frontend merges separate chat and Cursor catalogues only for search, never for active-provider mutation.

**Tech Stack:** Go 1.24, `net/http`, SQLite/Postgres store abstraction, React 19, TypeScript, Bun, SSE, Cursor Cloud Agent REST API.

## Global Constraints

- Cursor must remain rejected by `/api/model/set`, `llm.New`, CLI model selection, and TUI chat-provider selection.
- Cursor variants are authoritative. Send one exact upstream variant, including hidden params; never synthesize a Cartesian product.
- Cursor start, follow-up, and cancel require explicit human approval even when general tool approval mode is `auto`.
- Never retry Cursor create-agent, create-run, or cancel automatically.
- Never expose API keys in logs, errors, approval data, events, fixtures, tests, or persisted metadata.
- Auto reasoning sends no override. Provider values are opaque and case-sensitive.
- Off is displayed only for a capability value explicitly marked `kind:"disable"`.
- New explicit invalid reasoning values fail before an upstream request. Unsupported legacy stored values fall back to Auto with a one-time notice.
- Existing YAML stays compatible; `reasoning_effort` remains a string.
- Browser disconnect and local Stop do not cancel a remote Cursor run.
- All non-live tests are hermetic. The optional live test may read metadata only and must not create a paid run.
- User-facing copy must be added to all existing locale maps.

## File Structure

### New backend files

- `internal/llm/reasoning.go` — capability types, invariants, and value validation.
- `internal/llm/reasoning_catalog.go` — conservative provider/model capability catalogue.
- `internal/agent/reasoning.go` — effective-model capability resolution and legacy fallback.
- `internal/server/reasoning.go` — request/config/role validation boundary.
- `internal/approval/gate.go` — instance-owned generic operation approval gate.
- `internal/cursorrun/service.go` — shared Cursor lifecycle service and interface.
- `internal/cursorrun/catalog.go` — five-minute catalogue cache and exact variant validation.
- `internal/cursorrun/repository.go` — GitHub remote normalization and project inspection.
- `internal/store/cursor_sessions.go` — durable Cursor session/run state and compare-and-swap.
- `internal/server/handlers_cursor.go` — direct Cursor turn, cancel, and repository handlers.
- `internal/server/cursor_events.go` — Cursor-to-Antares event mapping and recovery.
- `internal/server/cursor_attachments.go` — strict Cursor image decoding and validation.

### New frontend files

- `web/src/lib/models.ts` — shared chat model and reasoning capability types.
- `web/src/lib/reasoning.ts` — adaptive options and per-model preference migration.
- `web/src/lib/cursorModels.ts` — Cursor catalogue types and exact-variant filtering.
- `web/src/lib/composerTargets.ts` — grouped chat/Cursor execution-target search.
- `web/src/lib/cursorAttachments.ts` — Cursor attachment preflight.
- `web/src/lib/chatEvents.ts` — approval-event parsing and deduplication.
- `web/src/components/chat/CursorOptions.tsx` — variant, mode, repo/ref, and PR controls.

### Main modified files

- Provider adapters under `internal/llm/`.
- `internal/agent/client.go`, `agent.go`, `harness.go`, and `approval.go`.
- Config schema/load and server config/model/role/chat handlers.
- Cursor types/client/stream and existing Cursor tools.
- Store types/migrations/sessions.
- Runtime construction under `cmd/antares/`.
- `ModelPicker`, `ReasoningPicker`, `ChatPage`, `ProvidersPage`, `RolesPage`, `ConfigPage`, and i18n.

---

### Task 1: Define the reasoning capability contract

**Files:**
- Create: `internal/llm/reasoning.go`
- Create: `internal/llm/reasoning_catalog.go`
- Create: `internal/llm/reasoning_test.go`
- Modify: `internal/llm/types.go`

**Interfaces:**
- Produces: `ReasoningCapability`, `ReasoningValue`, `ValidateReasoningEffort`, and `StaticReasoningCapability`.
- Consumes: provider kind, provider ID, base URL, and exact model ID.

- [ ] **Step 1: Write failing invariant and validation tests**

```go
func TestReasoningCapabilityRejectsInconsistentDisableMetadata(t *testing.T) {
	_, err := NewReasoningCapability(
		[]ReasoningValue{{Value: "none", Label: "Off", Kind: ReasoningValueDisable}},
		"", true, ReasoningCapabilityStatic,
	)
	if err == nil || !strings.Contains(err.Error(), "mandatory") {
		t.Fatalf("err = %v, want mandatory/disable conflict", err)
	}
}

func TestValidateReasoningEffortPreservesOpaqueValues(t *testing.T) {
	cap, err := NewReasoningCapability(
		[]ReasoningValue{
			{Value: "extra-high", Label: "Extra High"},
			{Value: "xhigh", Label: "Extra High (new)"},
		},
		"extra-high", false, ReasoningCapabilityLive,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateReasoningEffort("gpt-example", cap, "extra-high"); err != nil {
		t.Fatal(err)
	}
	if err := ValidateReasoningEffort("gpt-example", cap, "EXTRA-HIGH"); err == nil {
		t.Fatal("case-normalized value was accepted")
	}
}
```

Also add:

- `TestValidateReasoningEffortAcceptsAuto`
- `TestValidateReasoningEffortRejectsUnknownOverride`
- `TestReasoningCapabilityRequiresUniqueValues`
- `TestReasoningCapabilityDefaultMustBeAllowed`

- [ ] **Step 2: Run the focused test and confirm RED**

Run:

```bash
go test ./internal/llm -run 'Test(ReasoningCapability|ValidateReasoningEffort)' -count=1 -v
```

Expected: compile failure because the capability contract does not exist.

- [ ] **Step 3: Implement capability types and invariants**

```go
type ReasoningValueKind string

const ReasoningValueDisable ReasoningValueKind = "disable"

type ReasoningValue struct {
	Value string             `json:"value"`
	Label string             `json:"label"`
	Kind  ReasoningValueKind `json:"kind,omitempty"`
}

type ReasoningCapabilitySource string

const (
	ReasoningCapabilityLive   ReasoningCapabilitySource = "live"
	ReasoningCapabilityStatic ReasoningCapabilitySource = "static"
)

type ReasoningCapability struct {
	Values     []ReasoningValue          `json:"values"`
	Default    string                    `json:"default,omitempty"`
	Mandatory  bool                      `json:"mandatory"`
	CanDisable bool                      `json:"can_disable"`
	Source     ReasoningCapabilitySource `json:"source"`
}

type UnsupportedReasoningEffortError struct {
	Model   string
	Effort  string
	Allowed []string
}
```

`NewReasoningCapability` must trim neither values nor case, reject empty or
duplicate values, require the default to be present, derive `CanDisable` from
exactly one disable marker, and reject a disable marker on mandatory models.
`ValidateReasoningEffort` must always accept `""` as Auto.

- [ ] **Step 4: Add capability fields without removing the old boolean**

```go
type Request struct {
	// existing fields remain
	ReasoningEffort     string
	ReasoningCapability *ReasoningCapability
}

type ModelInfo struct {
	// existing fields remain
	Reasoning           bool                 `json:"reasoning"`
	ReasoningCapability *ReasoningCapability `json:"reasoning_capability,omitempty"`
}
```

Set `Reasoning` to true whenever a model has a non-nil capability, while
retaining existing JSON compatibility.

- [ ] **Step 5: Write failing static-catalogue table tests**

```go
func TestStaticReasoningCapabilityRepresentativeFamilies(t *testing.T) {
	tests := []struct {
		kind, provider, baseURL, model string
		want                          []string
		disable                       bool
	}{
		{"openai", "openai", "https://api.openai.com/v1", "gpt-5", []string{"minimal", "low", "medium", "high"}, false},
		{"codex", "openai", "https://api.openai.com/v1", "gpt-5.3-codex", []string{"low", "medium", "high", "xhigh"}, false},
		{"anthropic", "anthropic", "https://api.anthropic.com", "claude-sonnet-5", []string{"low", "medium", "high", "xhigh", "max"}, false},
		{"gemini", "google", "https://generativelanguage.googleapis.com/v1beta", "gemini-3.6-flash", []string{"minimal", "low", "medium", "high"}, false},
	}
	for _, tt := range tests {
		cap := StaticReasoningCapability(tt.kind, tt.provider, tt.baseURL, tt.model)
		if got := reasoningValues(cap); !slices.Equal(got, tt.want) {
			t.Errorf("%s: got %v, want %v", tt.model, got, tt.want)
		}
		if cap.CanDisable != tt.disable {
			t.Errorf("%s: can_disable=%v", tt.model, cap.CanDisable)
		}
	}
}

func TestStaticReasoningCapabilityDoesNotGuessUnknownCompatibleModels(t *testing.T) {
	if got := StaticReasoningCapability("openai-compatible", "custom", "https://example.test/v1", "gpt-5"); got != nil {
		t.Fatalf("got %#v, want Auto-only", got)
	}
}

func reasoningValues(cap *ReasoningCapability) []string {
	if cap == nil {
		return nil
	}
	out := make([]string, 0, len(cap.Values))
	for _, value := range cap.Values {
		out = append(out, value.Value)
	}
	return out
}
```

- [ ] **Step 6: Implement a narrow, documented static resolver**

Use exact families and valid dated-snapshot suffixes. Do not use broad
`strings.Contains`. Keep separate tables for direct OpenAI, Codex, Anthropic,
and Gemini. Unknown models and arbitrary OpenAI-compatible endpoints return
nil. Put the official documentation URL next to each table.

- [ ] **Step 7: Run the package tests and confirm GREEN**

```bash
gofmt -w internal/llm/reasoning.go internal/llm/reasoning_catalog.go internal/llm/reasoning_test.go internal/llm/types.go
go test ./internal/llm -run 'Test(ReasoningCapability|ValidateReasoningEffort|StaticReasoning)' -count=1 -v
```

Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add internal/llm/reasoning.go internal/llm/reasoning_catalog.go internal/llm/reasoning_test.go internal/llm/types.go
git commit -m "$(cat <<'EOF'
Define model-aware reasoning capabilities
EOF
)"
```

---

### Task 2: Make provider request bodies honor the capability contract

**Files:**
- Create: `internal/llm/reasoning_request_test.go`
- Modify: `internal/llm/openai.go`
- Modify: `internal/llm/codex.go`
- Modify: `internal/llm/anthropic.go`
- Modify: `internal/llm/gemini.go`
- Modify: `internal/llm/gemini_test.go`

**Interfaces:**
- Consumes: `Request.ReasoningCapability` and `StaticReasoningCapability`.
- Produces: validated provider-specific request bodies and OpenRouter live capability metadata.

- [ ] **Step 1: Add exact-body tests for Auto and explicit disable**

```go
func TestOpenRouterReasoningBodySendsExplicitDisable(t *testing.T) {
	cap, _ := NewReasoningCapability(
		[]ReasoningValue{
			{Value: "none", Label: "Off", Kind: ReasoningValueDisable},
			{Value: "high", Label: "High"},
		},
		"high", false, ReasoningCapabilityLive,
	)
	c := &openAIClient{opts: Options{BaseURL: "https://openrouter.ai/api/v1"}}
	body := c.buildBody(Request{
		Model: "vendor/model", ReasoningEffort: "none", ReasoningCapability: cap,
	}, false)
	reasoning, ok := body["reasoning"].(map[string]any)
	if !ok || reasoning["effort"] != "none" {
		t.Fatalf("reasoning = %#v", body["reasoning"])
	}
}

func TestReasoningAutoOmitsProviderFields(t *testing.T) {
	req := Request{Model: "gpt-5", ReasoningEffort: ""}
	if body := (&openAIClient{}).buildBody(req, false); body["reasoning_effort"] != nil {
		t.Fatalf("OpenAI body = %#v", body)
	}
	if body := (&codexClient{}).buildBody(req, false); body["reasoning"] != nil {
		t.Fatalf("Codex body = %#v", body)
	}
}
```

- [ ] **Step 2: Add failing modern Anthropic and Gemini tests**

```go
func TestAnthropicAdaptiveThinkingBody(t *testing.T) {
	cap := StaticReasoningCapability("anthropic", "anthropic", "", "claude-sonnet-5")
	body := (&anthropicClient{}).buildBody(Request{
		Model: "claude-sonnet-5", ReasoningEffort: "xhigh", ReasoningCapability: cap,
	}, false)
	if got, want := body["thinking"], map[string]any{"type": "adaptive"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("thinking = %#v, want %#v", got, want)
	}
	if got, want := body["output_config"], map[string]any{"effort": "xhigh"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("output_config = %#v, want %#v", got, want)
	}
}

func TestGemini3MinimalIsNotDisable(t *testing.T) {
	got := geminiThinkingConfig("gemini-3.6-flash", "minimal")
	want := map[string]any{"thinkingLevel": "MINIMAL", "includeThoughts": true}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}
```

Also add exact tests for Anthropic legacy fixed budget, Gemini 2.5 Flash budget
zero only when Off is supported, OpenAI `reasoning_effort`, and Codex nested
`reasoning.effort`.

- [ ] **Step 3: Run focused tests and confirm RED**

```bash
go test ./internal/llm -run 'Test(OpenAI|OpenRouter|Codex|Anthropic|Gemini).*Reason|TestGemini3Minimal' -count=1 -v
```

Expected: failures showing omitted disable, fixed Anthropic budgets, and missing
Gemini minimal handling.

- [ ] **Step 4: Add a shared request-value validator**

```go
func reasoningValue(req Request, kind, providerID, baseURL string) (string, error) {
	value := req.ReasoningEffort
	if value == "" {
		return "", nil
	}
	capability := req.ReasoningCapability
	if capability == nil {
		capability = StaticReasoningCapability(kind, providerID, baseURL, req.Model)
	}
	if err := ValidateReasoningEffort(req.Model, capability, value); err != nil {
		return "", err
	}
	return value, nil
}
```

Call this before every upstream HTTP request. Keep provider values unchanged;
remove adapter-wide lowercasing.

- [ ] **Step 5: Parse OpenRouter live reasoning metadata**

Add the wire type:

```go
type openRouterReasoningMetadata struct {
	SupportedEfforts  []string `json:"supported_efforts"`
	DefaultEffort     string   `json:"default_effort"`
	DefaultEnabled    *bool    `json:"default_enabled"`
	Mandatory         bool     `json:"mandatory"`
	SupportsMaxTokens bool     `json:"supports_max_tokens"`
}
```

Build a live capability in `openAIClient.Models`. Mark only OpenRouter's
documented `none` value as disable. Contradictory metadata returns no capability
instead of being repaired. Preserve whitelist behavior in the agent layer.

- [ ] **Step 6: Implement provider-specific body mapping**

- OpenAI Chat Completions: set `reasoning_effort` for every validated non-empty
  value, including disable.
- OpenRouter: set `reasoning: {"effort": value}`.
- Codex Responses: set `reasoning: {"effort": value}`.
- Anthropic modern models: set `thinking: {"type":"adaptive"}` and
  `output_config: {"effort": value}`; use `thinking: {"type":"disabled"}` only
  for a marked disable value supported by that model.
- Anthropic legacy models: retain fixed budgets only for catalogued legacy
  capabilities.
- Gemini 3: use `thinkingLevel`; Minimal has `includeThoughts:true`.
- Gemini legacy: use `thinkingBudget` only for catalogued legacy capabilities.

- [ ] **Step 7: Prove invalid values fail before network I/O**

Use an `httptest.Server` request counter. Call `Chat` with a capability that
allows only `low` and an explicit `max`; assert an
`UnsupportedReasoningEffortError` and zero requests.

- [ ] **Step 8: Run adapter tests and full LLM package**

```bash
gofmt -w internal/llm/openai.go internal/llm/codex.go internal/llm/anthropic.go internal/llm/gemini.go internal/llm/reasoning_request_test.go internal/llm/gemini_test.go
go test ./internal/llm -run 'Test.*(Reasoning|Thinking|Minimal)' -count=1 -v
go test ./internal/llm -count=1
```

Expected: PASS, excluding credential-gated live tests.

- [ ] **Step 9: Commit**

```bash
git add internal/llm
git commit -m "$(cat <<'EOF'
Honor model-specific reasoning controls
EOF
)"
```

---

### Task 3: Resolve reasoning against the effective model in the agent

**Files:**
- Create: `internal/agent/reasoning.go`
- Create: `internal/agent/reasoning_test.go`
- Modify: `internal/agent/client.go`
- Modify: `internal/agent/agent.go`
- Modify: `internal/agent/harness.go`
- Modify: `internal/llm/fallback.go`
- Modify: `cmd/antares/main.go`

**Interfaces:**
- Produces: `Agent.ReasoningCapability` and `Agent.ValidateReasoningEffort`.
- Consumes: live/static model metadata and request/role/config precedence.

- [ ] **Step 1: Add failing effective-value tests**

```go
func TestResolveReasoningExplicitUnsupportedReturnsError(t *testing.T) {
	a := agentWithConfig(config.Default())
	_, err := a.resolveReasoning(context.Background(), reasoningInput{
		ModelRef: "google/gemini-3.6-flash",
		Explicit: "max",
	})
	if err == nil || !llm.IsUnsupportedReasoningEffort(err) {
		t.Fatalf("err = %v", err)
	}
}

func TestResolveReasoningUnsupportedStoredValueFallsBackToAuto(t *testing.T) {
	cfg := config.Default()
	cfg.Model.Provider = "google"
	cfg.Model.Default = "gemini-3.6-flash"
	cfg.Agent.ReasoningEffort = "max"
	a := agentWithConfig(cfg)
	got, err := a.resolveReasoning(context.Background(), reasoningInput{ModelRef: cfg.Model.Default})
	if err != nil || got.Value != "" || got.DiscardedLegacy != "max" {
		t.Fatalf("got=%+v err=%v", got, err)
	}
}
```

Add tests for role precedence, per-model live metadata, curated-model static
enrichment, and fallback replacement.

- [ ] **Step 2: Run and confirm RED**

```bash
go test ./internal/agent -run 'Test.*Reasoning|TestFallbackReplacesPrimaryReasoningCapability' -count=1 -v
```

- [ ] **Step 3: Implement agent-level resolution**

```go
type reasoningInput struct {
	ModelRef string
	Explicit string
	Role     string
	Agent    string
	Model    string
}

type reasoningResolution struct {
	Value           string
	Capability      *llm.ReasoningCapability
	DiscardedLegacy string
}

func (a *Agent) ReasoningCapability(ctx context.Context, modelRef string) (*llm.ReasoningCapability, error)
func (a *Agent) ValidateReasoningEffort(ctx context.Context, modelRef, effort string) error
func (a *Agent) resolveReasoning(ctx context.Context, in reasoningInput) (reasoningResolution, error)
```

Explicit values return errors. Role/agent/model stored values are tried in
precedence order and skipped when unsupported. Return the first discarded
legacy value for a one-time notice.

- [ ] **Step 4: Enrich both live and curated model lists**

`Agent.Models` must attach a valid live capability when present. Curated lists
stay whitelists: fetch live metadata only to enrich matching IDs, never append
unlisted models. If live enrichment fails, apply the static resolver.

- [ ] **Step 5: Carry per-entry capability through fallback**

```go
type FallbackEntry struct {
	Client              Client
	Model               string
	ReasoningCapability *ReasoningCapability
}
```

Before each fallback call, replace both `Request.Model` and
`Request.ReasoningCapability`. A legacy configured value unsupported by the
fallback entry becomes Auto; an explicit model override has no fallback chain.

- [ ] **Step 6: Resolve once before the agent turn loop**

In `Agent.Run`, resolve effective reasoning after the final role/model is known
and before the first model call. Attach both value and capability to every
`llm.Request`. Emit one `EventNotice` if a stored value was discarded.

Change `applyRole` so it does not erase whether effort came from explicit
request data versus stored role metadata.

- [ ] **Step 7: Remove the unconditional relevance-classifier `"low"`**

Resolve the classifier model's capability. Use `low` only when supported;
otherwise use Auto. Add a regression test around `messageIsRelevant`.

- [ ] **Step 8: Run agent/fallback tests**

```bash
gofmt -w internal/agent/reasoning.go internal/agent/reasoning_test.go internal/agent/client.go internal/agent/agent.go internal/agent/harness.go internal/llm/fallback.go cmd/antares/main.go
go test ./internal/agent ./internal/llm ./cmd/antares -run 'Reasoning|Fallback|MessageIsRelevant' -count=1
go test -race ./internal/agent ./internal/llm -count=1
```

- [ ] **Step 9: Commit**

```bash
git add internal/agent internal/llm/fallback.go cmd/antares/main.go
git commit -m "$(cat <<'EOF'
Resolve reasoning against the effective model
EOF
)"
```

---

### Task 4: Validate reasoning in server, config, and role boundaries

**Files:**
- Create: `internal/server/reasoning.go`
- Create: `internal/server/reasoning_test.go`
- Create: `internal/config/schema_reasoning_test.go`
- Modify: `internal/config/defaults.go`
- Modify: `internal/config/schema.go`
- Modify: `internal/config/load.go`
- Modify: `internal/server/handlers_chat.go`
- Modify: `internal/server/handlers_config.go`
- Modify: `internal/server/handlers_providers.go`
- Modify: `internal/server/handlers_roles.go`

**Interfaces:**
- Consumes: `Agent.ValidateReasoningEffort` and `Agent.Models`.
- Produces: additive `reasoning_capability` API data and mutation-safe validation.

- [ ] **Step 1: Add config schema and parse-without-write tests**

```go
func TestSchemaMarksReasoningFieldsModelAware(t *testing.T) {
	schema := Schema()
	for _, path := range []string{"agent.reasoning_effort", "model.reasoning_effort"} {
		field := fieldByPath(t, schema, path)
		if len(field.Enum) != 0 || field.OptionsSource != "reasoning_capability" {
			t.Fatalf("%s = %+v", path, field)
		}
	}
}

func TestParseRawDoesNotWriteConfiguration(t *testing.T) {
	before := mustReadConfigFile(t)
	if _, err := ParseRaw("model:\n  default: gpt-5\n"); err != nil {
		t.Fatal(err)
	}
	if after := mustReadConfigFile(t); after != before {
		t.Fatal("ParseRaw changed the config file")
	}
}

func fieldByPath(t *testing.T, fields []Field, path string) Field {
	t.Helper()
	for _, field := range fields {
		if field.Path == path {
			return field
		}
	}
	t.Fatalf("field %q not found", path)
	return Field{}
}
```

- [ ] **Step 2: Implement schema marker and parser**

Add `OptionsSource string 'json:"options_source,omitempty"'` to `config.Field`.
Remove both static reasoning enums. Make fresh defaults `""` (Auto), but do not
rewrite persisted values. Extract `ParseRaw` and make `SaveRaw` call it.
Define `mustReadConfigFile` in the test with `os.ReadFile(ConfigFile())` and
restore the original bytes through `t.Cleanup`. Set `ANTARES_HOME` to
`t.TempDir()` before calling `ConfigFile()` so the real user config is never
touched.

- [ ] **Step 3: Add failing server mutation tests**

Cover:

- `TestHandleModelListAllIncludesReasoningCapability`
- `TestHandleProviderModelInfoReadsModelQueryAndIncludesCapability`
- `TestHandleChatRejectsUnsupportedReasoningBeforeChatRequest`
- `TestHandleUpdateConfigRejectsChangedUnsupportedReasoningWithoutSaving`
- `TestHandleUpdateConfigAllowsUnrelatedEditWithLegacyUnsupportedReasoning`
- `TestHandleSaveRawConfigRejectsNewUnsupportedReasoningWithoutSaving`
- `TestHandleSaveRoleRejectsExplicitUnsupportedReasoning`

Each rejected mutation must compare the config/role file before and after.

- [ ] **Step 4: Run and confirm RED**

```bash
go test ./internal/config ./internal/server -run 'Reasoning|ParseRaw|ProviderModelInfo' -count=1 -v
```

- [ ] **Step 5: Implement the server validation boundary**

```go
func (s *Server) validateExplicitReasoning(
	ctx context.Context,
	cfg *config.Config,
	modelRef string,
	effort string,
) error {
	if effort == "" {
		return nil
	}
	return s.agent.ValidateReasoningEffort(ctx, modelRef, effort)
}
```

Validate chat request effort before opening SSE/model I/O. For dotted config,
raw config, and role saves, validate only newly introduced or changed values.
Unchanged legacy values remain loadable and resolve to Auto at runtime.

- [ ] **Step 6: Fix model-info lookup and expose capability**

Change `r.URL.Query().Get("&model")` to `r.URL.Query().Get("model")`.
Return `reasoning_capability` from model info and list-all while retaining the
legacy `reasoning` boolean.

- [ ] **Step 7: Run focused and package tests**

```bash
gofmt -w internal/config/defaults.go internal/config/schema.go internal/config/load.go internal/config/schema_reasoning_test.go internal/server/reasoning.go internal/server/reasoning_test.go internal/server/handlers_chat.go internal/server/handlers_config.go internal/server/handlers_providers.go internal/server/handlers_roles.go
go test ./internal/config ./internal/server -run 'Reasoning|ParseRaw|ProviderModelInfo' -count=1
go test ./internal/config ./internal/server -count=1
```

- [ ] **Step 8: Commit**

```bash
git add internal/config internal/server
git commit -m "$(cat <<'EOF'
Validate reasoning at configuration boundaries
EOF
)"
```

---

### Task 5: Replace static reasoning UI with model-aware controls

**Files:**
- Create: `web/src/lib/models.ts`
- Create: `web/src/lib/reasoning.ts`
- Create: `web/src/lib/reasoning.test.mjs`
- Modify: `web/src/components/chat/ModelPicker.tsx`
- Modify: `web/src/components/chat/ReasoningPicker.tsx`
- Modify: `web/src/pages/ChatPage.tsx`
- Modify: `web/src/pages/RolesPage.tsx`
- Modify: `web/src/pages/ConfigPage.tsx`
- Modify: `web/src/pages/ModelsPage.tsx`
- Modify: `web/src/pages/ProvidersPage.tsx`
- Modify: `web/src/lib/i18n.tsx`

**Interfaces:**
- Consumes: API `reasoning_capability`.
- Produces: per-model Auto/effort state and reusable `ChatModelSelection`.

- [ ] **Step 1: Add failing pure helper tests**

```javascript
test('options preserve opaque values and mark only explicit disable', () => {
  const cap = {
    values: [
      { value: 'none', label: 'Off', kind: 'disable' },
      { value: 'extra-high', label: 'Extra High' },
    ],
    default: 'extra-high',
    mandatory: false,
    can_disable: true,
    source: 'live',
  }
  expect(reasoningOptions(cap).map((x) => x.value)).toEqual(['', 'none', 'extra-high'])
})

test('legacy preference migrates once only when valid', () => {
  const storage = memoryStorage({ 'antares:reasoning': 'high' })
  const cap = capability(['low', 'high'])
  expect(loadReasoningPreference(storage, 'openai', 'gpt-5', cap)).toEqual({
    value: 'high',
    migrated: true,
  })
  expect(storage.getItem('antares:reasoning')).toBeNull()
  expect(storage.getItem(reasoningPreferenceKey('openai', 'gpt-5'))).toBe('high')
})

function memoryStorage(initial = {}) {
  const values = new Map(Object.entries(initial))
  return {
    getItem: (key) => values.get(key) ?? null,
    setItem: (key, value) => values.set(key, value),
    removeItem: (key) => values.delete(key),
  }
}

function capability(values) {
  return {
    values: values.map((value) => ({ value, label: value })),
    mandatory: false,
    can_disable: false,
    source: 'static',
  }
}
```

Add tests for Auto-only models, mandatory models, invalid scoped values, and
provider/model storage-key isolation.

- [ ] **Step 2: Run and confirm RED**

```bash
cd web && bun test src/lib/reasoning.test.mjs
```

- [ ] **Step 3: Implement shared types and preference helpers**

```ts
export interface ReasoningValue {
  value: string
  label: string
  kind?: 'disable'
}

export interface ReasoningCapability {
  values: ReasoningValue[]
  default?: string
  mandatory: boolean
  can_disable: boolean
  source: 'live' | 'static'
}

export interface ChatModelSelection {
  provider: string
  model: string
  name: string
  providerLabel: string
  reasoningCapability?: ReasoningCapability
}
```

Use `antares:reasoning:v2:<encoded-provider>:<encoded-model>`. Always remove the
old global key after one migration attempt.

- [ ] **Step 4: Make `ReasoningPicker` presentational**

```ts
export interface ReasoningPickerProps {
  value: string
  capability?: ReasoningCapability
  onChange(value: string): void
  compact?: boolean
}
```

Render Auto plus exact capability values. Hide the chip when no capability is
present. Never turn `capability.default` into an explicit override.

- [ ] **Step 5: Make `ModelPicker` return the full selection**

Change `onModelChange` to receive `ChatModelSelection`. On initial load, resolve
active metadata through `/providers/{id}/model-info?model=...`; picker rows use
the capability already returned by `/model/list-all`.

- [ ] **Step 6: Scope composer reasoning by model**

In `ChatPage`, load/sanitize the preference synchronously whenever provider or
model changes. Store capability and value together in a ref used by `sendText`
so an old model's value cannot leak into the next request.

- [ ] **Step 7: Replace role/config enums**

Roles resolve capability from the role's explicit model or inherited active
model. Config fields with `options_source:"reasoning_capability"` render the
same options. Show an unchanged unsupported legacy value as disabled with a
one-time Auto notice; unrelated saves must not rewrite it.

- [ ] **Step 8: Add copy to all locale maps and verify**

Add translations for Auto, unsupported legacy value, adaptive/default hint,
mandatory reasoning, and provider-controlled reasoning.

```bash
cd web
bun test src/lib/reasoning.test.mjs
bun test
bun x tsc -b --noEmit
bun run build
```

- [ ] **Step 9: Commit**

```bash
git add web/src
git commit -m "$(cat <<'EOF'
Adapt reasoning controls to each model
EOF
)"
```

---

### Task 6: Extend Cursor wire types and resumable stream recovery

**Files:**
- Modify: `internal/cursor/types.go`
- Modify: `internal/cursor/client_test.go`
- Modify: `internal/cursor/stream.go`
- Modify: `internal/cursor/stream_test.go`

**Interfaces:**
- Produces: prompt images, complete tool events, and `StreamRunWithOptions`.
- Preserves: existing `StreamRun` API as a compatibility wrapper.

- [ ] **Step 1: Add failing image and exact-model encoding test**

```go
func TestCreateAgentEncodesPromptImagesAndExactModelParams(t *testing.T) {
	// Capture the request body in an httptest server.
	want := CreateAgentRequest{
		Prompt: Prompt{
			Text: "inspect this",
			Images: []PromptImage{{Data: "aGVsbG8=", MimeType: "image/png"}},
		},
		Model: &ModelSelection{
			ID: "gpt-5.6-sol",
			Params: []ModelParameterSelection{
				{ID: "context", Value: "1m"},
				{ID: "reasoning", Value: "max"},
				{ID: "fast", Value: "true"},
			},
		},
	}
	// Assert decoded body equals want exactly.
}
```

- [ ] **Step 2: Add failing persisted-event-ID and reset tests**

Cover:

- initial request carries the supplied `Last-Event-ID`;
- a 410/invalid ID calls `OnReset` before replay;
- terminal result still wins over a later retryable read error;
- full tool-call ID, args, result, and truncation fields survive decoding.

- [ ] **Step 3: Run and confirm RED**

```bash
go test ./internal/cursor -run 'CreateAgentEncodes|StreamRunWithOptions|CompleteToolCall' -count=1 -v
```

- [ ] **Step 4: Add prompt image and richer stream types**

```go
type PromptImage struct {
	Data     string `json:"data,omitempty"`
	URL      string `json:"url,omitempty"`
	MimeType string `json:"mimeType,omitempty"`
}

type Prompt struct {
	Text   string        `json:"text"`
	Images []PromptImage `json:"images,omitempty"`
}

type StreamOptions struct {
	LastEventID string
	OnReset     func() error
}
```

Extend `StreamEvent` with run/call IDs, raw tool args/result, and truncation.

- [ ] **Step 5: Implement `StreamRunWithOptions`**

```go
func (c *Client) StreamRunWithOptions(
	ctx context.Context,
	agentID, runID string,
	options StreamOptions,
	emit func(StreamEvent) error,
) (*Run, error)
```

`StreamRun` calls this with empty options. When the upstream resume token is
invalid, invoke `OnReset`, clear the token once, then reconnect.

- [ ] **Step 6: Run Cursor package tests**

```bash
gofmt -w internal/cursor/types.go internal/cursor/client_test.go internal/cursor/stream.go internal/cursor/stream_test.go
go test ./internal/cursor -count=1
go test -race ./internal/cursor -count=1
```

- [ ] **Step 7: Commit**

```bash
git add internal/cursor
git commit -m "$(cat <<'EOF'
Support resumable rich Cursor streams
EOF
)"
```

---

### Task 7: Build the shared Cursor catalogue and run service

**Files:**
- Create: `internal/cursorrun/service.go`
- Create: `internal/cursorrun/catalog.go`
- Create: `internal/cursorrun/service_test.go`
- Modify: `internal/server/handlers_providers.go`
- Modify: `internal/server/server.go`
- Modify: `internal/server/cursor_provider_test.go`

**Interfaces:**
- Produces: `cursorrun.Runner`.
- Consumes: resolved Cursor provider options and `internal/cursor.Client`.

- [ ] **Step 1: Write failing cache and exact-variant tests**

```go
func TestValidateModelAcceptsHiddenVariantParams(t *testing.T) {
	model := cursor.Model{
		ID: "claude-opus-5",
		Parameters: []cursor.ModelParameter{{ID: "effort"}},
		Variants: []cursor.ModelVariant{{
			Params: []cursor.ModelParameterSelection{
				{ID: "cyber", Value: "false"},
				{ID: "effort", Value: "max"},
			},
			IsDefault: true,
		}},
	}
	runner := newTestRunner(t, cursor.ModelCatalog{Items: []cursor.Model{model}})
	got, err := runner.ValidateModel(context.Background(), &cursor.ModelSelection{
		ID: model.ID,
		Params: model.Variants[0].Params,
	}, RequireExactVariant)
	if err != nil || !reflect.DeepEqual(got.Params, model.Variants[0].Params) {
		t.Fatalf("got=%+v err=%v", got, err)
	}
}

func newTestRunner(t *testing.T, catalog cursor.ModelCatalog) Runner {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(catalog)
	}))
	t.Cleanup(srv.Close)
	return New(Options{
		ResolveClient: func() (cursor.Options, error) {
			return cursor.Options{
				BaseURL: srv.URL, APIKey: "synthetic-key", HTTPClient: srv.Client(),
			}, nil
		},
		Now: time.Now, CatalogTTL: 5 * time.Minute,
	})
}
```

Add tests for five-minute TTL, key-change fingerprint isolation, explicit
invalidation, duplicate param IDs, order-insensitive matching, no synthetic
combination, stale refresh exactly once, and tool omission preserving default.

- [ ] **Step 2: Run and confirm RED**

```bash
go test ./internal/cursorrun -count=1
```

- [ ] **Step 3: Define the service interface**

```go
type SelectionPolicy uint8

const (
	PreserveUpstreamDefault SelectionPolicy = iota
	RequireExactVariant
)

type Runner interface {
	Catalog(ctx context.Context, force bool) (*cursor.ModelCatalog, error)
	InvalidateCatalog()
	ValidateModel(ctx context.Context, selection *cursor.ModelSelection, policy SelectionPolicy) (*cursor.ModelSelection, error)
	CreateAgent(ctx context.Context, req cursor.CreateAgentRequest) (*cursor.CreateAgentResponse, error)
	CreateRun(ctx context.Context, agentID string, req cursor.CreateRunRequest) (*cursor.Run, error)
	GetAgent(ctx context.Context, agentID string) (*cursor.Agent, error)
	GetRun(ctx context.Context, agentID, runID string) (*cursor.Run, error)
	CancelRun(ctx context.Context, agentID, runID string) error
	StreamRun(ctx context.Context, agentID, runID, lastEventID string, onReset func() error, emit func(cursor.StreamEvent) error) (*cursor.Run, error)
	Progress(cursor.StreamEvent) Progress
}

type ClientResolver func() (cursor.Options, error)

type Options struct {
	ResolveClient ClientResolver
	Now           func() time.Time
	CatalogTTL    time.Duration
}

type Progress struct {
	Message string
	Chunk   string
}
```

The production constructor receives `Options` with a five-minute TTL.

- [ ] **Step 4: Implement cache and canonical matching**

Cache per normalized base URL plus SHA-256 credential fingerprint; never expose
the fingerprint. Canonical matching sorts copies by param ID, rejects duplicate
IDs, and returns the original upstream variant order. Empty params are valid
only for a model with no variants or the backward-compatible tool policy.

- [ ] **Step 5: Implement lifecycle delegation and redaction**

Delegate to a freshly resolved client. Keep create/run/cancel single-attempt.
Centralize bounded progress and sanitize catalogue, stream, error, and Git text
with the existing Cursor redaction policy.

- [ ] **Step 6: Return the full Cursor catalogue from the provider endpoint**

Include:

```go
type modelOut struct {
	ID          string                  `json:"id"`
	Name        string                  `json:"name"`
	Description string                  `json:"description,omitempty"`
	Aliases     []string                `json:"aliases"`
	Parameters  []cursor.ModelParameter `json:"parameters"`
	Variants    []cursor.ModelVariant   `json:"variants"`
}
```

Normalize nil arrays to empty arrays. Use the shared runner cache. Invalidate it
after key/settings changes through `Server.SetConfig`.

- [ ] **Step 7: Run service and provider tests**

```bash
gofmt -w internal/cursorrun internal/server/handlers_providers.go internal/server/server.go internal/server/cursor_provider_test.go
go test ./internal/cursorrun ./internal/server -run 'Catalog|Variant|ProviderModels' -count=1
go test -race ./internal/cursorrun -count=1
```

- [ ] **Step 8: Commit**

```bash
git add internal/cursorrun internal/server
git commit -m "$(cat <<'EOF'
Share Cursor catalogue and run lifecycle
EOF
)"
```

---

### Task 8: Inspect repositories and validate Cursor attachments

**Files:**
- Create: `internal/cursorrun/repository.go`
- Create: `internal/cursorrun/repository_test.go`
- Create: `internal/server/cursor_attachments.go`
- Create: `internal/server/cursor_attachments_test.go`
- Create: `internal/server/handlers_cursor_repository.go`
- Modify: `internal/server/routes.go`
- Modify: `internal/server/handlers_project_env.go`

**Interfaces:**
- Produces: `InspectRepository`, `NormalizeGitHubRepository`, and `decodeCursorImages`.

- [ ] **Step 1: Add repository normalization tests**

```go
func TestNormalizeGitHubRepository(t *testing.T) {
	tests := map[string]string{
		"git@github.com:owner/repo.git":             "https://github.com/owner/repo",
		"ssh://git@github.com/owner/repo.git":       "https://github.com/owner/repo",
		"https://github.com/owner/repo.git":         "https://github.com/owner/repo",
		"https://github.com/owner/repo":             "https://github.com/owner/repo",
	}
	for in, want := range tests {
		got, err := NormalizeGitHubRepository(in)
		if err != nil || got != want {
			t.Errorf("%q => %q, %v; want %q", in, got, err, want)
		}
	}
}
```

Reject credentials, queries/fragments, local paths, non-GitHub hosts, and paths
other than exactly `owner/repo`.

- [ ] **Step 2: Add linked-worktree and dirty/ahead tests**

Create temporary Git repositories using `git init`, a bare remote, and
`git worktree add`. Verify origin, branch/detached SHA, dirty state, known
remote-tracking ref, and local-only commit count without fetching the network.

- [ ] **Step 3: Add strict image validation tests**

Cover exactly five PNG/JPEG/GIF/WebP images, six-image rejection, unsupported
MIME, decoded payload above 15 MiB, and MIME signature mismatch. Assert failures
occur before an approval/upstream callback. Also test strict JSON decoding and
a 105 MiB route-specific request cap so five legal 15 MiB images are possible
after base64 expansion while larger bodies fail with 413.

- [ ] **Step 4: Run and confirm RED**

```bash
go test ./internal/cursorrun ./internal/server -run 'Repository|CursorImages' -count=1 -v
```

- [ ] **Step 5: Implement repository inspection using Git commands**

```go
type RepositoryInfo struct {
	Repository       bool   `json:"repository"`
	URL              string `json:"url,omitempty"`
	StartingRef      string `json:"starting_ref,omitempty"`
	Dirty            bool   `json:"dirty"`
	LocalOnlyCommits int    `json:"local_only_commits"`
	RemoteRefKnown   bool   `json:"remote_ref_known"`
	Warning          string `json:"warning,omitempty"`
}
```

Use `git rev-parse --is-inside-work-tree`, not `.git` directory checks.

- [ ] **Step 6: Implement strict Cursor image decoding**

```go
func decodeCursorImages(dataURLs []string) ([]cursor.PromptImage, error)
func decodeCursorChatBody(w http.ResponseWriter, r *http.Request, dst any) error
```

Allow maximum five, exact supported MIME types, base64 data URLs only, and
15 MiB decoded per image. Sniff decoded bytes before returning. Do not retain a
second decoded copy after validation. `decodeCursorChatBody` uses
`http.MaxBytesReader` with a 105 MiB cap and
`json.Decoder.DisallowUnknownFields`.

- [ ] **Step 7: Add repository preflight route**

Register `GET /api/project/cursor-repository?dir=...`. Resolve the project path
through existing project security checks, require dashboard authentication,
and return `RepositoryInfo`.

- [ ] **Step 8: Verify and commit**

```bash
gofmt -w internal/cursorrun/repository.go internal/cursorrun/repository_test.go internal/server/cursor_attachments.go internal/server/cursor_attachments_test.go internal/server/handlers_cursor_repository.go internal/server/routes.go internal/server/handlers_project_env.go
go test ./internal/cursorrun ./internal/server -run 'Repository|CursorImages' -count=1
git add internal/cursorrun internal/server
git commit -m "$(cat <<'EOF'
Validate Cursor repositories and images
EOF
)"
```

---

### Task 9: Extract an explicit operation approval gate

**Files:**
- Create: `internal/approval/gate.go`
- Create: `internal/approval/gate_test.go`
- Modify: `internal/agent/approval.go`
- Modify: `internal/agent/agent.go`
- Modify: `internal/server/handlers_approval.go`
- Modify: `internal/tools/registry.go`
- Modify: `internal/tools/cursor_agent.go`
- Modify: `internal/tools/cursor_agent_test.go`

**Interfaces:**
- Produces: instance-owned `approval.Gate` and `tools.OperationApproval`.
- Preserves: existing `/api/approvals` list/resolve endpoints.

- [ ] **Step 1: Add failing generic gate tests**

```go
func TestGateRetainsImmutableOperation(t *testing.T) {
	g := NewGate(time.Minute)
	op := Operation{SessionID: "ses-1", Tool: "cursor_agent", Arguments: `{"model":"a"}`, Message: "Start Cursor"}
	emitted := make(chan Request, 1)
	done := make(chan bool, 1)
	go func() {
		ok, _ := g.Await(context.Background(), op, func(r Request) error {
			emitted <- r
			return nil
		})
		done <- ok
	}()
	req := <-emitted
	op.Arguments = `{"model":"b"}`
	if !g.Resolve(req.ID, true) || !<-done {
		t.Fatal("approval did not resolve")
	}
	if got := req.Arguments; got != `{"model":"a"}` {
		t.Fatalf("arguments mutated: %s", got)
	}
}
```

Add oldest-first, deny, timeout, context cancellation, unknown ID, and
concurrent-resolution tests.

- [ ] **Step 2: Run and confirm RED**

```bash
go test ./internal/approval -count=1
```

- [ ] **Step 3: Implement the instance-owned gate**

```go
type Operation struct {
	SessionID string
	Tool      string
	Arguments string
	Message   string
	Reason    string
}

type Gate struct {
	mu      sync.Mutex
	pending map[string]*pendingRequest
	timeout time.Duration
}
```

Deep-copy strings, sort `Pending()` by `CreatedAt` then ID, and remove requests
exactly once.

- [ ] **Step 4: Attach one gate to each Agent**

Construct it in `agent.New`. Keep `PendingApprovals` and `ResolveApproval` as
delegating compatibility methods used by server handlers.

- [ ] **Step 5: Add explicit Cursor operation classification**

```go
type OperationApproval interface {
	ApprovalOperation(args json.RawMessage, sessionID string) (approval.Operation, error)
}
```

`cursorAgentTool` returns a bounded/redacted display projection containing
operation, model/params, repo/ref, mode, auto-PR, and IDs. It must exclude full
prompt text, images, and API key.

- [ ] **Step 6: Force explicit Cursor approval**

In `agent.checkApproval`, an `OperationApproval` tool always calls the gate,
regardless of general auto mode. Normal tools retain current auto/prompt/deny
behavior.

- [ ] **Step 7: Add regressions and run tests**

```bash
go test ./internal/approval ./internal/agent ./internal/tools -run 'Approval|Cursor.*Approval|Denied|Expired' -count=1
go test -race ./internal/approval ./internal/agent ./internal/tools -count=1
```

Assert start/follow-up/cancel send zero upstream requests until allowed.

- [ ] **Step 8: Commit**

```bash
git add internal/approval internal/agent internal/server/handlers_approval.go internal/tools
git commit -m "$(cat <<'EOF'
Require explicit approval for Cursor operations
EOF
)"
```

---

### Task 10: Persist recoverable Cursor session state

**Files:**
- Create: `internal/store/cursor_sessions.go`
- Create: `internal/store/cursor_sessions_test.go`
- Modify: `internal/store/types.go`
- Modify: `internal/store/migrations.go`
- Modify: `internal/store/sessions.go`

**Interfaces:**
- Produces: durable, compare-and-swap Cursor run state and idempotent finalization.

- [ ] **Step 1: Add failing migration and round-trip tests**

Cover:

- round trip and session cascade delete;
- compare-and-swap rejecting a competing turn;
- recoverable list excluding committed terminal runs;
- reuse invalidation preserving agent/run IDs;
- atomic, idempotent final assistant commit.

- [ ] **Step 2: Run and confirm RED**

```bash
go test ./internal/store -run CursorSession -count=1 -v
```

- [ ] **Step 3: Define the state model**

```go
type CursorSessionState struct {
	SessionID          string    `json:"session_id"`
	TargetActive       bool      `json:"target_active"`
	ReuseValid         bool      `json:"reuse_valid"`
	ModelID            string    `json:"model_id"`
	ModelParams        string    `json:"model_params"`
	RepositoryURL      string    `json:"repository_url"`
	StartingRef        string    `json:"starting_ref"`
	Mode               string    `json:"mode"`
	AutoCreatePR       bool      `json:"auto_create_pr"`
	AgentID             string    `json:"agent_id"`
	RunID               string    `json:"run_id"`
	RemoteStatus        string    `json:"remote_status"`
	LastEventID         string    `json:"last_event_id"`
	PartialText         string    `json:"partial_text"`
	PartialReasoning    string    `json:"partial_reasoning"`
	GitState            string    `json:"git_state"`
	OperationState      string    `json:"operation_state"`
	UserMessageID      string    `json:"user_message_id"`
	AssistantMessageID string    `json:"assistant_message_id"`
	Revision            int64     `json:"revision"`
	UpdatedAt           time.Time `json:"updated_at"`
}
```

Allowed operation states are `idle`, `awaiting_approval`, `create_in_flight`,
`run_in_flight`, `terminal`, `committed`, and `ambiguous`.

- [ ] **Step 4: Add SQLite and Postgres migration SQL**

Use a primary/foreign key on `session_id` with cascade delete, a revision
column, and an index on operation state. Do not store API keys or image data.

- [ ] **Step 5: Extend the Store interface and implementations**

```go
PutCursorSessionState(context.Context, *CursorSessionState) error
GetCursorSessionState(context.Context, string) (*CursorSessionState, error)
ListRecoverableCursorSessionStates(context.Context) ([]CursorSessionState, error)
CompareAndSwapCursorSessionState(context.Context, *CursorSessionState, int64) (bool, error)
InvalidateCursorReuse(context.Context, string) error
CommitCursorAssistant(context.Context, *CursorSessionState, *Message) error
```

Canonicalize model params before persistence. `CommitCursorAssistant` appends a
deterministic run-associated message and marks committed in one transaction.

- [ ] **Step 6: Run store tests and race tests**

```bash
gofmt -w internal/store/cursor_sessions.go internal/store/cursor_sessions_test.go internal/store/types.go internal/store/migrations.go internal/store/sessions.go
go test ./internal/store -run CursorSession -count=1
go test -race ./internal/store -count=1
```

- [ ] **Step 7: Commit**

```bash
git add internal/store
git commit -m "$(cat <<'EOF'
Persist recoverable Cursor run state
EOF
)"
```

---

### Task 11: Refactor existing Cursor tools onto the shared runner

**Files:**
- Modify: `internal/tools/deps.go`
- Modify: `internal/tools/cursor_agent.go`
- Modify: `internal/tools/cursor_agent_test.go`
- Modify: `internal/agent/agent.go`
- Modify: `cmd/antares/main.go`
- Modify: `cmd/antares/setup.go`

**Interfaces:**
- Consumes: `cursorrun.Runner`.
- Produces: backward-compatible `cursor_agent` with optional `model_params`.

- [ ] **Step 1: Add failing backward-compatibility and exact-param tests**

```go
func TestCursorAgentStartAcceptsExactModelParams(t *testing.T) {
	args := `{
	  "action":"start",
	  "prompt":"fix it",
	  "model":"gpt-5.6-sol",
	  "model_params":[
	    {"id":"context","value":"1m"},
	    {"id":"reasoning","value":"max"}
	  ]
	}`
	// Execute with a fake Runner and assert CreateAgent receives these params exactly.
}

func TestCursorAgentModelWithoutParamsPreservesUpstreamDefault(t *testing.T) {
	// Existing callers with only "model" must not be forced onto a variant.
}
```

- [ ] **Step 2: Run and confirm RED**

```bash
go test ./internal/tools -run 'CursorAgent.*(Params|Default|Runner)' -count=1 -v
```

- [ ] **Step 3: Add the runner dependency**

```go
type Deps struct {
	// existing fields
	Cursor cursorrun.Runner
}
```

Add `cursorRunner` plus `SetCursorRunner` on `Agent`, and include it in every
tool `Input.Deps`.

- [ ] **Step 4: Construct the runner at runtime scope**

Create one runner in `runtimeServices` with a resolver closure that reads the
current atomically published config. Inject the same instance into the Agent and
Server. Config reload invalidates its catalogue.

- [ ] **Step 5: Refactor tools to call the runner**

Remove duplicated client construction, streaming, progress bounding, and error
classification from `cursor_agent.go`. Add:

```go
ModelParams []cursor.ModelParameterSelection `json:"model_params"`
```

Omitted params use `PreserveUpstreamDefault`; provided params are exact-variant
validated. Keep all existing result text/meta and timeout semantics.

- [ ] **Step 6: Run existing and new tool/runtime tests**

```bash
gofmt -w internal/tools/deps.go internal/tools/cursor_agent.go internal/tools/cursor_agent_test.go internal/agent/agent.go cmd/antares/main.go cmd/antares/setup.go
go test ./internal/tools ./internal/agent ./cmd/antares -run 'Cursor|Runtime' -count=1
go test -race ./internal/tools ./internal/agent -count=1
```

- [ ] **Step 7: Commit**

```bash
git add internal/tools internal/agent/agent.go cmd/antares
git commit -m "$(cat <<'EOF'
Run Cursor tools through the shared service
EOF
)"
```

---

### Task 12: Add the direct Cursor SSE coordinator and recovery

**Files:**
- Create: `internal/server/handlers_cursor.go`
- Create: `internal/server/cursor_events.go`
- Create: `internal/server/handlers_cursor_test.go`
- Modify: `internal/server/livechat.go`
- Modify: `internal/server/handlers_chat.go`
- Modify: `internal/server/routes.go`
- Modify: `internal/server/server.go`

**Interfaces:**
- Produces: `/api/chat/cursor`, `/api/chat/cursor/cancel`, and persisted attach recovery.
- Consumes: approval gate, Cursor runner, repository/image validation, store state, and live-run hub.

- [ ] **Step 1: Add no-request-before-approval SSE test**

```go
func TestCursorChatSendsNoUpstreamRequestBeforeApproval(t *testing.T) {
	s, fake := newCursorDirectTestServer(t)
	stream := postCursorChat(t, s, cursorChatRequest{
		Message: "fix it",
		Model: cursor.ModelSelection{
			ID: "gpt-5.6-sol",
			Params: []cursor.ModelParameterSelection{{ID: "reasoning", Value: "max"}},
		},
	})
	approval := stream.NextType(t, agent.EventApproval)
	if got := fake.CreateAgentCalls(); got != 0 {
		t.Fatalf("CreateAgent calls before approval = %d", got)
	}
	resolveApproval(t, s, approval.ID, true)
	stream.NextType(t, agent.EventToolProgress)
	if got := fake.CreateAgentCalls(); got != 1 {
		t.Fatalf("CreateAgent calls after approval = %d", got)
	}
}

type fakeCursorRunner struct {
	mu          sync.Mutex
	createCalls int
}

func (f *fakeCursorRunner) CreateAgent(context.Context, cursor.CreateAgentRequest) (*cursor.CreateAgentResponse, error) {
	f.mu.Lock()
	f.createCalls++
	f.mu.Unlock()
	return &cursor.CreateAgentResponse{
		Agent: cursor.Agent{ID: "bc-test"},
		Run: cursor.Run{ID: "run-test", Status: "CREATING"},
	}, nil
}

func (f *fakeCursorRunner) CreateAgentCalls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.createCalls
}
```

The test file's `newCursorDirectTestServer`, `postCursorChat`,
`resolveApproval`, and `sseTestStream.NextType` helpers must build a Server with
this fake runner, consume newline-delimited SSE frames, and use the existing
approval endpoint. Implement every remaining `Runner` method on the fake with
deterministic catalogue/status/stream values or an explicit test failure.

- [ ] **Step 2: Add lifecycle and identity tests**

Cover exact immutable selection, consecutive Create Run reuse, and new Create
Agent after model, variant, repository, ref, or auto-PR changes. Mode-only
changes must reuse the same agent.

- [ ] **Step 3: Add detach/recovery/cancel tests**

Cover:

- HTTP follower disconnect leaves remote running;
- `putIfAbsent` rejects a concurrent turn with 409;
- attach replays in-memory events;
- missing in-memory run recovers from persisted IDs and Last-Event-ID;
- reset clears partial accumulators before replay;
- terminal finalization is idempotent;
- local Stop detaches without remote cancel;
- approved Cancel invokes upstream exactly once;
- crash during `create_in_flight` without returned IDs becomes `ambiguous` and is
  never auto-retried;
- deleting a session with an active remote run returns 409 until approved
  cancellation or terminal completion;
- editing or retrying Cursor history invalidates reuse before the next direct
  run.

- [ ] **Step 4: Run and confirm RED**

```bash
go test ./internal/server -run 'TestCursorChat' -count=1 -v
```

- [ ] **Step 5: Add atomic live-run reservation**

```go
func (h *liveHub) putIfAbsent(session string, lr *liveRun) bool
```

Reserve before approval so two browser requests cannot create competing paid
runs.

- [ ] **Step 6: Define request and immutable plan**

```go
type cursorChatRequest struct {
	SessionID     string                `json:"session_id"`
	Message       string                `json:"message"`
	Images        []string              `json:"images"`
	Model         cursor.ModelSelection `json:"model"`
	Mode          string                `json:"mode"`
	ProjectDir    string                `json:"project_dir,omitempty"`
	RepositoryURL *string               `json:"repository_url,omitempty"`
	StartingRef   *string               `json:"starting_ref,omitempty"`
	AutoCreatePR  bool                  `json:"auto_create_pr"`
}
```

Pointer repo fields distinguish auto-discovery from explicit no-repo. Deep-copy
all slices into private `cursorTurnPlan`. The approval projection contains a
240-rune redacted prompt preview and image count, never full prompt/image data.
Decode this route with `decodeCursorChatBody`, not the generic 32 MiB decoder,
and require dashboard password authentication before allocating large image
buffers or preparing a paid operation.

- [ ] **Step 7: Create or hydrate the Antares session**

Emit the session event before approval. Persist the user message and
`awaiting_approval` state. Keep `Session.Provider` as the active Antares chat
provider; Cursor target state lives in `CursorSessionState`.

- [ ] **Step 8: Execute the approved immutable plan**

Before POST, CAS state to `create_in_flight` or `run_in_flight`. Persist returned
agent/run IDs before opening Cursor SSE. Publish status/reasoning/text/tool
progress through `liveRun`; persist Last-Event-ID and partial accumulators before
publishing each corresponding event.

- [ ] **Step 9: Finalize idempotently**

Use Cursor's final whole text as canonical reconciliation, not another delta.
Persist final reasoning and Git state, append one deterministic assistant
message, mark committed, then emit `done`. Remote tool events remain live
progress and are not persisted as ordinary Antares tool history.

- [ ] **Step 10: Recover from `handleChatAttach`**

When no in-memory run exists, load Cursor state. Reserve one recovery watcher,
resume from Last-Event-ID, or fetch/finalize a terminal run. Otherwise return
ordinary `done`.

- [ ] **Step 11: Add explicit cancel route**

`POST /api/chat/cursor/cancel` prepares an immutable cancel operation and waits
for explicit approval. It never shares semantics with local Stop.

Update session delete/edit handlers: active remote state returns 409 on delete;
editing or retrying a Cursor-authored turn atomically invalidates reuse.

- [ ] **Step 12: Run server and race suites**

```bash
gofmt -w internal/server/handlers_cursor.go internal/server/cursor_events.go internal/server/handlers_cursor_test.go internal/server/livechat.go internal/server/handlers_chat.go internal/server/routes.go internal/server/server.go
go test ./internal/server -run 'TestCursorChat|TestChatAttach' -count=1
go test -race ./internal/server ./internal/store ./internal/cursorrun ./internal/approval -count=1
```

- [ ] **Step 13: Commit**

```bash
git add internal/server
git commit -m "$(cat <<'EOF'
Run Cursor agents directly from chat
EOF
)"
```

---

### Task 13: Add unified model search and Cursor composer controls

**Files:**
- Create: `web/src/lib/cursorModels.ts`
- Create: `web/src/lib/cursorModels.test.mjs`
- Create: `web/src/lib/composerTargets.ts`
- Create: `web/src/lib/composerTargets.test.mjs`
- Create: `web/src/lib/cursorAttachments.ts`
- Create: `web/src/lib/cursorAttachments.test.mjs`
- Create: `web/src/lib/chatEvents.ts`
- Create: `web/src/lib/chatEvents.test.mjs`
- Create: `web/src/components/chat/CursorOptions.tsx`
- Modify: `web/src/components/chat/ModelPicker.tsx`
- Modify: `web/src/components/chat/ApprovalCard.tsx`
- Modify: `web/src/pages/ChatPage.tsx`
- Modify: `web/src/pages/ProvidersPage.tsx`
- Modify: `web/src/lib/api.ts`
- Modify: `web/src/lib/i18n.tsx`

**Interfaces:**
- Consumes: separate chat/Cursor catalogues and direct Cursor SSE routes.
- Produces: controlled `ComposerTarget` and exact `CursorOptionsValue`.

- [ ] **Step 1: Add pure exact-variant tests**

```javascript
test('default variant keeps hidden params', () => {
  const model = {
    id: 'claude-opus-5',
    name: 'Claude Opus 5',
    aliases: [],
    parameters: [{ id: 'effort', values: [{ value: 'max' }] }],
    variants: [{
      params: [
        { id: 'cyber', value: 'false' },
        { id: 'effort', value: 'max' },
      ],
      displayName: 'Claude Opus 5',
      isDefault: true,
    }],
  }
  expect(defaultCursorVariant(model).params).toEqual(model.variants[0].params)
})

test('filters never synthesize a missing combination', () => {
  expect(selectExactVariant(modelFixture, { context: '1m', reasoning: 'max', fast: 'true' })).toBeNull()
})

const modelFixture = {
  id: 'gpt-test',
  name: 'GPT Test',
  aliases: [],
  parameters: [
    { id: 'context', values: [{ value: '272k' }, { value: '1m' }] },
    { id: 'reasoning', values: [{ value: 'low' }, { value: 'max' }] },
    { id: 'fast', values: [{ value: 'false' }, { value: 'true' }] },
  ],
  variants: [{
    params: [
      { id: 'context', value: '272k' },
      { id: 'reasoning', value: 'max' },
      { id: 'fast', value: 'true' },
    ],
    displayName: 'GPT Test',
    isDefault: true,
  }],
}
```

- [ ] **Step 2: Add grouped-search, attachment, and approval tests**

Cover ID/name/alias/provider search, missing-key Connect action, five-image
limit, local-document rejection, approval parsing/deduplication, and
Cursor-local-Stop intentional detach. Include an `auto-smart` fixture and prove
`optimize_for` appears only when the connected catalogue returns it.

- [ ] **Step 3: Run and confirm RED**

```bash
cd web
bun test src/lib/cursorModels.test.mjs src/lib/composerTargets.test.mjs src/lib/cursorAttachments.test.mjs src/lib/chatEvents.test.mjs
```

- [ ] **Step 4: Implement pure target and variant helpers**

```ts
export type ChatTarget = {
  kind: 'chat'
  provider: string
  model: string
  name: string
  providerLabel: string
  reasoningCapability?: ReasoningCapability
}

export type CursorTarget = {
  kind: 'cursor'
  model: CursorModel
  variant: CursorVariant
}

export type ComposerTarget = ChatTarget | CursorTarget
```

Variant filters must return a concrete upstream variant or null.

- [ ] **Step 5: Make `ModelPicker` controlled and grouped**

```ts
export function ModelPicker(props: {
  value: ComposerTarget | null
  onChange(target: ComposerTarget): void
}): JSX.Element
```

Fetch both catalogues when opened. Selecting chat calls `/model/set`; selecting
Cursor never calls it. Mark sections and rows clearly.

- [ ] **Step 6: Implement `CursorOptions`**

Expose reasoning-like dimension, remaining variant dimensions, Agent/Plan,
repository/ref, warnings, and auto-PR. Controls filter variants and commit only
when one exact variant remains. Changing model/variant/repo/ref/auto-PR shows
“starts a new Cursor agent”; mode-only changes do not.

- [ ] **Step 7: Branch ChatPage send behavior**

For chat targets, preserve `/chat` and adaptive reasoning. For Cursor targets:

- reject local docs before clearing composer state;
- validate image preflight;
- send exact model params to `/chat/cursor`;
- hide RolePicker and generic ReasoningPicker;
- show CursorOptions;
- process approval events;
- load pending approvals for the current session;
- hydrate Cursor state from session detail.

- [ ] **Step 8: Separate local detach from remote cancel**

Cursor Stop closes the local stream and sets an intentional-detach flag so the
standing attach loop does not immediately reconnect. A separate Cancel action
posts to `/chat/cursor/cancel` and shows approval.

- [ ] **Step 9: Handle structured streaming errors**

Make `streamPost` parse non-2xx JSON like `api`, preserving status/body for 409,
429, auth, and stale-model UI messages.

- [ ] **Step 10: Improve Providers catalogue**

Add model search and compact parameter/variant summaries. Keep credential
management there; execution selection remains in composer.

- [ ] **Step 11: Add all locale strings**

Translate target groups, Cursor options, new-agent warning, repo/dirty/ahead
warnings, attachment errors, detach/cancel, stale model, and approval details in
all locale maps.

- [ ] **Step 12: Run frontend verification**

```bash
cd web
bun test
bun x tsc -b --noEmit
bun run build
```

- [ ] **Step 13: Commit**

```bash
git add web/src
git commit -m "$(cat <<'EOF'
Add direct Cursor controls to the composer
EOF
)"
```

---

### Task 14: Document, verify, deploy locally, and update the branch

**Files:**
- Modify: `docs/configuration.md`
- Modify: `docs/tools.md`
- Modify: `docs/verification.md`

**Interfaces:**
- Consumes: every prior task.
- Produces: verified build, local daemon running the same commit, and updated remote branch when authorized.

- [ ] **Step 1: Update user documentation**

Document:

- Auto and model-aware reasoning;
- provider-specific Off/minimal/effort semantics;
- direct Cursor selection and exact variants;
- repo/ref and local-only change warnings;
- explicit approval, local Stop, remote Cancel, and follow-up reuse identity;
- attachment limits;
- metadata-only live verification.

- [ ] **Step 2: Run focused suites**

```bash
go test ./internal/llm ./internal/agent ./internal/config ./internal/cursor ./internal/cursorrun ./internal/approval ./internal/tools ./internal/store ./internal/server ./cmd/antares -count=1
cd web && bun test && bun x tsc -b --noEmit
```

- [ ] **Step 3: Run race and full checks**

```bash
go test -race ./internal/llm ./internal/agent ./internal/cursor ./internal/cursorrun ./internal/approval ./internal/tools ./internal/store ./internal/server -count=1
make check
make smoke
```

Record any known pre-existing flake separately; do not mask a reproducible
failure with retries.

- [ ] **Step 4: Run a whole-branch defect review**

Review `origin/main...HEAD` for:

- any path that can activate Cursor as a chat provider;
- any paid request before approval;
- variant-param loss or synthesis;
- create/run retry;
- key leakage;
- duplicate stream text after recovery;
- stale reasoning crossing model boundaries;
- local Stop accidentally cancelling remote;
- persistence races and non-idempotent finalization.

Add a failing regression test before every required fix.

- [ ] **Step 5: Build and install the exact worktree**

```bash
make install-cli
"$HOME/.local/bin/antares" version
git rev-parse --short HEAD
```

Verify the reported binary commit matches the worktree commit.

- [ ] **Step 6: Restart and health-check the local daemon**

```bash
"$HOME/.local/bin/antares" stop
nohup "$HOME/.local/bin/antares" serve >"$HOME/.antares/logs/serve.log" 2>&1 &
curl --fail --silent --show-error "http://127.0.0.1:8787/api/health"
```

Do not run a paid Cursor agent automatically. In the dashboard, verify the live
catalogue loads, exact options appear, and the first Send stops at approval.

- [ ] **Step 7: Commit documentation and final regressions**

```bash
git add docs/configuration.md docs/tools.md docs/verification.md docs/superpowers/specs/2026-08-12-adaptive-reasoning-cursor-mode-design.md docs/superpowers/plans/2026-08-12-adaptive-reasoning-cursor-mode.md
git commit -m "$(cat <<'EOF'
Document adaptive reasoning and direct Cursor mode
EOF
)"
git status --short --branch
```

If prior task commits already include all documentation and no files changed,
skip this commit rather than creating an empty one.

- [ ] **Step 8: Push/update the existing PR only with explicit authorization**

```bash
git push -u origin HEAD
gh pr checks --watch
```

Return the PR URL, exact deployed commit, verification commands, and any
remaining known limitations.
