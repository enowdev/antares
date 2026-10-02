package server

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// queryTokenAllowed is deliberately narrow. EventSource and media elements
// cannot set an Authorization header, but ordinary API requests can and must
// never accept credentials in a URL.
func queryTokenAllowed(path string) bool {
	switch path {
	case "/api/chat/attach",
		"/api/intercept/breakpoints/stream",
		"/api/logs/stream",
		"/api/swarm/stream",
		"/api/board/stream",
		"/api/files/raw",
		"/api/social/image":
		return true
	default:
		if strings.HasPrefix(path, "/api/content-creator/projects/") && strings.HasSuffix(path, "/artifact") {
			return true
		}
		return strings.HasPrefix(path, "/api/subagent/") && strings.HasSuffix(path, "/attach")
	}
}

// (The query-token allowlist includes the raw file endpoint for browser media
// elements. It is intentionally not used for JSON or mutating routes.)

// bearerAuthorized reports whether the Authorization header carries
// server.auth_token or a live (non-revoked) device token.
func (s *Server) bearerAuthorized(r *http.Request) bool {
	ok, _ := s.bearerClient(r, false)
	return ok
}

// bearerAuthorizedOrQuery is bearerAuthorized plus ?token= (auth_token or a
// device token) on the EventSource/media allowlist.
func (s *Server) bearerAuthorizedOrQuery(r *http.Request) bool {
	ok, _ := s.bearerClient(r, true)
	return ok
}

