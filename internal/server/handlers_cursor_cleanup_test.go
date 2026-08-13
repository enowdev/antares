package server

import (
	"context"
	"fmt"
	"net/http"
	"slices"
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

func TestCursorAutomaticCleanupBlocksTerminalWithoutLiveWatcher(t *testing.T) {
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
			sessionID := "cleanup-terminal-" + test.name
			seedCleanupCursorState(
				t, fixture, sessionID, store.CursorOperationTerminal, "COMPLETED",
			)
			if live := fixture.server.hub.get(sessionID); live != nil {
				t.Fatal("terminal cleanup regression requires no live watcher")
			}

			session := store.Session{ID: sessionID}
			test.edit(&session)
			paged := &pagedCleanupStore{
				Store: fixture.db, sessions: []store.Session{session},
			}
			fixture.server.db = paged

			status, _ := postCursorCleanup(t, fixture, test.path, test.body)
			if status != http.StatusConflict {
				t.Fatalf("terminal cleanup status=%d, want 409", status)
			}
			if paged.deleteEmptyCalls != 0 || paged.pruneCalls != 0 {
				t.Fatal("automatic cleanup mutated terminal uncommitted work")
			}
		})
	}
}

func TestDeleteAllSessionsPaginatesBeforeCategoryFiltering(t *testing.T) {
	fixture := newCursorDirectTestServer(t)
	sessions, selected := pagedCategorySessions(1103)
	ambiguousID := sessions[1100].ID
	seedCleanupCursorState(
		t, fixture, ambiguousID, store.CursorOperationAmbiguous, "AMBIGUOUS_CREATE_OUTCOME",
	)
	paged := &pagedCleanupStore{Store: fixture.db, sessions: sessions}
	fixture.server.db = paged

	status, _ := postCursorCleanup(
		t, fixture, "/api/sessions/delete-all", `{"category":"project"}`,
	)
	if status != http.StatusOK {
		t.Fatalf("paginated category deletion status=%d, want 200", status)
	}
	if paged.listCalls < 3 {
		t.Fatalf("category deletion listed %d page(s), want at least 3", paged.listCalls)
	}
	if len(selected) <= cursorCleanupPageSize {
		t.Fatalf("test selected only %d sessions, need more than one page", len(selected))
	}
	if !slices.Equal(paged.deletedSessionIDs, selected) {
		t.Fatalf("deleted %d category sessions, want all %d",
			len(paged.deletedSessionIDs), len(selected))
	}
}

func TestDeleteAllSessionsPrechecksActiveStateOnLaterPage(t *testing.T) {
	fixture := newCursorDirectTestServer(t)
	sessions, _ := pagedCategorySessions(1103)
	activeID := sessions[1102].ID
	seedCleanupCursorState(
		t, fixture, activeID, store.CursorOperationRunInFlight, "RUNNING",
	)
	paged := &pagedCleanupStore{Store: fixture.db, sessions: sessions}
	fixture.server.db = paged

	status, _ := postCursorCleanup(
		t, fixture, "/api/sessions/delete-all", `{"category":"project"}`,
	)
	if status != http.StatusConflict {
		t.Fatalf("late-page active category deletion status=%d, want 409", status)
	}
	if len(paged.deletedSessionIDs) != 0 {
		t.Fatal("category deletion mutated storage before completing active precheck")
	}
}

func pagedCategorySessions(count int) ([]store.Session, []string) {
	sessions := make([]store.Session, count)
	var selected []string
	for i := range sessions {
		session := store.Session{ID: fmt.Sprintf("delete-all-page-%04d", i)}
		if i%2 == 0 {
			session.Meta = store.Meta{"project_dir": "/tmp/project"}
			selected = append(selected, session.ID)
		}
		sessions[i] = session
	}
	return sessions, selected
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
	sessions          []store.Session
	listCalls         int
	deleteEmptyCalls  int
	pruneCalls        int
	deletedSessionIDs []string
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
	if limit <= 0 || limit > cursorCleanupPageSize {
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

func (s *pagedCleanupStore) DeleteSessions(
	_ context.Context,
	sessionIDs []string,
) (int64, error) {
	s.deletedSessionIDs = append([]string(nil), sessionIDs...)
	return int64(len(sessionIDs)), nil
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
