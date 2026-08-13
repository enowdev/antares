package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/enowdev/antares/internal/agent"
	"github.com/enowdev/antares/internal/cursor"
	"github.com/enowdev/antares/internal/cursorrun"
	"github.com/enowdev/antares/internal/store"
)

func TestCursorChatAmbiguousStatesCanBeDeletedLocally(t *testing.T) {
	tests := []struct {
		name   string
		bulk   bool
		cancel bool
	}{
		{name: "create single"},
		{name: "create bulk", bulk: true},
		{name: "cancel single", cancel: true},
		{name: "cancel bulk", bulk: true, cancel: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newCursorDirectTestServer(t)
			sessionID := seedRecoverableCursorSession(t, fixture)
			_, err := fixture.server.mutateCursorState(context.Background(), sessionID,
				func(state *store.CursorSessionState) error {
					if test.cancel {
						state.OperationState = store.CursorOperationRunInFlight
						state.RemoteStatus = cursorCancelAmbiguous
					} else {
						state.OperationState = store.CursorOperationAmbiguous
						state.AgentID = ""
						state.RunID = ""
						state.RemoteStatus = "AMBIGUOUS_CREATE_OUTCOME"
					}
					return nil
				})
			if err != nil {
				t.Fatal(err)
			}

			request := defaultCursorChatRequest()
			request.SessionID = sessionID
			status, body := postCursorChatStatus(t, fixture, request)
			if status != http.StatusConflict ||
				!strings.Contains(strings.ToLower(body), "delete") {
				t.Fatalf("ambiguous turn status=%d body=%q, want 409 with local-delete escape", status, body)
			}

			if test.bulk {
				status = deleteAllCursorSessions(t, fixture)
			} else {
				status = deleteCursorSession(t, fixture, sessionID)
			}
			if status != http.StatusOK {
				t.Fatalf("local reconciliation delete status=%d, want 200", status)
			}
			if _, err := fixture.db.GetSession(context.Background(), sessionID); !errors.Is(err, store.ErrNotFound) {
				t.Fatalf("ambiguous local session survived deletion: %v", err)
			}
			if fixture.runner.CancelCalls() != 0 || fixture.runner.CreateAgentCalls() != 0 {
				t.Fatal("local ambiguous deletion retried or cancelled remote work")
			}
		})
	}
}

func TestOrdinaryChatSecondTurnSupersedesLiveRun(t *testing.T) {
	fixture := newCursorDirectTestServer(t)
	sessionID := "ses-ordinary-supersede"
	if err := fixture.db.CreateSession(context.Background(), &store.Session{
		ID: sessionID, Title: "ordinary", Platform: "web", Meta: store.Meta{},
	}); err != nil {
		t.Fatal(err)
	}
	first := newLiveRun()
	second := newLiveRun()
	if err := fixture.server.reserveOrdinaryChat(context.Background(), sessionID, first); err != nil {
		t.Fatal(err)
	}
	if err := fixture.server.reserveOrdinaryChat(context.Background(), sessionID, second); err != nil {
		t.Fatalf("second ordinary turn lost supersede compatibility: %v", err)
	}
	if got := fixture.server.hub.get(sessionID); got != second {
		t.Fatalf("ordinary hub selected %p, want replacement %p", got, second)
	}
}

func TestCursorCancelDefinitiveFailuresRestoreStatusAndAllowRetry(t *testing.T) {
	for _, statusCode := range []int{
		http.StatusBadRequest,
		http.StatusUnauthorized,
		http.StatusForbidden,
		http.StatusConflict,
		http.StatusTooManyRequests,
	} {
		t.Run(http.StatusText(statusCode), func(t *testing.T) {
			fixture := newCursorDirectTestServer(t)
			sessionID := seedRecoverableCursorSession(t, fixture)
			fixture.runner.holdStream()
			fixture.runner.mu.Lock()
			fixture.runner.cancelErr = &cursor.APIError{
				Status: statusCode, Code: "definitive", Message: "rejected",
			}
			fixture.runner.mu.Unlock()

			if status := approveCursorCancel(t, fixture, sessionID); status != statusCode {
				t.Fatalf("definitive cancel status=%d, want %d", status, statusCode)
			}
			state, err := fixture.db.GetCursorSessionState(context.Background(), sessionID)
			if err != nil {
				t.Fatal(err)
			}
			if state.RemoteStatus != "RUNNING" {
				t.Fatalf("definitive failure left status=%q, want restored RUNNING", state.RemoteStatus)
			}

			fixture.runner.mu.Lock()
			fixture.runner.cancelErr = nil
			fixture.runner.mu.Unlock()
			if status := approveCursorCancel(t, fixture, sessionID); status != http.StatusOK {
				t.Fatalf("approved retry status=%d, want 200", status)
			}
			if fixture.runner.CancelCalls() != 2 {
				t.Fatalf("CancelRun calls=%d, want retry call", fixture.runner.CancelCalls())
			}
			fixture.runner.releaseStream()
		})
	}
}

