package main

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/enowdev/antares/internal/config"
	"github.com/enowdev/antares/internal/skills"
)

func skillMigrationConfig(t *testing.T) *config.Config {
	t.Helper()
	home := t.TempDir()
	t.Setenv("ANTARES_HOME", home)
	t.Setenv("ANTARES_CONFIG", filepath.Join(home, "config.yaml"))
	t.Setenv("ANTARES_PROFILE", "default")
	cfg := config.Default()
	cfg.Skills.Dirs = []string{filepath.Join(home, "skills")}
	if err := os.MkdirAll(cfg.Skills.Dirs[0], 0o700); err != nil {
		t.Fatal(err)
	}
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}
	return cfg
}

func migrationSource(t *testing.T, dir, file, name, enabled string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, file)
	if err := os.WriteFile(path, []byte("---\nname: "+name+"\ndescription: fixture\nenabled: "+enabled+"\n---\nBODY\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestMigrateSkillStateOnceAndSelectedSources(t *testing.T) {
	cfg := skillMigrationConfig(t)
	cfg.Skills.Disabled = []string{"missing"}
	second := t.TempDir()
	cfg.Skills.Dirs = append(cfg.Skills.Dirs, second)
	path := migrationSource(t, cfg.Skills.Dirs[0], "legacy.md", "legacy", "false")
	migrationSource(t, cfg.Skills.Dirs[0], "duplicate.md", "duplicate", "false")
	migrationSource(t, second, "duplicate.md", "duplicate", "true")
	migrationSource(t, config.Path("security-skills"), "pack.md", "automatic-pack", "false")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}
	cfg, err = migrateSkillState(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Skills.FrontmatterMigrated || !reflect.DeepEqual(cfg.Skills.Disabled, []string{"legacy", "missing"}) {
		t.Fatalf("migration = %+v", cfg.Skills)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("migration rewrote legacy source")
	}
	cfg, err = config.SetSkillEnabled("legacy", true)
	if err != nil {
		t.Fatal(err)
	}
	migrationSource(t, cfg.Skills.Dirs[0], "later.md", "later", "false")
	cfg, err = config.Reload()
	if err != nil {
		t.Fatal(err)
	}
	cfg, err = migrateSkillState(cfg)
	if err != nil {
		t.Fatal(err)
	}
	m := skills.NewManager(skills.Options{Dirs: expandAll(cfg.Skills.Dirs)})
	if err := m.Reload(); err != nil {
		t.Fatal(err)
	}
	m.SetDisabled(cfg.Skills.Disabled)
	for _, name := range []string{"legacy", "later"} {
		s, ok := m.Get(name)
		if !ok || !s.Enabled {
			t.Fatalf("%s disabled by stale header after migration: %+v", name, s)
		}
	}
	// The completed marker bypasses even a now-malformed configured source.
	if err := os.WriteFile(path, []byte("---\nenabled: [\n---\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := migrateSkillState(cfg); err != nil {
		t.Fatalf("completed migration rescanned: %v", err)
	}
}

func TestMigrateSkillStateEmptyAndExplicitPack(t *testing.T) {
	cfg := skillMigrationConfig(t)
	cfg.Skills.Dirs = append(cfg.Skills.Dirs, filepath.Join(t.TempDir(), "missing"))
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}
	fresh, err := migrateSkillState(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !fresh.Skills.FrontmatterMigrated || len(fresh.Skills.Disabled) != 0 {
		t.Fatalf("empty migration = %+v", fresh.Skills)
	}
	// Explicitly configured bundled paths are configured sources, not excluded by location.
	cfg.Skills.Dirs = []string{config.Path("security-skills")}
	migrationSource(t, cfg.Skills.Dirs[0], "explicit.md", "explicit", "false")
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}
	fresh, err = migrateSkillState(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(fresh.Skills.Disabled, []string{"explicit"}) {
		t.Fatalf("explicit import = %v", fresh.Skills.Disabled)
	}
}

func TestMigrateSkillStateIncompleteRetry(t *testing.T) {
	cfg := skillMigrationConfig(t)
	migrationSource(t, cfg.Skills.Dirs[0], "good.md", "good", "false")
	broken := filepath.Join(cfg.Skills.Dirs[0], "broken.md")
	if err := os.WriteFile(broken, []byte("---\nenabled: [\n---\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := migrateSkillState(cfg); err == nil || !strings.Contains(err.Error(), "migrate skill preferences:") {
		t.Fatalf("incomplete migration error = %v", err)
	}
	fresh, err := config.Reload()
	if err != nil {
		t.Fatal(err)
	}
	if fresh.Skills.FrontmatterMigrated {
		t.Fatal("partial migration marked complete")
	}
	migrationSource(t, cfg.Skills.Dirs[0], "broken.md", "repaired", "false")
	fresh, err = migrateSkillState(fresh)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(fresh.Skills.Disabled, []string{"good", "repaired"}) {
		t.Fatalf("retry import = %v", fresh.Skills.Disabled)
	}
}

func TestMigrateSkillStateSaveFailureLeavesMarkerUnset(t *testing.T) {
	cfg := skillMigrationConfig(t)
	migrationSource(t, cfg.Skills.Dirs[0], "legacy.md", "legacy", "false")
	// The file remains readable, but atomic replacement needs directory write permission.
	original, err := os.ReadFile(config.ConfigFile())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Dir(config.ConfigFile()), 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(filepath.Dir(config.ConfigFile()), 0o700) })
	if os.Geteuid() == 0 {
		t.Skip("permission failure requires an unprivileged process; config package covers deterministic write failure")
	}
	if _, err := migrateSkillState(cfg); err == nil {
		t.Fatal("migration succeeded despite unwritable config directory")
	}
	after, err := os.ReadFile(config.ConfigFile())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(original, after) || cfg.Skills.FrontmatterMigrated {
		t.Fatal("failed migration changed persisted marker")
	}
}

func TestMigrateSkillStateUnreadableSourceRetry(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("permission semantics require an unprivileged process")
	}
	for _, rootUnreadable := range []bool{false, true} {
		t.Run(map[bool]string{false: "file", true: "root"}[rootUnreadable], func(t *testing.T) {
			cfg := skillMigrationConfig(t)
			p := migrationSource(t, cfg.Skills.Dirs[0], "legacy.md", "legacy", "false")
			if rootUnreadable {
				p = cfg.Skills.Dirs[0]
			}
			if err := os.Chmod(p, 0); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.Chmod(p, 0o700) })
			if _, err := migrateSkillState(cfg); err == nil {
				t.Fatal("unreadable source completed migration")
			}
			fresh, err := config.Reload()
			if err != nil {
				t.Fatal(err)
			}
			if fresh.Skills.FrontmatterMigrated {
				t.Fatal("incomplete import marked complete")
			}
			if err := os.Chmod(p, 0o700); err != nil {
				t.Fatal(err)
			}
			fresh, err = migrateSkillState(fresh)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(fresh.Skills.Disabled, []string{"legacy"}) {
				t.Fatalf("retry lost opt-out: %v", fresh.Skills.Disabled)
			}
		})
	}
}

