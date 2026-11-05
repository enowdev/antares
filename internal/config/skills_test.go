package config

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestMigrateSkillStateUnionsOnceWithoutPersistingRuntimeValues(t *testing.T) {
	home := isolateConfigHome(t)
	path := filepath.Join(home, "config.yaml")
	writeSkillConfigFixture(t, path, `server:
  host: 127.0.0.1
agent:
  workspace: $SKILL_WORKSPACE
skills:
  enabled: false
  disabled: [zeta, existing, zeta]
  frontmatter_migrated: false
`)
	t.Setenv("ANTARES_HOST", "0.0.0.0")
	t.Setenv("SKILL_WORKSPACE", filepath.Join(home, "runtime-workspace"))

	cfg, err := MigrateSkillState([]string{"legacy", "existing", " Alpha ", "legacy"})
	if err != nil {
		t.Fatalf("MigrateSkillState: %v", err)
	}
	wantDisabled := []string{" Alpha ", "existing", "legacy", "zeta"}
	if !reflect.DeepEqual(cfg.Skills.Disabled, wantDisabled) {
		t.Fatalf("runtime disabled = %#v, want %#v", cfg.Skills.Disabled, wantDisabled)
	}
	if !cfg.Skills.FrontmatterMigrated {
		t.Fatal("runtime migration marker is false")
	}
	if cfg.Skills.Enabled {
		t.Fatal("migration changed the independent global skills.enabled gate")
	}
	if cfg.Server.Host != "0.0.0.0" {
		t.Fatalf("runtime server.host = %q, want environment override", cfg.Server.Host)
	}
	if cfg.Agent.Workspace != filepath.Join(home, "runtime-workspace") {
		t.Fatalf("runtime agent.workspace = %q, want expanded environment path", cfg.Agent.Workspace)
	}

	disk := readSkillConfigFixture(t, path)
	if disk.Server.Host != "127.0.0.1" {
		t.Fatalf("persisted server.host = %q, want operator value", disk.Server.Host)
	}
	if disk.Agent.Workspace != "$SKILL_WORKSPACE" {
		t.Fatalf("persisted agent.workspace = %q, want unnormalized input", disk.Agent.Workspace)
	}
	if disk.Skills.Enabled {
		t.Fatal("persisted migration changed skills.enabled")
	}
	if !disk.Skills.FrontmatterMigrated {
		t.Fatal("persisted migration marker is false")
	}
	if !reflect.DeepEqual(disk.Skills.Disabled, wantDisabled) {
		t.Fatalf("persisted disabled = %#v, want %#v", disk.Skills.Disabled, wantDisabled)
	}

	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read migrated config: %v", err)
	}
	if err := os.Chmod(home, 0o500); err != nil {
		t.Fatalf("make config directory read-only: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(home, 0o700) })

	cfg, err = MigrateSkillState([]string{"later"})
	if err != nil {
		t.Fatalf("second MigrateSkillState should only reload: %v", err)
	}
	if containsExact(cfg.Skills.Disabled, "later") {
		t.Fatal("one-time migration imported a name after the marker was set")
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read config after second migration: %v", err)
	}
	if !reflect.DeepEqual(after, before) {
		t.Fatal("already-completed migration rewrote config")
	}
}

func TestSetSkillEnabledPreservesExactAndStaleNames(t *testing.T) {
	home := isolateConfigHome(t)
	path := filepath.Join(home, "config.yaml")
	writeSkillConfigFixture(t, path, `server:
  host: 127.0.0.1
skills:
  enabled: false
  disabled: [stale, Skill, " Skill ", Skill]
  frontmatter_migrated: true
`)
	t.Setenv("ANTARES_HOST", "0.0.0.0")

	cfg, err := SetSkillEnabled("Skill", true)
	if err != nil {
		t.Fatalf("enable Skill: %v", err)
	}
	want := []string{" Skill ", "stale"}
	if !reflect.DeepEqual(cfg.Skills.Disabled, want) {
		t.Fatalf("disabled after exact enable = %#v, want %#v", cfg.Skills.Disabled, want)
	}

	cfg, err = SetSkillEnabled("  exact name  ", false)
	if err != nil {
		t.Fatalf("disable exact spaced name: %v", err)
	}
	want = []string{"  exact name  ", " Skill ", "stale"}
	if !reflect.DeepEqual(cfg.Skills.Disabled, want) {
		t.Fatalf("disabled after exact disable = %#v, want %#v", cfg.Skills.Disabled, want)
	}
	if cfg.Skills.Enabled {
		t.Fatal("per-skill update changed the independent global gate")
	}

	cfg, err = SetSkillEnabled("stale", false)
	if err != nil {
		t.Fatalf("disable stale name again: %v", err)
	}
	if !reflect.DeepEqual(cfg.Skills.Disabled, want) {
		t.Fatalf("duplicate disable changed set = %#v, want %#v", cfg.Skills.Disabled, want)
	}

	disk := readSkillConfigFixture(t, path)
	if !reflect.DeepEqual(disk.Skills.Disabled, want) {
		t.Fatalf("persisted disabled = %#v, want %#v", disk.Skills.Disabled, want)
	}
	if disk.Skills.Enabled {
		t.Fatal("persisted per-skill update changed skills.enabled")
	}
	if disk.Server.Host != "127.0.0.1" {
		t.Fatalf("persisted server.host = %q, want operator value", disk.Server.Host)
	}
}

func TestSetSkillEnabledSerializesConcurrentUpdates(t *testing.T) {
	home := isolateConfigHome(t)
	path := filepath.Join(home, "config.yaml")
	writeSkillConfigFixture(t, path, `skills:
  frontmatter_migrated: true
`)

	start := make(chan struct{})
	errCh := make(chan error, 2)
	var wg sync.WaitGroup
	for _, name := range []string{"bravo", "alpha"} {
		name := name
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := SetSkillEnabled(name, false)
			errCh <- err
		}()
	}
	close(start)
	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			t.Fatalf("concurrent SetSkillEnabled: %v", err)
		}
	}

	disk := readSkillConfigFixture(t, path)
	want := []string{"alpha", "bravo"}
	if !reflect.DeepEqual(disk.Skills.Disabled, want) {
		t.Fatalf("concurrent disabled updates = %#v, want %#v", disk.Skills.Disabled, want)
	}
}

