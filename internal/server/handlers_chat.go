package server

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	"github.com/enowdev/antares/internal/agent"
	"github.com/enowdev/antares/internal/llm"
	"github.com/enowdev/antares/internal/store"
	"github.com/enowdev/antares/internal/version"
)

var errNotFound = errors.New("not found")

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	err := s.db.Ping(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":      err == nil,
		"version": version.Version,
	})
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	cfg := s.config()

	// Provider readiness is a config check, not a live call: the status pill
	// polls every ten seconds and must not bill the user for pings.
	_, provider := cfg.ResolveProvider(cfg.Model.Provider)
	ready := cfg.Model.Default != "" && (provider.APIKey != "" || isLocalEndpoint(provider.BaseURL))

	writeJSON(w, http.StatusOK, map[string]any{
		"ok":              true,
		"version":         version.Version,
		"model":           cfg.Model.Default,
		"provider":        cfg.Model.Provider,
		"provider_ready":  ready,
		"database":        s.db.Driver(),
		"uptime_seconds":  int(time.Since(s.started).Seconds()),
		"active_sessions": s.agent.ActiveCount(),
		"needs_setup":     NeedsSetup(cfg),
	})
}

func isLocalEndpoint(url string) bool {
	l := strings.ToLower(url)
	return strings.Contains(l, "localhost") || strings.Contains(l, "127.0.0.1") || strings.Contains(l, "0.0.0.0")
}

func (s *Server) handleSystemStats(w http.ResponseWriter, r *http.Request) {
	cfg := s.config()
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)

	st, err := s.db.Stats(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	ragProvider := ""
	if p := s.agent.RAG(); p != nil {
		ragProvider = p.Name()
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"version":        version.Version,
		"go_version":     runtime.Version(),
		"os":             runtime.GOOS,
		"arch":           runtime.GOARCH,
		"uptime_seconds": int(time.Since(s.started).Seconds()),
		"goroutines":     runtime.NumGoroutine(),
		"memory_alloc":   mem.Alloc,
		"memory_sys":     mem.Sys,
		"cpu_count":      runtime.NumCPU(),
		"workspace":      cfg.Agent.Workspace,
		"config_path":    configPath(),
		"database":       map[string]any{"driver": s.db.Driver(), "size_bytes": st.SizeBytes},
		"store":          st,
		"active_shells":  s.agent.Shell().Active(),
		"model":          cfg.Model.Default,
		"provider":       cfg.Model.Provider,
		"rag_provider":   ragProvider,
	})
}

type chatRequest struct {
	SessionID string   `json:"session_id"`
	Message   string   `json:"message"`
	Images    []string `json:"images"` // data URLs or bare base64
	Role      string   `json:"role"`   // run this turn as a specialist
	Model     string   `json:"model"`
	Toolset   string   `json:"toolset"`
	// ReasoningEffort overrides the configured effort for this turn (none|low|
	// medium|high). Empty falls back to agent, then model config.
	ReasoningEffort string `json:"reasoning_effort"`
	UserID          string `json:"user_id"`
	// ProjectDir turns a NEW session into a project session bound to this folder.
	// Only read on the session's first turn; ignored once the session exists.
	ProjectDir string `json:"project_dir"`
	// IndexRAG opts a new project session into RAG indexing + retrieval.
	IndexRAG bool `json:"index_rag"`
}

