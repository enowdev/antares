package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/enowdev/antares/internal/config"
)

func TestDashSessionsSurviveRestart(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ANTARES_HOME", home)
	if err := config.EnsureHome(); err != nil {
		t.Fatal(err)
	}

	cfg := config.Default()
	hash, err := config.HashPassword("test-pass")
	if err != nil {
		t.Fatal(err)
	}
	cfg.Server.DashboardPasswordHash = hash

	s1 := New(Options{Config: cfg})
	// Simulate login.
	tok := newSessionToken()
	exp := time.Now().Add(time.Hour)
	s1.dashMu.Lock()
	s1.dashSessions[tok] = exp
	s1.persistDashSessionsLocked()
	s1.dashMu.Unlock()

	path := filepath.Join(home, "dash_sessions.json")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("session file not written: %v", err)
	}
	raw, _ := os.ReadFile(path)
	var m map[string]int64
	if err := json.Unmarshal(raw, &m); err != nil || m[tok] == 0 {
		t.Fatalf("file content = %s err=%v", raw, err)
	}

	// New server process after restart: map empty until load.
	s2 := New(Options{Config: cfg})
	req := httptest.NewRequest(http.MethodGet, "/api/sessions", nil)
	req.AddCookie(&http.Cookie{Name: dashCookie, Value: tok})
	if !s2.dashSessionValid(req) {
		t.Fatal("dashboard session not restored after restart — attach would 401")
	}
}

func TestInvalidateDashSessionsClearsFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ANTARES_HOME", home)
	_ = config.EnsureHome()

	s := New(Options{Config: config.Default()})
	s.dashMu.Lock()
	s.dashSessions["tok"] = time.Now().Add(time.Hour)
	s.persistDashSessionsLocked()
	s.dashMu.Unlock()

	s.invalidateDashSessions()
	raw, err := os.ReadFile(dashSessionsFile())
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "{}" && string(raw) != "null" {
		var m map[string]int64
		_ = json.Unmarshal(raw, &m)
		if len(m) != 0 {
			t.Fatalf("expected empty sessions file, got %s", raw)
		}
	}
}
