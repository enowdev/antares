package vps

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"golang.org/x/net/websocket"
)

// TunnelCloudflared is the Target.Tunnel value for hosts whose SSH port is only
// reachable through a Cloudflare Tunnel. Empty means direct SSH.
const TunnelCloudflared = "cloudflared"

// ErrTunnelRefused is returned when Cloudflare rejects the WebSocket upgrade.
var ErrTunnelRefused = errors.New("cloudflare refused the tunnel connection")

// tunnelEndpoint returns the WebSocket location and Origin for a tunnel host.
// It is a variable so tests can point it at a local server.
var tunnelEndpoint = func(host string) (location, origin string) {
	return "wss://" + host + "/", "https://" + host
}

// NormalizeTunnelHost reduces a pasted URL or hostname to a bare hostname.
func NormalizeTunnelHost(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.Index(s, "://"); i >= 0 {
		s = s[i+3:]
	}
	if i := strings.IndexAny(s, "/?#"); i >= 0 {
		s = s[:i]
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
	cfg.Dialer = d
	ws, err := cfg.DialContext(ctx)
	if err != nil {
		var de *websocket.DialError
		if errors.As(err, &de) && de.Err == websocket.ErrBadStatus {
			return nil, fmt.Errorf("%w to %s: if the hostname is protected by Cloudflare Access add a service token; otherwise check that the tunnel routes it to ssh://localhost:22", ErrTunnelRefused, t.Host)
		}
		return nil, err
	}
	ws.PayloadType = websocket.BinaryFrame
	return ws, nil
}