// handleChat streams a full agent turn as server-sent events.
func (s *Server) handleChat(w http.ResponseWriter, r *http.Request) {
	var req chatRequest
	if err := decodeBody(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if strings.TrimSpace(req.Message) == "" && len(req.Images) == 0 {
		writeError(w, http.StatusBadRequest, errors.New("message is required"))
		return
	}
	if err := s.validateExplicitReasoning(
		r.Context(), s.config(), req.Model, req.ReasoningEffort,
	); err != nil {
		writeReasoningValidationError(w, err)
		return
	}
	lr := newLiveRun()
	reservedExisting := false
	if s.db != nil && strings.TrimSpace(req.SessionID) != "" {
		if err := s.reserveOrdinaryChat(r.Context(), req.SessionID, lr); err != nil {
			if errors.Is(err, errCursorSessionBusy) {
				writeError(w, http.StatusConflict, errors.New(
					"a turn is already active for this session",
				))
				return
			}
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		reservedExisting = true
	}

	// Persist the picked role against the session so a reload reflects it. The
	// session id is only known once the run assigns one, so a brand-new
	// conversation stores its role after the session event; an existing one
	// stores it now.
	if s.db != nil && req.SessionID != "" {
		if strings.TrimSpace(req.Role) == "" {
			_ = s.db.DeleteKV(r.Context(), "role:"+req.SessionID)
		} else {
			_ = s.db.SetKV(r.Context(), "role:"+req.SessionID, req.Role)
		}
	}

	sse, err := newSSE(w)
	if err != nil {
		if reservedExisting {
			s.hub.remove(req.SessionID, lr)
		}
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	// Keep intermediaries from closing an idle connection while the model thinks.
	ctx := r.Context()
	stopPing := make(chan struct{})
	defer close(stopPing)
	go func() {
		t := time.NewTicker(20 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-stopPing:
				return
			case <-ctx.Done():
				return
			case <-t.C:
				sse.comment("keepalive")
			}
		}
	}()

	agentReq := agent.Request{
		SessionID:       req.SessionID,
		Message:         req.Message,
		Images:          decodeImages(req.Images),
		Role:            req.Role,
		Platform:        "web",
		UserID:          req.UserID,
		Model:           req.Model,
		Toolset:         req.Toolset,
		ReasoningEffort: req.ReasoningEffort,
		ProjectDir:      req.ProjectDir,
		IndexRAG:        req.IndexRAG,
	}

	// The turn is driven on a background context so it survives this request:
	// navigating away no longer cancels the model, and a returning client can
	// reattach through /chat/attach. Events flow into a liveRun; this request is
	// just the first follower. Interrupt still stops it (agent.Interrupt keys off
	// the session id), and the run persists its own messages as it goes.
	if req.SessionID != "" && !reservedExisting {
		s.hub.put(req.SessionID, lr)
	}
	sessionKey := req.SessionID

	emit := func(e agent.Event) error {
		if e.Type == agent.EventSession && e.ID != "" && sessionKey == "" {
			// Brand-new conversation: register the run under its real id so a
			// reattach can find it, and remember the picked role.
			sessionKey = e.ID
			s.hub.put(e.ID, lr)
			if strings.TrimSpace(req.Role) != "" && s.db != nil {
				_ = s.db.SetKV(context.Background(), "role:"+e.ID, req.Role)
			}
		}
		lr.publish(e)
		return nil // a follower leaving must never fail the run
	}

	go func() {
		defer func() {
			// A panic inside a tool or the agent loop must not take down the
			// whole server: recover, surface it to the UI as an error event,
			// and let the deferred finish/remove below run so the liveRun is
			// not leaked in the hub.
			if r := recover(); r != nil {
				slog.Error("chat turn panicked", "session", sessionKey, "panic", r,
					"stack", string(debug.Stack()))
				lr.publish(agent.Event{Type: agent.EventError, Err: fmt.Sprintf("internal error: %v", r)})
				lr.publish(agent.Event{Type: agent.EventDone})
			}
			lr.finish()
			s.hub.remove(sessionKey, lr)
			// A sub-agent that finished while this turn streamed queued its
			// result; act on it now as the next turn (context preserved).
			s.drainAfterTurn(sessionKey)
		}()
		if _, err := s.agent.Run(context.Background(), agentReq, emit); err != nil {
			slog.Debug("chat turn failed", "error", err)
		}
	}()

	// Follow the run until it finishes or this client disconnects.
	_ = lr.follow(ctx, 0, func(e agent.Event, _ int) error { return sse.send(e) })
}

func (s *Server) reserveOrdinaryChat(
	ctx context.Context,
	sessionID string,
	live *liveRun,
) error {
	unlock := s.cursorLifecycles.Lock(sessionID)
	defer unlock()
	active, err := s.cursorSessionHasUnfinishedState(ctx, sessionID)
	if err != nil {
		return err
	}
	if active {
		return errCursorSessionBusy
	}
	if err := s.invalidateCursorTarget(ctx, sessionID, false); err != nil {
		return err
	}
	// Ordinary turns historically supersede the attach log for an older
	// ordinary turn. The lifecycle lock still prevents crossing an unfinished
	// Cursor reservation while preserving that replacement behavior.
	s.hub.put(sessionID, live)
	return nil
}

// handleChatAttach reconnects a client to a turn already in flight for a session
// (e.g. after navigating away and back), replaying from the given cursor. If no
// run is live, it reports done at once so the client falls back to the persisted
// history it already hydrated.
func (s *Server) handleChatAttach(w http.ResponseWriter, r *http.Request) {
	session := r.URL.Query().Get("session_id")
	cursor, _ := strconv.Atoi(r.URL.Query().Get("cursor"))

	unlock := s.cursorLifecycles.Lock(session)
	lr := s.hub.get(session)
	needsCursorRecovery := false
	if lr == nil && s.db != nil && s.cursorRunner != nil {
		state, err := s.db.GetCursorSessionState(r.Context(), session)
		switch {
		case err == nil:
			needsCursorRecovery = cursorStateNeedsRecovery(state)
		case errors.Is(err, store.ErrNotFound):
		default:
			unlock()
			writeError(w, http.StatusInternalServerError, s.cursorSafeError(err))
			return
		}
	}
	unlock()

	if (lr != nil && lr.isCursor()) || needsCursorRecovery {
		if s.requireDashboardPassword(w, r) {
			return
		}
	}
	if lr == nil && needsCursorRecovery {
		lr = s.cursorRecoveryRun(session)
	}
	if cursorAttachShouldReset(lr) {
		// Every recovery log starts with reset plus durable replay. The caller's
		// cursor may belong to the lost process, so it is never valid here.
		cursor = 0
	}

	sse, err := newSSE(w)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if lr == nil {
		_ = sse.send(agent.Event{Type: agent.EventDone})
		return
	}

	ctx := r.Context()
	stopPing := make(chan struct{})
	defer close(stopPing)
	go func() {
		t := time.NewTicker(20 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-stopPing:
				return
			case <-ctx.Done():
				return
			case <-t.C:
				sse.comment("keepalive")
			}
		}
	}()

	_ = lr.follow(ctx, cursor, func(e agent.Event, nextCursor int) error {
		return sse.send(struct {
			agent.Event
			Cursor int `json:"cursor"`
		}{Event: e, Cursor: nextCursor})
	})
}

func cursorAttachShouldReset(live *liveRun) bool {
	return live != nil && live.runKind() == liveRunCursorRecovery
}

// decodeImages accepts data URLs and bare base64 payloads.
func decodeImages(images []string) []llm.Part {
	out := make([]llm.Part, 0, len(images))
	for _, img := range images {
		img = strings.TrimSpace(img)
		if img == "" {
			continue
		}
		if strings.HasPrefix(img, "http://") || strings.HasPrefix(img, "https://") {
			out = append(out, llm.Part{Type: "image", URL: img})
			continue
		}
		mime := "image/png"
		data := img
		if strings.HasPrefix(img, "data:") {
			meta, payload, ok := strings.Cut(strings.TrimPrefix(img, "data:"), ",")
			if !ok {
				continue
			}
			if m, _, found := strings.Cut(meta, ";"); found && m != "" {
				mime = m
			}
			data = payload
		}
		if _, err := base64.StdEncoding.DecodeString(data); err != nil {
			continue
		}
		out = append(out, llm.Part{Type: "image", MimeType: mime, Data: data})
	}
	return out
}

func (s *Server) handleInterrupt(w http.ResponseWriter, r *http.Request) {
	var body struct {
		SessionID string `json:"session_id"`
	}
	if err := decodeBody(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if live := s.hub.get(body.SessionID); live != nil && live.stopWatching() {
		writeJSON(w, http.StatusOK, map[string]bool{"interrupted": true})
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"interrupted": s.agent.Interrupt(body.SessionID)})
}

// ---- sessions ---------------------------------------------------------------

func (s *Server) handleListSessions(w http.ResponseWriter, r *http.Request) {
	f := store.SessionFilter{
		Platform: r.URL.Query().Get("platform"),
		UserID:   r.URL.Query().Get("user_id"),
		Query:    r.URL.Query().Get("q"),
		Limit:    queryInt(r, "limit", 50),
		Offset:   queryInt(r, "offset", 0),
		OrderBy:  r.URL.Query().Get("order_by"),
		// Sub-agent sessions are internal machinery, not conversations the user
		// started — keep them out of the sessions list.
		ExcludePlatforms: []string{"subagent", "background"},
	}
	sessions, total, err := s.db.ListSessions(r.Context(), f)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"sessions": sessions, "total": total})
}

