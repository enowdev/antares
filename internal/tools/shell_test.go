package tools

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/enowdev/antares/internal/config"
)

func TestDefaultShellDoesNotInheritNonPOSIXShell(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell selection does not apply on Windows")
	}

	old := os.Getenv("SHELL")
	t.Cleanup(func() { _ = os.Setenv("SHELL", old) })
	if err := os.Setenv("SHELL", "/bin/fish"); err != nil {
		t.Fatal(err)
	}

	shell, _ := defaultShell("")
	if shell == "/bin/fish" {
		t.Fatalf("defaultShell inherited %q, but the sentinel protocol requires a POSIX shell", shell)
	}
	if shell != "/bin/bash" && shell != "/bin/sh" {
		t.Fatalf("defaultShell = %q, want /bin/bash or /bin/sh", shell)
	}
}

func TestDefaultShellHonorsExplicitConfiguration(t *testing.T) {
	shell, args := defaultShell("/custom/shell")
	if shell != "/custom/shell" {
		t.Fatalf("defaultShell(configured) = %q", shell)
	}
	if len(args) != 0 {
		t.Fatalf("configured shell args = %#v, want non-interactive mode", args)
	}
}

func TestDefaultShellCommandEmitsCompletionSentinel(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX persistent shell protocol does not apply on Windows")
	}

	m := NewShellManager(config.Terminal{})
	t.Cleanup(m.CloseAll)
	sess, err := m.session("test-session", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	out, code, err := sess.run(context.Background(), "printf ANTARES_SHELL_OK", 2*time.Second, nil)
	if err != nil {
		t.Fatalf("command did not complete via sentinel: %v", err)
	}
	if code != 0 || out != "ANTARES_SHELL_OK" {
		t.Fatalf("command result = (%q, %d), want (%q, 0)", out, code, "ANTARES_SHELL_OK")
	}
}

func TestPersistentShellErrexitDoesNotLeakBetweenCalls(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell options do not apply on Windows")
	}

	m := NewShellManager(config.Terminal{})
	t.Cleanup(m.CloseAll)
	sess, err := m.session("errexit-session", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	if _, code, err := sess.run(context.Background(), "set -e; true", 2*time.Second, nil); err != nil || code != 0 {
		t.Fatalf("enabling errexit = code %d, err %v", code, err)
	}

	start := time.Now()
	_, code, err := sess.run(context.Background(), "printf x | grep definitely-no-match", 2*time.Second, nil)
	if err != nil {
		t.Fatalf("no-match pipeline after prior set -e did not reach sentinel: %v", err)
	}
	if code != 1 {
		t.Fatalf("no-match pipeline exit code = %d, want 1", code)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("failure after prior set -e took %s, want prompt completion", elapsed)
	}
}

func TestPersistentShellExitReturnsPromptlyAndRecovers(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell exit behavior does not apply on Windows")
	}

	m := NewShellManager(config.Terminal{})
	t.Cleanup(m.CloseAll)
	workspace := t.TempDir()
	sess, err := m.session("exit-session", workspace)
	if err != nil {
		t.Fatal(err)
	}

	start := time.Now()
	_, _, err = sess.run(context.Background(), "set -e; false", 5*time.Second, nil)
	if err == nil || !strings.Contains(err.Error(), "shell exited") {
		t.Fatalf("shell exit error = %v, want shell exited", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("shell exit detected after %s, want under 1s", elapsed)
	}

	replacement, err := m.session("exit-session", workspace)
	if err != nil {
		t.Fatal(err)
	}
	if replacement == sess {
		t.Fatal("dead persistent shell was reused")
	}
	out, code, err := replacement.run(context.Background(), "printf RECOVERED", 2*time.Second, nil)
	if err != nil || code != 0 || out != "RECOVERED" {
		t.Fatalf("replacement shell result = (%q, %d, %v)", out, code, err)
	}
}