func TestCursorCancelNotConfiguredRestoresStatusAndReturnsActionableError(t *testing.T) {
	fixture := newCursorDirectTestServer(t)
	sessionID := seedRecoverableCursorSession(t, fixture)
	fixture.runner.holdStream()
	fixture.runner.mu.Lock()
	fixture.runner.cancelErr = fmt.Errorf("runner options: %w", cursorrun.ErrNotConfigured)
	fixture.runner.mu.Unlock()

	result := approveCursorCancelResponse(t, fixture, sessionID)
	if result.status != http.StatusPreconditionRequired {
		t.Fatalf("not-configured cancellation status=%d, want 428", result.status)
	}
	if !strings.Contains(result.body, cursorrun.ErrNotConfigured.Error()) ||
		strings.Contains(strings.ToLower(result.body), "ambiguous") {
		t.Fatalf("not-configured cancellation response is not actionable: %q", result.body)
	}
	state, err := fixture.db.GetCursorSessionState(context.Background(), sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if state.RemoteStatus != "RUNNING" {
		t.Fatalf("not-configured cancellation left status=%q, want restored RUNNING",
			state.RemoteStatus)
	}

	fixture.runner.mu.Lock()
	fixture.runner.cancelErr = nil
	fixture.runner.mu.Unlock()
	if status := approveCursorCancel(t, fixture, sessionID); status != http.StatusOK {
		t.Fatalf("configured cancellation retry status=%d, want 200", status)
	}
	if fixture.runner.CancelCalls() != 2 {
		t.Fatalf("CancelRun calls=%d, want one definitive retry", fixture.runner.CancelCalls())
	}
	fixture.runner.releaseStream()
}

func TestCursorCancelNotFoundReconcilesNoActiveRun(t *testing.T) {
	fixture := newCursorDirectTestServer(t)
	sessionID := seedRecoverableCursorSession(t, fixture)
	fixture.runner.holdStream()
	fixture.runner.mu.Lock()
	fixture.runner.cancelErr = &cursor.APIError{
		Status: http.StatusNotFound, Code: "not_found", Message: "gone",
	}
	fixture.runner.mu.Unlock()

	if status := approveCursorCancel(t, fixture, sessionID); status != http.StatusOK {
		t.Fatalf("404 reconciliation status=%d, want 200", status)
	}
	state, err := fixture.db.GetCursorSessionState(context.Background(), sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if state.OperationState != store.CursorOperationIdle ||
		state.RemoteStatus != "ANTARES_CANCEL_NO_ACTIVE_RUN" ||
		state.ReuseValid {
		t.Fatalf("404 was not reconciled as no-active success: %+v", state)
	}
	if status := deleteCursorSession(t, fixture, sessionID); status != http.StatusOK {
		t.Fatalf("delete reconciled no-active session status=%d", status)
	}
	fixture.runner.releaseStream()
}

func TestCursorCancelUncertainFailuresRemainAmbiguousAndDeletable(t *testing.T) {
	tests := []struct {
		name string
		err  error
	}{
		{name: "context", err: context.DeadlineExceeded},
		{name: "transport", err: errors.New("connection reset")},
		{name: "api transport", err: &cursor.APIError{Status: 0, Message: "transport failed"}},
		{name: "request timeout", err: &cursor.APIError{Status: http.StatusRequestTimeout}},
		{name: "server", err: &cursor.APIError{Status: http.StatusInternalServerError}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newCursorDirectTestServer(t)
			sessionID := seedRecoverableCursorSession(t, fixture)
			fixture.runner.holdStream()
			fixture.runner.mu.Lock()
			fixture.runner.cancelErr = test.err
			fixture.runner.mu.Unlock()

			if status := approveCursorCancel(t, fixture, sessionID); status == http.StatusOK {
				t.Fatal("uncertain cancellation unexpectedly reported success")
			}
			state, err := fixture.db.GetCursorSessionState(context.Background(), sessionID)
			if err != nil {
				t.Fatal(err)
			}
			if state.RemoteStatus != cursorCancelAmbiguous {
				t.Fatalf("uncertain cancellation status=%q, want %q",
					state.RemoteStatus, cursorCancelAmbiguous)
			}
			if status := postCursorCancel(t, fixture, sessionID); status != http.StatusConflict {
				t.Fatalf("ambiguous cancellation retry status=%d, want 409", status)
			}
			if fixture.runner.CancelCalls() != 1 {
				t.Fatalf("ambiguous cancellation calls=%d, want exactly one", fixture.runner.CancelCalls())
			}
			if status := deleteCursorSession(t, fixture, sessionID); status != http.StatusOK {
				t.Fatalf("delete cancel-ambiguous session status=%d", status)
			}
			fixture.runner.releaseStream()
		})
	}
}

func TestCursorCancelAmbiguousResponseIsBoundedAndNonRetryable(t *testing.T) {
	secret := "round2-cancel-upstream-secret"
	tests := []struct {
		name string
		err  error
	}{
		{
			name: "request timeout",
			err: &cursor.APIError{
				Status:  http.StatusRequestTimeout,
				Message: secret + strings.Repeat("x", 8<<10),
			},
		},
		{
			name: "server",
			err: &cursor.APIError{
				Status:  http.StatusServiceUnavailable,
				Message: secret + strings.Repeat("x", 8<<10),
			},
		},
		{name: "context", err: fmt.Errorf("%s: %w", secret, context.DeadlineExceeded)},
		{name: "transport", err: errors.New(secret + strings.Repeat("x", 8<<10))},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newCursorDirectTestServer(t)
			sessionID := seedRecoverableCursorSession(t, fixture)
			fixture.runner.holdStream()
			fixture.runner.mu.Lock()
			fixture.runner.cancelErr = test.err
			fixture.runner.mu.Unlock()

			result := approveCursorCancelResponse(t, fixture, sessionID)
			if result.status != http.StatusBadGateway {
				t.Fatalf("ambiguous response status=%d, want 502", result.status)
			}
			lower := strings.ToLower(result.body)
			if !strings.Contains(lower, "ambiguous") ||
				!strings.Contains(lower, "will not be retried") {
				t.Fatalf("ambiguous response is not explicit: %q", result.body)
			}
			if strings.Contains(result.body, secret) || len(result.body) > 1024 {
				t.Fatalf("ambiguous response leaked or exceeded bound: bytes=%d body=%q",
					len(result.body), result.body)
			}
			fixture.runner.releaseStream()
		})
	}
}

