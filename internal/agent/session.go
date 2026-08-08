package agent

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/enowdev/antares/internal/config"
	"github.com/enowdev/antares/internal/llm"
	"github.com/enowdev/antares/internal/store"
)

// resolveSession loads the requested session or creates a fresh one.
func (a *Agent) resolveSession(ctx context.Context, req *Request) (*store.Session, error) {
	if req.SessionID != "" {
		sess, err := a.db.GetSession(ctx, req.SessionID)
		if err == nil {
			return sess, nil
		}
		if !errors.Is(err, store.ErrNotFound) {
			return nil, err
		}
	}

	platform := req.Platform
	if platform == "" {
		platform = "web"
	}
	sess := &store.Session{
		ID:        newID("ses"),
		Title:     defaultTitle(req.Message),
		Platform:  platform,
		ChannelID: req.ChannelID,
		UserID:    req.UserID,
		Model:     firstNonEmpty(req.Model, a.cfg.Model.Default),
		Provider:  a.cfg.Model.Provider,
		Workspace: firstNonEmpty(req.Workspace, a.cfg.Agent.Workspace),
		Meta:      store.Meta{},
	}
	// A project session binds to a chosen folder: the workspace becomes the
	// project (so the terminal, reads, and relative paths all centre on it), and
	// the project dir is recorded in Meta so later turns and the UI know it.
	if pd := strings.TrimSpace(config.Expand(req.ProjectDir)); pd != "" {
		sess.Workspace = pd
		sess.Meta["project_dir"] = pd
		if req.IndexRAG {
			sess.Meta["rag_indexed"] = true
		}
	}
	if req.Quiet {
		// Sub-agent runs are ephemeral: keep the session in memory only.
		req.SessionID = sess.ID
		return sess, nil
	}
	if err := a.db.CreateSession(ctx, sess); err != nil {
		return nil, err
	}
	req.SessionID = sess.ID
	return sess, nil
}

func defaultTitle(msg string) string {
	msg = strings.TrimSpace(strings.ReplaceAll(msg, "\n", " "))
	if msg == "" {
		return "Percakapan baru"
	}
	runes := []rune(msg)
	if len(runes) > 60 {
		return string(runes[:60]) + "…"
	}
	return msg
}

// contextCompactMetaKey is stored on session.Meta after a successful
// compaction. Subsequent turns rebuild history as head + summary + tail so
// we do not re-summarise thousands of messages on every turn.
const contextCompactMetaKey = "context_compact"

// loadHistory rebuilds the model-facing message list from storage.
// When a prior compaction was persisted on the session, messages with
// seq ≤ through_seq (except the first keep_first) are replaced by the stored
// summary — matching what maybeCompact produced in memory.
func (a *Agent) loadHistory(ctx context.Context, sess *store.Session, req Request) ([]llm.Message, error) {
	if req.Quiet {
		return nil, nil
	}
	rows, err := a.db.ListMessages(ctx, sess.ID, 0, 0)
	if err != nil {
		return nil, err
	}

	// Optional persisted compaction (written by maybeCompact).
	var (
		summary    string
		throughSeq int64
		keepFirst  int
		hasCompact bool
	)
	if sess != nil && sess.Meta != nil {
		if raw, ok := sess.Meta[contextCompactMetaKey]; ok {
			if m, ok := raw.(map[string]any); ok {
				summary, _ = m["summary"].(string)
				throughSeq = metaInt64(m["through_seq"])
				keepFirst = int(metaInt64(m["keep_first"]))
				hasCompact = summary != "" && throughSeq > 0
				if keepFirst <= 0 {
					keepFirst = 1
				}
			}
		}
	}

	visible := make([]store.Message, 0, len(rows))
	for _, r := range rows {
		if r.Hidden || r.Compacted {
			continue
		}
		visible = append(visible, r)
	}

	toLLM := func(r store.Message) (llm.Message, bool) {
		m := llm.Message{Content: r.Content, Reasoning: r.Reasoning}
		switch r.Role {
		case store.RoleUser:
			m.Role = llm.RoleUser
			if r.Attachments != "" {
				var parts []llm.Part
				if err := json.Unmarshal([]byte(r.Attachments), &parts); err == nil {
					m.Parts = parts
				}
			}
		case store.RoleAssistant:
			m.Role = llm.RoleAssistant
			if r.ToolCalls != "" {
				var calls []llm.ToolCall
				if err := json.Unmarshal([]byte(r.ToolCalls), &calls); err == nil {
					m.ToolCalls = calls
				}
			}
		case store.RoleTool:
			m.Role = llm.RoleTool
			m.ToolCallID = r.ToolCallID
			m.Name = r.ToolName
		default:
			return llm.Message{}, false
		}
		return m, true
	}

	if !hasCompact {
		out := make([]llm.Message, 0, len(visible))
		for _, r := range visible {
			if m, ok := toLLM(r); ok {
				out = append(out, m)
			}
		}
		return out, nil
	}

	// head: first keepFirst visible messages (by order, regardless of seq holes)
	headN := keepFirst
	if headN > len(visible) {
		headN = len(visible)
	}
	out := make([]llm.Message, 0, headN+1+len(visible))
	for i := 0; i < headN; i++ {
		if m, ok := toLLM(visible[i]); ok {
			out = append(out, m)
		}
	}
	out = append(out, llm.Message{
		Role: llm.RoleUser,
		Content: "[Compacted summary of the earlier conversation]\n\n" + summary +
			"\n\n[Continue from here. This summary replaces the older messages.]",
	})
	// tail: everything after the last seq covered by the summary
	for _, r := range visible {
		if r.Seq <= throughSeq {
			continue
		}
		// Skip rows already included in head (keep_first may overlap low seqs)
		if headN > 0 && r.Seq <= visible[headN-1].Seq {
			continue
		}
		if m, ok := toLLM(r); ok {
			out = append(out, m)
		}
	}
	return out, nil
}