func TestPersistentShellTimeoutKillsCommandAndRecovers(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX process-group behavior does not apply on Windows")
	}

	m := NewShellManager(config.Terminal{})
	t.Cleanup(m.CloseAll)
	workspace := t.TempDir()
	sess, err := m.session("timeout-session", workspace)
	if err != nil {
		t.Fatal(err)
	}

	start := time.Now()
	_, _, err = sess.run(context.Background(), "sleep 60", 150*time.Millisecond, nil)
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("timeout error = %v, want timed out", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("timeout returned after %s, want prompt cancellation", elapsed)
	}
	if !sess.dead.Load() {
		t.Fatal("timed-out shell was left marked live")
	}

	// The next call must receive a replacement shell rather than appending to
	// the command that was killed at the timeout boundary.
	replacement, err := m.session("timeout-session", workspace)
	if err != nil {
		t.Fatal(err)
	}
	if replacement == sess {
		t.Fatal("timed-out persistent shell was reused")
	}
	out, code, err := replacement.run(context.Background(), "printf RECOVERED", 2*time.Second, nil)
	if err != nil || code != 0 || out != "RECOVERED" {
		t.Fatalf("replacement shell result = (%q, %d, %v)", out, code, err)
	}
}

// Commands like `adb shell …` inherit the persistent shell's stdin pipe. Because
// that pipe stays open between tool calls, they block reading (or steal the
// completion sentinel). Wrapping the user command so its stdin is /dev/null
// lets the sentinel run as soon as the command finishes.
func TestPersistentShellClosesCommandStdinSoADBStyleCommandsFinish(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX persistent shell protocol does not apply on Windows")
	}

	// Build a helper that prints then blocks on stdin — the adb shell pattern.
	dir := t.TempDir()
	src := dir + "/block.go"
	bin := dir + "/block"
	if err := os.WriteFile(src, []byte(`package main
import ("fmt"; "io"; "os")
func main() {
	fmt.Println("Events injected: 1")
	io.Copy(io.Discard, os.Stdin)
}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	build := exec.Command("go", "build", "-o", bin, src)
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build helper: %v\n%s", err, out)
	}

	m := NewShellManager(config.Terminal{})
	t.Cleanup(m.CloseAll)
	sess, err := m.session("stdin-block-session", dir)
	if err != nil {
		t.Fatal(err)
	}

	start := time.Now()
	out, code, err := sess.run(context.Background(), bin, 2*time.Second, nil)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("stdin-blocking command hung/failed: %v (after %s)\nout=%q", err, elapsed, out)
	}
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; out=%q", code, out)
	}
	if !strings.Contains(out, "Events injected: 1") {
		t.Fatalf("output = %q", out)
	}
	// The real signal is that the command returned at all rather than running to
	// the 2s timeout: a stolen sentinel makes the tool wait for the deadline.
	// The bound is generous because this whole package runs in parallel under
	// `go test ./...`, where process startup alone can cost several hundred ms —
	// a 1s bound failed there while passing in isolation.
	if elapsed > 1500*time.Millisecond {
		t.Fatalf("took %s, want completion well before the 2s timeout (sentinel not stolen)", elapsed)
	}

	// Session still works after a stdin-hungry command.
	out, code, err = sess.run(context.Background(), "printf STILL_ALIVE", 2*time.Second, nil)
	if err != nil || code != 0 || out != "STILL_ALIVE" {
		t.Fatalf("follow-up = (%q, %d, %v)", out, code, err)
	}
}

// cd/export must survive the stdin redirect wrap (brace group, not subshell).
func TestPersistentShellStdinWrapPreservesCdAndExport(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX persistent shell protocol does not apply on Windows")
	}
	m := NewShellManager(config.Terminal{})
	t.Cleanup(m.CloseAll)
	ws := t.TempDir()
	sub := ws + "/sub"
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	sess, err := m.session("persist-session", ws)
	if err != nil {
		t.Fatal(err)
	}
	if _, code, err := sess.run(context.Background(), "cd sub && export ANTARES_TEST_FLAG=1", 2*time.Second, nil); err != nil || code != 0 {
		t.Fatalf("cd/export: code=%d err=%v", code, err)
	}
	out, code, err := sess.run(context.Background(), "pwd; printf '%s' \"$ANTARES_TEST_FLAG\"", 2*time.Second, nil)
	if err != nil || code != 0 {
		t.Fatalf("follow-up: %v code=%d", err, code)
	}
	if !strings.Contains(out, "sub") || !strings.Contains(out, "1") {
		t.Fatalf("cwd/export not preserved: %q", out)
	}
}

// TestPersistentShellClosesInheritedStdinForDaemonForkingClients guards against
// the adb regression: a client like `adb` forks a background daemon at first
// invocation, and that daemon inherits every fd of the persistent shell it was
// launched from. Without redirecting stdin to /dev/null, bash then blocks on
// the completion sentinel forever because a reader on its stdin pipe is still
// alive, so a command that finished in milliseconds times out at
// terminal.timeout (300 s) instead.
func TestPersistentShellClosesInheritedStdinForDaemonForkingClients(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX persistent shell protocol does not apply on Windows")
	}
	if _, err := exec.LookPath("setsid"); err != nil {
		t.Skip("setsid unavailable — cannot simulate a daemon-forking client")
	}

	m := NewShellManager(config.Terminal{Shell: "/bin/bash"})
	t.Cleanup(m.CloseAll)
	sess, err := m.session("daemon-session", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	pidFile := filepath.Join(t.TempDir(), "daemon.pid")
	command := "setsid /bin/sh -c 'sleep 60' >/dev/null 2>&1 & echo $! >" + pidFile + "; echo parent-done"
	start := time.Now()
	out, code, err := sess.run(context.Background(), command, 3*time.Second, nil)
	elapsed := time.Since(start)

	// Clean up the lingering child before asserting so a slow assertion path
	// does not leak a stray process into the test environment.
	if raw, readErr := os.ReadFile(pidFile); readErr == nil {
		if pid := strings.TrimSpace(string(raw)); pid != "" {
			_ = exec.Command("/bin/sh", "-c", "kill -TERM "+pid+" 2>/dev/null || true").Run()
		}
	}

	if err != nil {
		t.Fatalf("daemon-forking command hung/failed: %v (after %s)\nout=%q", err, elapsed, out)
	}
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; out=%q", code, out)
	}
	if !strings.Contains(out, "parent-done") {
		t.Fatalf("output = %q, want parent-done", out)
	}
	if elapsed > time.Second {
		t.Fatalf("took %s, want completion well under 1s (daemon must not hold shell stdin)", elapsed)
	}
}

// ---------------------------------------------------------------------------
// Windows: persistent PowerShell session (interactive console host protocol).
// ---------------------------------------------------------------------------

func newPSSession(t *testing.T) *shellSession {
	t.Helper()
	m := NewShellManager(config.Terminal{})
	// TempDir must be created before the CloseAll cleanup is registered:
	// cleanups run LIFO, so the shell process is killed before TempDir's
	// RemoveAll runs, otherwise powershell still holding the CWD makes
	// RemoveAll fail and the test fails on cleanup.
	dir := t.TempDir()
	t.Cleanup(m.CloseAll)
	sess, err := m.session("ps-test-session", dir)
	if err != nil {
		t.Fatal(err)
	}
	if !sess.ps {
		t.Fatalf("session shell is not PowerShell, got ps=%v", sess.ps)
	}
	return sess
}

func TestPSBasicEchoCompletesViaSentinel(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("PowerShell session protocol is Windows-only")
	}
	sess := newPSSession(t)
	start := time.Now()
	out, code, err := sess.run(context.Background(), "Write-Output HELLO", 5*time.Second, nil)
	if err != nil {
		t.Fatalf("command did not complete via sentinel: %v", err)
	}
	if code != 0 || !strings.Contains(out, "HELLO") {
		t.Fatalf("command result = (%q, %d), want HELLO + 0", out, code)
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("basic command took %s, want prompt completion", elapsed)
	}
	if strings.Contains(out, "PS>") || strings.Contains(out, "PS ") {
		t.Fatalf("output polluted with console prompts: %q", out)
	}
}

func TestPSNativeExitCodePropagates(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("PowerShell session protocol is Windows-only")
	}
	sess := newPSSession(t)
	_, code, err := sess.run(context.Background(), "cmd /c exit 7", 5*time.Second, nil)
	if err != nil {
		t.Fatalf("native command failed: %v", err)
	}
	if code != 7 {
		t.Fatalf("native exit code = %d, want 7", code)
	}
}

func TestPSCmdletFailureMapsToOne(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("PowerShell session protocol is Windows-only")
	}
	sess := newPSSession(t)
	_, code, err := sess.run(context.Background(), "dir C:\\nope-not-real-zzz", 5*time.Second, nil)
	if err != nil {
		t.Fatalf("cmdlet failure must still reach the sentinel: %v", err)
	}
	if code != 1 {
		t.Fatalf("cmdlet failure code = %d, want 1", code)
	}
}

func TestPSExitCodeDoesNotGoStale(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("PowerShell session protocol is Windows-only")
	}
	sess := newPSSession(t)
	if _, code, err := sess.run(context.Background(), "cmd /c exit 3", 5*time.Second, nil); err != nil || code != 3 {
		t.Fatalf("first command = code %d, err %v", code, err)
	}
	_, code, err := sess.run(context.Background(), "Write-Output hi", 5*time.Second, nil)
	if err != nil {
		t.Fatalf("second command failed: %v", err)
	}
	if code != 0 {
		t.Fatalf("second command code = %d, want 0 (stale LASTEXITCODE leaked)", code)
	}
}

func TestPSClosesCommandStdinSoStdinReadingCommandsFinish(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("PowerShell session protocol is Windows-only")
	}
	sess := newPSSession(t)
	start := time.Now()
	_, code, err := sess.run(context.Background(), "cmd /c \"set /p x=READ:\"", 5*time.Second, nil)
	if err != nil {
		t.Fatalf("stdin-reading command did not finish: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("took %s, want prompt completion (stdin must be detached)", elapsed)
	}
	if code != 1 {
		t.Fatalf("set /p on detached stdin should fail, code = %d, want 1", code)
	}
}

func TestPSCdPersistsAcrossCalls(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("PowerShell session protocol is Windows-only")
	}
	sess := newPSSession(t)
	if _, code, err := sess.run(context.Background(), "Set-Location C:\\Windows", 5*time.Second, nil); err != nil || code != 0 {
		t.Fatalf("Set-Location = code %d, err %v", code, err)
	}
	out, code, err := sess.run(context.Background(), "(Get-Location).Path", 5*time.Second, nil)
	if err != nil || code != 0 {
		t.Fatalf("Get-Location = code %d, err %v", code, err)
	}
	if !strings.Contains(out, "C:\\Windows") {
		t.Fatalf("cwd did not persist: %q", out)
	}
}

func TestPSTimeoutKillsSessionAndNextCallRecovers(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("PowerShell session protocol is Windows-only")
	}
	m := NewShellManager(config.Terminal{})
	dir := t.TempDir()
	t.Cleanup(m.CloseAll)
	sess, err := m.session("ps-timeout-session", dir)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	_, _, err = sess.run(context.Background(), "Start-Sleep -Seconds 30", 1*time.Second, nil)
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("blocking command err = %v, want timeout", err)
	}
	if time.Since(start) > 4*time.Second {
		t.Fatalf("timeout took %s, want ~1s", time.Since(start))
	}
	// The timed-out command held the shell; the next call must start fresh.
	sess, err = m.session("ps-timeout-session", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	out, code, err := sess.run(context.Background(), "Write-Output RECOVERED", 5*time.Second, nil)
	if err != nil || code != 0 || !strings.Contains(out, "RECOVERED") {
		t.Fatalf("post-timeout command = (%q, %d, %v), want RECOVERED/0/nil", out, code, err)
	}
}

func TestPSEAPStopLeakDoesNotHangNextCall(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("PowerShell session protocol is Windows-only")
	}
	sess := newPSSession(t)
	// A command that flips ErrorActionPreference to Stop and then fails must
	// not kill the session or hang the sentinel.
	if _, code, err := sess.run(context.Background(), "$ErrorActionPreference='Stop'; dir C:\\nope-stop-zzz", 5*time.Second, nil); err != nil || code != 1 {
		t.Fatalf("EAP=Stop failing command = code %d, err %v", code, err)
	}
	start := time.Now()
	out, code, err := sess.run(context.Background(), "Write-Output AFTER_STOP", 5*time.Second, nil)
	if err != nil || code != 0 || !strings.Contains(out, "AFTER_STOP") {
		t.Fatalf("post-Stop command = (%q, %d, %v), want AFTER_STOP/0/nil", out, code, err)
	}
	if time.Since(start) > 3*time.Second {
		t.Fatalf("post-Stop command took %s, want prompt completion", time.Since(start))
	}
}

func TestPSMultiLineCommandFlattens(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("PowerShell session protocol is Windows-only")
	}
	sess := newPSSession(t)
	out, code, err := sess.run(context.Background(), "Write-Output A\nWrite-Output B", 5*time.Second, nil)
	if err != nil || code != 0 {
		t.Fatalf("multi-line command = code %d, err %v", code, err)
	}
	if !strings.Contains(out, "A") || !strings.Contains(out, "B") {
		t.Fatalf("multi-line output = %q, want A and B", out)
	}
}

// TestPSBareExpressionRuns covers the construct that the old `$null |`
// protocol rejected with a parse error ("Expressions are only allowed as the
// first element of a pipeline") — the iex classification must run it plainly.
func TestPSBareExpressionRuns(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("PowerShell session protocol is Windows-only")
	}
	sess := newPSSession(t)
	out, code, err := sess.run(context.Background(), "(Get-Location).Path", 5*time.Second, nil)
	if err != nil || code != 0 {
		t.Fatalf("bare expression = code %d, err %v", code, err)
	}
	if !strings.Contains(out, "Temp") && !strings.Contains(out, "Windows") {
		t.Fatalf("expression output = %q, want a filesystem path", out)
	}
}

// TestPSAssignmentPersists ensures assignments run in the session scope and
// survive into the next call (the old protocol needed a dot-sourced block for
// these; iex runs them in scope directly).
func TestPSAssignmentPersists(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("PowerShell session protocol is Windows-only")
	}
	sess := newPSSession(t)
	if _, code, err := sess.run(context.Background(), "$antares_probe = 42", 5*time.Second, nil); err != nil || code != 0 {
		t.Fatalf("assignment = code %d, err %v", code, err)
	}
	out, code, err := sess.run(context.Background(), "Write-Output $antares_probe", 5*time.Second, nil)
	if err != nil || code != 0 || !strings.Contains(out, "42") {
		t.Fatalf("assigned var read = (%q, %d, %v), want 42/0/nil", out, code, err)
	}
}

// TestPSSingleQuotesSurviveEmbedding guards the $c='...' wrapper: user single
// quotes are doubled when embedded and must come back exactly.
func TestPSSingleQuotesSurviveEmbedding(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("PowerShell session protocol is Windows-only")
	}
	sess := newPSSession(t)
	out, code, err := sess.run(context.Background(), "Write-Output 'it''s fine'", 5*time.Second, nil)
	if err != nil || code != 0 {
		t.Fatalf("quoted command = code %d, err %v", code, err)
	}
	if !strings.Contains(out, "it's fine") {
		t.Fatalf("quoted output = %q, want it's fine", out)
	}
}

// TestSanitizePSKeepsLegitimateOutput pins the fix for the regression where
// sanitizePS dropped any line beginning with "PS " or ">>", silently deleting
// real command output. These are pure-string cases and run on every platform.
func TestSanitizePSKeepsLegitimateOutput(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "strips prompt echo but keeps output",
			in:   "PS C:\\Users\\me> Get-Date\nMonday, 1 January 2026\n",
			want: "Monday, 1 January 2026\n",
		},
		{
			name: "strips bare PS> prompt",
			in:   "PS> echo hi\nhi\n",
			want: "hi\n",
		},
		{
			name: "keeps prose starting with 'PS '",
			in:   "PS scripts are great\n",
			want: "PS scripts are great\n",
		},
		{
			name: "keeps conflict markers",
			in:   "<<<<<<< HEAD\nmine\n>>>>>>> theirs\n",
			want: "<<<<<<< HEAD\nmine\n>>>>>>> theirs\n",
		},
		{
			name: "keeps a line that merely starts with >>",
			in:   ">>note: this is real output\n",
			want: ">>note: this is real output\n",
		},
		{
			name: "strips a bare continuation marker",
			in:   ">>\nreal\n",
			want: "real\n",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := sanitizePS(c.in); got != c.want {
				t.Fatalf("sanitizePS(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestIsPSPromptEcho(t *testing.T) {
	yes := []string{"PS C:\\Users\\me>", "PS C:\\path> some command", "PS >"}
	no := []string{"PS scripts are great", "PSReadLine module", "plain text", "PS no closing angle here"}
	for _, s := range yes {
		if !isPSPromptEcho(s) {
			t.Errorf("isPSPromptEcho(%q) = false, want true", s)
		}
	}
	for _, s := range no {
		if isPSPromptEcho(s) {
			t.Errorf("isPSPromptEcho(%q) = true, want false", s)
		}
	}
}