func TestRuntimeReloadAbortsIncompleteSkillMigration(t *testing.T) {
	cfg := skillMigrationConfig(t)
	if err := os.WriteFile(filepath.Join(cfg.Skills.Dirs[0], "bad.md"), []byte("---\nenabled: [\n---\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	rt := &runtimeServices{cfg: cfg.Clone()}
	rt.cfg.Skills.FrontmatterMigrated = true
	if err := rt.reload(); err == nil || !strings.Contains(err.Error(), "migrate skill preferences:") {
		t.Fatalf("reload migration error = %v", err)
	}
	if !rt.cfg.Skills.FrontmatterMigrated {
		t.Fatal("failed migration published replacement config")
	}
}

func TestMigrationDoesNotImportAutomaticBorrowedOptOut(t *testing.T) {
	cfg := skillMigrationConfig(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	borrowed := migrationSource(t, filepath.Join(home, ".agent", "skills", "borrowed"), "SKILL.md", "borrowed", "false")
	before, err := os.ReadFile(borrowed)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err = migrateSkillState(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Skills.Disabled) != 0 {
		t.Fatalf("imported automatic opt-out: %v", cfg.Skills.Disabled)
	}
	m := skills.NewManager(skills.Options{Dirs: expandAll(cfg.Skills.Dirs), UserHome: home})
	if err := m.Reload(); err != nil {
		t.Fatal(err)
	}
	m.SetDisabled(cfg.Skills.Disabled)
	s, ok := m.Get("borrowed")
	if !ok || !s.Enabled || !s.ReadOnly {
		t.Fatalf("borrowed control: %+v", s)
	}
	after, err := os.ReadFile(borrowed)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("borrowed source rewritten")
	}
}
