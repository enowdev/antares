package secrets

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func exercise(t *testing.T, s Store) {
	t.Helper()
	if _, err := s.Get("conn_a"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("empty get: %v", err)
	}
	if err := s.Set("conn_a", "atd_1"); err != nil {
		t.Fatal(err)
	}
	if v, err := s.Get("conn_a"); err != nil || v != "atd_1" {
		t.Fatalf("%q %v", v, err)
	}
	if err := s.Delete("conn_a"); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete("conn_a"); err != nil {
		t.Fatalf("second delete: %v", err)
	}
	if _, err := s.Get("conn_a"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("after delete: %v", err)
	}
}

func TestMemory(t *testing.T) { exercise(t, NewMemory()) }

func TestFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "k", "tokens.json")
	exercise(t, &File{Path: p})
	(&File{Path: p}).Set("conn_b", "atd_2")
	if v, _ := (&File{Path: p}).Get("conn_b"); v != "atd_2" {
		t.Fatal("not persisted")
	}
	if fi, _ := os.Stat(p); fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", fi.Mode().Perm())
	}
}

func TestFromEnv(t *testing.T) {
	if _, ok := FromEnv("").(Keychain); !ok {
		t.Fatal("default not keychain")
	}
	if _, ok := FromEnv("memory").(*Memory); !ok {
		t.Fatal("memory")
	}
	if f, ok := FromEnv("file:/tmp/x.json").(*File); !ok || f.Path != "/tmp/x.json" {
		t.Fatal("file")
	}
}

// The real keychain, only when asked: ANTARES_DESKTOP_TEST_KEYCHAIN=1.
func TestKeychainReal(t *testing.T) {
	if os.Getenv("ANTARES_DESKTOP_TEST_KEYCHAIN") == "" {
		t.Skip("set ANTARES_DESKTOP_TEST_KEYCHAIN=1 to touch the OS keychain")
	}
	exercise(t, Keychain{})
}
