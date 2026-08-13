package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
)

func (s *sqlStore) CreateSession(ctx context.Context, sess *Session) error {
	now := time.Now()
	if sess.CreatedAt.IsZero() {
		sess.CreatedAt = now
	}
	sess.UpdatedAt = now
	if sess.Meta == nil {
		sess.Meta = Meta{}
	}
	meta, _ := sess.Meta.Value()
	_, err := s.exec(ctx, `INSERT INTO sessions
		(id,title,platform,channel_id,user_id,model,provider,workspace,message_count,tokens_in,tokens_out,cost,archived,pinned,created_at,updated_at,meta)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		sess.ID, sess.Title, sess.Platform, sess.ChannelID, sess.UserID, sess.Model, sess.Provider, sess.Workspace,
		sess.MessageCount, sess.TokensIn, sess.TokensOut, sess.Cost, sess.Archived, sess.Pinned,
		ms(sess.CreatedAt), ms(sess.UpdatedAt), meta)
	return err
}

const sessionCols = `id,title,platform,channel_id,user_id,model,provider,workspace,message_count,tokens_in,tokens_out,cost,archived,pinned,created_at,updated_at,meta`

// SetSessionTitle updates only the title, never touching meta or the counters —
// so a title save from a struct loaded before a tool wrote meta cannot undo it.
func (s *sqlStore) SetSessionTitle(ctx context.Context, id, title string) error {
	_, err := s.exec(ctx, `UPDATE sessions SET title=?, updated_at=? WHERE id=?`,
		title, ms(time.Now()), id)
	return err
}

// TouchSession bumps only updated_at.
func (s *sqlStore) TouchSession(ctx context.Context, id string) error {
	_, err := s.exec(ctx, `UPDATE sessions SET updated_at=? WHERE id=?`, ms(time.Now()), id)
	return err
}

func scanSession(sc interface{ Scan(...any) error }) (*Session, error) {
	var (
		s            Session
		created, upd int64
	)
	err := sc.Scan(&s.ID, &s.Title, &s.Platform, &s.ChannelID, &s.UserID, &s.Model, &s.Provider, &s.Workspace,
		&s.MessageCount, &s.TokensIn, &s.TokensOut, &s.Cost, &s.Archived, &s.Pinned, &created, &upd, &s.Meta)
	if err != nil {
		return nil, err
	}
	s.CreatedAt, s.UpdatedAt = fromMS(created), fromMS(upd)
	return &s, nil
}

func (s *sqlStore) GetSession(ctx context.Context, id string) (*Session, error) {
	sess, err := scanSession(s.row(ctx, `SELECT `+sessionCols+` FROM sessions WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return sess, err
}

func (s *sqlStore) UpdateSession(ctx context.Context, sess *Session) error {
	sess.UpdatedAt = time.Now()
	meta, _ := sess.Meta.Value()
	// message_count / tokens_in / tokens_out are a running tally owned solely by
	// AppendMessage (it increments them per message). They are deliberately NOT
	// written here: callers pass a Session struct loaded before the turn's
	// messages were appended, so including them would clobber the real counts
	// back to the stale in-memory value — the "0 messages / 0 tokens" bug.
	_, err := s.exec(ctx, `UPDATE sessions SET title=?,platform=?,channel_id=?,user_id=?,model=?,provider=?,workspace=?,
		cost=?,archived=?,pinned=?,updated_at=?,meta=? WHERE id=?`,
		sess.Title, sess.Platform, sess.ChannelID, sess.UserID, sess.Model, sess.Provider, sess.Workspace,
		sess.Cost, sess.Archived, sess.Pinned, ms(sess.UpdatedAt), meta, sess.ID)
	return err
}

func (s *sqlStore) ListSessions(ctx context.Context, f SessionFilter) ([]Session, int64, error) {
	var where []string
	var args []any
	if f.Platform != "" {
		where = append(where, "platform=?")
		args = append(args, f.Platform)
	}
	for _, p := range f.ExcludePlatforms {
		if p == "" {
			continue
		}
		where = append(where, "platform<>?")
		args = append(args, p)
	}
	if f.UserID != "" {
		where = append(where, "user_id=?")
		args = append(args, f.UserID)
	}
	if f.Archived != nil {
		where = append(where, "archived=?")
		args = append(args, *f.Archived)
	}
	if q := strings.TrimSpace(f.Query); q != "" {
		where = append(where, "LOWER(title) LIKE ?")
		args = append(args, "%"+strings.ToLower(q)+"%")
	}
	clause := ""
	if len(where) > 0 {
		clause = " WHERE " + strings.Join(where, " AND ")
	}

	var total int64
	if err := s.row(ctx, `SELECT COUNT(*) FROM sessions`+clause, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	order := "updated_at DESC"
	switch f.OrderBy {
	case "created_at":
		order = "created_at DESC"
	case "message_count":
		order = "message_count DESC"
	}
	limit := f.Limit
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	rows, err := s.query(ctx, `SELECT `+sessionCols+` FROM sessions`+clause+
		` ORDER BY pinned DESC, `+order+`, id ASC LIMIT ? OFFSET ?`, append(args, limit, f.Offset)...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	out := []Session{}
	for rows.Next() {
		sess, err := scanSession(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, *sess)
	}
	return out, total, rows.Err()
}

func (s *sqlStore) DeleteSession(ctx context.Context, id string) error {
	if _, err := s.exec(ctx, `DELETE FROM cursor_session_states WHERE session_id=?`, id); err != nil {
		return err
	}
	if _, err := s.exec(ctx, `DELETE FROM messages WHERE session_id=?`, id); err != nil {
		return err
	}
	if _, err := s.exec(ctx, `DELETE FROM memories WHERE scope='session' AND scope_key=?`, id); err != nil {
		// Non-fatal.
	}
	_, err := s.exec(ctx, `DELETE FROM sessions WHERE id=?`, id)
	return err
}

func (s *sqlStore) DeleteSessions(ctx context.Context, ids []string) (int64, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	deletions := [...]string{
		`DELETE FROM cursor_session_states WHERE session_id=?`,
		`DELETE FROM messages WHERE session_id=?`,
		`DELETE FROM memories WHERE scope='session' AND scope_key=?`,
		`DELETE FROM sessions WHERE id=?`,
	}
	for _, id := range ids {
		for _, deletion := range deletions {
			if _, err := tx.ExecContext(ctx, s.rebind(deletion), id); err != nil {
				return 0, err
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return int64(len(ids)), nil
}

func (s *sqlStore) CountEmptySessions(ctx context.Context) (int64, error) {
	var n int64
	err := s.row(ctx, `SELECT COUNT(*) FROM sessions WHERE message_count=0`).Scan(&n)
	return n, err
}

func (s *sqlStore) DeleteEmptySessions(ctx context.Context) (int64, error) {
	return s.deleteSessionsAndChildren(ctx,
		`DELETE FROM sessions WHERE message_count=0`+cursorCleanupInactivePredicate+` RETURNING id`,
		cursorCleanupInactiveArgs()...,
	)
}

const cursorCleanupInactivePredicate = `
	AND NOT EXISTS (
		SELECT 1 FROM cursor_session_states AS cursor_cleanup
		WHERE cursor_cleanup.session_id=sessions.id
		  AND (
			cursor_cleanup.operation_state IN (?,?,?)
			OR (
				cursor_cleanup.operation_state=?
				AND COALESCE(cursor_cleanup.remote_status,'') NOT IN (?,?,?)
			)
		  )
	)`

func cursorCleanupInactiveArgs() []any {
	// Cancellation markers are local-reconciliation states. The server blocks a
	// currently executing CancelRun with its process-local reservation; without
	// that reservation these durable markers must remain locally deletable.
	return []any{
		CursorOperationAwaitingApproval,
		CursorOperationCreateInFlight,
		CursorOperationTerminal,
		CursorOperationRunInFlight,
		"ANTARES_CANCEL_REQUESTED",
		"ANTARES_CANCEL_OUTCOME_AMBIGUOUS",
		"ANTARES_CANCEL_IN_FLIGHT",
	}
}

func (s *sqlStore) deleteSessionsAndChildren(ctx context.Context, deleteQuery string, args ...any) (int64, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	rows, err := tx.QueryContext(ctx, s.rebind(deleteQuery), args...)
	if err != nil {
		return 0, err
	}
	var sessionIDs []string
	for rows.Next() {
		var sessionID string
		if err := rows.Scan(&sessionID); err != nil {
			rows.Close()
			return 0, err
		}
		sessionIDs = append(sessionIDs, sessionID)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, err
	}
	if err := rows.Close(); err != nil {
		return 0, err
	}

	for _, sessionID := range sessionIDs {
		if _, err := tx.ExecContext(ctx, s.rebind(
			`DELETE FROM cursor_session_states WHERE session_id=?`,
		), sessionID); err != nil {
			return 0, err
		}
		if _, err := tx.ExecContext(ctx, s.rebind(
			`DELETE FROM messages WHERE session_id=?`,
		), sessionID); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return int64(len(sessionIDs)), nil
}

func (s *sqlStore) PruneSessions(ctx context.Context, olderThan time.Time) (int64, error) {
	cutoff := ms(olderThan)
	args := append([]any{cutoff}, cursorCleanupInactiveArgs()...)
	return s.deleteSessionsAndChildren(ctx,
		`DELETE FROM sessions WHERE updated_at < ? AND pinned = FALSE`+
			cursorCleanupInactivePredicate+` RETURNING id`,
		args...,
	)
}

// ---- messages ---------------------------------------------------------------

const messageCols = `id,session_id,seq,role,content,reasoning,tool_calls,tool_call_id,tool_name,attachments,model,tokens_in,tokens_out,hidden,compacted,created_at,meta`

func (s *sqlStore) AppendMessage(ctx context.Context, m *Message) error {
	if m.CreatedAt.IsZero() {
		m.CreatedAt = time.Now()
	}
	if m.Meta == nil {
		m.Meta = Meta{}
	}
	if m.Seq == 0 {
		var maxSeq sql.NullInt64
		if err := s.row(ctx, `SELECT MAX(seq) FROM messages WHERE session_id=?`, m.SessionID).Scan(&maxSeq); err != nil {
			return err
		}
		m.Seq = maxSeq.Int64 + 1
	}
	meta, _ := m.Meta.Value()
	if _, err := s.exec(ctx, `INSERT INTO messages (`+messageCols+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		m.ID, m.SessionID, m.Seq, m.Role, m.Content, m.Reasoning, m.ToolCalls, m.ToolCallID, m.ToolName,
		m.Attachments, m.Model, m.TokensIn, m.TokensOut, m.Hidden, m.Compacted, ms(m.CreatedAt), meta); err != nil {
		return err
	}
	_, err := s.exec(ctx, `UPDATE sessions SET message_count=message_count+1, tokens_in=tokens_in+?, tokens_out=tokens_out+?, updated_at=? WHERE id=?`,
		m.TokensIn, m.TokensOut, ms(time.Now()), m.SessionID)
	return err
}

func scanMessage(sc interface{ Scan(...any) error }) (*Message, error) {
	var m Message
	var created int64
	err := sc.Scan(&m.ID, &m.SessionID, &m.Seq, &m.Role, &m.Content, &m.Reasoning, &m.ToolCalls, &m.ToolCallID,
		&m.ToolName, &m.Attachments, &m.Model, &m.TokensIn, &m.TokensOut, &m.Hidden, &m.Compacted, &created, &m.Meta)
	if err != nil {
		return nil, err
	}
	m.CreatedAt = fromMS(created)
	return &m, nil
}

func (s *sqlStore) ListMessages(ctx context.Context, sessionID string, limit, offset int) ([]Message, error) {
	q := `SELECT ` + messageCols + ` FROM messages WHERE session_id=? ORDER BY seq ASC`
	args := []any{sessionID}
	if limit > 0 {
		q += ` LIMIT ? OFFSET ?`
		args = append(args, limit, offset)
	}
	rows, err := s.query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Message{}
	for rows.Next() {
		m, err := scanMessage(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *m)
	}
	return out, rows.Err()
}

func (s *sqlStore) DeleteMessage(ctx context.Context, id string) error {
	_, err := s.exec(ctx, `DELETE FROM messages WHERE id=?`, id)
	return err
}

// DeleteMessagesFrom removes the message with fromID and every message after it
// in the session (by seq), then recomputes the session's message/token tallies
// from what remains. Used by "edit message", which drops the edited turn and all
// that followed so the conversation can be re-sent from that point.
func (s *sqlStore) DeleteMessagesFrom(ctx context.Context, sessionID, fromID string) error {
	_, err := s.exec(ctx,
		`DELETE FROM messages WHERE session_id=? AND seq >= (SELECT seq FROM messages WHERE id=?)`,
		sessionID, fromID)
	if err != nil {
		return err
	}
	// Keep the owned counters honest (see session-counter ownership).
	_, err = s.exec(ctx, `UPDATE sessions SET
		message_count = (SELECT COUNT(*) FROM messages WHERE session_id=?),
		tokens_in     = (SELECT COALESCE(SUM(tokens_in),0) FROM messages WHERE session_id=?),
		tokens_out    = (SELECT COALESCE(SUM(tokens_out),0) FROM messages WHERE session_id=?)
		WHERE id=?`, sessionID, sessionID, sessionID, sessionID)
	return err
}

func (s *sqlStore) SearchMessages(ctx context.Context, query string, limit int) ([]SearchHit, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return []SearchHit{}, nil
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}

	var (
		rows *sql.Rows
		err  error
	)
	if s.dialect == "sqlite" {
		rows, err = s.query(ctx, `
			SELECT m.id, m.session_id, COALESCE(s.title,''), m.role, m.content, m.created_at, bm25(messages_fts)
			FROM messages_fts f
			JOIN messages m ON m.id = f.message_id
			LEFT JOIN sessions s ON s.id = m.session_id
			WHERE messages_fts MATCH ?
			ORDER BY bm25(messages_fts) LIMIT ?`, ftsQuery(query), limit)
		if err != nil {
			// FTS syntax errors fall back to LIKE so the UI still returns something.
			rows, err = s.likeSearch(ctx, query, limit)
		}
	} else {
		rows, err = s.query(ctx, `
			SELECT m.id, m.session_id, COALESCE(s.title,''), m.role, m.content, m.created_at,
			       ts_rank(to_tsvector('simple', m.content), plainto_tsquery('simple', ?)) AS score
			FROM messages m
			LEFT JOIN sessions s ON s.id = m.session_id
			WHERE to_tsvector('simple', m.content) @@ plainto_tsquery('simple', ?)
			ORDER BY score DESC LIMIT ?`, query, query, limit)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []SearchHit{}
	for rows.Next() {
		var (
			h       SearchHit
			content string
			created int64
			score   float64
		)
		if err := rows.Scan(&h.MessageID, &h.SessionID, &h.SessionTitle, &h.Role, &content, &created, &score); err != nil {
			return nil, err
		}
		h.CreatedAt = fromMS(created)
		h.Score = score
		h.Snippet = snippet(content, query, 240)
		out = append(out, h)
	}
	return out, rows.Err()
}

func (s *sqlStore) likeSearch(ctx context.Context, query string, limit int) (*sql.Rows, error) {
	like := "%" + strings.ToLower(query) + "%"
	return s.query(ctx, `
		SELECT m.id, m.session_id, COALESCE(s.title,''), m.role, m.content, m.created_at, 0.0
		FROM messages m LEFT JOIN sessions s ON s.id = m.session_id
		WHERE LOWER(m.content) LIKE ? ORDER BY m.created_at DESC LIMIT ?`, like, limit)
}

// ftsQuery escapes user input into a safe FTS5 phrase query.
func ftsQuery(q string) string {
	fields := strings.Fields(q)
	parts := make([]string, 0, len(fields))
	for _, f := range fields {
		f = strings.ReplaceAll(f, `"`, "")
		if f == "" {
			continue
		}
		parts = append(parts, `"`+f+`"`)
	}
	if len(parts) == 0 {
		return `""`
	}
	return strings.Join(parts, " ")
}

func snippet(content, query string, width int) string {
	lower := strings.ToLower(content)
	idx := strings.Index(lower, strings.ToLower(strings.Fields(query + " ")[0]))
	if idx < 0 {
		idx = 0
	}
	start := idx - width/3
	if start < 0 {
		start = 0
	}
	end := start + width
	if end > len(content) {
		end = len(content)
	}
	out := strings.TrimSpace(content[start:end])
	if start > 0 {
		out = "…" + out
	}
	if end < len(content) {
		out += "…"
	}
	return out
}

// ---- stats ------------------------------------------------------------------

func (s *sqlStore) Stats(ctx context.Context) (Stats, error) {
	var st Stats
	count := func(q string, dst *int64) error { return s.row(ctx, q).Scan(dst) }
	if err := count(`SELECT COUNT(*) FROM sessions`, &st.Sessions); err != nil {
		return st, err
	}
	if err := count(`SELECT COUNT(*) FROM messages`, &st.Messages); err != nil {
		return st, err
	}
	if err := count(`SELECT COUNT(*) FROM memories`, &st.Memories); err != nil {
		return st, err
	}
	if err := count(`SELECT COUNT(*) FROM cron_jobs`, &st.CronJobs); err != nil {
		return st, err
	}
	if err := count(`SELECT COUNT(*) FROM sessions WHERE message_count=0`, &st.EmptySessions); err != nil {
		return st, err
	}
	var in, out sql.NullInt64
	var cost sql.NullFloat64
	if err := s.row(ctx, `SELECT COALESCE(SUM(tokens_in),0), COALESCE(SUM(tokens_out),0), COALESCE(SUM(cost),0) FROM usage_records`).
		Scan(&in, &out, &cost); err != nil {
		return st, err
	}
	st.TokensIn, st.TokensOut, st.Cost = in.Int64, out.Int64, cost.Float64
	if s.dialect == "sqlite" && !strings.Contains(s.dsn, ":memory:") {
		if fi, err := os.Stat(s.dsn); err == nil {
			st.SizeBytes = fi.Size()
		}
	} else if s.dialect == "postgres" {
		_ = s.row(ctx, `SELECT pg_database_size(current_database())`).Scan(&st.SizeBytes)
	}
	return st, nil
}

var _ = fmt.Sprintf