func metaInt64(v any) int64 {
	switch n := v.(type) {
	case float64:
		return int64(n)
	case int64:
		return n
	case int:
		return int64(n)
	case json.Number:
		i, _ := n.Int64()
		return i
	default:
		return 0
	}
}

// clearContextCompact drops a persisted summary (e.g. after edit-message
// rewrites history so the summary would be stale).
func (a *Agent) clearContextCompact(ctx context.Context, sessionID string) {
	if a.db == nil || sessionID == "" {
		return
	}
	sess, err := a.db.GetSession(ctx, sessionID)
	if err != nil || sess.Meta == nil {
		return
	}
	if _, ok := sess.Meta[contextCompactMetaKey]; !ok {
		return
	}
	delete(sess.Meta, contextCompactMetaKey)
	if err := a.db.UpdateSession(ctx, sess); err != nil {
		slog.Warn("clear context compact failed", "session", sessionID, "error", err)
	}
}

// persistAssistant stores an assistant turn including any tool calls.
func (a *Agent) persistAssistant(ctx context.Context, sessionID, model string, resp *llm.Response) {
	toolCalls := ""
	if len(resp.ToolCalls) > 0 {
		if b, err := json.Marshal(resp.ToolCalls); err == nil {
			toolCalls = string(b)
		}
	}
	if resp.Content == "" && toolCalls == "" && resp.Reasoning == "" {
		return
	}
	if err := a.db.AppendMessage(ctx, &store.Message{
		ID: newID("msg"), SessionID: sessionID, Role: store.RoleAssistant,
		Content: resp.Content, Reasoning: resp.Reasoning, ToolCalls: toolCalls,
		Model: model, TokensIn: resp.Usage.InputTokens, TokensOut: resp.Usage.OutputTokens,
	}); err != nil {
		slog.Warn("persist assistant message failed", "error", err)
	}
}

func (a *Agent) recordUsage(ctx context.Context, sessionID, provider, model string, u llm.Usage) {
	if err := a.db.RecordUsage(ctx, &store.Usage{
		ID: newID("use"), SessionID: sessionID, Provider: provider, Model: model,
		TokensIn: u.InputTokens, TokensOut: u.OutputTokens, CacheRead: u.CacheReadTokens,
		Cost: estimateCost(model, u), Source: "chat",
	}); err != nil {
		slog.Debug("record usage failed", "error", err)
	}
}

