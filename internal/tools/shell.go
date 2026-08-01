package tools

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/enowdev/antares/internal/config"
	"github.com/enowdev/antares/internal/sandbox"
)

// ShellManager owns one long-lived shell per session so `cd`, exported
// variables, and activated virtualenvs survive between tool calls.
type ShellManager struct {
	mu       sync.Mutex
	sessions map[string]*shellSession
	jobs     map[string]*backgroundProcess
	cfg      config.Terminal
	// sandboxOnce keeps a confinement warning from repeating on every shell.
	sandboxOnce sync.Once
	// httpShim, when set, routes curl/wget through the fingerprinted client by
	// prepending a shim directory to the shell's PATH.
	httpShim httpShimEnv
}

// httpShimEnv holds the environment a shell needs to use the HTTP shims.
type httpShimEnv struct {
	dir    string
	preset string
	proxy  string
}

// NewShellManager builds a manager for the given terminal config.
func NewShellManager(cfg config.Terminal) *ShellManager {
	return &ShellManager{sessions: map[string]*shellSession{}, jobs: map[string]*backgroundProcess{}, cfg: cfg}
}

// EnableHTTPShim makes new shells route curl/wget through the fingerprinted
// client. dir is the directory holding the shim scripts.
func (m *ShellManager) EnableHTTPShim(dir, preset, proxy string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.httpShim = httpShimEnv{dir: dir, preset: preset, proxy: proxy}
}

type shellSession struct {
	mu       sync.Mutex
	cmd      *exec.Cmd
	stdin    io.WriteCloser
	out      *lockedBuffer
	lastUsed time.Time
	cwd      string
	dead     bool
}

// lockedBuffer collects interleaved stdout/stderr safely.
type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) drain() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	s := l.b.String()
	l.b.Reset()
	return s
}

func (l *lockedBuffer) snapshot() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// withShimEnv prepends the shim directory to PATH and adds the variables the
// shims read. The existing PATH entry is rewritten in place so the shim
// directory is searched first.
func withShimEnv(env []string, shim httpShimEnv) []string {
	sep := string(os.PathListSeparator)
	out := make([]string, 0, len(env)+3)
	replaced := false
	for _, kv := range env {
		if strings.HasPrefix(kv, "PATH=") {
			out = append(out, "PATH="+shim.dir+sep+strings.TrimPrefix(kv, "PATH="))
			replaced = true
			continue
		}
		out = append(out, kv)
	}
	if !replaced {
		out = append(out, "PATH="+shim.dir+sep+os.Getenv("PATH"))
	}
	out = append(out,
		"ANTARES_SHIM_DIR="+shim.dir,
		"ANTARES_HTTP_PRESET="+shim.preset,
		"ANTARES_HTTP_PROXY="+shim.proxy,
	)
	return out
}

func defaultShell(configured string) (string, []string) {
	if configured != "" {
		// Persistent state does not require an interactive shell. Running with -i
		// enables readline and history expansion, which corrupts pasted commands:
		// literal tabs trigger completion and `!` raises "event not found".
		return configured, nil
	}
	if runtime.GOOS == "windows" {
		return "powershell.exe", []string{"-NoLogo", "-NoProfile", "-Command", "-"}
	}
	// The persistent-shell protocol below emits POSIX syntax (`$?`, `printf`).
	// Do not inherit an arbitrary interactive shell such as fish: fish parses
	// `$?` as an error, never prints the completion sentinel, and leaves every
	// terminal call waiting until its timeout even though the command finished.
	for _, sh := range []string{"/bin/bash", "/bin/sh"} {
		if sh == "" {
			continue
		}
		if _, err := os.Stat(sh); err == nil {
			return sh, nil
		}
	}
	return "/bin/sh", nil
}

