package commands

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/enowdev/antares/internal/agent"
	"github.com/enowdev/antares/internal/config"
	"github.com/enowdev/antares/internal/skills"
	"github.com/enowdev/antares/internal/store"
)

func TestSkillCommandSessionIsolation(t *testing.T) {
	ctx := context.Background()
	base := t.TempDir()
	home, startup := filepath.Join(base, "home"), filepath.Join(base, "startup")
	projects := map[string]string{"S": startup, "A": filepath.Join(base, "a"), "B": filepath.Join(base, "b")}
	write := func(root, name, description string) {
		t.Helper()
		path := filepath.Join(root, ".agent", "skills", name, "SKILL.md")
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("---\nname: "+name+"\ndescription: "+description+"\n---\nbody\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(home, "scope-global", "GLOBAL_MARKER")
	for id, root := range projects {
		write(root, "scope-"+strings.ToLower(id)+"-only", id+"_ONLY_MARKER")
		write(root, "scope-collision", id+"_COLLISION_MARKER")
	}
	mgr := skills.NewManager(skills.Options{UserHome: home, ProjectDir: startup})
	if err := mgr.Reload(); err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(ctx, "memory", "", 1, 5000, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	a := agent.New(config.Default(), db, nil, nil, nil)
	a.SetSkills(mgr)
	// A stale fallback must not override the live agent manager.
	fallback := skills.NewManager(skills.Options{})
	deps := Deps{Agent: a, Skills: fallback, Store: db}
	for id, root := range projects {
		if err := db.CreateSession(ctx, &store.Session{ID: id, Meta: store.Meta{"project_dir": root}}); err != nil {
			t.Fatal(err)
		}
	}
	for id, value := range map[string]any{"blank": "  ", "nonstring": 42, "missing": nil} {
		if err := db.CreateSession(ctx, &store.Session{ID: id, Meta: store.Meta{"project_dir": value}}); err != nil {
			t.Fatal(err)
		}
	}
	check := func(sessionID, wanted string) {
		t.Helper()
		result, err := Run(ctx, deps, Input{Name: "skills", Args: "scope-", SessionID: sessionID, Surface: "web"})
		if err != nil {
			t.Errorf("session %s: %v", sessionID, err)
			return
		}
		for _, marker := range []string{"GLOBAL_MARKER", wanted + "_ONLY_MARKER", wanted + "_COLLISION_MARKER"} {
			if !strings.Contains(result.Output, marker) {
				t.Errorf("session %s missing %s: %s", sessionID, marker, result.Output)
			}
		}
		for _, other := range []string{"S", "A", "B"} {
			if other != wanted && (strings.Contains(result.Output, other+"_ONLY_MARKER") || strings.Contains(result.Output, other+"_COLLISION_MARKER")) {
				t.Errorf("session %s leaked %s catalog: %s", sessionID, other, result.Output)
			}
		}
	}
	for _, id := range []string{"", "S", "blank", "nonstring", "missing"} {
		check(id, "S")
	}
	check("A", "A")
	check("B", "B")
	check("", "S")
	var wg sync.WaitGroup
	for _, id := range []string{"A", "B"} {
		wg.Go(func() {
			for range 10 {
				check(id, id)
			}
		})
	}
	wg.Wait()
	if _, err := Run(ctx, Deps{Skills: mgr}, Input{Name: "skills", SessionID: "A"}); !errors.Is(err, errNoStore) {
		t.Fatalf("missing store = %v, want errNoStore", err)
	}
	if _, err := Run(ctx, deps, Input{Name: "skills", SessionID: "absent"}); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("missing session = %v, want ErrNotFound", err)
	}
	// Malformed project content must not leak the startup catalog or hide valid entries.
	write(projects["A"], "scope-partial", "A_PARTIAL_MARKER")
	bad := filepath.Join(projects["A"], ".agent", "skills", "bad", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(bad), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bad, []byte("---\nname: [\n---\nbad"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := mgr.Reload(); err == nil {
		t.Fatal("malformed skill did not report error")
	}
	check("A", "A")
	result, err := Run(ctx, deps, Input{Name: "skills", Args: "scope-partial", SessionID: "A"})
	if err != nil || !strings.Contains(result.Output, "A_PARTIAL_MARKER") {
		t.Fatalf("partial catalog: %+v, %v", result, err)
	}
	if err := db.CreateSession(ctx, &store.Session{ID: "invalid", Meta: store.Meta{"project_dir": "bad\x00path"}}); err != nil {
		t.Fatal(err)
	}
	result, err = Run(ctx, deps, Input{Name: "skills", Args: "scope-", SessionID: "invalid"})
	if err != nil || !strings.Contains(result.Output, "GLOBAL_MARKER") {
		t.Fatalf("shared-only catalog: %+v, %v", result, err)
	}
	for _, id := range []string{"S", "A", "B"} {
		if strings.Contains(result.Output, id+"_ONLY_MARKER") || strings.Contains(result.Output, id+"_COLLISION_MARKER") {
			t.Fatalf("normalization failure leaked project %s: %s", id, result.Output)
		}
	}
}

func TestSkillsListKeepsTheBundledLibraryOutOfTheDefaultListing(t *testing.T) {
	base := t.TempDir()
	everyday, pack := filepath.Join(base, "skills"), filepath.Join(base, "pack")
	write := func(root, name, description string) {
		t.Helper()
		path := filepath.Join(root, name, "SKILL.md")
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("---\nname: "+name+"\ndescription: "+description+"\n---\nbody\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(everyday, "daily-notes", "Everyday note taking")
	for i := 0; i < 60; i++ {
		write(pack, fmt.Sprintf("pack-skill-%02d", i), "library entry")
	}
	mgr := skills.NewManager(skills.Options{Dirs: []string{everyday}, PackDirs: []string{pack}})
	if err := mgr.Reload(); err != nil {
		t.Fatal(err)
	}
	deps := Deps{Skills: mgr}

	res, err := Run(context.Background(), deps, Input{Name: "skills", Surface: SurfaceTUI})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Output, "**1 skill(s)**") || strings.Contains(res.Output, "pack-skill") {
		t.Fatalf("default listing should show only everyday skills:\n%s", res.Output)
	}

	res, err = Run(context.Background(), deps, Input{Name: "skills", Args: "library", Surface: SurfaceTUI})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Output, "**60 skill(s)**") || !strings.Contains(res.Output, "and 10 more") {
		t.Fatalf("a filter should search the library, capped:\n%s", res.Output)
	}
}
