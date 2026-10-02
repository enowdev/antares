// Package api is the desktop shell's client for the Antares server endpoints
// in the desktop contract (version, pairing, handoff, devices, health), with
// HTTP failures mapped to errors the connection screen can show as-is.
package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// ContractVersion is the desktop contract this shell speaks.
const ContractVersion = 1

var (
	// ErrTooOld: the server predates the desktop endpoints.
	ErrTooOld = errors.New("this server is too old for the desktop app — update Antares there")
	// ErrSignedOut: the device token was refused (revoked or unknown).
	ErrSignedOut = errors.New("signed out — this device was removed on the server; pair again")
	// ErrBadPassword: pairing was refused for the password.
	ErrBadPassword = errors.New("wrong password")
	// ErrUnreachable: no HTTP answer at all.
	ErrUnreachable = errors.New("can't reach the server")
)

// Error is a non-2xx answer that maps to no sentinel above.
type Error struct {
	Status  int
	Message string
}

func (e *Error) Error() string {
	if e.Message != "" {
		return e.Message
	}
	return fmt.Sprintf("server answered %d %s", e.Status, http.StatusText(e.Status))
}

// Client talks to one Antares server.
type Client struct {
	BaseURL string // no trailing slash
	Token   string // device token; empty for unauthenticated calls
	HTTP    *http.Client
}

// New returns a client with sane timeouts.
func New(baseURL, token string) *Client {
	return &Client{
		BaseURL: strings.TrimRight(baseURL, "/"),
		Token:   token,
		HTTP: &http.Client{
			Timeout: 15 * time.Second,
			// The handoff and API calls never need redirects; following one
			// could replay the bearer to another origin.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}
}

// Version is GET /api/version.
type Version struct {
	Version    string `json:"version"`
	Contract   int    `json:"contract"`
	MinDesktop string `json:"min_desktop"`
}

// Device is a paired device as the server reports it.
type Device struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	Platform   string     `json:"platform"`
	CreatedAt  time.Time  `json:"created_at"`
	LastSeenAt *time.Time `json:"last_seen_at,omitempty"`
	RevokedAt  *time.Time `json:"revoked_at,omitempty"`
	Current    bool       `json:"current,omitempty"`
}

// Pairing is POST /api/devices/pair's answer.
type Pairing struct {
	Device Device `json:"device"`
	Token  string `json:"token"`
}

// Handoff is POST /api/auth/handoff's answer.
type Handoff struct {
	Code      string `json:"code"`
	URL       string `json:"url"`
	ExpiresIn int    `json:"expires_in"`
}

// Version checks the server speaks the desktop contract (and accepts this
// shell's version, appVersion, when the server names a minimum).
func (c *Client) Version(ctx context.Context, appVersion string) (Version, error) {
	var v Version
	status, body, err := c.do(ctx, http.MethodGet, "/api/version", nil, false)
	if err != nil {
		return v, err
	}
	if status == http.StatusNotFound || status == http.StatusMethodNotAllowed {
		return v, ErrTooOld
	}
	if status != http.StatusOK {
		return v, errorFrom(status, body)
	}
	if json.Unmarshal(body, &v) != nil || v.Version == "" && v.Contract == 0 {
		// A 200 that isn't the version JSON: an old server's SPA fallback
		// serving index.html, or not an Antares at all.
		return v, ErrTooOld
	}
	if v.Contract < ContractVersion {
		return v, ErrTooOld
	}
	if v.MinDesktop != "" && appVersion != "" && CompareVersions(appVersion, v.MinDesktop) < 0 {
		return v, fmt.Errorf("this server needs Antares desktop %s or newer (this is %s)", v.MinDesktop, appVersion)
	}
	return v, nil
}

// Pair creates a device. password may be empty on an open server or when the
// client already holds a bearer.
func (c *Client) Pair(ctx context.Context, name, platform, password string) (Pairing, error) {
	var p Pairing
	req := map[string]string{"name": name, "platform": platform}
	if password != "" {
		req["password"] = password
	}
	status, body, err := c.do(ctx, http.MethodPost, "/api/devices/pair", req, c.Token != "")
	if err != nil {
		return p, err
	}
	switch status {
	case http.StatusOK, http.StatusCreated:
	case http.StatusNotFound, http.StatusMethodNotAllowed:
		return p, ErrTooOld
	case http.StatusUnauthorized:
		msg := serverMessage(body)
		if password != "" && (msg == "" || strings.Contains(strings.ToLower(msg), "password")) {
			return p, ErrBadPassword
		}
		if msg == "" {
			msg = "this server needs its dashboard password to pair"
		}
		return p, &Error{Status: status, Message: msg}
	case http.StatusTooManyRequests:
		return p, &Error{Status: status, Message: "too many attempts — wait a minute and try again"}
	default:
		return p, errorFrom(status, body)
	}
	if err := json.Unmarshal(body, &p); err != nil || p.Token == "" {
		return Pairing{}, fmt.Errorf("unexpected pairing answer from the server")
	}
	return p, nil
}

// Handoff mints a one-time login code; next is a dashboard path ("" = "/").
func (c *Client) Handoff(ctx context.Context, next string) (Handoff, error) {
	var h Handoff
	req := map[string]string{}
	if next != "" {
		req["next"] = next
	}
	status, body, err := c.do(ctx, http.MethodPost, "/api/auth/handoff", req, true)
	if err != nil {
		return h, err
	}
	if status == http.StatusNotFound {
		return h, ErrTooOld
	}
	if err := authorizedStatus(status, body); err != nil {
		return h, err
	}
	if err := json.Unmarshal(body, &h); err != nil || h.URL == "" || !strings.HasPrefix(h.URL, "/") || strings.HasPrefix(h.URL, "//") {
		return Handoff{}, fmt.Errorf("unexpected handoff answer from the server")
	}
	return h, nil
}

// RevokeDevice is DELETE /api/devices/{id} (revoking this device is allowed).
func (c *Client) RevokeDevice(ctx context.Context, id string) error {
	status, body, err := c.do(ctx, http.MethodDelete, "/api/devices/"+url.PathEscape(id), nil, true)
	if err != nil {
		return err
	}
	return authorizedStatus(status, body)
}

// CheckToken makes a cheap authorized call (GET /api/devices) to learn
// whether the device token is still accepted; ErrSignedOut when not.
func (c *Client) CheckToken(ctx context.Context) error {
	status, body, err := c.do(ctx, http.MethodGet, "/api/devices", nil, true)
	if err != nil {
		return err
	}
	return authorizedStatus(status, body)
}

// Health is GET /api/health; nil means {"ok": true}.
func (c *Client) Health(ctx context.Context) error {
	status, body, err := c.do(ctx, http.MethodGet, "/api/health", nil, false)
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return errorFrom(status, body)
	}
	var h struct {
		OK bool `json:"ok"`
	}
	if json.Unmarshal(body, &h) != nil || !h.OK {
		return &Error{Status: status, Message: "the server is not healthy"}
	}
	return nil
}

