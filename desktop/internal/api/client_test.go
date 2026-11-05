package api_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/enowdev/antares/desktop/internal/api"
	"github.com/enowdev/antares/desktop/internal/mockserver"
)

var ctx = context.Background()

func TestPairAndHandoff(t *testing.T) {
	srv := mockserver.Start(mockserver.Options{Password: "pw", AuthToken: "srv-token"})
	defer srv.Close()

	anon := api.New(srv.URL, "")
	v, err := anon.Version(ctx, "0.1.0")
	if err != nil || v.Contract != 1 {
		t.Fatalf("version: %+v %v", v, err)
	}
	if _, err := anon.Pair(ctx, "Mac", "desktop-macos", "nope"); !errors.Is(err, api.ErrBadPassword) {
		t.Fatalf("wrong password: %v", err)
	}
	p, err := anon.Pair(ctx, "Mac", "desktop-macos", "pw")
	if err != nil || !strings.HasPrefix(p.Token, "atd_") || !strings.HasPrefix(p.Device.ID, "dev_") || p.Device.Platform != "desktop-macos" {
		t.Fatalf("pair: %+v %v", p, err)
	}

	authed := api.New(srv.URL, p.Token)
	h, err := authed.Handoff(ctx, "/c/ses_123")
	if err != nil || !strings.HasPrefix(h.Code, "ahc_") || !strings.HasPrefix(h.URL, "/auth/handoff?code=") || h.ExpiresIn != 60 {
		t.Fatalf("handoff: %+v %v", h, err)
	}

	// Following the handoff URL in a "browser" lands on next, signed in, once.
	jar, _ := cookiejar.New(nil)
	browser := &http.Client{Jar: jar}
	resp, err := browser.Get(srv.URL + h.URL)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.Request.URL.Path != "/c/ses_123" || resp.StatusCode != 200 {
		t.Fatalf("handoff landed on %s (%d)", resp.Request.URL, resp.StatusCode)
	}
	fresh := &http.Client{}
	resp, _ = fresh.Get(srv.URL + h.URL)
	resp.Body.Close()
	if !strings.HasPrefix(resp.Request.URL.RequestURI(), "/login") {
		t.Fatalf("code reused: %s", resp.Request.URL)
	}

	// Revoking this device signs it out.
	if err := authed.RevokeDevice(ctx, p.Device.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := authed.Handoff(ctx, ""); !errors.Is(err, api.ErrSignedOut) {
		t.Fatalf("after revoke: %v", err)
	}
}

func TestPairTokenOnlyServerExplains(t *testing.T) {
	srv := mockserver.Start(mockserver.Options{AuthToken: "srv-token"})
	defer srv.Close()
	_, err := api.New(srv.URL, "").Pair(ctx, "Mac", "desktop-macos", "")
	var e *api.Error
	if !errors.As(err, &e) || e.Status != 401 || !strings.Contains(e.Message, "no dashboard password") {
		t.Fatalf("got %v", err)
	}
	// With the server token as bearer, pairing works without a password.
	if _, err := api.New(srv.URL, "srv-token").Pair(ctx, "Mac", "desktop-macos", ""); err != nil {
		t.Fatal(err)
	}
}

func TestOldServer(t *testing.T) {
	srv := mockserver.Start(mockserver.Options{Old: true, Password: "pw"})
	defer srv.Close()
	c := api.New(srv.URL, "atd_x")
	if _, err := c.Version(ctx, "0.1.0"); !errors.Is(err, api.ErrTooOld) {
		t.Fatalf("version: %v", err)
	}
	if _, err := c.Pair(ctx, "Mac", "desktop-macos", "pw"); !errors.Is(err, api.ErrTooOld) {
		t.Fatalf("pair: %v", err)
	}
	if _, err := c.Handoff(ctx, "/"); !errors.Is(err, api.ErrTooOld) {
		t.Fatalf("handoff: %v", err)
	}
	if !strings.Contains(api.ErrTooOld.Error(), "update Antares there") {
		t.Fatal(api.ErrTooOld)
	}
}

func TestContractTooLowAndMinDesktop(t *testing.T) {
	srv := mockserver.Start(mockserver.Options{Contract: -1})
	defer srv.Close()
	if _, err := api.New(srv.URL, "").Version(ctx, "0.1.0"); !errors.Is(err, api.ErrTooOld) {
		t.Fatalf("contract 0: %v", err)
	}
	srv2 := mockserver.Start(mockserver.Options{MinDesktop: "0.3.0"})
	defer srv2.Close()
	if _, err := api.New(srv2.URL, "").Version(ctx, "0.1.0"); err == nil || !strings.Contains(err.Error(), "0.3.0 or newer") {
		t.Fatalf("min_desktop: %v", err)
	}
}

func TestSPAFallbackIsTooOld(t *testing.T) {
	// An old server's catch-all answers 200 text/html for unknown paths.
	html := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte("<!doctype html><div id=root></div>"))
	}))
	defer html.Close()
	c := api.New(html.URL, "atd_x")
	if _, err := c.Version(ctx, "0.1.0"); !errors.Is(err, api.ErrTooOld) {
		t.Fatalf("version: %v", err)
	}
	if _, err := c.Handoff(ctx, "/"); !errors.Is(err, api.ErrTooOld) {
		t.Fatalf("handoff: %v", err)
	}
}

func TestErrorsMapping(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/auth/handoff":
			w.WriteHeader(500)
			w.Write([]byte(`{"error": "database is locked"}`))
		case "/api/devices/pair":
			w.WriteHeader(429)
			w.Write([]byte(`{"error": "slow down"}`))
		case "/api/health":
			w.Write([]byte(`{"ok": false}`))
		}
	}))
	defer srv.Close()
	c := api.New(srv.URL, "atd_x")
	_, err := c.Handoff(ctx, "/")
	var e *api.Error
	if !errors.As(err, &e) || e.Status != 500 || err.Error() != "database is locked" {
		t.Fatalf("500: %v", err)
	}
	if _, err := c.Pair(ctx, "m", "desktop-macos", "pw"); err == nil || !strings.Contains(err.Error(), "too many attempts") {
		t.Fatalf("429: %v", err)
	}
	if err := c.Health(ctx); err == nil {
		t.Fatal("ok:false healthy")
	}
	srv.Close()
	if err := c.Health(ctx); !errors.Is(err, api.ErrUnreachable) {
		t.Fatalf("closed: %v", err)
	}
}

func TestHandoffRejectsAbsoluteURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"code": "ahc_1", "url": "//evil.example/auth", "expires_in": 60}`))
	}))
	defer srv.Close()
	if _, err := api.New(srv.URL, "atd_x").Handoff(ctx, "/"); err == nil {
		t.Fatal("protocol-relative handoff url accepted")
	}
}

func TestCompareVersions(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"0.1.0", "0.1.0", 0}, {"v0.5.0-16-g9b24008", "0.5.0", 0}, {"0.1.0", "0.2.0", -1},
		{"1.0", "0.9.9", 1}, {"0.10.0", "0.9.0", 1}, {"0.1.0-dev", "0.1.0", 0},
	}
	for _, c := range cases {
		if got := api.CompareVersions(c.a, c.b); got != c.want {
			t.Errorf("Compare(%q,%q)=%d want %d", c.a, c.b, got, c.want)
		}
	}
}
