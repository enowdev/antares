package daemon

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestResolverOrder(t *testing.T) {
	present := map[string]bool{}
	r := Resolver{
		Executable: "/Applications/Antares.app/Contents/MacOS/Antares",
		GOOS:       "darwin",
		LookPath: func(name string) (string, error) {
			if name != "antares" {
				t.Fatalf("looked up %q", name)
			}
			return "/opt/homebrew/bin/antares", nil
		},
		HomeDir: "/Users/me",
		IsFile:  func(p string) bool { return present[p] },
	}
	bundled := "/Applications/Antares.app/Contents/Resources/antares"
	onPath := "/opt/homebrew/bin/antares"
	localBin := "/Users/me/.local/bin/antares"

	if _, err := r.Resolve(); !errors.Is(err, ErrNotInstalled) {
		t.Fatalf("nothing present: %v", err)
	}
	present[localBin] = true
	if c, _ := r.Resolve(); c.Path != localBin || c.Source != "local-bin" {
		t.Fatalf("want local-bin, got %+v", c)
	}
	present[onPath] = true
	if c, _ := r.Resolve(); c.Path != onPath || c.Source != "path" {
		t.Fatalf("want path, got %+v", c)
	}
	present[bundled] = true
	if c, _ := r.Resolve(); c.Path != bundled || c.Source != "bundled" {
		t.Fatalf("want bundled, got %+v", c)
	}
}

func TestResolverNonBundleLayouts(t *testing.T) {
	r := Resolver{Executable: `C:\Program Files\Antares\Antares.exe`, GOOS: "windows",
		LookPath: func(string) (string, error) { return "", exec.ErrNotFound }, HomeDir: "/home/me"}
	cs := r.Candidates()
	if len(cs) != 2 || cs[0].Source != "bundled" || filepath.Base(cs[0].Path) != "antares.exe" || cs[1].Source != "local-bin" {
		t.Fatalf("windows candidates: %+v", cs)
	}
	r = Resolver{Executable: "/usr/lib/antares-desktop/antares-desktop", GOOS: "linux",
		LookPath: func(string) (string, error) { return "", exec.ErrNotFound }, HomeDir: "/home/me"}
	if cs := r.Candidates(); cs[0].Path != "/usr/lib/antares-desktop/antares" {
		t.Fatalf("linux bundled: %+v", cs)
	}
	// A darwin binary run outside a bundle looks next to itself.
	r = Resolver{Executable: "/tmp/build/Antares", GOOS: "darwin",
		LookPath: func(string) (string, error) { return "", exec.ErrNotFound }, HomeDir: "/Users/me"}
	if cs := r.Candidates(); cs[0].Path != "/tmp/build/antares" {
		t.Fatalf("darwin unbundled: %+v", cs)
	}
}

func TestResolverRealFiles(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("exec bit")
	}
	dir := t.TempDir()
	exe := filepath.Join(dir, "Antares.app", "Contents", "MacOS", "Antares")
	res := filepath.Join(dir, "Antares.app", "Contents", "Resources", "antares")
	os.MkdirAll(filepath.Dir(exe), 0o755)
	os.MkdirAll(filepath.Dir(res), 0o755)
	os.WriteFile(res, []byte("#!/bin/sh\n"), 0o644) // not executable yet
	r := Resolver{Executable: exe, GOOS: "darwin", HomeDir: dir,
		LookPath: func(string) (string, error) { return "", exec.ErrNotFound }}
	if _, err := r.Resolve(); err == nil {
		t.Fatal("non-executable file accepted")
	}
	os.Chmod(res, 0o755)
	if c, err := r.Resolve(); err != nil || c.Path != res {
		t.Fatalf("%+v %v", c, err)
	}
}

func TestParseState(t *testing.T) {
	st, err := ParseState([]byte(`{"pid": 4242, "start_time": "Thu Oct  2 03:00:00 2026", "exe": "/x/antares", "url": "http://127.0.0.1:8787", "version": "v0.5.0", "started_at": "2026-10-02T03:00:00Z", "managed": false}`))
	if err != nil || st.PID != 4242 || st.URL != "http://127.0.0.1:8787" || st.Version != "v0.5.0" {
		t.Fatalf("%+v %v", st, err)
	}
	for _, bad := range []string{`not json`, `{"pid": 0, "url": "http://x"}`, `{"pid": 1, "url": "http://x"}`, `{"pid": 77}`} {
		if _, err := ParseState([]byte(bad)); err == nil {
			t.Errorf("ParseState(%s) accepted", bad)
		}
	}
}

