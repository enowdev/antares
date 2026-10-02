package conns

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"strings"
)

// Private ranges where plain http:// is accepted: loopback, RFC 1918, link
// local, IPv6 unique-local, and Tailscale's CGNAT block.
var plainHTTPPrefixes = []netip.Prefix{
	netip.MustParsePrefix("127.0.0.0/8"),
	netip.MustParsePrefix("10.0.0.0/8"),
	netip.MustParsePrefix("172.16.0.0/12"),
	netip.MustParsePrefix("192.168.0.0/16"),
	netip.MustParsePrefix("169.254.0.0/16"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("::1/128"),
	netip.MustParsePrefix("fc00::/7"),
	netip.MustParsePrefix("fe80::/10"),
}

// ValidateURL normalises a remote server URL and applies the contract's rules:
// https:// everywhere, http:// only for loopback, private LAN ranges and
// Tailscale (100.64.0.0/10, *.ts.net). A bare host gets https://. The result
// has no trailing slash, query, fragment or credentials.
func ValidateURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errors.New("enter the server's URL")
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("not a valid URL: %w", err)
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return "", errors.New("the URL must start with https://")
	}
	host := u.Hostname()
	if host == "" {
		return "", errors.New("the URL has no host")
	}
	if u.User != nil {
		return "", errors.New("leave the user name and password out of the URL")
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return "", errors.New("the URL cannot have a query or fragment")
	}
	if scheme == "http" && !PlainHTTPAllowed(host) {
		return "", errors.New("use https:// — plain http:// is only allowed for this machine, a private network or Tailscale")
	}
	out := url.URL{Scheme: scheme, Host: strings.ToLower(u.Host), Path: strings.TrimRight(u.EscapedPath(), "/")}
	if p, err := url.PathUnescape(out.Path); err == nil {
		out.Path = p
	}
	return out.String(), nil
}

// PlainHTTPAllowed reports whether host may be reached over http://.
func PlainHTTPAllowed(host string) bool {
	host = strings.TrimSuffix(strings.ToLower(strings.Trim(host, "[]")), ".")
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	if host == "ts.net" || strings.HasSuffix(host, ".ts.net") {
		return true
	}
	if i := strings.IndexByte(host, '%'); i >= 0 { // fe80::1%en0
		host = host[:i]
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	addr, ok := netip.AddrFromSlice(ip)
	if !ok {
		return false
	}
	addr = addr.Unmap()
	for _, p := range plainHTTPPrefixes {
		if p.Contains(addr) {
			return true
		}
	}
	return false
}
