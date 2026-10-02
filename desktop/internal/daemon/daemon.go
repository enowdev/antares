// Package daemon finds the `antares` binary and drives the local daemon
// through it (status from the state file, serve, stop, device pair). The
// desktop shell never writes the daemon's state file.
package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// StartTimeout is how long Start waits for the daemon's health check.
const StartTimeout = 20 * time.Second

// ErrNotInstalled means no antares binary was found anywhere.
var ErrNotInstalled = errors.New("the antares command was not found — install Antares or use the app's bundled copy")

// Home is the Antares state directory: $ANTARES_HOME, default ~/.antares.
func Home() string {
	if v := strings.TrimSpace(os.Getenv("ANTARES_HOME")); v != "" {
		if strings.HasPrefix(v, "~") {
			if h, err := os.UserHomeDir(); err == nil {
				return filepath.Join(h, strings.TrimPrefix(v, "~"))
			}
		}
		return os.ExpandEnv(v)
	}
	h, err := os.UserHomeDir()
	if err != nil || h == "" {
		return ".antares"
	}
	return filepath.Join(h, ".antares")
}

// State is <home>/antares.pid as the daemon writes it.
type State struct {
	PID       int    `json:"pid"`
	StartTime string `json:"start_time,omitempty"`
	Exe       string `json:"exe"`
	URL       string `json:"url"`
	Version   string `json:"version"`
	StartedAt string `json:"started_at"`
	Managed   bool   `json:"managed,omitempty"`
}

// Status is what the connection screen shows for "This Mac".
type Status struct {
	Running bool   `json:"running"`
	URL     string `json:"url,omitempty"`
	PID     int    `json:"pid,omitempty"`
	Version string `json:"version,omitempty"`
	// Stale: a state file is present but its process is gone (or its
	// server doesn't answer).
	Stale bool `json:"stale,omitempty"`
}

// ParseState decodes a state file's bytes.
func ParseState(b []byte) (State, error) {
	var s State
	if err := json.Unmarshal(b, &s); err != nil {
		return State{}, fmt.Errorf("invalid daemon state: %w", err)
	}
	if s.PID <= 1 || s.URL == "" {
		return State{}, errors.New("invalid daemon state: missing pid or url")
	}
	return s, nil
}

// Probe reads the daemon state under home and reports whether it is
// running: file present, pid alive, GET {url}/api/health → {"ok": true}.
type Probe struct {
	Home   string
	Alive  func(pid int) bool                          // default: processAlive
	Health func(ctx context.Context, url string) error // default: httpHealth
}

// Status never returns an error for "not running"; only for an unreadable
// state file.
func (p Probe) Status(ctx context.Context) (Status, error) {
	b, err := os.ReadFile(filepath.Join(p.home(), "antares.pid"))
	if errors.Is(err, os.ErrNotExist) {
		return Status{}, nil
	}
	if err != nil {
		return Status{}, err
	}
	st, err := ParseState(b)
	if err != nil {
		return Status{Stale: true}, nil
	}
	alive := p.Alive
	if alive == nil {
		alive = processAlive
	}
	if !alive(st.PID) {
		return Status{Stale: true, PID: st.PID}, nil
	}
	health := p.Health
	if health == nil {
		health = httpHealth
	}
	if err := health(ctx, st.URL); err != nil {
		return Status{Stale: true, PID: st.PID, URL: st.URL}, nil
	}
	return Status{Running: true, URL: strings.TrimRight(st.URL, "/"), PID: st.PID, Version: st.Version}, nil
}

func (p Probe) home() string {
	if p.Home != "" {
		return p.Home
	}
	return Home()
}

func httpHealth(ctx context.Context, url string) error {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(url, "/")+"/api/health", nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	var h struct {
		OK bool `json:"ok"`
	}
	if resp.StatusCode != http.StatusOK || json.NewDecoder(resp.Body).Decode(&h) != nil || !h.OK {
		return fmt.Errorf("health check failed (%d)", resp.StatusCode)
	}
	return nil
}

// Resolver finds the antares binary in the contract's order: the copy
// bundled with the app (Contents/Resources/antares on macOS, next to the
// executable elsewhere), then `antares` on PATH, then ~/.local/bin/antares.
type Resolver struct {
	Executable string                       // this app's executable; default os.Executable
	GOOS       string                       // default runtime.GOOS
	LookPath   func(string) (string, error) // default exec.LookPath
	HomeDir    string                       // default os.UserHomeDir
	IsFile     func(path string) bool       // default: regular, executable file
}

// Candidate is one place the resolver looked.
type Candidate struct {
	Source string `json:"source"` // bundled | path | local-bin
	Path   string `json:"path"`
}

// Resolve returns the first usable binary.
func (r Resolver) Resolve() (Candidate, error) {
	for _, c := range r.Candidates() {
		if r.isFile(c.Path) {
			return c, nil
		}
	}
	return Candidate{}, ErrNotInstalled
}