func TestProbeStatus(t *testing.T) {
	home := t.TempDir()
	healthy := true
	p := Probe{
		Home:  home,
		Alive: func(pid int) bool { return pid == 4242 },
		Health: func(_ context.Context, url string) error {
			if !healthy {
				return errors.New("down")
			}
			return nil
		},
	}
	ctx := context.Background()
	if st, err := p.Status(ctx); err != nil || st.Running || st.Stale {
		t.Fatalf("no file: %+v %v", st, err)
	}
	write := func(s string) { os.WriteFile(filepath.Join(home, "antares.pid"), []byte(s), 0o600) }

	write(`{"pid": 4242, "exe": "/x/antares", "url": "http://127.0.0.1:8787/", "version": "v1"}`)
	if st, _ := p.Status(ctx); !st.Running || st.URL != "http://127.0.0.1:8787" || st.Version != "v1" {
		t.Fatalf("running: %+v", st)
	}
	healthy = false
	if st, _ := p.Status(ctx); st.Running || !st.Stale {
		t.Fatalf("unhealthy should be stale: %+v", st)
	}
	healthy = true
	write(`{"pid": 99999, "exe": "/x/antares", "url": "http://127.0.0.1:8787"}`) // dead pid
	if st, _ := p.Status(ctx); st.Running || !st.Stale || st.PID != 99999 {
		t.Fatalf("stale pid: %+v", st)
	}
	write(`garbage`)
	if st, _ := p.Status(ctx); st.Running || !st.Stale {
		t.Fatalf("garbage: %+v", st)
	}
}

func TestProcessAliveReal(t *testing.T) {
	if !processAlive(os.Getpid()) {
		t.Fatal("own pid not alive")
	}
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	if processAlive(cmd.Process.Pid) {
		t.Skip("pid reused already") // vanishingly rare
	}
}

func TestHomeHonoursEnv(t *testing.T) {
	t.Setenv("ANTARES_HOME", "/tmp/antares-test-home")
	if Home() != "/tmp/antares-test-home" {
		t.Fatal(Home())
	}
	t.Setenv("ANTARES_HOME", "")
	h, _ := os.UserHomeDir()
	if Home() != filepath.Join(h, ".antares") {
		t.Fatal(Home())
	}
}

func TestParsePair(t *testing.T) {
	out := []byte("note: something\n{\"device\": {\"id\": \"dev_0123456789abcdef\", \"name\": \"Mac\", \"platform\": \"desktop-macos\", \"created_at\": \"2026-10-02T03:00:00Z\"}, \"token\": \"atd_x\", \"url\": \"http://127.0.0.1:8787\"}\n")
	p, err := ParsePair(out)
	if err != nil || p.Token != "atd_x" || p.Device.ID != "dev_0123456789abcdef" || p.URL != "http://127.0.0.1:8787" {
		t.Fatalf("%+v %v", p, err)
	}
	if _, err := ParsePair([]byte("Error: unknown command \"device\"")); err == nil {
		t.Fatal("no json accepted")
	}
	if _, err := ParsePair([]byte(`{"device": {"id": "dev_1"}}`)); err == nil {
		t.Fatal("missing token accepted")
	}
}

// A fake antares script exercises CLI end to end (args, env, error mapping).
func TestCLIAgainstFakeBinary(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "antares")
	script := `#!/bin/sh
case "$1 $2" in
  "device pair") echo "{\"device\":{\"id\":\"dev_abc\",\"name\":\"$4\",\"platform\":\"$6\"},\"token\":\"atd_fake\",\"url\":\"http://127.0.0.1:1\",\"home\":\"$ANTARES_HOME\"}";;
  "stop "*) echo "Antares is not running" >&2; exit 1;;
  *) echo "Error: unknown command \"$1\"" >&2; exit 1;;
esac
`
	os.WriteFile(bin, []byte(script), 0o755)
	t.Setenv("ANTARES_HOME", filepath.Join(dir, "home"))
	cli := CLI{Bin: bin}
	p, err := cli.Pair(context.Background(), "My Mac", "desktop-macos")
	if err != nil || p.Token != "atd_fake" || p.Device.Name != "My Mac" || p.Device.Platform != "desktop-macos" {
		t.Fatalf("%+v %v", p, err)
	}
	err = cli.Stop(context.Background())
	if err == nil || err.Error() != "antares stop: Antares is not running" {
		t.Fatalf("stop error: %v", err)
	}
	old := CLI{Bin: filepath.Join(dir, "old")}
	os.WriteFile(old.Bin, []byte("#!/bin/sh\necho 'Error: unknown command \"device\" for \"antares\"' >&2\nexit 1\n"), 0o755)
	if _, err := old.Pair(context.Background(), "m", "desktop-macos"); err == nil || !contains(err.Error(), "too old") {
		t.Fatalf("old binary: %v", err)
	}
	if _, err := (CLI{}).Pair(context.Background(), "m", "p"); !errors.Is(err, ErrNotInstalled) {
		t.Fatalf("no bin: %v", err)
	}
}

func contains(s, sub string) bool { return strings.Contains(s, sub) }