// maybeTitle replaces the placeholder title once a session has real content.
func (a *Agent) maybeTitle(ctx context.Context, sess *store.Session, userMsg, reply string) {
	if sess.Title != "" && sess.Title != "Percakapan baru" && !strings.HasPrefix(sess.Title, userMsg[:minInt(len(userMsg), 20)]) {
		return
	}
	title := ""
	if a.cfg.Agent.SmartTitles {
		title = a.llmTitle(ctx, userMsg, reply)
	}
	if title == "" {
		title = summariseTitle(userMsg, reply)
	}
	if title == "" || title == sess.Title {
		return
	}
	sess.Title = title
	// Save ONLY the title. Using UpdateSession here would write the whole row
	// from this in-memory struct, whose Meta was loaded at turn start — clobbering
	// any meta a tool wrote mid-turn (e.g. project_info). See session-counter bug.
	if err := a.db.SetSessionTitle(ctx, sess.ID, title); err != nil {
		slog.Debug("update session title failed", "error", err)
	}
}

// llmTitle asks the auxiliary model for a short, human title. It returns "" on
// any error so the caller falls back to the heuristic — a title is never worth
// failing a turn over.
func (a *Agent) llmTitle(ctx context.Context, userMsg, reply string) string {
	client, model, _, err := a.newAuxClient("")
	if err != nil {
		return ""
	}
	msg := strings.TrimSpace(userMsg)
	if r := strings.TrimSpace(reply); r != "" {
		msg += "\n\nAssistant replied: " + trimForModel(r, 500)
	}
	if msg == "" {
		return ""
	}
	tctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	resp, err := client.Chat(tctx, llm.Request{
		Model:     model,
		System:    "Write a 3–6 word title for this conversation. Plain text, no quotes, no trailing punctuation, in the user's language.",
		Messages:  []llm.Message{{Role: llm.RoleUser, Content: msg}},
		MaxTokens: 24,
	})
	if err != nil || resp == nil {
		return ""
	}
	title := strings.TrimSpace(resp.Content)
	title = strings.Trim(title, "\"'`")
	title = strings.ReplaceAll(title, "\n", " ")
	if r := []rune(title); len(r) > 70 {
		title = strings.TrimSpace(string(r[:70]))
	}
	// Guard against a model that ignored the instruction and wrote a paragraph.
	if strings.Count(title, " ") > 10 || title == "" {
		return ""
	}
	return title
}

// summariseTitle derives a short label without spending a model call.
func summariseTitle(userMsg, reply string) string {
	base := strings.TrimSpace(strings.ReplaceAll(userMsg, "\n", " "))
	if base == "" {
		base = strings.TrimSpace(strings.ReplaceAll(reply, "\n", " "))
	}
	if base == "" {
		return ""
	}
	// Cut at the first sentence boundary when it lands in a reasonable range.
	for _, sep := range []string{". ", "? ", "! "} {
		if i := strings.Index(base, sep); i > 15 && i < 70 {
			base = base[:i]
			break
		}
	}
	runes := []rune(base)
	if len(runes) > 60 {
		return strings.TrimSpace(string(runes[:60])) + "…"
	}
	return base
}

// estimateCost is a coarse fallback used when the provider omits pricing.
func estimateCost(model string, u llm.Usage) float64 {
	if u.Cost > 0 {
		return u.Cost
	}
	in, out := 0.0, 0.0
	l := strings.ToLower(model)
	switch {
	case strings.Contains(l, "opus"):
		in, out = 15, 75
	case strings.Contains(l, "sonnet"):
		in, out = 3, 15
	case strings.Contains(l, "haiku"):
		in, out = 0.8, 4
	case strings.Contains(l, "gpt-5"), strings.Contains(l, "gpt-4o"):
		in, out = 2.5, 10
	case strings.Contains(l, "gemini") && strings.Contains(l, "pro"):
		in, out = 1.25, 5
	case strings.Contains(l, "gemini"):
		in, out = 0.3, 2.5
	default:
		return 0
	}
	return (float64(u.InputTokens)*in + float64(u.OutputTokens)*out) / 1_000_000
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// touchSession refreshes the updated_at stamp without other changes. It writes
// only that column so it cannot clobber meta/title a tool changed mid-turn.
func (a *Agent) touchSession(ctx context.Context, sess *store.Session) {
	sess.UpdatedAt = time.Now()
	_ = a.db.TouchSession(ctx, sess.ID)
}
