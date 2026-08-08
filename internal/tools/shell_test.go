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
	if elapsed > time.Second {
		t.Fatalf("took %s, want prompt completion well under 1s (sentinel not stolen)", elapsed)
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
