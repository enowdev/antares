package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/enowdev/antares/internal/llm"
	"github.com/enowdev/antares/internal/providers"
	"github.com/enowdev/antares/internal/store"
)

// contextWindowFor returns the active model's token budget for the usage event.
// Precedence: an explicit per-model model_meta override, then the provider
// catalogue's known window for the model (so e.g. glm-5.2 reads its real 1M
// window instead of the generic default), then the configured window, then a
// sane fallback. It mirrors the window maybeCompact governs, so the UI's
// "context full" bar agrees with compaction.
func (a *Agent) contextWindowFor(model string) int {
	if a.cfg != nil {
		for _, p := range a.cfg.Providers {
			if m, ok := p.ModelMeta[model]; ok && m.ContextWindow > 0 {
				return m.ContextWindow
			}
		}
	}
	if w := providers.ContextWindow(model); w > 0 {
		return w
	}
	if a.cfg != nil && a.cfg.Model.ContextWindow > 0 {
		return a.cfg.Model.ContextWindow
	}
	return 128000
}

// maybeCompact summarises older turns once the conversation approaches the
// model's context window, keeping recent turns verbatim. On success the
// summary is persisted on the session so the next turn does not re-run a
// multi-minute summarise over thousands of raw messages.
func (a *Agent) maybeCompact(ctx context.Context, history []llm.Message, system, model string, tools []llm.Tool, emit Emit, sess *store.Session) []llm.Message {
	cfg := a.cfg.Compression
	if !cfg.Enabled || len(history) < 8 {
		return history
	}
	// Use the same window the usage gauge reports (per-model meta, catalogue,
	// then config) so "context full" and compaction agree.
	window := a.contextWindowFor(model)
	if window <= 0 {
		window = 128000
	}
	threshold := cfg.Threshold
	if threshold <= 0 || threshold >= 1 {
		threshold = 0.8
	}

	used := estimateRequestTokens(history, system, tools)
	if float64(used) < float64(window)*threshold {
		// Even below the threshold, prune oversized tool results that are no
		// longer near the tail — they dominate context growth.
		return a.prunedToolResults(history)
	}

	protectFirst := maxInt(cfg.ProtectFirstN, 1)
	protectLast := maxInt(cfg.ProtectLastN, 4)
	if len(history) <= protectFirst+protectLast+2 {
		return history
	}

	head := history[:protectFirst]
	middle := history[protectFirst : len(history)-protectLast]
	tail := history[len(history)-protectLast:]

	// Never split an assistant tool-call turn from its tool results, or the
	// provider will reject the request.
	middle, tail = rebalanceToolBoundary(middle, tail)
	if len(middle) == 0 {
		return history
	}

	// Always surface compaction to the UI: on long sessions this LLM call can
	// take tens of seconds and without a notice the dashboard only shows
	// "Working… · Ns", which looks like a hang.
	if emit != nil {
		_ = emit(Event{Type: EventNotice, Message: fmt.Sprintf(
			"compacting %d older messages to free context (~%d tokens)", len(middle), used)})
	}

	summary, err := a.summarise(ctx, middle)
	if err != nil {
		slog.Warn("context compaction failed; dropping oldest turns instead", "error", err)
		// Fall back to truncation so the turn can still proceed.
		return append(append([]llm.Message{}, head...), tail...)
	}

	compacted := make([]llm.Message, 0, len(head)+len(tail)+1)
	compacted = append(compacted, head...)
	compacted = append(compacted, llm.Message{
		Role: llm.RoleUser,
		Content: "[Compacted summary of the earlier conversation]\n\n" + summary +
			"\n\n[Continue from here. This summary replaces the older messages.]",
	})
	compacted = append(compacted, tail...)

	// Persist so the next turn loads head+summary+tail instead of re-summarising.
	if sess != nil && !isQuietSession(sess) {
		a.persistContextCompact(ctx, sess, summary, protectFirst, protectLast)
	}

	slog.Info("context compacted", "before", len(history), "after", len(compacted), "tokens_before", used)
	return compacted
}

// isQuietSession is true for ephemeral sub-agent sessions we never persist.
func isQuietSession(sess *store.Session) bool {
	return sess == nil || sess.ID == ""
}

// persistContextCompact records the summary and the highest seq it covers so
// loadHistory can rebuild the compacted view without another LLM call.
func (a *Agent) persistContextCompact(ctx context.Context, sess *store.Session, summary string, protectFirst, protectLast int) {
	if a.db == nil || sess == nil {
		return
	}
	rows, err := a.db.ListMessages(ctx, sess.ID, 0, 0)
	if err != nil {
		slog.Warn("persist compact: list messages failed", "error", err)
		return
	}
	visible := make([]store.Message, 0, len(rows))
	for _, r := range rows {
		if r.Hidden {
			continue
		}
		visible = append(visible, r)
	}
	if len(visible) <= protectFirst+protectLast {
		return
	}
	// Middle ends at the last message before the protected tail.
	middleEnd := visible[len(visible)-protectLast-1]
	throughSeq := middleEnd.Seq

	if sess.Meta == nil {
		sess.Meta = store.Meta{}
	}
	// Reload session to avoid clobbering concurrent meta updates with a stale
	// struct, then merge our key.
	fresh, err := a.db.GetSession(ctx, sess.ID)
	if err != nil {
		slog.Warn("persist compact: get session failed", "error", err)
		return
	}
	if fresh.Meta == nil {
		fresh.Meta = store.Meta{}
	}
	fresh.Meta[contextCompactMetaKey] = map[string]any{
		"summary":     summary,
		"through_seq": throughSeq,
		"keep_first":  protectFirst,
	}
	if err := a.db.UpdateSession(ctx, fresh); err != nil {
		slog.Warn("persist compact: update session failed", "error", err)
		return
	}
	// Keep the in-memory session in sync for the rest of this turn.
	sess.Meta = fresh.Meta
	slog.Info("context compact persisted", "session", sess.ID, "through_seq", throughSeq, "keep_first", protectFirst)
}

