package core

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/enowdev/antares/desktop/internal/api"
	"github.com/enowdev/antares/desktop/internal/conns"
	"github.com/enowdev/antares/desktop/internal/daemon"
	"github.com/enowdev/antares/desktop/internal/mockserver"
	"github.com/enowdev/antares/desktop/internal/secrets"
)

var bg = context.Background()

type fakeNav struct {
	mu     sync.Mutex
	events []string
	urls   []string
}

func (n *fakeNav) ShowConnections() { n.add("connections", "") }
func (n *fakeNav) ShowReconnect()   { n.add("reconnect", "") }
func (n *fakeNav) Navigate(u string) {
	n.add("navigate", u)
}
func (n *fakeNav) Changed() {}
func (n *fakeNav) add(ev, u string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.events = append(n.events, ev)
	if u != "" {
		n.urls = append(n.urls, u)
	}
}
func (n *fakeNav) last() string {
	n.mu.Lock()
	defer n.mu.Unlock()
	if len(n.events) == 0 {
		return ""
	}
	return n.events[len(n.events)-1]
}
func (n *fakeNav) lastURL() string {
	n.mu.Lock()
	defer n.mu.Unlock()
	if len(n.urls) == 0 {
		return ""
	}
	return n.urls[len(n.urls)-1]
}

// fakeCLI is the antares CLI against a mock "local" server: pair goes
// straight to the mock (as the real CLI writes the local store), serve
// starts the mock and writes the daemon state file.
type fakeCLI struct {
	home    string
	srv     *mockserver.Server
	serves  int
	stops   int
	revoked []string
	pairErr error
}

func (f *fakeCLI) Pair(ctx context.Context, name, platform string) (daemon.PairResult, error) {
	var r daemon.PairResult
	if f.pairErr != nil {
		return r, f.pairErr
	}
	p, err := api.New(f.srv.URL, "srv-token").Pair(ctx, name, platform, "")
	if err != nil {
		return r, err
	}
	r.Token, r.Device.ID, r.Device.Name, r.Device.Platform = p.Token, p.Device.ID, p.Device.Name, p.Device.Platform
	return r, nil
}
func (f *fakeCLI) Revoke(ctx context.Context, id string) error {
	f.revoked = append(f.revoked, id)
	return nil
}
func (f *fakeCLI) Serve(ctx context.Context) error {
	f.serves++
	f.srv.SetHealthy(true)
	return os.WriteFile(filepath.Join(f.home, "antares.pid"),
		[]byte(fmt.Sprintf(`{"pid": %d, "exe": "/x/antares", "url": %q, "version": "v-test"}`, os.Getpid(), f.srv.URL)), 0o600)
}
func (f *fakeCLI) Stop(ctx context.Context) error {
	f.stops++
	f.srv.SetHealthy(false)
	return os.Remove(filepath.Join(f.home, "antares.pid"))
}

type rig struct {
	m     *Manager
	nav   *fakeNav
	store *conns.Store
	keys  *secrets.Memory
	cli   *fakeCLI
	local *mockserver.Server
}

func newRig(t *testing.T) *rig {
	t.Helper()
	dir := t.TempDir()
	store, err := conns.Open(filepath.Join(dir, "cfg", "connections.json"))
	if err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(dir, "home")
	os.MkdirAll(home, 0o700)
	local := mockserver.Start(mockserver.Options{Password: "pw", AuthToken: "srv-token"})
	t.Cleanup(local.Close)
	local.SetHealthy(false) // "not running" until serve
	cli := &fakeCLI{home: home, srv: local}
	nav := &fakeNav{}
	keys := secrets.NewMemory()
	m := New(Deps{
		Store:   store,
		Secrets: keys,
		Resolver: daemon.Resolver{Executable: "/A.app/Contents/MacOS/A", GOOS: "darwin", HomeDir: dir,
			LookPath: func(string) (string, error) { return "", errors.New("no") },
			IsFile:   func(p string) bool { return p == "/A.app/Contents/Resources/antares" }},
		Probe:          daemon.Probe{Home: home},
		CLI:            func(string) LocalCLI { return cli },
		Nav:            nav,
		MachineName:    "Test Mac",
		Platform:       "desktop-macos",
		AppVersion:     "0.1.0",
		PollEvery:      30 * time.Millisecond,
		ReconnectEvery: 20 * time.Millisecond,
		StartTimeout:   2 * time.Second,
	})
	t.Cleanup(m.SwitchConnection)
	return &rig{m: m, nav: nav, store: store, keys: keys, cli: cli, local: local}
}