func (s *Server) handleGetSession(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	sess, err := s.db.GetSession(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, errNotFound)
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	messages, err := s.db.ListMessages(r.Context(), id, 0, 0)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"session": sess, "messages": messages})
}

// handleEditPreview lists the files the agent changed at/after a given user
// message, so the "edit message" UI can ask whether to revert them. The message
// id doubles as the checkpoint marker for its turn.
func (s *Server) handleEditPreview(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("id")
	msgID := strings.TrimSpace(r.URL.Query().Get("message_id"))
	if msgID == "" {
		writeError(w, http.StatusBadRequest, errors.New("message_id is required"))
		return
	}
	changes, err := s.agent.PreviewChangesSince(sessionID, msgID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"changes": changes})
}

// handleEditMessage applies an edit: it optionally reverts the files changed
// since the edited message, then drops that message and everything after it.
// The dashboard follows up by sending the edited text as a new message, so the
// conversation resumes from that point.
func (s *Server) handleEditMessage(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("id")
	var body struct {
		MessageID string `json:"message_id"`
		Revert    bool   `json:"revert"`
	}
	if err := decodeBody(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if strings.TrimSpace(body.MessageID) == "" {
		writeError(w, http.StatusBadRequest, errors.New("message_id is required"))
		return
	}
	unlock := s.cursorLifecycles.Lock(sessionID)
	defer unlock()
	active, err := s.cursorSessionHasUnfinishedState(r.Context(), sessionID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if active {
		writeError(w, http.StatusConflict, errors.New(
			"a remote Cursor operation is active for this session",
		))
		return
	}
	// Invalidate before rollback or transcript deletion. A later direct send
	// must create a fresh remote agent because edited history no longer matches
	// the remote conversation.
	if err := s.invalidateCursorTarget(r.Context(), sessionID, true); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	var reverted, skipped []string
	if body.Revert {
		// Skip files edited outside the session, per the product decision.
		res, err := s.agent.RollbackSince(sessionID, body.MessageID, true)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		reverted = append(append([]string{}, res.Restored...), res.Deleted...)
		for p := range res.Failed {
			skipped = append(skipped, p)
		}
	}

	if err := s.db.DeleteMessagesFrom(r.Context(), sessionID, body.MessageID); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	// Drop any persisted context summary — history was rewritten and the
	// old through_seq would hide live messages or re-apply a stale summary.
	if sess, err := s.db.GetSession(r.Context(), sessionID); err == nil && sess.Meta != nil {
		if _, ok := sess.Meta["context_compact"]; ok {
			delete(sess.Meta, "context_compact")
			_ = s.db.UpdateSession(r.Context(), sess)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":       true,
		"reverted": reverted,
		"skipped":  skipped,
	})
}

// handleBackgroundActivity reports the background-tool usage for a session (RAG
// index/retrieve, etc.) — the actions that never appear in the transcript as
// model tool calls. The sidebar's Tools panel shows these next to the
// transcript-derived counts.
func (s *Server) handleBackgroundActivity(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"activity": s.agent.BackgroundActivity(r.PathValue("id")),
	})
}