// requestIsLoopback does not trust X-Forwarded-For. A proxy may opt into
// handling that header separately, but bootstrap capabilities must default to
// the actual peer address.
func requestIsLoopback(r *http.Request) bool {
	host := strings.TrimSpace(r.RemoteAddr)
	if host == "" {
		return false
	}
	if parsed, _, err := net.SplitHostPort(host); err == nil {
		host = parsed
	} else {
		host = strings.Trim(host, "[]")
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// requireSetupAccess keeps the first-run mutating endpoints local unless the
// operator has already configured a bearer token. Setup status remains a
// read-only endpoint so the UI can explain how to bootstrap an instance.
func (s *Server) requireSetupAccess(w http.ResponseWriter, r *http.Request) bool {
	cfg := s.config()
	if !NeedsSetup(cfg) {
		writeError(w, http.StatusConflict, errors.New("initial setup has already been completed"))
		return true
	}
	if requestIsLoopback(r) || s.bearerAuthorized(r) {
		return false
	}
	writeError(w, http.StatusForbidden, errors.New("initial setup is available only from loopback or with a configured bearer token"))
	return true
}

// validateProviderBaseURL validates the destination before the HTTP client is
// constructed. Local providers in the built-in catalogue are allowed to use a
// loopback endpoint; custom/provider URLs are not allowed to resolve into
// private, link-local, metadata, multicast, or otherwise non-public ranges.
func validateProviderBaseURL(ctx context.Context, raw string, allowLocal bool) error {
	return validateProviderBaseURLWithResolver(ctx, raw, allowLocal, net.DefaultResolver)
}

// validateProviderBaseURL validates a provider's base_url using the server's
// injected resolver when tests set one, or net.DefaultResolver otherwise.
// Production request handlers must call this method (not the package-level
// function) so DNS64 discovery and hostname resolution stay hermetically
// testable end to end.
func (s *Server) validateProviderBaseURL(ctx context.Context, raw string, allowLocal bool) error {
	resolver := s.providerResolver
	if resolver == nil {
		resolver = net.DefaultResolver
	}
	return validateProviderBaseURLWithResolver(ctx, raw, allowLocal, resolver)
}

func providerIPError(ip net.IP) error {
	return fmt.Errorf("provider base_url resolves to a non-public address (%s)", ip.String())
}

// dns64AddressMatches reports whether ip is a synthesized NAT64 address (per
// one of the discovered prefixes) whose embedded IPv4 is itself public and
// was also observed as one of the host's plain A records. This is the only
// way a blocked (non-public per providerIPBlocked) IPv6 literal is accepted:
// it must decode, under a locally discovered RFC 6052 prefix, to an IPv4
// address that is both public and independently confirmed by the same
// lookup — never trusting the embedded IPv4 alone.
func dns64AddressMatches(ip net.IP, prefixes []nat64Prefix, publicV4 map[string]struct{}) bool {
	for _, prefix := range prefixes {
		if !prefixMatches(ip, prefix.network, prefix.bits) {
			continue
		}
		embedded, ok := extractRFC6052IPv4(ip, prefix.bits)
		if !ok || providerIPBlocked(embedded) {
			continue
		}
		if _, ok := publicV4[embedded.String()]; ok {
			return true
		}
	}
	return false
}

// validateProviderBaseURLWithResolver is validateProviderBaseURL with an
// injectable resolver, so tests can exercise DNS64/NAT64 behavior
// hermetically. Blocked IPv4 addresses fail immediately. A blocked IPv6
// address is accepted only when it decodes under a prefix discovered via
// ipv4only.arpa to a public IPv4 that was also returned as a plain A record
// for the same host; discovery failure is fail-closed.
func validateProviderBaseURLWithResolver(
	ctx context.Context, raw string, allowLocal bool, resolver providerIPResolver,
) error {
	return validateProviderBaseURLWithOptions(ctx, raw, allowLocal, false, resolver)
}

// providerIPPermitted reports whether an otherwise-blocked address may be used.
// allowLocal (built-in local catalogue entries) admits loopback only. A custom
// user-defined endpoint (allowPrivate) is the user pointing Antares at their
// own service, so private/LAN ranges are accepted there as well. Link-local
// stays blocked for both — it carries the cloud metadata endpoints.
func providerIPPermitted(ip net.IP, allowLocal, allowPrivate bool) bool {
	if allowLocal && ip.IsLoopback() {
		return true
	}
	if allowPrivate && (ip.IsLoopback() || ip.IsPrivate()) {
		return true
	}
	return false
}

// validateCustomProviderBaseURL validates a user-defined provider endpoint:
// the user names the service, so loopback and LAN addresses are allowed.
func (s *Server) validateCustomProviderBaseURL(ctx context.Context, raw string) error {
	resolver := s.providerResolver
	if resolver == nil {
		resolver = net.DefaultResolver
	}
	return validateProviderBaseURLWithOptions(ctx, raw, true, true, resolver)
}

// validateChosenBaseURL picks the URL rule for a provider being connected:
// user-defined endpoints may live on loopback/LAN, built-ins use their
// catalogue's local flag. Callers pass local=false alongside custom=true.
func (s *Server) validateChosenBaseURL(ctx context.Context, raw string, custom, local bool) error {
	if custom {
		return s.validateCustomProviderBaseURL(ctx, raw)
	}
	return s.validateProviderBaseURL(ctx, raw, local)
}

func validateProviderBaseURLWithOptions(
	ctx context.Context, raw string, allowLocal, allowPrivate bool, resolver providerIPResolver,
) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return errors.New("provider base_url is required")
	}
	u, err := url.ParseRequestURI(raw)
	if err != nil || u.Scheme == "" || u.Hostname() == "" {
		return errors.New("provider base_url must be an absolute HTTP(S) URL")
	}
	if !strings.EqualFold(u.Scheme, "http") && !strings.EqualFold(u.Scheme, "https") {
		return errors.New("provider base_url must use http or https")
	}
	if u.User != nil {
		return errors.New("provider base_url must not contain userinfo")
	}

	host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	if ip := net.ParseIP(host); ip != nil {
		if providerIPBlocked(ip) && !providerIPPermitted(ip, allowLocal, allowPrivate) {
			return providerIPError(ip)
		}
		return nil
	}

	lookupCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	ips, err := resolver.LookupIP(lookupCtx, "ip", host)
	if err != nil {
		return fmt.Errorf("provider host cannot be resolved: %w", err)
	}
	if len(ips) == 0 {
		return errors.New("provider host has no address")
	}

	publicV4 := map[string]struct{}{}
	var blockedV6 []net.IP
	for _, ip := range ips {
		blocked := providerIPBlocked(ip) && !providerIPPermitted(ip, allowLocal, allowPrivate)
		if !blocked {
			if v4 := ip.To4(); v4 != nil && !providerIPBlocked(v4) {
				publicV4[v4.String()] = struct{}{}
			}
			continue
		}
		if ip.To4() != nil {
			return providerIPError(ip)
		}
		blockedV6 = append(blockedV6, append(net.IP(nil), ip...))
	}
	if len(blockedV6) == 0 {
		return nil
	}

	prefixes, err := discoverNAT64Prefixes(lookupCtx, resolver)
	if err != nil {
		return providerIPError(blockedV6[0])
	}
	for _, ip := range blockedV6 {
		if !dns64AddressMatches(ip, prefixes, publicV4) {
			return providerIPError(ip)
		}
	}
	return nil
}

