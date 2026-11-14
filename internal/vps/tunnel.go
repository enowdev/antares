package vps

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/websocket"
)

// TunnelCloudflared is the Target.Tunnel value for hosts whose SSH port is only
// reachable through a Cloudflare Tunnel. Empty means direct SSH.
const TunnelCloudflared = "cloudflared"

// ErrTunnelRefused is returned when Cloudflare rejects the WebSocket upgrade.
var ErrTunnelRefused = errors.New("cloudflare refused the tunnel connection")

// tunnelDialTimeout bounds the WebSocket upgrade. It is a variable so a test
// can shrink it.
var tunnelDialTimeout = 20 * time.Second

// tunnelEndpoint returns the WebSocket location and Origin for a tunnel host.
// It is a variable so tests can point it at a local server.
var tunnelEndpoint = func(host string) (location, origin string) {
	return "wss://" + host + "/", "https://" + host
}

// NormalizeTunnelHost reduces a pasted URL or hostname to a bare host: scheme,
// path and userinfo are dropped; an explicit :port is kept.
func NormalizeTunnelHost(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.Index(s, "://"); i >= 0 {
		s = s[i+3:]
	}
	if i := strings.IndexAny(s, "/?#"); i >= 0 {
		s = s[:i]
	}
	if i := strings.LastIndex(s, "@"); i >= 0 {
		s = s[i+1:]
	}
	return s
}

// endpoint is the human-readable address of the target, for error messages.
func (t Target) endpoint() string {
	if t.Tunnel == TunnelCloudflared {
		return "wss://" + t.Host
	}
	return t.addr()
}

// dialTransport opens the raw byte stream SSH runs over: plain TCP, or a
// WebSocket to a Cloudflare Tunnel the way `cloudflared access ssh` does.
func dialTransport(ctx context.Context, t Target) (net.Conn, error) {
	d := &net.Dialer{Timeout: 20 * time.Second}
	if t.Tunnel != TunnelCloudflared {
		return d.DialContext(ctx, "tcp", t.addr())
	}
	return dialTunnel(ctx, t, d)
}

// dialTunnel performs the WebSocket upgrade itself rather than through
// websocket.Config.DialContext: x/net reports every refused upgrade as a bare
// ErrBadStatus, and the HTTP status is what tells an offline tunnel apart from
// an Access policy. The connection records the first bytes it reads so the
// status line and Location header can be recovered on refusal.
func dialTunnel(ctx context.Context, t Target, d *net.Dialer) (net.Conn, error) {
	loc, origin := tunnelEndpoint(t.Host)
	cfg, err := websocket.NewConfig(loc, origin)
	if err != nil {
		return nil, fmt.Errorf("tunnel endpoint %s: %w", t.Host, err)
	}
	cfg.Header.Set("User-Agent", "antares")
	if t.CFClientID != "" {
		cfg.Header.Set("Cf-Access-Client-Id", t.CFClientID)
	}
	if t.CFClientSecret != "" {
		cfg.Header.Set("Cf-Access-Client-Secret", t.CFClientSecret)
	}

	dctx, cancel := context.WithTimeout(ctx, tunnelDialTimeout)
	defer cancel()
	raw, err := dialTunnelConn(dctx, d, cfg)
	if err != nil {
		return nil, tunnelDialErr(ctx, dctx, t, err)
	}
	// Bound the handshake by dctx: its deadline, and an early unblock if the
	// caller cancels.
	if dl, ok := dctx.Deadline(); ok {
		_ = raw.SetDeadline(dl)
	}
	stop := context.AfterFunc(dctx, func() { _ = raw.SetDeadline(time.Now()) })
	rc := &recordingConn{Conn: raw, limit: upgradeRecordLimit}
	ws, err := websocket.NewClient(cfg, rc)
	stopped := stop()
	if err != nil {
		_ = raw.Close()
		if errors.Is(err, websocket.ErrBadStatus) {
			status, location := parseUpgradeResponse(rc.recorded())
			return nil, refusedError(t.Host, status, location)
		}
		return nil, tunnelDialErr(ctx, dctx, t, err)
	}
	if !stopped {
		// dctx ended as the handshake finished; the deadline is already in the past.
		_ = ws.Close()
		return nil, tunnelDialErr(ctx, dctx, t, context.DeadlineExceeded)
	}
	_ = raw.SetDeadline(time.Time{})
	rc.stopRecording()
	ws.PayloadType = websocket.BinaryFrame
	return ws, nil
}

