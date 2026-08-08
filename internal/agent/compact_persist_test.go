package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/enowdev/antares/internal/config"
	"github.com/enowdev/antares/internal/llm"
	"github.com/enowdev/antares/internal/store"
)

// stubStore is a minimal store for compact persistence tests.
type compactMemStore struct {
	store.Store
	sess *store.Session
	msgs []store.Message
	seq  int64
}

func (m *compactMemStore) GetSession(ctx context.Context, id string) (*store.Session, error) {
	if m.sess == nil || m.sess.ID != id {
		return nil, store.ErrNotFound
	}
	// return a copy so UpdateSession mutations are visible on re-get after we reassign
	cp := *m.sess
	if m.sess.Meta != nil {
		cp.Meta = store.Meta{}
		for k, v := range m.sess.Meta {
			cp.Meta[k] = v
		}
	}
	return &cp, nil
}

func (m *compactMemStore) UpdateSession(ctx context.Context, sess *store.Session) error {
	m.sess = sess
	return nil
}

func (m *compactMemStore) ListMessages(ctx context.Context, sessionID string, limit, offset int) ([]store.Message, error) {
	out := make([]store.Message, len(m.msgs))
	copy(out, m.msgs)
	return out, nil
}

func (m *compactMemStore) AppendMessage(ctx context.Context, msg *store.Message) error {
	m.seq++
	msg.Seq = m.seq
	m.msgs = append(m.msgs, *msg)
	return nil
}

func TestLoadHistoryAppliesPersistedCompact(t *testing.T) {
	sess := &store.Session{ID: "s1", Meta: store.Meta{
		contextCompactMetaKey: map[string]any{
			"summary":     "We fixed the skin bug and built the APK.",
			"through_seq": int64(5),
			"keep_first":  1,
		},
	}}
	db := &compactMemStore{sess: sess, msgs: []store.Message{
		{ID: "1", SessionID: "s1", Seq: 1, Role: store.RoleUser, Content: "hello"},
		{ID: "2", SessionID: "s1", Seq: 2, Role: store.RoleAssistant, Content: "hi"},
		{ID: "3", SessionID: "s1", Seq: 3, Role: store.RoleUser, Content: "do work"},
		{ID: "4", SessionID: "s1", Seq: 4, Role: store.RoleAssistant, Content: "working"},
		{ID: "5", SessionID: "s1", Seq: 5, Role: store.RoleTool, Content: "huge tool output " + strings.Repeat("x", 1000), ToolName: "terminal", ToolCallID: "t1"},
		{ID: "6", SessionID: "s1", Seq: 6, Role: store.RoleUser, Content: "continue"},
		{ID: "7", SessionID: "s1", Seq: 7, Role: store.RoleAssistant, Content: "ok"},
	}}
	a := &Agent{cfg: config.Default(), db: db}
	hist, err := a.loadHistory(context.Background(), sess, Request{})
	if err != nil {
		t.Fatal(err)
	}
	// head(1) + summary + tail(seq>5) = continue + ok
	if len(hist) < 3 {
		t.Fatalf("history len=%d, want at least 3 (head+summary+tail)", len(hist))
	}
	if hist[0].Content != "hello" {
		t.Fatalf("head = %q", hist[0].Content)
	}
	if !strings.Contains(hist[1].Content, "We fixed the skin bug") {
		t.Fatalf("summary missing: %q", hist[1].Content)
	}
	// Must NOT include the huge tool output (seq 5 covered by compact)
	for _, m := range hist {
		if strings.Contains(m.Content, "huge tool output") {
			t.Fatal("compacted middle still present in history")
		}
	}
	joined := ""
	for _, m := range hist {
		joined += m.Content
	}
	if !strings.Contains(joined, "continue") {
		t.Fatalf("tail missing: %#v", hist)
	}
}

func TestMaybeCompactSkipsWhenUnderThreshold(t *testing.T) {
	a := &Agent{cfg: config.Default()}
	a.cfg.Model.ContextWindow = 200000
	hist := []llm.Message{
		{Role: llm.RoleUser, Content: "hi"},
		{Role: llm.RoleAssistant, Content: "hello"},
	}
	// pad a bit but stay under 80% of 200k
	for i := 0; i < 10; i++ {
		hist = append(hist, llm.Message{Role: llm.RoleUser, Content: "x"})
	}
	out := a.maybeCompact(context.Background(), hist, "sys", "m", nil, nil, nil)
	if len(out) != len(hist) {
		t.Fatalf("should not compact small history")
	}
}