func (s *Server) handleDeleteSession(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("id")
	unlock := s.cursorLifecycles.Lock(sessionID)
	defer unlock()
	active, err := s.cursorSessionHasActiveRemoteState(r.Context(), sessionID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if active {
		writeError(w, http.StatusConflict, errors.New(
			"cannot delete a session with an active remote Cursor operation",
		))
		return
	}
	if live := s.hub.get(sessionID); live != nil {
		live.stopWatching()
	}
	if err := s.db.DeleteSession(r.Context(), sessionID); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"deleted": true})
}

// handleDeleteAllSessions deletes all sessions in a category (chat or project).
// Categories: "chat" (non-project sessions), "project" (sessions with meta.project_dir),
// or "all" (every session).
func (s *Server) handleDeleteAllSessions(w http.ResponseWriter, r *http.Request) {
	if s.requireDashboardPassword(w, r) {
		return
	}
	var body struct {
		Category string `json:"category"` // chat|project|all
	}
	if err := decodeBody(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	category := strings.TrimSpace(strings.ToLower(body.Category))
	if category == "" {
		category = "all"
	}

	ids, err := s.cursorCleanupSessionIDs(r.Context(), func(sess store.Session) bool {
		isProject := false
		if sess.Meta != nil {
			if value, ok := sess.Meta["project_dir"]; ok {
				if projectDir, ok := value.(string); ok && projectDir != "" {
					isProject = true
				}
			}
		}
		switch category {
		case "chat":
			return !isProject
		case "project":
			return isProject
		case "all":
			return true
		default:
			return false
		}
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	if len(ids) == 0 {
		writeJSON(w, http.StatusOK, map[string]any{"deleted": 0})
		return
	}
	unlock := s.cursorLifecycles.LockMany(ids)
	defer unlock()
	// Check every selected session before DeleteSessions mutates the first one,
	// so a later active entry cannot produce a partial bulk deletion.
	for _, id := range ids {
		active, err := s.cursorSessionHasActiveRemoteState(r.Context(), id)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		if active {
			writeError(w, http.StatusConflict, errors.New(
				"cannot bulk delete while a remote Cursor operation is active",
			))
			return
		}
	}
	for _, id := range ids {
		if live := s.hub.get(id); live != nil {
			live.stopWatching()
		}
	}

	n, err := s.db.DeleteSessions(r.Context(), ids)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": n})
}

func (s *Server) handleRenameSession(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Title string `json:"title"`
	}
	if err := decodeBody(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	sess, err := s.db.GetSession(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, errNotFound)
		return
	}
	sess.Title = strings.TrimSpace(body.Title)
	if err := s.db.UpdateSession(r.Context(), sess); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, sess)
}

func (s *Server) handleSearchSessions(w http.ResponseWriter, r *http.Request) {
	hits, err := s.db.SearchMessages(r.Context(), r.URL.Query().Get("q"), queryInt(r, "limit", 30))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"hits": hits})
}