// estimateRequestTokens includes tool schemas as well as system/history. Large
// agent tool packs are sent on every call and can consume a material part of the
// context window; omitting them delays compaction until the provider rejects the
// request even though the history-only estimate still looks safe.
func estimateRequestTokens(history []llm.Message, system string, tools []llm.Tool) int {
	total := estimateTokens(history, system)
	for _, tool := range tools {
		total += len(tool.Name)/4 + len(tool.Description)/4 + 8
		if schema, err := json.Marshal(tool.Parameters); err == nil {
			total += len(schema) / 4
		}
	}
	return total
}

// rebalanceToolBoundary moves messages between middle and tail so that no tool
// result is separated from the assistant turn that requested it.
func rebalanceToolBoundary(middle, tail []llm.Message) ([]llm.Message, []llm.Message) {
	// If the tail starts with tool results, their assistant turn sits at the end
	// of middle: move it across.
	for len(tail) > 0 && tail[0].Role == llm.RoleTool && len(middle) > 0 {
		last := middle[len(middle)-1]
		middle = middle[:len(middle)-1]
		tail = append([]llm.Message{last}, tail...)
		if last.Role == llm.RoleAssistant {
			break
		}
	}
	// If middle now ends with an assistant turn holding unresolved tool calls,
	// drop it too — its results already moved to the tail.
	if n := len(middle); n > 0 && len(middle[n-1].ToolCalls) > 0 {
		middle = middle[:n-1]
	}
	return middle, tail
}

// summarise asks the auxiliary (or main) model to condense a message span.
func (a *Agent) summarise(ctx context.Context, msgs []llm.Message) (string, error) {
	client, model, _, err := a.newAuxClient("")
	if err != nil {
		return "", err
	}

	var transcript strings.Builder
	for _, m := range msgs {
		switch m.Role {
		case llm.RoleUser:
			transcript.WriteString("USER: " + truncate(m.Content, 4000) + "\n\n")
		case llm.RoleAssistant:
			if m.Content != "" {
				transcript.WriteString("ASSISTANT: " + truncate(m.Content, 4000) + "\n")
			}
			for _, tc := range m.ToolCalls {
				transcript.WriteString("ASSISTANT called " + tc.Name + "(" + truncate(tc.Arguments, 400) + ")\n")
			}
			transcript.WriteString("\n")
		case llm.RoleTool:
			transcript.WriteString("TOOL " + m.Name + " → " + truncate(m.Content, 1500) + "\n\n")
		}
	}

	prompt := `Summarise the conversation excerpt below so another instance of the assistant can continue seamlessly.

Preserve, in this order:
1. What the user is trying to achieve, and any constraints or preferences they stated.
2. Decisions made and their rationale.
3. Concrete facts discovered: file paths, commands that worked, error messages, values, URLs.
4. What is done and what is still outstanding.

Drop pleasantries and redundant tool output. Be dense and specific — names and paths, not "a file was read".
Write in the same language the user used.

--- EXCERPT ---
` + transcript.String()

	resp, err := client.Chat(ctx, llm.Request{
		Model:       model,
		Messages:    []llm.Message{{Role: llm.RoleUser, Content: prompt}},
		MaxTokens:   2048,
		Temperature: 0.2,
	})
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(resp.Content) == "" {
		return "", fmt.Errorf("summariser returned empty output")
	}
	return resp.Content, nil
}

// prunedToolResults shrinks large tool outputs that are far from the tail.
func (a *Agent) prunedToolResults(history []llm.Message) []llm.Message {
	cfg := a.cfg.Compression
	minChars := cfg.ProactivePruneMinChars
	if minChars <= 0 {
		return history
	}
	protect := maxInt(cfg.ProtectLastN, 4)
	if len(history) <= protect+2 {
		return history
	}

	out := make([]llm.Message, len(history))
	copy(out, history)
	for i := 0; i < len(out)-protect; i++ {
		m := out[i]
		if m.Role != llm.RoleTool || len(m.Content) <= minChars {
			continue
		}
		out[i].Content = truncate(m.Content, minChars/2) +
			fmt.Sprintf("\n\n[tool result pruned: %d characters removed to free context]", len(m.Content)-minChars/2)
	}
	return out
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