// session returns the live shell for a session id, starting one if needed.
func (m *ShellManager) session(id, workspace string) (*shellSession, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s, ok := m.sessions[id]; ok && !s.dead && s.cmd.ProcessState == nil {
		s.lastUsed = time.Now()
		return s, nil
	}

	shell, shellArgs := defaultShell(m.cfg.Shell)
	var cmd *exec.Cmd
	switch strings.ToLower(m.cfg.Backend) {
	case "docker":
		image := m.cfg.DockerImage
		if image == "" {
			image = "debian:bookworm-slim"
		}
		net := "none"
		if m.cfg.AllowNetwork {
			net = "bridge"
		}
		cmd = exec.Command("docker", "run", "--rm", "-i",
			"--network", net,
			"-v", workspace+":/workspace", "-w", "/workspace",
			image, "/bin/sh")
	case "ssh":
		if m.cfg.SSHHost == "" {
			return nil, fmt.Errorf("terminal.ssh_host is not configured")
		}
		cmd = exec.Command("ssh", "-tt", m.cfg.SSHHost, "/bin/sh")
	default:
		// The shell is what gives the agent its reach, so it is what gets
		// confined. A sandbox that cannot be built is reported once and then
		// stepped around: refusing to run anything helps nobody.
		mode, note := sandbox.Resolve(sandbox.Mode(m.cfg.Sandbox))
		if note != "" {
			m.warnSandboxOnce(note)
		}
		policy := sandbox.Policy{
			Workspace:    workspace,
			AllowNetwork: m.cfg.AllowNetwork,
			Hidden:       m.hiddenPaths(),
		}
		built, err := sandbox.Command(mode, policy, shell, shellArgs...)
		if err != nil {
			m.warnSandboxOnce(err.Error())
			built = exec.Command(shell, shellArgs...)
		}
		cmd = built
		cmd.Dir = workspace
		env := append(os.Environ(), "ANTARES_SESSION="+id, "TERM=dumb", "PAGER=cat", "GIT_PAGER=cat")
		if m.httpShim.dir != "" {
			env = withShimEnv(env, m.httpShim)
		}
		cmd.Env = env
	}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	out := &lockedBuffer{}
	cmd.Stdout = out
	cmd.Stderr = out
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start shell: %w", err)
	}

	s := &shellSession{cmd: cmd, stdin: stdin, out: out, lastUsed: time.Now(), cwd: workspace}
	go func() {
		_ = cmd.Wait()
		s.mu.Lock()
		s.dead = true
		s.mu.Unlock()
	}()
	m.sessions[id] = s
	return s, nil
}

// Close terminates a session's shell.
func (m *ShellManager) Close(id string) {
	m.mu.Lock()
	s, ok := m.sessions[id]
	delete(m.sessions, id)
	var jobs []*backgroundProcess
	for jobID, job := range m.jobs {
		if job.sessionID == id {
			jobs = append(jobs, job)
			delete(m.jobs, jobID)
		}
	}
	m.mu.Unlock()
	if ok {
		s.terminate()
	}
	stopProcesses(jobs, processCancelled)
}

// CloseAll terminates every live shell.
func (m *ShellManager) CloseAll() {
	m.mu.Lock()
	all := make([]*shellSession, 0, len(m.sessions))
	for _, s := range m.sessions {
		all = append(all, s)
	}
	m.sessions = map[string]*shellSession{}
	jobs := make([]*backgroundProcess, 0, len(m.jobs))
	for _, job := range m.jobs {
		jobs = append(jobs, job)
	}
	m.jobs = map[string]*backgroundProcess{}
	m.mu.Unlock()
	for _, s := range all {
		s.terminate()
	}
	stopProcesses(jobs, processCancelled)
}

// Active reports how many shells are currently running.
func (m *ShellManager) Active() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.sessions)
}

// ReapIdle closes shells idle for longer than lifetime.
func (m *ShellManager) ReapIdle(lifetime time.Duration) {
	if lifetime <= 0 {
		return
	}
	cutoff := time.Now().Add(-lifetime)
	m.mu.Lock()
	var stale []*shellSession
	for id, s := range m.sessions {
		if s.lastUsed.Before(cutoff) {
			stale = append(stale, s)
			delete(m.sessions, id)
		}
	}
	m.mu.Unlock()
	for _, s := range stale {
		s.terminate()
	}
}

func (s *shellSession) terminate() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cmd == nil || s.cmd.Process == nil {
		return
	}
	_ = s.stdin.Close()
	_ = s.cmd.Process.Kill()
	s.dead = true
}

// sentinel marks the end of one command's output and carries its exit code.
const sentinelPrefix = "__ANTARES_DONE_"

// run executes a command in the persistent shell and waits for the sentinel.
func (s *shellSession) run(ctx context.Context, command string, timeout time.Duration, onChunk func(string)) (string, int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.dead {
		return "", -1, fmt.Errorf("shell has exited")
	}
	s.lastUsed = time.Now()
	s.out.drain()

	marker := fmt.Sprintf("%s%d__", sentinelPrefix, time.Now().UnixNano())
	script := command + "\nprintf '\\n" + marker + "%s\\n' \"$?\"\n"
	if _, err := io.WriteString(s.stdin, script); err != nil {
		s.dead = true
		return "", -1, fmt.Errorf("write to shell: %w", err)
	}

	deadline := time.Now().Add(timeout)
	ticker := time.NewTicker(60 * time.Millisecond)
	defer ticker.Stop()
	sent := 0

	for {
		select {
		case <-ctx.Done():
			return s.finish(marker), -1, ctx.Err()
		case <-ticker.C:
			buf := s.out.snapshot()
			if onChunk != nil && len(buf) > sent {
				chunk := buf[sent:]
				if idx := strings.Index(chunk, marker); idx >= 0 {
					chunk = chunk[:idx]
				}
				if chunk != "" {
					onChunk(chunk)
				}
				sent = len(buf)
			}
			if idx := strings.Index(buf, marker); idx >= 0 {
				tail := buf[idx+len(marker):]
				if strings.Contains(tail, "\n") {
					code := 0
					fmt.Sscanf(strings.TrimSpace(strings.SplitN(tail, "\n", 2)[0]), "%d", &code)
					s.out.drain()
					return strings.TrimRight(buf[:idx], "\n"), code, nil
				}
			}
			if time.Now().After(deadline) {
				out := s.finish(marker)
				return out, -1, fmt.Errorf("command timed out after %s", timeout)
			}
		}
	}
}

