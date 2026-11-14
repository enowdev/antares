package vps

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/net/websocket"
)

// useTunnelEndpoint points the tunnel dial at a plain-ws test server.
func useTunnelEndpoint(t *testing.T, host string) {
	t.Helper()
	old := tunnelEndpoint
	tunnelEndpoint = func(string) (string, string) { return "ws://" + host + "/", "http://" + host }
	t.Cleanup(func() { tunnelEndpoint = old })
}

// newTunnelBridge serves a WebSocket endpoint that forwards binary frames to a
// plain TCP SSH server, like cloudflared does. It records the upgrade headers.
func newTunnelBridge(t *testing.T, sshAddr string) (string, *http.Header) {
	t.Helper()
	var mu sync.Mutex
	got := &http.Header{}
	srv := httptest.NewServer(websocket.Server{
		Handshake: func(_ *websocket.Config, r *http.Request) error {
			mu.Lock()
			*got = r.Header.Clone()
			mu.Unlock()
			return nil
		},
		Handler: func(ws *websocket.Conn) {
			ws.PayloadType = websocket.BinaryFrame
			up, err := net.Dial("tcp", sshAddr)
			if err != nil {
				return
			}
			defer up.Close()
			done := make(chan struct{}, 2)
			go func() { _, _ = io.Copy(up, ws); done <- struct{}{} }()
			go func() { _, _ = io.Copy(ws, up); done <- struct{}{} }()
			<-done
		},
	})
	t.Cleanup(srv.Close)
	host := strings.TrimPrefix(srv.URL, "http://")
	useTunnelEndpoint(t, host)
	return host, got
}

func tunnelTarget(srv *testSSHServer, host string) Target {
	tg := srv.target()
	tg.Tunnel, tg.Host, tg.Port = TunnelCloudflared, host, 1
	return tg
}

func TestRunOverCloudflaredTunnel(t *testing.T) {
	srv := newTestSSHServer(t, func(s *testSSHServer) { s.execOutput = "tunnel-ok\n" })
	host, _ := newTunnelBridge(t, srv.addr)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	out, _, err := Run(ctx, tunnelTarget(srv, host), "whoami")
	if err != nil {
		t.Fatal(err)
	}
	if out != "tunnel-ok\n" {
		t.Fatalf("out = %q", out)
	}
}

func TestPingOverCloudflaredTunnel(t *testing.T) {
	srv := newTestSSHServer(t)
	host, _ := newTunnelBridge(t, srv.addr)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_, seen, err := Ping(ctx, tunnelTarget(srv, host))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(seen, "ssh-") {
		t.Fatalf("seenHostKey = %q", seen)
	}
}

func TestTunnelSendsAccessHeaders(t *testing.T) {
	srv := newTestSSHServer(t)
	host, got := newTunnelBridge(t, srv.addr)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	tg := tunnelTarget(srv, host)
	tg.CFClientID, tg.CFClientSecret = "abc.access", "s3cret"
	if _, _, err := Ping(ctx, tg); err != nil {
		t.Fatal(err)
	}
	if got.Get("Cf-Access-Client-Id") != "abc.access" || got.Get("Cf-Access-Client-Secret") != "s3cret" {
		t.Fatalf("access headers = %v", *got)
	}
	if !strings.Contains(got.Get("User-Agent"), "antares") {
		t.Fatalf("user agent = %q", got.Get("User-Agent"))
	}
	tg.CFClientID, tg.CFClientSecret = "", ""
	if _, _, err := Ping(ctx, tg); err != nil {
		t.Fatal(err)
	}
	if got.Get("Cf-Access-Client-Id") != "" || got.Get("Cf-Access-Client-Secret") != "" {
		t.Fatalf("headers should be absent, got %v", *got)
	}
}

func TestTunnelRefusedIsReadable(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer ts.Close()
	host := strings.TrimPrefix(ts.URL, "http://")
	useTunnelEndpoint(t, host)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, _, err := Ping(ctx, Target{Host: host, Tunnel: TunnelCloudflared, Password: "x"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, ErrTunnelRefused) {
		t.Fatalf("not ErrTunnelRefused: %v", err)
	}
	if !strings.Contains(err.Error(), "service token") {
		t.Fatalf("error not readable: %v", err)
	}
}

func TestTunnelDialHonoursContext(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	var mu sync.Mutex
	var conns []net.Conn
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			conns = append(conns, c)
			mu.Unlock()
		}
	}()
	defer func() {
		mu.Lock()
		for _, c := range conns {
			c.Close()
		}
		mu.Unlock()
	}()
	host := ln.Addr().String()
	useTunnelEndpoint(t, host)
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, _, err = Ping(ctx, Target{Host: host, Tunnel: TunnelCloudflared, Password: "x"})
	if time.Since(start) > 2*time.Second {
		t.Fatalf("took %v", time.Since(start))
	}
	if !errors.Is(err, ErrTimeout) {
		t.Fatalf("want ErrTimeout, got %v", err)
	}
}

func TestNormalizeTunnelHost(t *testing.T) {
	for _, in := range []string{
		"https://ssh.example.com/", "ssh://ssh.example.com", "wss://ssh.example.com/x/y",
		" ssh.example.com ", "ssh.example.com",
		"root@ssh.example.com",
	} {
		if got := NormalizeTunnelHost(in); got != "ssh.example.com" {
			t.Errorf("NormalizeTunnelHost(%q) = %q", in, got)
		}
	}
}

func TestNormalizeTunnelHostKeepsPort(t *testing.T) {
	if got := NormalizeTunnelHost("ssh.example.com:8443"); got != "ssh.example.com:8443" {
		t.Errorf("got %q", got)
	}
}

func TestTunnelUpgradeTimeout(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			t.Cleanup(func() { c.Close() })
		}
	}()
	useTunnelEndpoint(t, ln.Addr().String())
	old := tunnelDialTimeout
	tunnelDialTimeout = 200 * time.Millisecond
	t.Cleanup(func() { tunnelDialTimeout = old })

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	start := time.Now()
	_, _, err = Ping(ctx, Target{Host: "ssh.example.com", Tunnel: TunnelCloudflared, Username: "u", Password: "p"})
	if !errors.Is(err, ErrTimeout) {
		t.Fatalf("err = %v, want ErrTimeout", err)
	}
	if time.Since(start) > 2*time.Second {
		t.Fatalf("took %v", time.Since(start))
	}
}

func TestLiveCloudflaredBanner(t *testing.T) {
	host := os.Getenv("ANTARES_LIVE_TUNNEL_HOST")
	if host == "" {
		t.Skip("set ANTARES_LIVE_TUNNEL_HOST to run against a real tunnel")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	c, err := dialTransport(ctx, Target{Host: host, Tunnel: TunnelCloudflared})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_ = c.SetReadDeadline(time.Now().Add(15 * time.Second))
	line, err := bufio.NewReader(c).ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(line, "SSH-2.0-") {
		t.Fatalf("banner = %q", line)
	}
}
