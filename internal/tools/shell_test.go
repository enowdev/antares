package tools

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
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
		t.Fatalf("configured shell args = %#v, want non-interactive shell", args)
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

func TestPersistentShellPreservesLiteralTabsBangUnicodeAndHeredoc(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX persistent shell protocol does not apply on Windows")
	}

	m := NewShellManager(config.Terminal{Shell: "/bin/bash"})
	t.Cleanup(m.CloseAll)
	workspace := t.TempDir()
	sess, err := m.session("literal-session", workspace)
	if err != nil {
		t.Fatal(err)
	}

	command := "python3 - <<'PY'\nfrom pathlib import Path\nPath('literal.txt').write_text('\\tif (!ready) — ok\\n', encoding='utf-8')\nPY\ncat literal.txt"
	out, code, err := sess.run(context.Background(), command, 2*time.Second, nil)
	if err != nil || code != 0 {
		t.Fatalf("literal command result = (%q, %d, %v)", out, code, err)
	}
	want := "\tif (!ready) — ok"
	if out != want {
		t.Fatalf("literal command output = %q, want %q", out, want)
	}
	data, err := os.ReadFile(filepath.Join(workspace, "literal.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != want+"\n" {
		t.Fatalf("literal file = %q, want %q", data, want+"\n")
	}
}