func TestCursorCancelInFlightRecoveryBecomesAmbiguousWithoutResubmission(t *testing.T) {
	fixture := newCursorDirectTestServer(t)
	sessionID := seedRecoverableCursorSession(t, fixture)
	_, err := fixture.server.mutateCursorState(context.Background(), sessionID,
		func(state *store.CursorSessionState) error {
			state.RemoteStatus = cursorCancelInFlight
			return nil
		})
	if err != nil {
		t.Fatal(err)
	}
	fixture.runner.holdStream()
	attach := getCursorAttach(t, fixture, sessionID)
	defer attach.Close()

	waitCursorState(t, fixture.db, sessionID, func(state *store.CursorSessionState) bool {
		return state.RemoteStatus == cursorCancelAmbiguous
	})
	if fixture.runner.CancelCalls() != 0 {
		t.Fatal("restart recovery resubmitted an in-flight cancellation")
	}
	if status := deleteCursorSession(t, fixture, sessionID); status != http.StatusOK {
		t.Fatalf("delete recovered cancel-ambiguous session status=%d", status)
	}
	fixture.runner.releaseStream()
}

func TestCursorCancelInFlightMarkerAfterRestartIsLocallyDeletable(t *testing.T) {
	fixture := newCursorDirectTestServer(t)
	sessionID := seedRecoverableCursorSession(t, fixture)
	_, err := fixture.server.mutateCursorState(context.Background(), sessionID,
		func(state *store.CursorSessionState) error {
			state.RemoteStatus = cursorCancelInFlight
			return nil
		})
	if err != nil {
		t.Fatal(err)
	}

	if status := deleteCursorSession(t, fixture, sessionID); status != http.StatusOK {
		t.Fatalf("delete crash-marker session status=%d, want 200", status)
	}
	if fixture.runner.CancelCalls() != 0 {
		t.Fatal("local reconciliation resubmitted the persisted cancellation marker")
	}
}