func TestSkillStateMutationErrors(t *testing.T) {
	t.Run("missing config", func(t *testing.T) {
		isolateConfigHome(t)
		if _, err := MigrateSkillState(nil); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("MigrateSkillState error = %v, want os.ErrNotExist", err)
		}
		if _, err := SetSkillEnabled("known", false); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("SetSkillEnabled error = %v, want os.ErrNotExist", err)
		}
	})

	t.Run("malformed config", func(t *testing.T) {
		home := isolateConfigHome(t)
		path := filepath.Join(home, "config.yaml")
		writeSkillConfigFixture(t, path, "skills: [not: valid\n")
		if _, err := MigrateSkillState(nil); err == nil || !strings.Contains(err.Error(), "parse "+path) {
			t.Fatalf("MigrateSkillState error = %v, want parse error for path", err)
		}
		if _, err := SetSkillEnabled("known", false); err == nil || !strings.Contains(err.Error(), "parse "+path) {
			t.Fatalf("SetSkillEnabled error = %v, want parse error for path", err)
		}
	})

	t.Run("unmigrated", func(t *testing.T) {
		home := isolateConfigHome(t)
		writeSkillConfigFixture(t, filepath.Join(home, "config.yaml"), `skills:
  frontmatter_migrated: false
`)
		if _, err := SetSkillEnabled("known", false); err == nil || err.Error() != "skill preferences have not been migrated" {
			t.Fatalf("SetSkillEnabled error = %v, want unmigrated error", err)
		}
	})

	t.Run("blank name", func(t *testing.T) {
		isolateConfigHome(t)
		if _, err := SetSkillEnabled(" \t\n ", false); err == nil || err.Error() != "skill name is required" {
			t.Fatalf("SetSkillEnabled error = %v, want required-name error", err)
		}
	})
}

func TestMigrateSkillStateAtomicSaveFailureLeavesMarkerUnset(t *testing.T) {
	home := isolateConfigHome(t)
	path := filepath.Join(home, "config.yaml")
	writeSkillConfigFixture(t, path, `skills:
  disabled: [existing]
  frontmatter_migrated: false
`)
	if err := os.Chmod(home, 0o500); err != nil {
		t.Fatalf("make config directory read-only: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(home, 0o700) })

	if _, err := MigrateSkillState([]string{"legacy"}); err == nil {
		t.Skip("filesystem bypasses directory write permissions")
	}

	disk := readSkillConfigFixture(t, path)
	if disk.Skills.FrontmatterMigrated {
		t.Fatal("failed atomic save persisted the migration marker")
	}
	if !reflect.DeepEqual(disk.Skills.Disabled, []string{"existing"}) {
		t.Fatalf("failed atomic save changed disabled names to %#v", disk.Skills.Disabled)
	}
}

func TestSkillsCloneAndSchema(t *testing.T) {
	cfg := Default()
	cfg.Skills.Disabled = []string{"one", "two"}
	cfg.Skills.FrontmatterMigrated = true
	clone := cfg.Clone()
	clone.Skills.Disabled[0] = "changed"
	clone.Skills.FrontmatterMigrated = false
	if !reflect.DeepEqual(cfg.Skills.Disabled, []string{"one", "two"}) {
		t.Fatalf("Clone shares skills.disabled backing storage: %#v", cfg.Skills.Disabled)
	}
	if !cfg.Skills.FrontmatterMigrated {
		t.Fatal("changing clone migration marker changed original")
	}

	paths := make(map[string]bool)
	for _, field := range Schema() {
		paths[field.Path] = true
	}
	if !paths["skills.disabled"] {
		t.Fatal("skills.disabled is absent from editable schema")
	}
	if paths["skills.frontmatter_migrated"] {
		t.Fatal("migration bookkeeping marker is exposed in editable schema")
	}
}

type skillConfigFixture struct {
	Server struct {
		Host string `yaml:"host"`
	} `yaml:"server"`
	Agent struct {
		Workspace string `yaml:"workspace"`
	} `yaml:"agent"`
	Skills Skills `yaml:"skills"`
}

func writeSkillConfigFixture(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("create config directory: %v", err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write config fixture: %v", err)
	}
}

func readSkillConfigFixture(t *testing.T, path string) skillConfigFixture {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read config fixture: %v", err)
	}
	var cfg skillConfigFixture
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("parse config fixture: %v", err)
	}
	return cfg
}

func containsExact(names []string, want string) bool {
	for _, name := range names {
		if name == want {
			return true
		}
	}
	return false
}
