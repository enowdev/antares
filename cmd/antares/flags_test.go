package main

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// `antares --version` and `--help` are commands, not options of the default
// serve; the installers and the release workflow run `antares --version`.
func TestTopLevelFlags(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the binary")
	}
	bin := filepath.Join(t.TempDir(), "antares-test")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	for _, tc := range []struct{ arg, want string }{
		{"--version", "commit"},
		{"-v", "commit"},
		{"version", "commit"},
		{"--help", "antares"},
		{"-h", "antares"},
	} {
		out, err := exec.Command(bin, tc.arg).CombinedOutput()
		if err != nil || !strings.Contains(string(out), tc.want) {
			t.Errorf("antares %s: %v\n%s", tc.arg, err, out)
		}
	}
}
