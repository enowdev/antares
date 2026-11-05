package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/enowdev/antares/internal/config"
	"github.com/enowdev/antares/internal/store"
)

const (
	testPassword  = "pw-test-123"
	testAuthToken = "tok-test-123"
)

type devFixture struct {
	t   *testing.T
	s   *Server
	h   http.Handler
	db  store.Store
	cfg *config.Config
}

// newDevFixture builds a server on a temp ANTARES_HOME and SQLite store with
// an optional dashboard password and auth_token.
func newDevFixture(t *testing.T, password, token string) *devFixture {
	t.Helper()
	home := t.TempDir()
	t.Setenv("ANTARES_HOME", home)
	if err := config.EnsureHome(); err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(context.Background(), "sqlite", filepath.Join(home, "t.db"), 4, 5000, true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	cfg := config.Default()
	cfg.Server.AuthToken = token
	if password != "" {
		hash, err := config.HashPassword(password)
		if err != nil {
			t.Fatal(err)
		}
		cfg.Server.DashboardPasswordHash = hash
	}
	s := New(Options{Config: cfg, Store: db})
	t.Cleanup(s.devAuth.touches.Wait) // runs before db.Close (LIFO)
	return &devFixture{t: t, s: s, h: s.Handler(), db: db, cfg: cfg}
}

type reqOpt func(*http.Request)

func bearer(tok string) reqOpt {
	return func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+tok) }
}

func cookie(v string) reqOpt {
	return func(r *http.Request) { r.AddCookie(&http.Cookie{Name: dashCookie, Value: v}) }
}

func (f *devFixture) do(method, path string, body any, opts ...reqOpt) *httptest.ResponseRecorder {
	f.t.Helper()
	var rd *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	} else {
		rd = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, rd)
	req.RemoteAddr = "192.0.2.10:5555"
	for _, o := range opts {
		o(req)
	}
	rr := httptest.NewRecorder()
	f.h.ServeHTTP(rr, req)
	return rr
}

// pair creates a device directly in the store.
func (f *devFixture) pair(name string) (*store.Device, string) {
	f.t.Helper()
	d, tok, err := store.PairDevice(context.Background(), f.db, name, "desktop-macos")
	if err != nil {
		f.t.Fatal(err)
	}
	return d, tok
}

func sessionCookie(t *testing.T, rr *httptest.ResponseRecorder) string {
	t.Helper()
	for _, c := range rr.Result().Cookies() {
		if c.Name == dashCookie && c.Value != "" {
			return c.Value
		}
	}
	t.Fatalf("no %s cookie in response (status %d)", dashCookie, rr.Code)
	return ""
}

func TestDeviceAuthMatrix(t *testing.T) {
	for _, srv := range []struct {
		name            string
		password, token string
		openForNone     bool
	}{
		{"open", "", "", true},
		{"password-only", testPassword, "", false},
		{"token-only", "", testAuthToken, false},
		{"password+token", testPassword, testAuthToken, false},
	} {
		t.Run(srv.name, func(t *testing.T) {
			f := newDevFixture(t, srv.password, srv.token)
			_, devTok := f.pair("live")
			revoked, revTok := f.pair("gone")
			if err := f.db.RevokeDevice(context.Background(), revoked.ID); err != nil {
				t.Fatal(err)
			}
			// A session cookie as the password login (or a handoff) mints it.
			rr := httptest.NewRecorder()
			f.s.mintDashSession(rr, httptest.NewRequest("POST", "/", nil), "")
			sess := sessionCookie(t, rr)

			closed := http.StatusUnauthorized
			if srv.openForNone {
				closed = http.StatusOK
			}
			for _, c := range []struct {
				name string
				opts []reqOpt
				want int
			}{
				{"none", nil, closed},
				{"auth_token", []reqOpt{bearer(testAuthToken)}, map[bool]int{true: http.StatusOK, false: closed}[srv.token != ""]},
				{"device", []reqOpt{bearer(devTok)}, http.StatusOK},
				{"revoked device", []reqOpt{bearer(revTok)}, closed},
				{"junk bearer", []reqOpt{bearer("atd_" + strings.Repeat("0", 48))}, closed},
				{"cookie", []reqOpt{cookie(sess)}, http.StatusOK},
				{"stale cookie", []reqOpt{cookie("nope")}, closed},
			} {
				if got := f.do("GET", "/api/devices", nil, c.opts...).Code; got != c.want {
					t.Errorf("%s: GET /api/devices = %d, want %d", c.name, got, c.want)
				}
			}
			// Exempt from every gate.
			for _, p := range []string{"/api/version", "/api/health"} {
				if got := f.do("GET", p, nil).Code; got != http.StatusOK {
					t.Errorf("%s without credentials = %d, want 200", p, got)
				}
			}
		})
	}
}

