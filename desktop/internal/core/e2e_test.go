package core

import (
	"context"
	"errors"
	"net/http"
	"net/http/cookiejar"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/enowdev/antares/desktop/internal/api"
	"github.com/enowdev/antares/desktop/internal/conns"
	"github.com/enowdev/antares/desktop/internal/secrets"
)

// TestRemoteAgainstRealServer runs the remote flow against a real Antares:
//
//	ANTARES_DESKTOP_E2E_URL=http://127.0.0.1:18951 \
//	ANTARES_DESKTOP_E2E_PASSWORD=pw-smoke-123 go test -run RealServer -v ./internal/core/
//
// Point it at an isolated server (a smokefixture home), never a real one: it
// pairs a device and revokes it again.
func TestRemoteAgainstRealServer(t *testing.T) {
	base := os.Getenv("ANTARES_DESKTOP_E2E_URL")
	if base == "" {
		t.Skip("set ANTARES_DESKTOP_E2E_URL (and _PASSWORD) to run against a real server")
	}
	password := os.Getenv("ANTARES_DESKTOP_E2E_PASSWORD")
	ctx := context.Background()
	store, _ := conns.Open(filepath.Join(t.TempDir(), "connections.json"))
	nav := &fakeNav{}
	keys := secrets.NewMemory()
	m := New(Deps{Store: store, Secrets: keys, Nav: nav, MachineName: "Desktop E2E", Platform: "desktop-macos", AppVersion: "0.1.0"})
	defer m.SwitchConnection()

	if _, err := m.AddRemote(ctx, "E2E", base, "definitely-wrong"); !errors.Is(err, api.ErrBadPassword) {
		t.Fatalf("wrong password: %v", err)
	}
	v, err := m.AddRemote(ctx, "E2E", base, password)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("paired %s as device %s", v.ID, mustConn(t, store, v.ID).DeviceID)

	if err := m.Open(ctx, v.ID); err != nil {
		t.Fatal(err)
	}
	href := nav.lastURL()
	if !strings.Contains(href, "/auth/handoff?code=") {
		t.Fatalf("navigated to %q", href)
	}
	// The webview's view of it: follow the handoff, then the session cookie
	// alone must authorize the API.
	jar, _ := cookiejar.New(nil)
	browser := &http.Client{Jar: jar}
	resp, err := browser.Get(href)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if strings.HasPrefix(resp.Request.URL.Path, "/login") {
		t.Fatalf("handoff landed on %s", resp.Request.URL)
	}
	resp, err = browser.Get(strings.TrimRight(base, "/") + "/api/devices")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("session cookie not accepted by /api/devices: %d", resp.StatusCode)
	}
	// A used code is refused.
	resp, _ = (&http.Client{}).Get(href)
	resp.Body.Close()
	if !strings.HasPrefix(resp.Request.URL.Path, "/login") {
		t.Fatalf("code reused: landed on %s", resp.Request.URL)
	}

	// Remove revokes the device; its token is then refused.
	token, _ := keys.Get(v.ID)
	dev := mustConn(t, store, v.ID).DeviceID
	if err := m.Remove(ctx, v.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := api.New(base, token).Handoff(ctx, "/"); !errors.Is(err, api.ErrSignedOut) {
		t.Fatalf("token of removed device %s still works: %v", dev, err)
	}
}

func mustConn(t *testing.T, s *conns.Store, id string) conns.Connection {
	t.Helper()
	c, err := s.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	return c
}