func (s *Server) handleEmptyCount(w http.ResponseWriter, r *http.Request) {
	n, err := s.db.CountEmptySessions(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"count": n})
}

func (s *Server) handleDeleteEmpty(w http.ResponseWriter, r *http.Request) {
	ids, err := s.cursorCleanupSessionIDs(r.Context(), func(session store.Session) bool {
		return session.MessageCount == 0
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if len(ids) == 0 {
		writeJSON(w, http.StatusOK, map[string]any{"deleted": int64(0)})
		return
	}
	unlock, active, err := s.lockCursorCleanupSessions(r.Context(), ids)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if active {
		writeError(w, http.StatusConflict, errors.New(
			"cannot delete empty sessions while a remote Cursor operation is active",
		))
		return
	}
	defer unlock()
	s.stopCursorCleanupWatchers(ids)

	n, err := s.db.DeleteEmptySessions(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": n})
}

func (s *Server) handlePruneSessions(w http.ResponseWriter, r *http.Request) {
	var body struct {
		OlderThanDays int `json:"older_than_days"`
	}
	_ = decodeBody(r, &body)
	if body.OlderThanDays <= 0 {
		body.OlderThanDays = 30
	}
	cutoff := time.Now().AddDate(0, 0, -body.OlderThanDays)
	ids, err := s.cursorCleanupSessionIDs(r.Context(), func(session store.Session) bool {
		return !session.Pinned && session.UpdatedAt.Before(cutoff)
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if len(ids) == 0 {
		writeJSON(w, http.StatusOK, map[string]any{"deleted": int64(0)})
		return
	}
	unlock, active, err := s.lockCursorCleanupSessions(r.Context(), ids)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if active {
		writeError(w, http.StatusConflict, errors.New(
			"cannot prune sessions while a remote Cursor operation is active",
		))
		return
	}
	defer unlock()
	s.stopCursorCleanupWatchers(ids)

	n, err := s.db.PruneSessions(r.Context(), cutoff)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": n})
}

const cursorCleanupPageSize = 500

func (s *Server) cursorCleanupSessionIDs(
	ctx context.Context,
	include func(store.Session) bool,
) ([]string, error) {
	var ids []string
	for offset := 0; ; {
		sessions, total, err := s.db.ListSessions(ctx, store.SessionFilter{
			Limit: cursorCleanupPageSize, Offset: offset,
		})
		if err != nil {
			return nil, err
		}
		for _, session := range sessions {
			if include(session) {
				ids = append(ids, session.ID)
			}
		}
		offset += len(sessions)
		if len(sessions) == 0 || int64(offset) >= total {
			return ids, nil
		}
	}
}

func (s *Server) lockCursorCleanupSessions(
	ctx context.Context,
	sessionIDs []string,
) (unlock func(), active bool, err error) {
	unlock = s.cursorLifecycles.LockMany(sessionIDs)
	for _, sessionID := range sessionIDs {
		active, err = s.cursorSessionBlocksAutomaticCleanup(ctx, sessionID)
		if err != nil || active {
			unlock()
			return nil, active, err
		}
	}
	return unlock, false, nil
}

func (s *Server) stopCursorCleanupWatchers(sessionIDs []string) {
	for _, sessionID := range sessionIDs {
		if live := s.hub.get(sessionID); live != nil {
			live.stopWatching()
		}
	}
}