// finish drains whatever output exists, stripping any sentinel fragment.
func (s *shellSession) finish(marker string) string {
	buf := s.out.drain()
	if idx := strings.Index(buf, marker); idx >= 0 {
		buf = buf[:idx]
	}
	return strings.TrimRight(buf, "\n")
}

// ---- terminal tool ----------------------------------------------------------

type terminalTool struct{}

func (terminalTool) Name() string { return "terminal" }
func (terminalTool) Description() string {
	return "Run a shell command. Foreground commands use a persistent shell; set background=true for work of unknown duration and monitor the returned process_id with the process tool instead of shell sleep."
}
func (terminalTool) RequiresApproval() bool { return true }
func (terminalTool) Schema() map[string]any {
	return schema(map[string]any{
		"command":    prop("string", "Shell command to execute."),
		"timeout":    propDefault("integer", "Foreground: seconds before aborting. Background: optional total runtime limit; zero means no runtime limit.", 0),
		"background": propDefault("boolean", "Start as a managed background process and return immediately with a process_id. Use for builds, analysis, imports, servers, and other commands whose duration is unknown.", false),
	}, "command")
}

func (terminalTool) Execute(ctx context.Context, in Input) Result {
	var args struct {
		Command    string `json:"command"`
		Timeout    int    `json:"timeout"`
		Background bool   `json:"background"`
	}
	if err := in.Bind(&args); err != nil {
		return Errorf("%v", err)
	}
	cmdText := strings.TrimSpace(args.Command)
	if cmdText == "" {
		return Errorf("command is required")
	}
	if in.Deps == nil || in.Deps.Shell == nil {
		return Errorf("terminal backend is not available")
	}
	cfg := in.Deps.Config
	for _, blocked := range cfg.Terminal.BlockedCommands {
		if blocked != "" && strings.Contains(cmdText, blocked) {
			return Errorf("command blocked by policy (matched %q). Adjust terminal.blocked_commands to allow it.", blocked)
		}
	}
	if args.Background {
		if args.Timeout < 0 {
			return Errorf("timeout must be non-negative")
		}
		job, err := in.Deps.Shell.startBackground(in.SessionID, in.Workspace, cmdText, time.Duration(args.Timeout)*time.Second)
		if err != nil {
			return Errorf("cannot start background process: %v", err)
		}
		return processJSON(job.view(0, false))
	}

	timeout := time.Duration(args.Timeout) * time.Second
	if timeout <= 0 {
		timeout = time.Duration(max(cfg.Terminal.Timeout, 60)) * time.Second
	}
	if timeout > 30*time.Minute {
		timeout = 30 * time.Minute
	}

	sess, err := in.Deps.Shell.session(in.SessionID, in.Workspace)
	if err != nil {
		return Errorf("cannot start shell: %v", err)
	}

	out, code, err := sess.run(ctx, cmdText, timeout, func(chunk string) {
		in.Emit(Progress{Tool: "terminal", Chunk: chunk})
	})
	out = trimOutput(out, cfg.Tools.MaxOutputChars)

	switch {
	case err != nil && strings.Contains(err.Error(), "timed out"):
		return Result{Content: fmt.Sprintf("Command timed out after %s.\n\nPartial output:\n%s", timeout, out), IsError: true}
	case err != nil:
		return Errorf("shell error: %v\n%s", err, out)
	case code != 0:
		return Result{
			Content: fmt.Sprintf("Exit code %d\n\n%s", code, out),
			IsError: true,
			Meta:    map[string]any{"exit_code": code},
		}
	}
	if strings.TrimSpace(out) == "" {
		out = "(no output)"
	}
	return Result{Content: out, Meta: map[string]any{"exit_code": 0}}
}

func trimOutput(s string, limit int) string {
	if limit <= 0 {
		limit = 60000
	}
	if len(s) <= limit {
		return s
	}
	head := limit * 2 / 3
	tail := limit - head
	return s[:head] + fmt.Sprintf("\n\n… %d characters omitted …\n\n", len(s)-limit) + s[len(s)-tail:]
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// warnSandboxOnce logs a confinement problem the first time only. It happens
// per shell start, and repeating it every time would bury the log.
func (m *ShellManager) warnSandboxOnce(note string) {
	m.sandboxOnce.Do(func() {
		slog.Warn("terminal sandbox", "detail", note)
	})
}

// hiddenPaths resolves the credential directories kept out of the sandbox.
func (m *ShellManager) hiddenPaths() []string {
	list := m.cfg.SandboxHidden
	if len(list) == 0 {
		list = sandbox.DefaultHidden
	}
	out := make([]string, 0, len(list))
	for _, p := range list {
		if expanded := config.Expand(p); expanded != "" {
			out = append(out, expanded)
		}
	}
	return out
}