// dialTunnelConn opens the TCP (ws) or TLS (wss) connection the upgrade runs on.
func dialTunnelConn(ctx context.Context, d *net.Dialer, cfg *websocket.Config) (net.Conn, error) {
	u := cfg.Location
	addr := u.Host
	if u.Port() == "" {
		port := "80"
		if u.Scheme == "wss" {
			port = "443"
		}
		addr = net.JoinHostPort(u.Hostname(), port)
	}
	if u.Scheme == "wss" {
		td := &tls.Dialer{NetDialer: d, Config: cfg.TlsConfig}
		return td.DialContext(ctx, "tcp", addr)
	}
	return d.DialContext(ctx, "tcp", addr)
}

// tunnelDialErr maps a failed connect or handshake: running out of the tunnel's
// own budget while the caller's context is still live is a timeout; anything
// else (including the caller's cancellation, which dial wraps) passes through.
func tunnelDialErr(ctx, dctx context.Context, t Target, err error) error {
	if ctx.Err() == nil && (errors.Is(dctx.Err(), context.DeadlineExceeded) || errors.Is(err, os.ErrDeadlineExceeded)) {
		return fmt.Errorf("%w: no response from %s", ErrTimeout, t.endpoint())
	}
	return err
}

// refusedError words a refused upgrade by what Cloudflare's status implies.
// Every result wraps ErrTunnelRefused.
func refusedError(host string, status int, location string) error {
	switch {
	case status == http.StatusUnauthorized || status == http.StatusForbidden ||
		(status >= 300 && status < 400 && isAccessLogin(location)):
		return fmt.Errorf("%w to %s (HTTP %d): the hostname is protected by Cloudflare Access; add a service token, or check that its Client ID and Secret are allowed by the Access policy", ErrTunnelRefused, host, status)
	case status == http.StatusBadGateway || status == http.StatusServiceUnavailable ||
		status == http.StatusGatewayTimeout || status == 530:
		return fmt.Errorf("%w to %s (HTTP %d): the tunnel is offline; check that cloudflared is running on the server and connected to Cloudflare", ErrTunnelRefused, host, status)
	case status > 0:
		return fmt.Errorf("%w to %s (HTTP %d): the hostname is not routed to SSH; check that the tunnel's ingress sends it to ssh://localhost:22", ErrTunnelRefused, host, status)
	default:
		return fmt.Errorf("%w to %s: if the hostname is protected by Cloudflare Access add a service token; otherwise check that the tunnel routes it to ssh://localhost:22", ErrTunnelRefused, host)
	}
}

// isAccessLogin reports whether a redirect target is a Cloudflare Access login.
func isAccessLogin(location string) bool {
	l := strings.ToLower(location)
	return strings.Contains(l, "cloudflareaccess.com") || strings.Contains(l, "/cdn-cgi/access/")
}

// parseUpgradeResponse pulls the status code and Location header out of the
// recorded start of an HTTP response. It returns 0 when no status line is
// readable; the body and any header past the recorded bytes are ignored.
func parseUpgradeResponse(b []byte) (status int, location string) {
	lines := strings.Split(string(b), "\r\n")
	if f := strings.Fields(lines[0]); len(f) >= 2 && strings.HasPrefix(f[0], "HTTP/") {
		status, _ = strconv.Atoi(f[1])
	}
	for _, line := range lines[1:] {
		if line == "" {
			break
		}
		if name, value, ok := strings.Cut(line, ":"); ok && strings.EqualFold(strings.TrimSpace(name), "Location") {
			location = strings.TrimSpace(value)
		}
	}
	return status, location
}

// upgradeRecordLimit caps how much of the upgrade response is kept: enough
// for the status line and Cloudflare's headers, matching bufio's default read.
const upgradeRecordLimit = 4096

// recordingConn copies the first bytes read from the connection so a refused
// upgrade's status can be recovered. Recording stops after the handshake.
type recordingConn struct {
	net.Conn
	buf   []byte
	limit int
}

func (c *recordingConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	if room := c.limit - len(c.buf); room > 0 && n > 0 {
		c.buf = append(c.buf, p[:min(n, room)]...)
	}
	return n, err
}

func (c *recordingConn) recorded() []byte { return c.buf }

func (c *recordingConn) stopRecording() { c.buf, c.limit = nil, 0 }