// follow opens a dashboard URL like the webview would and returns the final
// path, proving the handoff signs in.
func follow(t *testing.T, u string) string {
	t.Helper()
	jar, _ := cookiejar.New(nil)
	resp, err := (&http.Client{Jar: jar}).Get(u)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp.Request.URL.Path
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestRemoteAddOpenRemove(t *testing.T) {
	r := newRig(t)
	srv := mockserver.Start(mockserver.Options{Password: "secret"})
	defer srv.Close()

	if _, err := r.m.AddRemote(bg, "VPS", srv.URL, "wrong"); !errors.Is(err, api.ErrBadPassword) {
		t.Fatalf("wrong password: %v", err)
	}
	if len(r.store.List()) != 0 {
		t.Fatal("saved after a failed pair")
	}
	v, err := r.m.AddRemote(bg, "VPS", srv.URL+"/", "secret")
	if err != nil {
		t.Fatal(err)
	}
	if v.Mode != conns.ModeRemote || v.URL != srv.URL || v.Status != StatusOnline {
		t.Fatalf("view: %+v", v)
	}
	tok, err := r.keys.Get(v.ID)
	if err != nil || !strings.HasPrefix(tok, "atd_") {
		t.Fatalf("token not in keychain: %q %v", tok, err)
	}
	if b, _ := os.ReadFile(r.store.Path()); strings.Contains(string(b), tok) {
		t.Fatal("token written to connections.json")
	}

	if err := r.m.Open(bg, v.ID); err != nil {
		t.Fatal(err)
	}
	u := r.nav.lastURL()
	if !strings.HasPrefix(u, srv.URL+"/auth/handoff?code=ahc_") {
		t.Fatalf("navigated to %q", u)
	}
	if got := follow(t, u); got != "/" {
		t.Fatalf("handoff landed on %s", got)
	}
	st := r.m.State()
	if st.Phase != PhaseOpen || st.Current != v.ID || r.store.Last() != v.ID || st.CurrentName != "VPS" {
		t.Fatalf("state: %+v", st)
	}

	if err := r.m.Remove(bg, v.ID); err != nil {
		t.Fatal(err)
	}
	if srv.ActiveDevices() != 0 {
		t.Fatal("device not revoked on remove")
	}
	if _, err := r.keys.Get(v.ID); !errors.Is(err, secrets.ErrNotFound) {
		t.Fatal("keychain entry left behind")
	}
	if len(r.store.List()) != 0 || r.store.Last() != "" || r.m.State().Phase != PhaseConnections {
		t.Fatalf("not removed: %+v", r.m.State())
	}
}

func TestRemoteRemoveWorksWhenServerGone(t *testing.T) {
	r := newRig(t)
	srv := mockserver.Start(mockserver.Options{})
	v, err := r.m.AddRemote(bg, "", srv.URL, "")
	if err != nil {
		t.Fatal(err)
	}
	if v.Name != "127.0.0.1" {
		t.Fatalf("default name %q", v.Name)
	}
	srv.Close()
	if err := r.m.Remove(bg, v.ID); err != nil {
		t.Fatalf("remove with server down: %v", err)
	}
}

func TestOldServerRefusedWithClearMessage(t *testing.T) {
	r := newRig(t)
	srv := mockserver.Start(mockserver.Options{Old: true})
	defer srv.Close()
	_, err := r.m.AddRemote(bg, "Old", srv.URL, "pw")
	if !errors.Is(err, api.ErrTooOld) || !strings.Contains(err.Error(), "too old") {
		t.Fatalf("got %v", err)
	}
}

func TestPublicHTTPRefused(t *testing.T) {
	r := newRig(t)
	if _, err := r.m.AddRemote(bg, "x", "http://example.com", "pw"); err == nil || !strings.Contains(err.Error(), "https") {
		t.Fatalf("got %v", err)
	}
}

func TestRevokedDeviceIsSignedOut(t *testing.T) {
	r := newRig(t)
	srv := mockserver.Start(mockserver.Options{Password: "pw"})
	defer srv.Close()
	v, err := r.m.AddRemote(bg, "VPS", srv.URL, "pw")
	if err != nil {
		t.Fatal(err)
	}
	srv.RevokeAll()
	err = r.m.Open(bg, v.ID)
	if !errors.Is(err, api.ErrSignedOut) {
		t.Fatalf("open after revoke: %v", err)
	}
	st := r.m.State()
	if st.Connections[0].Status != StatusSignedOut || st.Phase != PhaseConnections || !strings.Contains(st.Notice, "pair again") {
		t.Fatalf("state: %+v", st)
	}
	if r.nav.last() != "connections" {
		t.Fatalf("nav: %v", r.nav.events)
	}
	// Re-pairing restores it under the same id.
	if err := r.m.Repair(bg, v.ID, "pw"); err != nil {
		t.Fatal(err)
	}
	if err := r.m.Open(bg, v.ID); err != nil {
		t.Fatal(err)
	}
	if got := r.store.List(); len(got) != 1 || got[0].ID != v.ID {
		t.Fatalf("repair changed the list: %+v", got)
	}
}

func TestReconnectReturnsToSamePath(t *testing.T) {
	r := newRig(t)
	srv := mockserver.Start(mockserver.Options{Password: "pw"})
	defer srv.Close()
	v, _ := r.m.AddRemote(bg, "VPS", srv.URL, "pw")
	if err := r.m.Open(bg, v.ID); err != nil {
		t.Fatal(err)
	}
	r.m.PagePath(srv.URL, "/c/ses_42")
	r.m.PagePath("https://elsewhere.example", "/evil") // other origins ignored

	srv.SetHealthy(false)
	waitFor(t, "reconnect page", func() bool { return r.nav.last() == "reconnect" })
	if r.m.State().Phase != PhaseReconnecting {
		t.Fatal(r.m.State().Phase)
	}
	srv.SetHealthy(true)
	waitFor(t, "recovery", func() bool { return r.m.State().Phase == PhaseOpen })
	u := r.nav.lastURL()
	if !strings.Contains(u, "next=%2Fc%2Fses_42") {
		t.Fatalf("recovered to %q", u)
	}
	if got := follow(t, u); got != "/c/ses_42" {
		t.Fatalf("landed on %s", got)
	}
}

func TestRetryKeepsReconnectPageWhileDown(t *testing.T) {
	r := newRig(t)
	srv := mockserver.Start(mockserver.Options{})
	v, _ := r.m.AddRemote(bg, "VPS", srv.URL, "")
	r.m.Open(bg, v.ID)
	srv.Close()
	waitFor(t, "reconnect page", func() bool { return r.m.State().Phase == PhaseReconnecting })
	if err := r.m.Retry(bg); !errors.Is(err, api.ErrUnreachable) {
		t.Fatalf("retry: %v", err)
	}
	if r.m.State().Phase != PhaseReconnecting || r.m.State().Current != v.ID {
		t.Fatalf("retry left reconnect: %+v", r.m.State())
	}
}

func TestLocalAddStartsDaemonAndStopIsExplicit(t *testing.T) {
	r := newRig(t)
	st := r.m.Refresh(bg)
	if !st.Local.Installed || st.Local.Running || st.Local.Source != "bundled" {
		t.Fatalf("local view: %+v", st.Local)
	}
	v, err := r.m.AddLocal(bg)
	if err != nil {
		t.Fatal(err)
	}
	if v.Mode != conns.ModeLocal || v.URL != "" || v.Name != "This Mac" || v.Status != StatusStopped {
		t.Fatalf("view: %+v", v)
	}
	if _, err := r.m.AddLocal(bg); !errors.Is(err, conns.ErrLocalExists) {
		t.Fatalf("second local: %v", err)
	}
	if err := r.m.Open(bg, v.ID); err != nil {
		t.Fatal(err)
	}
	if r.cli.serves != 1 {
		t.Fatalf("serve ran %d times", r.cli.serves)
	}
	if u := r.nav.lastURL(); !strings.HasPrefix(u, r.local.URL+"/auth/handoff?") || follow(t, u) != "/" {
		t.Fatalf("local open navigated to %q", u)
	}
	if s := r.m.State(); !s.Local.Running || s.Connections[0].Status != StatusRunning {
		t.Fatalf("state: %+v", s)
	}

	// Switching away never stops the daemon.
	r.m.SwitchConnection()
	if r.cli.stops != 0 {
		t.Fatal("switch stopped the daemon")
	}
	// Opening again reuses the running daemon.
	r.m.Open(bg, v.ID)
	if r.cli.serves != 1 {
		t.Fatalf("serve ran again: %d", r.cli.serves)
	}
	if err := r.m.StopLocal(bg); err != nil {
		t.Fatal(err)
	}
	if r.cli.stops != 1 || r.m.State().Phase != PhaseConnections || r.m.State().Local.Running {
		t.Fatalf("after stop: %+v", r.m.State())
	}

	// Removing with the daemon down revokes through the CLI.
	if err := r.m.Remove(bg, v.ID); err != nil {
		t.Fatal(err)
	}
	if len(r.cli.revoked) != 1 {
		t.Fatalf("revoked via cli: %v", r.cli.revoked)
	}
}

func TestLocalNotInstalled(t *testing.T) {
	r := newRig(t)
	r.m.d.Resolver.IsFile = func(string) bool { return false }
	if _, err := r.m.AddLocal(bg); !errors.Is(err, daemon.ErrNotInstalled) {
		t.Fatalf("got %v", err)
	}
	if st := r.m.Refresh(bg); st.Local.Installed {
		t.Fatalf("%+v", st.Local)
	}
}

func TestAutoOpenLast(t *testing.T) {
	r := newRig(t)
	srv := mockserver.Start(mockserver.Options{})
	defer srv.Close()
	v, _ := r.m.AddRemote(bg, "VPS", srv.URL, "")
	r.store.SetLast(v.ID)
	r.m.AutoOpen(bg)
	if r.m.State().Phase != PhaseOpen || !strings.HasPrefix(r.nav.lastURL(), srv.URL) {
		t.Fatalf("auto-open: %+v %v", r.m.State(), r.nav.events)
	}
}

func TestLoginPageTriggersRecheck(t *testing.T) {
	r := newRig(t)
	srv := mockserver.Start(mockserver.Options{Password: "pw"})
	defer srv.Close()
	v, _ := r.m.AddRemote(bg, "VPS", srv.URL, "pw")
	r.m.Open(bg, v.ID)
	srv.RevokeAll()
	r.m.PagePath(srv.URL, "/login")
	waitFor(t, "signed out", func() bool {
		s := r.m.State()
		return s.Phase == PhaseConnections && len(s.Connections) == 1 && s.Connections[0].Status == StatusSignedOut
	})
}

func TestPollNoticesRevokedDevice(t *testing.T) {
	r := newRig(t)
	srv := mockserver.Start(mockserver.Options{Password: "pw"})
	defer srv.Close()
	v, _ := r.m.AddRemote(bg, "VPS", srv.URL, "pw")
	if err := r.m.Open(bg, v.ID); err != nil {
		t.Fatal(err)
	}
	srv.RevokeAll()
	waitFor(t, "signed out by the poll", func() bool {
		s := r.m.State()
		return s.Phase == PhaseConnections && s.Connections[0].Status == StatusSignedOut && strings.Contains(s.Notice, "pair again")
	})
	if r.nav.last() != "connections" {
		t.Fatalf("nav: %v", r.nav.events)
	}
}
