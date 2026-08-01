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
	if len(args) != 1 || args[0] != "-i" {
		t.Fatalf("configured shell args = %#v, want [-i]", args)
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
		t.Fatalf("daemon-forking command did not complete via sentinel: %v", err)
	}
	if code != 0 || !strings.Contains(out, "parent-done") {
		t.Fatalf("command result = (%q, %d), want parent-done and code 0", out, code)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("sentinel arrived after %s — the shell blocked on the daemon's inherited stdin", elapsed)
	}
}