func TestCursorCancelReservationCoversWholeSession(t *testing.T) {
	fixture := newCursorDirectTestServer(t)
	if !fixture.server.reserveCursorCancel("ses-one", "run-one") {
		t.Fatal("first cancellation reservation failed")
	}
	defer fixture.server.releaseCursorCancel("ses-one", "run-one")
	if fixture.server.reserveCursorCancel("ses-one", "run-two") {
		t.Fatal("different run bypassed the session cancellation reservation")
	}
}

func TestCursorApprovalCarriesRepositoryPreflightWarnings(t *testing.T) {
	fixture := newCursorDirectTestServer(t)
	dir := initCursorWarningRepository(t)
	request := defaultCursorChatRequest()
	request.ProjectDir = dir

	session, _, projectDir, err := fixture.server.cursorSessionCandidate(
		context.Background(), request,
	)
	if err != nil {
		t.Fatal(err)
	}
	plan, _, err := fixture.server.prepareCursorTurn(
		context.Background(), request, session, projectDir,
	)
	if err != nil {
		t.Fatal(err)
	}
	var projection struct {
		WorktreeDirty  bool     `json:"worktree_dirty"`
		LocalOnly      int      `json:"local_only_commits"`
		RemoteRefKnown bool     `json:"remote_ref_known"`
		Warnings       []string `json:"warnings"`
	}
	if err := json.Unmarshal([]byte(plan.approvalArguments), &projection); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(plan.approvalArguments, "warning-secret") {
		t.Fatal("approval projection leaked dirty-worktree content")
	}
	if !projection.WorktreeDirty || projection.LocalOnly != 1 ||
		!projection.RemoteRefKnown || len(projection.Warnings) != 2 {
		t.Fatalf("approval lost repository preflight: %+v", projection)
	}
	for _, warning := range projection.Warnings {
		if len([]rune(warning)) > 240 ||
			!strings.Contains(warning, "cloud VM") {
			t.Fatalf("warning is not fixed and bounded: %q", warning)
		}
	}
}

func TestCursorApprovalWarnsWhenRemoteRefCannotBeVerified(t *testing.T) {
	fixture := newCursorDirectTestServer(t)
	dir := initCursorWarningRepository(t)
	runCursorWarningGit(t, dir, "reset", "--hard", "HEAD")
	runCursorWarningGit(t, dir, "update-ref", "-d", "refs/remotes/origin/main")
	request := defaultCursorChatRequest()
	request.ProjectDir = dir

	session, _, projectDir, err := fixture.server.cursorSessionCandidate(
		context.Background(), request,
	)
	if err != nil {
		t.Fatal(err)
	}
	plan, _, err := fixture.server.prepareCursorTurn(
		context.Background(), request, session, projectDir,
	)
	if err != nil {
		t.Fatal(err)
	}
	var projection struct {
		RemoteRefKnown bool     `json:"remote_ref_known"`
		Warnings       []string `json:"warnings"`
	}
	if err := json.Unmarshal([]byte(plan.approvalArguments), &projection); err != nil {
		t.Fatal(err)
	}
	if projection.RemoteRefKnown || len(projection.Warnings) != 1 ||
		!strings.Contains(projection.Warnings[0], "cannot verify") {
		t.Fatalf("unknown remote ref warning missing: %+v", projection)
	}
}

func TestCursorAttachRecoveryRequiresProtectionBeforeRunnerCalls(t *testing.T) {
	fixture := newCursorDirectTestServer(t)
	fixture.cfg.Server.AuthToken = ""
	sessionID := seedRecoverableCursorSession(t, fixture)

	status, _ := cursorAttachStatus(t, fixture, sessionID, false)
	if status != http.StatusPreconditionRequired {
		t.Fatalf("unprotected Cursor recovery attach status=%d, want 428", status)
	}
	if fixture.runner.GetRunCalls() != 0 || len(fixture.runner.StreamCalls()) != 0 {
		t.Fatalf("Cursor runner used before protection: get=%d stream=%d",
			fixture.runner.GetRunCalls(), len(fixture.runner.StreamCalls()))
	}
}