func authorizedStatus(status int, body []byte) error {
	switch {
	case status >= 200 && status < 300:
		return nil
	case status == http.StatusUnauthorized:
		return ErrSignedOut
	case status == http.StatusNotFound && !isJSONError(body):
		return ErrTooOld
	case status == http.StatusMethodNotAllowed:
		return ErrTooOld
	default:
		return errorFrom(status, body)
	}
}

func (c *Client) do(ctx context.Context, method, path string, in any, auth bool) (int, []byte, error) {
	var rd io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return 0, nil, err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, rd)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Accept", "application/json")
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if auth && c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	hc := c.HTTP
	if hc == nil {
		hc = http.DefaultClient
	}
	resp, err := hc.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return 0, nil, ctx.Err()
		}
		return 0, nil, fmt.Errorf("%w: %v", ErrUnreachable, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return 0, nil, fmt.Errorf("%w: %v", ErrUnreachable, err)
	}
	if ct, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type")); ct == "text/html" && resp.StatusCode == http.StatusOK && method != http.MethodGet {
		// An HTML 200 to a POST/DELETE is an SPA fallback, not our API.
		return http.StatusNotFound, nil, nil
	}
	return resp.StatusCode, body, nil
}

func errorFrom(status int, body []byte) error {
	return &Error{Status: status, Message: serverMessage(body)}
}

func serverMessage(body []byte) string {
	var e struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(body, &e) == nil {
		return strings.TrimSpace(e.Error)
	}
	return ""
}

func isJSONError(body []byte) bool { return serverMessage(body) != "" }

// CompareVersions compares dotted versions ("v0.5.0-16-g9b24008" counts as
// 0.5.0): -1, 0 or 1. Missing or non-numeric parts count as 0.
func CompareVersions(a, b string) int {
	pa, pb := versionParts(a), versionParts(b)
	for i := 0; i < 3; i++ {
		if pa[i] != pb[i] {
			if pa[i] < pb[i] {
				return -1
			}
			return 1
		}
	}
	return 0
}

func versionParts(v string) [3]int {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if i := strings.IndexAny(v, "-+ "); i >= 0 {
		v = v[:i]
	}
	var out [3]int
	for i, part := range strings.SplitN(v, ".", 3) {
		n, _ := strconv.Atoi(part)
		out[i] = n
	}
	return out
}