func TestDeviceTokenOnQueryAllowlist(t *testing.T) {
	f := newDevFixture(t, testPassword, testAuthToken)
	_, tok := f.pair("q")
	for _, c := range []struct {
		path string
		want bool
	}{
		{"/api/files/raw?token=" + tok, true},
		{"/api/devices?token=" + tok, false},
	} {
		r := httptest.NewRequest("GET", c.path, nil)
		if got := f.s.bearerAuthorizedOrQuery(r); got != c.want {
			t.Errorf("%s: %v want %v", c.path, got, c.want)
		}
	}
}

func TestVersionEndpoint(t *testing.T) {
	f := newDevFixture(t, testPassword, testAuthToken)
	rr := f.do("GET", "/api/version", nil)
	var v struct {
		Version    string `json:"version"`
		Contract   int    `json:"contract"`
		MinDesktop string `json:"min_desktop"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &v); err != nil || rr.Code != 200 {
		t.Fatalf("%d %s", rr.Code, rr.Body)
	}
	if v.Contract != 1 || v.Version == "" || v.MinDesktop != "0.1.0" {
		t.Fatalf("%+v", v)
	}
}

func TestPairRules(t *testing.T) {
	body := func(pw string) map[string]string {
		return map[string]string{"name": "MacBook Pro", "platform": "desktop-macos", "password": pw}
	}
	for _, c := range []struct {
		name            string
		password, token string
		body            map[string]string
		opts            []reqOpt
		want            int
	}{
		{"open server, no password", "", "", body(""), nil, 200},
		{"password-only, missing password", testPassword, "", body(""), nil, 401},
		{"password-only, wrong password", testPassword, "", body("nope"), nil, 401},
		{"password-only, right password", testPassword, "", body(testPassword), nil, 200},
		{"token-only, no credential", "", testAuthToken, body(""), nil, 401},
		{"token-only, password is not enough", "", testAuthToken, body("whatever"), nil, 401},
		{"token-only, auth_token bearer", "", testAuthToken, body(""), []reqOpt{bearer(testAuthToken)}, 200},
		{"both, right password", testPassword, testAuthToken, body(testPassword), nil, 200},
		{"both, auth_token bearer ignores password", testPassword, testAuthToken, body("wrong"), []reqOpt{bearer(testAuthToken)}, 200},
		{"bad name", "", "", map[string]string{"name": "   "}, nil, 400},
		{"long name", "", "", map[string]string{"name": strings.Repeat("x", 65)}, nil, 400},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := newDevFixture(t, c.password, c.token)
			rr := f.do("POST", "/api/devices/pair", c.body, c.opts...)
			if rr.Code != c.want {
				t.Fatalf("status %d want %d: %s", rr.Code, c.want, rr.Body)
			}
			if c.want != 200 {
				return
			}
			var out struct {
				Device struct {
					ID, Name, Platform, CreatedAt string
				} `json:"device"`
				Token string `json:"token"`
			}
			if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
				t.Fatal(err)
			}
			if !store.LooksLikeDeviceToken(out.Token) || !strings.HasPrefix(out.Device.ID, "dev_") {
				t.Fatalf("bad response shape: %s", out.Device.ID)
			}
			if rr.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("pair response must be no-store")
			}
			// The new token authorizes on its own.
			if got := f.do("GET", "/api/devices", nil, bearer(out.Token)).Code; got != 200 {
				t.Fatalf("new token: %d", got)
			}
		})
	}

	t.Run("device bearer pairs another device without password", func(t *testing.T) {
		f := newDevFixture(t, testPassword, "")
		_, tok := f.pair("first")
		if got := f.do("POST", "/api/devices/pair", map[string]string{"name": "second"}, bearer(tok)).Code; got != 200 {
			t.Fatalf("%d", got)
		}
	})

	t.Run("unknown platform becomes other", func(t *testing.T) {
		f := newDevFixture(t, "", "")
		rr := f.do("POST", "/api/devices/pair", map[string]string{"name": "x", "platform": "toaster"})
		if !strings.Contains(rr.Body.String(), `"platform":"other"`) {
			t.Fatalf("%s", rr.Body)
		}
	})

	t.Run("rate limited after repeated failures", func(t *testing.T) {
		f := newDevFixture(t, testPassword, "")
		for i := 0; i < authFailLimit; i++ {
			if got := f.do("POST", "/api/devices/pair", body("wrong")).Code; got != 401 {
				t.Fatalf("attempt %d: %d", i, got)
			}
		}
		if got := f.do("POST", "/api/devices/pair", body(testPassword)).Code; got != http.StatusTooManyRequests {
			t.Fatalf("pair after limit: %d", got)
		}
		if got := f.do("POST", "/api/auth/login", map[string]string{"password": testPassword}).Code; got != http.StatusTooManyRequests {
			t.Fatalf("login shares the limiter: %d", got)
		}
	})
}

func TestListAndRevokeDevices(t *testing.T) {
	f := newDevFixture(t, testPassword, testAuthToken)
	me, tok := f.pair("me")
	other, _ := f.pair("other")

	rr := f.do("GET", "/api/devices", nil, bearer(tok))
	var out struct {
		Devices []deviceView `json:"devices"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil || len(out.Devices) != 2 {
		t.Fatalf("%d %s", rr.Code, rr.Body)
	}
	for _, d := range out.Devices {
		if d.Current != (d.ID == me.ID) {
			t.Errorf("current flag wrong for %s", d.ID)
		}
		if d.RevokedAt != nil {
			t.Errorf("not revoked yet")
		}
	}
	if !strings.Contains(rr.Body.String(), `"revoked_at":null`) {
		t.Fatal("revoked_at must serialise as null")
	}

	if got := f.do("DELETE", "/api/devices/dev_missing", nil, bearer(tok)).Code; got != 404 {
		t.Fatalf("unknown id: %d", got)
	}
	if got := f.do("DELETE", "/api/devices/"+other.ID, nil, bearer(tok)).Code; got != 200 {
		t.Fatalf("revoke other: %d", got)
	}
	// Revoking yourself is allowed; the token stops working at once.
	if got := f.do("DELETE", "/api/devices/"+me.ID, nil, bearer(tok)).Code; got != 200 {
		t.Fatalf("revoke self: %d", got)
	}
	if got := f.do("GET", "/api/devices", nil, bearer(tok)).Code; got != 401 {
		t.Fatalf("revoked token: %d", got)
	}
}

func TestRevocationOutsideServerWithinCacheWindow(t *testing.T) {
	f := newDevFixture(t, "", testAuthToken)
	d, tok := f.pair("cli-revoked")
	if got := f.do("GET", "/api/devices", nil, bearer(tok)).Code; got != 200 {
		t.Fatal(got)
	}
	// The CLI revokes straight in the store; the cache may serve the old answer
	// for at most deviceCacheTTL. Expire it to simulate the window passing.
	_ = f.db.RevokeDevice(context.Background(), d.ID)
	f.s.devAuth.mu.Lock()
	for k, e := range f.s.devAuth.byHash {
		e.exp = time.Now().Add(-time.Second)
		f.s.devAuth.byHash[k] = e
	}
	f.s.devAuth.mu.Unlock()
	if got := f.do("GET", "/api/devices", nil, bearer(tok)).Code; got != 401 {
		t.Fatalf("after cache window: %d", got)
	}
}

func TestDeviceLastSeenTouched(t *testing.T) {
	f := newDevFixture(t, "", testAuthToken)
	d, tok := f.pair("seen")
	f.do("GET", "/api/devices", nil, bearer(tok))
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		got, _ := f.db.GetDevice(context.Background(), d.ID)
		if got.LastSeenAt != nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("last_seen_at never written")
}

func handoff(t *testing.T, f *devFixture, next string, opts ...reqOpt) (code, u string) {
	t.Helper()
	rr := f.do("POST", "/api/auth/handoff", map[string]string{"next": next}, opts...)
	if rr.Code != 200 {
		t.Fatalf("handoff: %d %s", rr.Code, rr.Body)
	}
	var out struct {
		Code      string `json:"code"`
		URL       string `json:"url"`
		ExpiresIn int    `json:"expires_in"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &out)
	if !strings.HasPrefix(out.Code, "ahc_") || len(out.Code) != 36 || out.ExpiresIn != 60 {
		t.Fatalf("handoff shape: %+v", out)
	}
	return out.Code, out.URL
}

func TestHandoff(t *testing.T) {
	f := newDevFixture(t, "", testAuthToken) // token-only: a cookie is new to withAuth
	dev, tok := f.pair("desk")

	if got := f.do("POST", "/api/auth/handoff", map[string]string{}).Code; got != 401 {
		t.Fatalf("handoff needs authorization: %d", got)
	}

	_, u := handoff(t, f, "/c/ses_123", bearer(tok))
	if u != "/auth/handoff?code="+u[len("/auth/handoff?code="):len("/auth/handoff?code=")+36]+"&next=%2Fc%2Fses_123" {
		t.Fatalf("url shape: %s", u)
	}

	rr := f.do("GET", u, nil)
	if rr.Code != http.StatusFound || rr.Header().Get("Location") != "/c/ses_123" {
		t.Fatalf("exchange: %d %q", rr.Code, rr.Header().Get("Location"))
	}
	if rr.Header().Get("Cache-Control") != "no-store" || rr.Header().Get("Referrer-Policy") != "no-referrer" {
		t.Fatal("missing no-store / no-referrer")
	}
	var c *http.Cookie
	for _, k := range rr.Result().Cookies() {
		if k.Name == dashCookie {
			c = k
		}
	}
	if c == nil || !c.HttpOnly || c.SameSite != http.SameSiteLaxMode || c.MaxAge != int(dashSessionTTL/time.Second) {
		t.Fatalf("cookie: %+v", c)
	}

	// The cookie alone now passes withAuth, and knows its device.
	rr = f.do("GET", "/api/devices", nil, cookie(c.Value))
	if rr.Code != 200 || !strings.Contains(rr.Body.String(), `"current":true`) {
		t.Fatalf("cookie-only: %d %s", rr.Code, rr.Body)
	}

	// Reuse fails.
	rr = f.do("GET", u, nil)
	if rr.Code != http.StatusFound || rr.Header().Get("Location") != "/login?handoff=expired" {
		t.Fatalf("reuse: %d %q", rr.Code, rr.Header().Get("Location"))
	}

	// Expired code.
	code, u2 := handoff(t, f, "/", bearer(tok))
	f.s.devAuth.mu.Lock()
	hc := f.s.devAuth.handoffs[code]
	hc.exp = time.Now().Add(-time.Second)
	f.s.devAuth.handoffs[code] = hc
	f.s.devAuth.mu.Unlock()
	if loc := f.do("GET", u2, nil).Header().Get("Location"); loc != "/login?handoff=expired" {
		t.Fatalf("expired: %q", loc)
	}
	if loc := f.do("GET", "/auth/handoff?code=ahc_bogus&next=/x", nil).Header().Get("Location"); loc != "/login?handoff=expired" {
		t.Fatalf("bogus: %q", loc)
	}

	// Revoking the device ends the sessions its handoffs minted.
	if got := f.do("DELETE", "/api/devices/"+dev.ID, nil, bearer(testAuthToken)).Code; got != 200 {
		t.Fatal(got)
	}
	if got := f.do("GET", "/api/devices", nil, cookie(c.Value)).Code; got != 401 {
		t.Fatalf("session of revoked device: %d", got)
	}
}

func TestHandoffBadNext(t *testing.T) {
	f := newDevFixture(t, "", "")
	for _, next := range []string{
		"//evil.com", "https://evil.com", `/\evil.com`, `\\evil.com`, "javascript:alert(1)",
		"evil.com", "/\t/evil.com", "/\n/evil.com", "http:/evil.com", "///evil.com",
	} {
		// Through the API: next is sanitised before it goes into the URL.
		_, u := handoff(t, f, next)
		parsed, _ := url.Parse(u)
		if got := parsed.Query().Get("next"); got != "/" {
			t.Errorf("POST next=%q -> %q", next, got)
		}
		// Straight at the exchange with a hostile next.
		code, _ := handoff(t, f, "/")
		rr := f.do("GET", "/auth/handoff?code="+code+"&next="+url.QueryEscape(next), nil)
		if loc := rr.Header().Get("Location"); loc != "/" {
			t.Errorf("GET next=%q -> Location %q", next, loc)
		}
	}
}

func TestSafeNextPath(t *testing.T) {
	for in, want := range map[string]string{
		"":                       "/",
		"/":                      "/",
		"/c/ses_1":               "/c/ses_1",
		"/system/settings?x=1#y": "/system/settings?x=1#y",
		"//evil.com":             "/",
		"https://evil.com":       "/",
		`/\evil.com`:             "/",
		"/%2F/evil.com":          "/%2F/evil.com",
		"relative":               "/",
		"/a\x7f":                 "/",
	} {
		if got := safeNextPath(in); got != want {
			t.Errorf("safeNextPath(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestHandoffRouteNotSwallowedBySPA(t *testing.T) {
	f := newDevFixture(t, "", "")
	rr := f.do("GET", "/auth/handoff?code=x", nil)
	if rr.Code != http.StatusFound {
		t.Fatalf("SPA fallback answered /auth/handoff: %d", rr.Code)
	}
}