func TestCursorAttachProtectionPreservesOrdinaryLiveAndDone(t *testing.T) {
	fixture := newCursorDirectTestServer(t)
	fixture.cfg.Server.AuthToken = ""

	ordinary := newLiveRun()
	ordinary.publish(agent.Event{Type: agent.EventNotice, Message: "ordinary"})
	ordinary.publish(agent.Event{Type: agent.EventDone})
	ordinary.finish()
	fixture.server.hub.put("ses-ordinary-live", ordinary)
	if status, body := cursorAttachStatus(t, fixture, "ses-ordinary-live", false); status != http.StatusOK ||
		!strings.Contains(body, "ordinary") {
		t.Fatalf("ordinary live attach status=%d body=%q", status, body)
	}
	if status, body := cursorAttachStatus(t, fixture, "ses-no-live", false); status != http.StatusOK ||
		!strings.Contains(body, `"done"`) {
		t.Fatalf("ordinary done attach status=%d body=%q", status, body)
	}

	direct := newCursorLiveRun(liveRunCursorDirect)
	direct.publish(agent.Event{Type: agent.EventDone})
	direct.finish()
	fixture.server.hub.put("ses-direct-live", direct)
	if status, _ := cursorAttachStatus(t, fixture, "ses-direct-live", false); status != http.StatusPreconditionRequired {
		t.Fatalf("unprotected direct live attach status=%d, want 428", status)
	}
}

func TestCursorAttachCursorResetTargetsFreshRecoveryLogOnly(t *testing.T) {
	tests := []struct {
		name string
		live *liveRun
		want bool
	}{
		{
			name: "own recovery",
			live: newCursorLiveRun(liveRunCursorRecovery), want: true,
		},
		{
			name: "another recovery winner",
			live: newCursorLiveRun(liveRunCursorRecovery), want: true,
		},
		{
			name: "concurrent direct run",
			live: newCursorLiveRun(liveRunCursorDirect), want: false,
		},
		{
			name: "concurrent ordinary run",
			live: newLiveRun(), want: false,
		},
		{
			name: "existing recovery reconnect",
			live: newCursorLiveRun(liveRunCursorRecovery), want: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := cursorAttachShouldReset(test.live); got != test.want {
				t.Fatalf("cursor reset=%v, want %v", got, test.want)
			}
		})
	}
}

func TestCursorAttachEverySequentialRecoveryFollowerStartsAtZero(t *testing.T) {
	fixture := newCursorDirectTestServer(t)
	sessionID := "ses-sequential-recovery-log"
	recovery := newCursorLiveRun(liveRunCursorRecovery)
	recovery.publish(agent.Event{Type: agent.EventReset})
	recovery.publish(agent.Event{Type: agent.EventText, Delta: "durable replay"})
	recovery.publish(agent.Event{Type: agent.EventDone})
	recovery.finish()
	fixture.server.hub.put(sessionID, recovery)

	for follower := 1; follower <= 2; follower++ {
		stream := getCursorAttachAt(t, fixture, sessionID, 999)
		stream.NextType(t, agent.EventReset)
		if event := stream.NextType(t, agent.EventText); event.Delta != "durable replay" {
			t.Fatalf("follower %d replay=%q", follower, event.Delta)
		}
		stream.NextType(t, agent.EventDone)
		stream.Close()
	}
}

func TestCursorAttachConcurrentFollowersResetToRecoveryWinner(t *testing.T) {
	fixture := newCursorDirectTestServer(t)
	sessionID := seedRecoverableCursorSession(t, fixture)
	fixture.runner.holdGetRun()
	defer fixture.runner.releaseGetRun()
	winner := fixture.server.cursorRecoveryRun(sessionID)
	if winner == nil || winner.runKind() != liveRunCursorRecovery {
		t.Fatal("did not reserve a recovery winner")
	}
	select {
	case <-fixture.runner.getRunStarted:
	case <-time.After(time.Second):
		t.Fatal("recovery winner did not reach GetRun")
	}

	streams := make(chan *sseTestStream, 2)
	for range 2 {
		go func() {
			streams <- getCursorAttachAt(t, fixture, sessionID, 999)
		}()
	}
	first, second := <-streams, <-streams
	fixture.runner.releaseGetRun()
	for follower, stream := range []*sseTestStream{first, second} {
		stream.NextType(t, agent.EventReset)
		if event := stream.NextType(t, agent.EventReasoning); event.Delta != "old reasoning" {
			t.Fatalf("follower %d reasoning replay=%q", follower+1, event.Delta)
		}
		if event := stream.NextType(t, agent.EventText); event.Delta != "old text" {
			t.Fatalf("follower %d text replay=%q", follower+1, event.Delta)
		}
		stream.Close()
	}
}