func providerIPBlocked(ip net.IP) bool {
	if ip == nil || ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast() {
		return true
	}
	for _, cidr := range []string{
		"0.0.0.0/8", "100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24",
		"198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "240.0.0.0/4",
		"2001:db8::/32",
	} {
		_, network, err := net.ParseCIDR(cidr)
		if err == nil && network.Contains(ip) {
			return true
		}
	}
	return false
}

var rfc6052PrefixLengths = [...]int{32, 40, 48, 56, 64, 96}

func validRFC6052PrefixLength(bits int) bool {
	for _, candidate := range rfc6052PrefixLengths {
		if bits == candidate {
			return true
		}
	}
	return false
}

func extractRFC6052IPv4(ip net.IP, prefixBits int) (net.IP, bool) {
	v6 := ip.To16()
	if v6 == nil || ip.To4() != nil || !validRFC6052PrefixLength(prefixBits) {
		return nil, false
	}
	// RFC 6052 reserves bits 64-71 as the zero-valued "u" octet.
	if v6[8] != 0 {
		return nil, false
	}
	if prefixBits == 96 {
		return net.IPv4(v6[12], v6[13], v6[14], v6[15]), true
	}
	compact := make([]byte, 15)
	copy(compact[:8], v6[:8])
	copy(compact[8:], v6[9:])
	offset := prefixBits / 8
	return net.IPv4(
		compact[offset],
		compact[offset+1],
		compact[offset+2],
		compact[offset+3],
	), true
}

type providerIPResolver interface {
	LookupIP(context.Context, string, string) ([]net.IP, error)
}

type nat64Prefix struct {
	network net.IP
	bits    int
}

func prefixMatches(ip, network net.IP, bits int) bool {
	left, right := ip.To16(), network.To16()
	if left == nil || right == nil {
		return false
	}
	mask := net.CIDRMask(bits, 128)
	return left.Mask(mask).Equal(right.Mask(mask))
}

func isIPv4OnlyWKA(ip net.IP) bool {
	return ip.Equal(net.IPv4(192, 0, 0, 170)) || ip.Equal(net.IPv4(192, 0, 0, 171))
}

// nat64Candidate records the evidence an ipv4only.arpa answer gives for one
// prefix: which well-known IPv4 addresses were embedded there, and whether
// some answer placed a well-known address there and nowhere else.
type nat64Candidate struct {
	prefix nat64Prefix
	wka    map[string]bool
	sole   bool
}

// discoverNAT64Prefixes learns the NAT64 prefixes in use from ipv4only.arpa,
// keeping them in the order the resolver returned (RFC 7050, Section 3).
//
// A well-known IPv4 address can sit at more than one RFC 6052 placement of
// the same answer when the prefix itself repeats those octets. RFC 7050
// requires the value to be present only once and, when it is not, to repeat
// the search with the other well-known address: only a placement both
// 192.0.0.170 and 192.0.0.171 agree on survives. Candidates that neither
// test resolves are dropped, because a spurious shorter prefix would widen
// the network that validation is willing to accept.
func discoverNAT64Prefixes(ctx context.Context, resolver providerIPResolver) ([]nat64Prefix, error) {
	ips, err := resolver.LookupIP(ctx, "ip6", "ipv4only.arpa")
	if err != nil {
		return nil, err
	}
	candidates := map[string]*nat64Candidate{}
	var order []string
	for _, ip := range ips {
		var placements []*nat64Candidate
		for _, bits := range rfc6052PrefixLengths {
			embedded, ok := extractRFC6052IPv4(ip, bits)
			if !ok || !isIPv4OnlyWKA(embedded) {
				continue
			}
			mask := net.CIDRMask(bits, 128)
			network := append(net.IP(nil), ip.To16().Mask(mask)...)
			key := fmt.Sprintf("%d:%x", bits, []byte(network))
			candidate := candidates[key]
			if candidate == nil {
				candidate = &nat64Candidate{
					prefix: nat64Prefix{network: network, bits: bits},
					wka:    map[string]bool{},
				}
				candidates[key] = candidate
				order = append(order, key)
			}
			candidate.wka[embedded.String()] = true
			placements = append(placements, candidate)
		}
		if len(placements) == 1 {
			placements[0].sole = true
		}
	}

	var out []nat64Prefix
	for _, key := range order {
		if candidate := candidates[key]; candidate.sole || len(candidate.wka) > 1 {
			out = append(out, candidate.prefix)
		}
	}
	if len(out) == 0 {
		return nil, errors.New("DNS64 prefix discovery returned no unambiguous RFC 6052 prefix")
	}
	return out, nil
}
