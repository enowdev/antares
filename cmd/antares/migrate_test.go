package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/enowdev/antares/internal/config"
	"github.com/enowdev/antares/internal/migrate"
)

func migrateTestHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("ANTARES_HOME", home)
	t.Setenv("ANTARES_CONFIG", "")
	t.Setenv("ANTARES_PROFILE", "")
	for _, k := range []string{"OPENROUTER_API_KEY", "OPENAI_API_KEY", "ANTHROPIC_API_KEY", "ANTARES_API_KEY", "ANTARES_BASE_URL"} {
		t.Setenv(k, "")
	}
	old := migrate.RunningCheck
	migrate.RunningCheck = func(_, _ []string) bool { return false }
	t.Cleanup(func() { migrate.RunningCheck = old; config.Reload() })
	return home
}

func hermesFixtureRoot(t *testing.T) string {
	root, err := filepath.Abs(filepath.Join("..", "..", "internal", "migrate", "testdata", "hermes", "home"))
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func TestMigrateDryRunWritesNothing(t *testing.T) {
	home := migrateTestHome(t)
	var out bytes.Buffer
	err := runMigrate(context.Background(), []string{"hermes", "--root", hermesFixtureRoot(t), "--dry-run", "--only", "provider,channel"}, &out, false)
	if err != nil {
		t.Fatal(err)
	}
	s := out.String()
	for _, want := range []string{"Hermes Agent at", "provider (", "+ OpenRouter", "channel (", "? Slack", "- DingTalk", "Dry run: nothing was changed."} {
		if !strings.Contains(s, want) {
			t.Fatalf("output lacks %q:\n%s", want, s)
		}
	}
	if strings.Contains(s, "skill (") {
		t.Fatal("--only did not filter categories")
	}
	for _, sec := range []string{"FAKEopenrouterKEY0001", "FAKEtelegramTOKEN0007", "FAKEslackTOKEN0009"} {
		if strings.Contains(s, sec) {
			t.Fatal("dry run printed a secret")
		}
	}
	if entries, _ := os.ReadDir(filepath.Join(home, "backups")); len(entries) != 0 {
		t.Fatal("dry run created a backup")
	}
}

func TestMigrateNeedsYesWithoutTerminal(t *testing.T) {
	migrateTestHome(t)
	err := runMigrate(context.Background(), []string{"hermes", "--root", hermesFixtureRoot(t)}, &bytes.Buffer{}, false)
	if err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("err = %v", err)
	}
}

func TestMigrateYesThenUndo(t *testing.T) {
	home := migrateTestHome(t)
	var out bytes.Buffer
	if err := runMigrate(context.Background(), []string{"hermes", "--root", hermesFixtureRoot(t), "--yes", "--only", "provider,skill,memory"}, &out, false); err != nil {
		t.Fatal(err)
	}
	cfg, _ := config.Reload()
	if cfg.Providers["openrouter"].APIKey == "" {
		t.Fatalf("provider not imported:\n%s", out.String())
	}
	if _, err := os.Stat(filepath.Join(home, "skills", "web-research", "SKILL.md")); err != nil {
		t.Fatal("skill not imported")
	}
	list := migrate.ListBackups()
	if len(list) != 1 {
		t.Fatalf("backups = %#v", list)
	}
	out.Reset()
	if err := runMigrate(context.Background(), []string{"undo", filepath.Join(migrate.BackupsDir(), list[0].Name)}, &out, false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, "skills", "web-research")); err == nil {
		t.Fatal("undo left the skill")
	}
	if err := runMigrate(context.Background(), []string{"undo", "../etc"}, &out, false); err == nil {
		t.Fatal("undo accepted a path outside backups")
	}
}