func TestCursorTerminalRecoveryReservationBlocksConcurrentDelete(t *testing.T) {
	fixture := newCursorDirectTestServer(t)
	sessionID := seedRecoverableCursorSession(t, fixture)
	_, err := fixture.server.mutateCursorState(context.Background(), sessionID,
		func(state *store.CursorSessionState) error {
			state.OperationState = store.CursorOperationTerminal
			state.RemoteStatus = "FINISHED"
			return nil
		})
	if err != nil {
		t.Fatal(err)
	}
	fixture.runner.mu.Lock()
	fixture.runner.getRun = cursor.Run{Status: "FINISHED", Result: "recovered"}
	fixture.runner.mu.Unlock()
	fixture.runner.holdGetRun()
	defer fixture.runner.releaseGetRun()

	attachResult := make(chan int, 1)
	go func() {
		status, _ := cursorAttachStatus(t, fixture, sessionID, true)
		attachResult <- status
	}()
	select {
	case <-fixture.runner.getRunStarted:
	case <-time.After(time.Second):
		t.Fatal("terminal recovery did not reach GetRun")
	}

	if status := deleteCursorSession(t, fixture, sessionID); status != http.StatusConflict {
		t.Fatalf("delete racing terminal recovery status=%d, want 409", status)
	}
	if _, err := fixture.db.GetSession(context.Background(), sessionID); err != nil {
		t.Fatalf("terminal recovery race deleted local session: %v", err)
	}
	fixture.runner.releaseGetRun()
	select {
	case status := <-attachResult:
		if status != http.StatusOK {
			t.Fatalf("terminal attach status=%d", status)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("terminal attach did not finish")
	}
}

func TestCursorTerminalRecoveryKeepsEditAtomic(t *testing.T) {
	fixture := newCursorDirectTestServer(t)
	sessionID := seedRecoverableCursorSession(t, fixture)
	_, err := fixture.server.mutateCursorState(context.Background(), sessionID,
		func(state *store.CursorSessionState) error {
			state.OperationState = store.CursorOperationTerminal
			state.RemoteStatus = "FINISHED"
			return nil
		})
	if err != nil {
		t.Fatal(err)
	}
	state, err := fixture.db.GetCursorSessionState(context.Background(), sessionID)
	if err != nil {
		t.Fatal(err)
	}
	userMessageID := state.UserMessageID
	fixture.runner.mu.Lock()
	fixture.runner.getRun = cursor.Run{Status: "FINISHED", Result: "recovered"}
	fixture.runner.mu.Unlock()
	fixture.runner.holdGetRun()
	defer fixture.runner.releaseGetRun()

	attachResult := make(chan int, 1)
	go func() {
		status, _ := cursorAttachStatus(t, fixture, sessionID, true)
		attachResult <- status
	}()
	select {
	case <-fixture.runner.getRunStarted:
	case <-time.After(time.Second):
		t.Fatal("terminal recovery did not reach GetRun")
	}
	if status := editCursorMessage(t, fixture, sessionID, userMessageID); status != http.StatusConflict {
		t.Fatalf("edit racing terminal recovery status=%d, want 409", status)
	}
	fixture.runner.releaseGetRun()
	select {
	case <-attachResult:
	case <-time.After(2 * time.Second):
		t.Fatal("terminal recovery did not finish")
	}
	if status := editCursorMessage(t, fixture, sessionID, userMessageID); status != http.StatusOK {
		t.Fatalf("edit after terminal finalization status=%d, want 200", status)
	}
}

func TestCursorLifecycleSlowSessionDoesNotBlockUnrelatedSession(t *testing.T) {
	fixture := newCursorDirectTestServer(t)
	for _, id := range []string{"session-slow", "session-fast"} {
		if err := fixture.db.CreateSession(context.Background(), &store.Session{
			ID: id, Title: id, Platform: "web", Meta: store.Meta{},
		}); err != nil {
			t.Fatal(err)
		}
	}
	blocking := &blockingCursorStateStore{
		Store: fixture.db, sessionID: "session-slow",
		started: make(chan struct{}), release: make(chan struct{}),
	}
	fixture.server.db = blocking

	slowResult := make(chan error, 1)
	go func() {
		slowResult <- fixture.server.reserveOrdinaryChat(
			context.Background(), "session-slow", newLiveRun(),
		)
	}()
	select {
	case <-blocking.started:
	case <-time.After(time.Second):
		t.Fatal("slow session did not enter cursor-state lookup")
	}
	fastResult := make(chan error, 1)
	go func() {
		fastResult <- fixture.server.reserveOrdinaryChat(
			context.Background(), "session-fast", newLiveRun(),
		)
	}()

	var fastErr error
	blocked := false
	select {
	case fastErr = <-fastResult:
	case <-time.After(200 * time.Millisecond):
		blocked = true
	}
	close(blocking.release)
	if err := <-slowResult; err != nil {
		t.Fatalf("slow reservation failed: %v", err)
	}
	if blocked {
		fastErr = <-fastResult
	}
	if fastErr != nil {
		t.Fatalf("unrelated reservation failed: %v", fastErr)
	}
	if blocked {
		t.Fatal("slow session lifecycle blocked an unrelated session")
	}
}

type blockingCursorStateStore struct {
	store.Store
	sessionID string
	started   chan struct{}
	release   chan struct{}
	once      sync.Once
}

func (s *blockingCursorStateStore) GetCursorSessionState(
	ctx context.Context,
	sessionID string,
) (*store.CursorSessionState, error) {
	if sessionID == s.sessionID {
		s.once.Do(func() { close(s.started) })
		select {
		case <-s.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return s.Store.GetCursorSessionState(ctx, sessionID)
}

func cursorAttachStatus(
	t *testing.T,
	fixture *cursorDirectFixture,
	sessionID string,
	authenticated bool,
) (int, string) {
	t.Helper()
	request, err := http.NewRequest(
		http.MethodGet,
		fixture.http.URL+"/api/chat/attach?session_id="+sessionID+"&cursor=0",
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if authenticated {
		request.Header.Set("Authorization", "Bearer test-token")
	}
	response, err := fixture.http.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return response.StatusCode, string(raw)
}

func approveCursorCancel(
	t *testing.T,
	fixture *cursorDirectFixture,
	sessionID string,
) int {
	t.Helper()
	return approveCursorCancelResponse(t, fixture, sessionID).status
}

type cursorCancelResponse struct {
	status int
	body   string
}

func approveCursorCancelResponse(
	t *testing.T,
	fixture *cursorDirectFixture,
	sessionID string,
) cursorCancelResponse {
	t.Helper()
	result := make(chan cursorCancelResponse, 1)
	go func() {
		status, body := postCursorCancelResponse(t, fixture, sessionID)
		result <- cursorCancelResponse{status: status, body: body}
	}()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		for _, pending := range fixture.server.agent.PendingApprovals() {
			if pending.SessionID == sessionID && pending.Tool == "cursor_direct_cancel" {
				if !fixture.server.agent.ResolveApproval(pending.ID, true) {
					t.Fatal("could not resolve Cursor cancellation approval")
				}
				select {
				case response := <-result:
					return response
				case <-time.After(2 * time.Second):
					t.Fatal("approved Cursor cancellation did not return")
				}
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("Cursor cancellation approval did not appear")
	return cursorCancelResponse{}
}

func initCursorWarningRepository(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	runCursorWarningGit(t, dir, "init")
	runCursorWarningGit(t, dir, "checkout", "-b", "main")
	runCursorWarningGit(t, dir, "config", "user.email", "cursor@example.com")
	runCursorWarningGit(t, dir, "config", "user.name", "Cursor Test")
	path := filepath.Join(dir, "work.txt")
	if err := os.WriteFile(path, []byte("base\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runCursorWarningGit(t, dir, "add", "work.txt")
	runCursorWarningGit(t, dir, "commit", "-m", "base")
	runCursorWarningGit(t, dir, "remote", "add", "origin", "https://github.com/acme/repo.git")
	runCursorWarningGit(t, dir, "update-ref", "refs/remotes/origin/main", "HEAD")
	if err := os.WriteFile(path, []byte("local commit\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runCursorWarningGit(t, dir, "add", "work.txt")
	runCursorWarningGit(t, dir, "commit", "-m", "local")
	if err := os.WriteFile(path, []byte("dirty token=warning-secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func runCursorWarningGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, output)
	}
}
