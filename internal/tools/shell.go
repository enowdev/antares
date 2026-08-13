package tools

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
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
	done     chan struct{}
	lastUsed time.Time
	cwd      string
	dead     atomic.Bool
	ps       bool // true when the session shell speaks PowerShell
}

// isPowerShellShell reports whether a configured shell is PowerShell (any
// edition). Dialect detection is by binary base name, not GOOS, so a user who
// points terminal.shell at bash.exe on Windows still gets the POSIX protocol.
func isPowerShellShell(shell string) bool {
	base := strings.ToLower(filepath.Base(shell))
	base = strings.TrimSuffix(base, ".exe")
	return base == "powershell" || base == "pwsh"
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
		return configured, nil
	}
	if runtime.GOOS == "windows" {
		// Interactive console host. `-Command -` is unusable for a persistent
		// session: it reads all of stdin as one script and executes only at
		// EOF, which never happens on the long-lived pipe, so every call
		// would wait out its full timeout. The interactive host executes each
		// complete line as it arrives, which the PowerShell branch of run()
		// depends on. -NonInteractive makes confirmations fail instead of
		// prompting on stdin (a prompt would wedge the session).
		return "powershell.exe", []string{"-NoLogo", "-NoProfile", "-NonInteractive"}
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
	if s, ok := m.sessions[id]; ok && !s.dead.Load() {
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
	// Keep the persistent shell and every foreground command it starts in an
	// isolated process group. A timed-out command must be terminated as a unit;
	// killing only the shell can leave descendants holding the shell's pipes and
	// wedge the session for all subsequent calls.
	configureProcessGroup(cmd)
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start shell: %w", err)
	}

	s := &shellSession{
		cmd: cmd, stdin: stdin, out: out, done: make(chan struct{}),
		lastUsed: time.Now(), cwd: workspace,
		ps: isPowerShellShell(shell),
	}
	go func() {
		_ = cmd.Wait()
		s.dead.Store(true)
		close(s.done)
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

// killLocked marks the session dead and kills its shell. The caller must hold
// s.mu. Closing stdin and killing the process makes the session unusable, so
// the next session() call starts a fresh shell. Used by terminate() and by
// run() when it abandons a command (timeout, cancellation): leaving the shell
// alive with a half-run command on its stdin pipe would make every subsequent
// call queue behind it and compound timeouts.
func (s *shellSession) killLocked() {
	if s.cmd != nil && s.cmd.Process != nil {
		_ = s.stdin.Close()
		// Kill the whole process group, not just the shell: a timed-out
		// command's descendants keep the shell's pipes open and would wedge
		// the session even after the shell itself is gone.
		killProcessGroup(s.cmd)
		// The kill returns before the OS has released the process's
		// handles (including its working directory), which races callers that
		// remove the workspace right after CloseAll. Wait for the Wait
		// goroutine so resources are actually freed; cap the wait so a wedged
		// process cannot stall the session-recycle path.
		select {
		case <-s.done:
		case <-time.After(2 * time.Second):
		}
	}
	s.dead.Store(true)
}

func (s *shellSession) terminate() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.killLocked()
}

// sentinel marks the end of one command's output and carries its exit code.
const sentinelPrefix = "__ANTARES_DONE_"

// run executes a command in the persistent shell and waits for the sentinel.
func (s *shellSession) run(ctx context.Context, command string, timeout time.Duration, onChunk func(string)) (string, int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.dead.Load() {
		return "", -1, fmt.Errorf("shell has exited")
	}
	s.lastUsed = time.Now()
	s.out.drain()

	marker := fmt.Sprintf("%s%d__", sentinelPrefix, time.Now().UnixNano())
	// A terminal call may enable errexit (`set -e`). Shell options persist just
	// like cwd and exported variables, but errexit must not leak into the next
	// call and kill the shell before its completion sentinel can run.
	//
	// The user command must not inherit this pipe as its stdin. The pipe stays
	// open for the life of the session (so the next tool call can write another
	// script). Commands such as `adb shell …` and `ssh host …` read stdin by
	// default: they either block forever on the open pipe or consume the
	// completion sentinel meant for the parent shell, so the tool hangs until
	// timeout even though the remote work already finished. A brace group with
	// `</dev/null` detaches the command without a subshell, so `cd` and
	// `export` still persist across calls.
	//
	// PowerShell (the Windows default shell) cannot parse any of that syntax:
	// `set +e`, `{ … } </dev/null` and `printf` are all errors there, so the
	// user command never runs, no sentinel is ever printed, and every terminal
	// call waits out the full timeout. The Windows branch emits a
	// PowerShell-native script instead, with these protocol invariants (all
	// verified empirically against PS 5.1):
	//
	//  1. The interactive console host only completes a statement when it is
	//     parsed complete, so every protocol line is a single-line statement.
	//  2. The command is embedded in single-quoted `$c` (embedded single quotes
	//     doubled) and executed with Invoke-Expression. The `$null | <command>`
	//     detach used for everything before broke cmdlets with
	//     ParameterBindingException and rejected bare expressions with a parse
	//     error; only natives tolerate it. Natives still need it to detach
	//     stdin, so the first token is classified with Get-Command.
	//  3. Get-Command needs -ErrorAction Ignore: SilentlyContinue still appends
	//     the miss to $Error and poisons the exit-code delta below. The first
	//     command token is resolved with the PowerShell tokenizer
	//     ([PSParser]::Tokenize) rather than a naive split on spaces, so a
	//     quoted path ("C:\Program Files\..."), the call operator (& exe), and
	//     pipelines all classify their real leading command — a space-split
	//     misfired on every one of these and skipped the stdin detach for the
	//     exact native commands (adb, ssh, ...) that need it.
	//  4. Exit status: native codes ride in $LASTEXITCODE; cmdlet/expression
	//     failures are detected via $Error. $Error is cleared right before the
	//     command (rather than diffing a saved count) so a flood of >256 errors
	//     cannot saturate the ring buffer and hide a failure. `$ok` defaults to
	//     false so a terminating error — e.g. a command that sets
	//     ErrorActionPreference='Stop' and then fails, aborting the line before
	//     `$ok` is computed — still maps to exit code 1.
	//  5. The sentinel is assembled from quoted fragments so the console's echo
	//     never contains the full marker verbatim; `function global:prompt
	//     { '' }` only shrinks the prompt (the `PS>` echo prefix is hardcoded
	//     and stripped by sanitizePS).
	var script string
	if s.ps {
		nano := marker[len(sentinelPrefix) : len(marker)-2] // bare digits, no trailing __
		flat := flattenPSCommand(command)
		// Embed the flattened command in a single-quoted PowerShell string;
		// doubling embedded single quotes is the PS escape for a literal quote.
		quoted := strings.ReplaceAll(flat, "'", "''")
		// __antares_cmd0: the real leading command name, via the PS tokenizer.
		// Falls back to the first whitespace-delimited field if tokenizing finds
		// no command token (e.g. a bare expression), which correctly classifies
		// as non-Application and skips the stdin detach.
		classify := "$__t=[System.Management.Automation.PSParser]::Tokenize($c,[ref]$null); " +
			"$cmd0=($__t | Where-Object { $_.Type -eq 'Command' } | Select-Object -First 1).Content; " +
			"if (-not $cmd0) { $cmd0=($c -split '\\s+')[0] }"
		script = "$ErrorActionPreference = 'Continue'; $LASTEXITCODE = $null; $global:__antares_code = $null; function global:prompt { '' }\n" +
			"$c='" + quoted + "'; $ok=$false; " + classify + "; $Error.Clear(); " +
			"if ((Get-Command $cmd0 -ErrorAction Ignore | Select-Object -First 1).CommandType -eq 'Application') { iex ('$null | ' + $c) } else { iex $c }; " +
			"if ($Error.Count -eq 0) { $ok=$true }\n" +
			"if ($LASTEXITCODE -ne $null) { $global:__antares_code = $LASTEXITCODE } elseif (-not $ok) { $global:__antares_code = 1 } else { $global:__antares_code = 0 }\n" +
			"Write-Output (\"`n\" + '" + sentinelPrefix + "' + '" + nano + "__' + $global:__antares_code)\n"
	} else {
		script = "set +e\n{\n" + command + "\n} </dev/null\nprintf '\\n" + marker + "%s\\n' \"$?\"\n"
	}
	if _, err := io.WriteString(s.stdin, script); err != nil {
		s.dead.Store(true)
		return "", -1, fmt.Errorf("write to shell: %w", err)
	}

	deadline := time.Now().Add(timeout)
	ticker := time.NewTicker(60 * time.Millisecond)
	defer ticker.Stop()
	sent := 0

	for {
		select {
		case <-ctx.Done():
			// The command's fate is unknowable and it may still hold the shell
			// busy; recycle so the next call starts fresh instead of queuing.
			s.killLocked()
			return s.finish(marker), -1, ctx.Err()
		case <-s.done:
			// The shell can exit before printing the sentinel (for example when a
			// command enables `set -e` and then fails). Do not wait out the full
			// timeout for a marker that can no longer arrive.
			return s.finish(marker), -1, fmt.Errorf("shell exited before command completed")
		case <-ticker.C:
			buf := s.out.snapshot()
			if onChunk != nil && len(buf) > sent {
				chunk := buf[sent:]
				if idx := strings.Index(chunk, marker); idx >= 0 {
					chunk = chunk[:idx]
				}
				if s.ps {
					chunk = sanitizePS(chunk)
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
					out := buf[:idx]
					if s.ps {
						out = sanitizePS(out)
					}
					return strings.TrimRight(out, "\n"), code, nil
				}
			}
			if time.Now().After(deadline) {
				out := s.finish(marker)
				// Same recycle as ctx.Done: the timed-out command is still
				// occupying the persistent shell and would block the next call
				// on the same stdin pipe for another full timeout.
				s.killLocked()
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
	if s.ps {
		buf = sanitizePS(buf)
	}
	return strings.TrimRight(buf, "\n")
}

// flattenPSCommand joins a possibly multi-line command into a single line for
// the interactive PowerShell session. Newlines become statement separators;
// the `; ;` runs left by blank lines are collapsed because PowerShell rejects
// empty statements. Multi-line quoted strings change meaning under flattening;
// agent commands are single-line in practice, and the alternative (letting the
// console host parse multi-line constructs) hangs the session on continuation
// prompts.
func flattenPSCommand(command string) string {
	flat := strings.ReplaceAll(strings.ReplaceAll(command, "\r\n", "\n"), "\n", "; ")
	flat = strings.ReplaceAll(flat, "\r", "; ")
	for {
		next := strings.ReplaceAll(flat, "; ;", ";")
		if next == flat {
			break
		}
		flat = next
	}
	flat = strings.TrimSpace(flat)
	if flat == "" {
		flat = "Write-Output ''"
	}
	return flat
}

// sanitizePS strips the interactive console's prompt and echoed-input lines
// from captured output. PowerShell echoes every script line it reads: the
// prompt line is `PS>` (the custom empty prompt shrinks it to that) followed by
// the echoed command, and continuation lines are prefixed `>>`. CRLF is
// normalized so parsing and display match the POSIX path.
//
// The match is deliberately narrow. An earlier version stripped any line
// beginning with `PS ` or `>>`, which silently deleted legitimate command
// output — a line like `Write-Output "PS scripts"`, or diff/merge output
// containing `>>>>>>>` conflict markers. We only drop the prompt echo (`PS>`)
// and a continuation line that is *only* the `>>` marker (optionally followed
// by echoed input), never an arbitrary line that happens to start with `>>`.
func sanitizePS(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	lines := strings.Split(s, "\n")
	kept := make([]string, 0, len(lines))
	for _, ln := range lines {
		t := strings.TrimLeft(ln, " \t")
		// Prompt echo: `PS>` or `PS C:\path>` — a `PS` prefix immediately
		// followed by (optional path and) `>` then the echoed command. Require
		// the `>` so ordinary prose beginning with "PS " is preserved.
		if strings.HasPrefix(t, "PS>") || isPSPromptEcho(t) {
			continue
		}
		// Continuation echo is the bare `>>` marker the host prints while it
		// waits for more input; real output almost never is exactly this.
		if t == ">>" || strings.HasPrefix(t, ">> ") {
			continue
		}
		kept = append(kept, ln)
	}
	return strings.Join(kept, "\n")
}

// isPSPromptEcho reports whether a line looks like a `PS <path>>` prompt echo:
// it starts with "PS " and, before any space after that, contains a ">" (the
// prompt terminator). This matches `PS C:\Users\me>` without matching ordinary
// text such as "PS scripts are great".
func isPSPromptEcho(t string) bool {
	if !strings.HasPrefix(t, "PS ") {
		return false
	}
	rest := t[len("PS "):]
	gt := strings.IndexByte(rest, '>')
	if gt < 0 {
		return false
	}
	sp := strings.IndexByte(rest, ' ')
	// The ">" must come before the first space (it is part of the prompt token,
	// not somewhere later in echoed command text).
	return sp < 0 || gt < sp
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