// Candidates lists the places Resolve tries, in order. PATH lookups that
// fail are left out.
func (r Resolver) Candidates() []Candidate {
	goos := r.GOOS
	if goos == "" {
		goos = runtime.GOOS
	}
	name := "antares"
	if goos == "windows" {
		name = "antares.exe"
	}
	var out []Candidate
	exe := r.Executable
	if exe == "" {
		exe, _ = os.Executable()
		if resolved, err := filepath.EvalSymlinks(exe); err == nil {
			exe = resolved
		}
	}
	if exe != "" {
		dir := filepath.Dir(exe)
		if goos == "darwin" && filepath.Base(dir) == "MacOS" && filepath.Base(filepath.Dir(dir)) == "Contents" {
			out = append(out, Candidate{"bundled", filepath.Join(filepath.Dir(dir), "Resources", name)})
		} else {
			out = append(out, Candidate{"bundled", filepath.Join(dir, name)})
		}
	}
	look := r.LookPath
	if look == nil {
		look = exec.LookPath
	}
	if p, err := look(name); err == nil && p != "" {
		if abs, err := filepath.Abs(p); err == nil {
			p = abs
		}
		out = append(out, Candidate{"path", p})
	}
	home := r.HomeDir
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	if home != "" {
		out = append(out, Candidate{"local-bin", filepath.Join(home, ".local", "bin", name)})
	}
	return out
}

func (r Resolver) isFile(p string) bool {
	if r.IsFile != nil {
		return r.IsFile(p)
	}
	fi, err := os.Stat(p)
	if err != nil || !fi.Mode().IsRegular() {
		return false
	}
	return runtime.GOOS == "windows" || fi.Mode().Perm()&0o111 != 0
}

// PairResult is `antares device pair --json`'s output.
type PairResult struct {
	Device struct {
		ID        string `json:"id"`
		Name      string `json:"name"`
		Platform  string `json:"platform"`
		CreatedAt string `json:"created_at"`
	} `json:"device"`
	Token string `json:"token"`
	URL   string `json:"url"`
}

// ParsePair decodes `device pair --json` output (tolerating log lines before
// the JSON object).
func ParsePair(out []byte) (PairResult, error) {
	var res PairResult
	i := bytes.IndexByte(out, '{')
	if i < 0 {
		return res, errors.New("antares device pair printed no JSON")
	}
	if err := json.Unmarshal(bytes.TrimSpace(out[i:]), &res); err != nil {
		return res, fmt.Errorf("antares device pair: %w", err)
	}
	if res.Token == "" || res.Device.ID == "" {
		return res, errors.New("antares device pair returned no token")
	}
	return res, nil
}

// CLI runs the antares binary.
type CLI struct {
	Bin string
	Env []string // extra env; os.Environ() (and so ANTARES_HOME) is inherited
}

// Pair creates a device directly in the local store.
func (c CLI) Pair(ctx context.Context, name, platform string) (PairResult, error) {
	out, err := c.run(ctx, 30*time.Second, "device", "pair", "--name", name, "--platform", platform, "--json")
	if err != nil {
		if looksLikeUnknownCommand(out) {
			return PairResult{}, fmt.Errorf("this antares (%s) is too old to pair the desktop app — update it", c.Bin)
		}
		return PairResult{}, cmdError("antares device pair", out, err)
	}
	return ParsePair(out)
}

// Revoke revokes a device in the local store (used when the daemon is down).
func (c CLI) Revoke(ctx context.Context, id string) error {
	out, err := c.run(ctx, 30*time.Second, "device", "revoke", id)
	if err != nil {
		return cmdError("antares device revoke", out, err)
	}
	return nil
}

// Serve starts the daemon; `antares serve` returns once it is up (or says it
// already was).
func (c CLI) Serve(ctx context.Context) error {
	out, err := c.run(ctx, StartTimeout+5*time.Second, "serve")
	if err != nil {
		return cmdError("antares serve", out, err)
	}
	return nil
}

// Stop stops the daemon.
func (c CLI) Stop(ctx context.Context) error {
	out, err := c.run(ctx, 30*time.Second, "stop")
	if err != nil {
		return cmdError("antares stop", out, err)
	}
	return nil
}

func (c CLI) run(ctx context.Context, timeout time.Duration, args ...string) ([]byte, error) {
	if c.Bin == "" {
		return nil, ErrNotInstalled
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, c.Bin, args...)
	cmd.Env = append(os.Environ(), c.Env...)
	cmd.Stdin = nil
	// `serve` forks the daemon, which inherits our stdout/stderr pipes;
	// WaitDelay keeps a long-lived child holding them from wedging us.
	cmd.WaitDelay = 2 * time.Second
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	if errors.Is(err, exec.ErrWaitDelay) {
		err = nil
	}
	return buf.Bytes(), err
}

func looksLikeUnknownCommand(out []byte) bool {
	s := strings.ToLower(string(out))
	return strings.Contains(s, "unknown command") || strings.Contains(s, "unknown subcommand")
}

func cmdError(what string, out []byte, err error) error {
	msg := strings.TrimSpace(string(out))
	if len(msg) > 400 {
		msg = msg[len(msg)-400:]
	}
	if msg == "" {
		return fmt.Errorf("%s: %w", what, err)
	}
	return fmt.Errorf("%s: %s", what, msg)
}

// WaitRunning polls p until the daemon is healthy or timeout passes.
func WaitRunning(ctx context.Context, p Probe, timeout time.Duration) (Status, error) {
	deadline := time.Now().Add(timeout)
	for {
		st, err := p.Status(ctx)
		if err == nil && st.Running {
			return st, nil
		}
		if time.Now().After(deadline) {
			return st, fmt.Errorf("Antares did not come up within %s", timeout)
		}
		select {
		case <-ctx.Done():
			return st, ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
}
