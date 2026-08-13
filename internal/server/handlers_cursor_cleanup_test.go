package server

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/enowdev/antares/internal/store"
)

func TestCursorCleanupHandlersPrecheckActiveStateAcrossPagination(t *testing.T) {
	tests := []struct {
		name string
		path string
		body string
		edit func(*store.Session)
	}{
		{
			name: "empty", path: "/api/sessions/empty/delete",
			edit: func(session *store.Session) { session.MessageCount = 0 },
		},
		{
			name: "prune", path: "/api/sessions/prune",
			body: `{"older_than_days":1}`,
			edit: func(session *store.Session) {
				session.MessageCount = 1
				session.UpdatedAt = time.Now().Add(-48 * time.Hour)
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newCursorDirectTestServer(t)
			activeID := "cleanup-active-" + test.name
			seedCleanupCursorState(
				t, fixture, activeID, store.CursorOperationRunInFlight, "RUNNING",
			)

			sessions := make([]store.Session, 501)
			for i := range 500 {
				sessions[i] = store.Session{
					ID: fmt.Sprintf("cleanup-page-%s-%03d", test.name, i),
				}
				test.edit(&sessions[i])
			}
			sessions[500] = store.Session{ID: activeID}
			test.edit(&sessions[500])
			paged := &pagedCleanupStore{Store: fixture.db, sessions: sessions}
			fixture.server.db = paged

			status, _ := postCursorCleanup(t, fixture, test.path, test.body)
			if status != http.StatusConflict {
				t.Fatalf("cleanup status=%d, want 409", status)
			}
			if paged.listCalls < 2 {
				t.Fatalf("cleanup listed %d page(s), want at least 2", paged.listCalls)
			}
			if paged.deleteEmptyCalls != 0 || paged.pruneCalls != 0 {
				t.Fatal("cleanup mutated storage after finding active remote state")
			}
		})
	}
}

func TestCursorActiveRemotePredicateIsPureForCancelCrashMarker(t *testing.T) {
	fixture := newCursorDirectTestServer(t)
	sessionID := "cleanup-stale-cancel-marker"
	seedCleanupCursorState(
		t, fixture, sessionID, store.CursorOperationRunInFlight, cursorCancelInFlight,
	)

	if !fixture.server.reserveCursorCancel(sessionID, "run-"+sessionID) {
		t.Fatal("could not reserve process-local cancellation")
	}
	active, err := fixture.server.cursorSessionHasActiveRemoteState(
		context.Background(), sessionID,
	)
	if err != nil || !active {
		t.Fatalf("locally executing cancellation active=%v err=%v", active, err)
	}
	fixture.server.releaseCursorCancel(sessionID, "run-"+sessionID)

	active, err = fixture.server.cursorSessionHasActiveRemoteState(
		context.Background(), sessionID,
	)
	if err != nil || active {
		t.Fatalf("stale cancellation marker active=%v err=%v", active, err)
	}
	state, err := fixture.db.GetCursorSessionState(context.Background(), sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if state.RemoteStatus != cursorCancelInFlight {
		t.Fatalf("predicate mutated crash marker to %q", state.RemoteStatus)
	}
}

func seedCleanupCursorState(
	t *testing.T,
	fixture *cursorDirectFixture,
	sessionID string,
	operation string,
	remoteStatus string,
) {
	t.Helper()
	if err := fixture.db.CreateSession(context.Background(), &store.Session{
		ID: sessionID, Title: sessionID, Platform: "web", Meta: store.Meta{},
	}); err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.PutCursorSessionState(context.Background(), &store.CursorSessionState{
		SessionID:      sessionID,
		TargetActive:   true,
		ReuseValid:     true,
		ModelID:        "gpt-5.6-sol",
		ModelParams:    `[]`,
		AgentID:        "bc-" + sessionID,
		RunID:          "run-" + sessionID,
		RemoteStatus:   remoteStatus,
		OperationState: operation,
	}); err != nil {
		t.Fatal(err)
	}
}

type pagedCleanupStore struct {
	store.Store
	sessions         []store.Session
	listCalls        int
	deleteEmptyCalls int
	pruneCalls       int
}

func (s *pagedCleanupStore) ListSessions(
	_ context.Context,
	filter store.SessionFilter,
) ([]store.Session, int64, error) {
	s.listCalls++
	start := filter.Offset
	if start > len(s.sessions) {
		start = len(s.sessions)
	}
	limit := filter.Limit
	if limit <= 0 {
		limit = 50
	}
	end := min(start+limit, len(s.sessions))
	return append([]store.Session(nil), s.sessions[start:end]...),
		int64(len(s.sessions)), nil
}

func (s *pagedCleanupStore) DeleteEmptySessions(context.Context) (int64, error) {
	s.deleteEmptyCalls++
	return 0, nil
}

func (s *pagedCleanupStore) PruneSessions(context.Context, time.Time) (int64, error) {
	s.pruneCalls++
	return 0, nil
}

func postCursorCleanup(
	t *testing.T,
	fixture *cursorDirectFixture,
	path string,
	body string,
) (int, string) {
	t.Helper()
	request, err := http.NewRequest(
		http.MethodPost, fixture.http.URL+path, strings.NewReader(body),
	)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer test-token")
	request.Header.Set("Content-Type", "application/json")
	response, err := fixture.http.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	raw := make([]byte, 16<<10)
	n, _ := response.Body.Read(raw)
	return response.StatusCode, string(raw[:n])
}
