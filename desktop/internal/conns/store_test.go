package conns

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestStoreRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "Antares", "connections.json")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := s.List(); len(got) != 0 {
		t.Fatalf("empty store listed %v", got)
	}
	local, err := s.Add(Connection{Name: "This Mac", Mode: ModeLocal, DeviceID: "dev_1"})
	if err != nil {
		t.Fatal(err)
	}
	remote, err := s.Add(Connection{Name: " VPS ", Mode: ModeRemote, URL: "https://Antares.Example.com/", DeviceID: "dev_2"})
	if err != nil {
		t.Fatal(err)
	}
	if remote.Name != "VPS" || remote.URL != "https://antares.example.com" {
		t.Fatalf("not normalised: %+v", remote)
	}
	if err := s.SetLast(remote.ID); err != nil {
		t.Fatal(err)
	}

	again, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	got := again.List()
	if len(got) != 2 || got[0].ID != local.ID || got[1].ID != remote.ID || again.Last() != remote.ID {
		t.Fatalf("round trip lost data: %+v last=%q", got, again.Last())
	}
	if got[0].URL != "" || got[0].Mode != ModeLocal || got[1].DeviceID != "dev_2" || got[1].CreatedAt.IsZero() {
		t.Fatalf("fields lost: %+v", got)
	}

	// The file is the contract's shape and holds no secrets.
	b, _ := os.ReadFile(path)
	var raw map[string]any
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatal(err)
	}
	if raw["version"].(float64) != 1 || raw["last"] != remote.ID {
		t.Fatalf("bad file: %s", b)
	}
	for _, c := range raw["connections"].([]any) {
		for _, k := range []string{"id", "name", "mode", "url", "device_id", "created_at"} {
			if _, ok := c.(map[string]any)[k]; !ok {
				t.Fatalf("missing %s in %s", k, b)
			}
		}
		if len(c.(map[string]any)) != 6 {
			t.Fatalf("unexpected fields in %s", b)
		}
	}
	if runtime.GOOS != "windows" {
		fi, _ := os.Stat(path)
		if fi.Mode().Perm() != 0o600 {
			t.Fatalf("mode %v", fi.Mode().Perm())
		}
	}

	if err := again.Remove(remote.ID); err != nil {
		t.Fatal(err)
	}
	third, _ := Open(path)
	if third.Last() != "" || len(third.List()) != 1 {
		t.Fatalf("remove did not clear last: %q %v", third.Last(), third.List())
	}
}

func TestStoreRules(t *testing.T) {
	s, _ := Open(filepath.Join(t.TempDir(), "c.json"))
	if _, err := s.Add(Connection{Name: "a", Mode: ModeLocal}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Add(Connection{Name: "b", Mode: ModeLocal}); !errors.Is(err, ErrLocalExists) {
		t.Fatalf("second local: %v", err)
	}
	if _, err := s.Add(Connection{Name: "c", Mode: ModeLocal, URL: "http://127.0.0.1:1"}); err == nil {
		t.Fatal("local with url accepted")
	}
	if _, err := s.Add(Connection{Name: "d", Mode: ModeRemote, URL: "http://example.com"}); err == nil {
		t.Fatal("public http accepted")
	}
	if _, err := s.Add(Connection{Name: "", Mode: ModeRemote, URL: "https://x.dev"}); err == nil {
		t.Fatal("empty name accepted")
	}
	if _, err := s.Add(Connection{Name: "e", Mode: "weird"}); err == nil {
		t.Fatal("bad mode accepted")
	}
	if err := s.SetLast("conn_missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("set last missing: %v", err)
	}
	if err := s.Remove("conn_missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("remove missing: %v", err)
	}
}

func TestStoreRefusesNewerFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "c.json")
	os.WriteFile(path, []byte(`{"version": 2, "connections": []}`), 0o600)
	if _, err := Open(path); !errors.Is(err, ErrNewerFile) {
		t.Fatalf("got %v", err)
	}
}

func TestDefaultPathOverride(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ANTARES_DESKTOP_CONFIG_DIR", dir)
	p, err := DefaultPath()
	if err != nil || p != filepath.Join(dir, "connections.json") {
		t.Fatalf("%q %v", p, err)
	}
	t.Setenv("ANTARES_DESKTOP_CONFIG_DIR", "")
	p, _ = DefaultPath()
	if filepath.Base(filepath.Dir(p)) != "Antares" {
		t.Fatalf("default path %q", p)
	}
}
