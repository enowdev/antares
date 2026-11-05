package skills

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func writeStateSkill(t *testing.T, dir, file, name, header, body string) string {
	t.Helper()
	path := filepath.Join(dir, file)
	content := "---\nname: " + name + "\n" + header + "---\n\n" + body + "\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestDisabledStateIsEffectiveWithoutMutatingSources(t *testing.T) {
	base := t.TempDir()
	dir := filepath.Join(base, "configured")
	packDir := filepath.Join(base, "pack")
	home := filepath.Join(base, "home")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(packDir, 0o755); err != nil {
		t.Fatal(err)
	}
	legacyPath := writeStateSkill(t, dir, "legacy.md", "legacy", "description: legacy description\nenabled: false\n", "legacy body")
	writeStateSkill(t, dir, "active.md", "active", "description: active description\n", "active body")
	packPath := writeStateSkill(t, packDir, "pack.md", "pack", "description: pack description\ncategory: web\nenabled: false\n", "pack body")
	importedDir := filepath.Join(home, ".agent", "skills", "imported")
	if err := os.MkdirAll(importedDir, 0o755); err != nil {
		t.Fatal(err)
	}
	importedPath := writeStateSkill(t, importedDir, "SKILL.md", "imported", "description: imported description\nenabled: false\n", "imported body")

	fixedTime := time.Unix(1_700_000_000, 0)
	for _, path := range []string{legacyPath, packPath, importedPath} {
		if err := os.Chtimes(path, fixedTime, fixedTime); err != nil {
			t.Fatal(err)
		}
	}
	type sourceSnapshot struct {
		bytes []byte
		info  os.FileInfo
	}
	before := make(map[string]sourceSnapshot)
	for _, path := range []string{legacyPath, packPath, importedPath} {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		before[path] = sourceSnapshot{bytes: raw, info: info}
	}

	m := NewManager(Options{Dirs: []string{dir}, PackDirs: []string{packDir}, UserHome: home})
	if err := m.Reload(); err != nil {
		t.Fatal(err)
	}
	if got := m.LegacyDisabled(); !reflect.DeepEqual(got, []string{"legacy"}) {
		t.Fatalf("LegacyDisabled() = %v, want configured-only [legacy]", got)
	}
	for _, name := range []string{"legacy", "pack", "imported"} {
		if skill, ok := m.Get(name); !ok || !skill.Enabled {
			t.Fatalf("%s enabled state before config overlay = (%+v, %v), want enabled", name, skill, ok)
		}
	}

	m.SetDisabled([]string{"legacy", "pack", "imported"})

	listed := m.List()
	if got := enabledByName(listed); !reflect.DeepEqual(got, map[string]bool{"active": true, "imported": false, "legacy": false, "pack": false}) {
		t.Fatalf("List enabled state = %v", got)
	}
	for _, name := range []string{"legacy", "imported"} {
		assertSkillDisabled(t, m, name)
	}
	library, total := m.Library("web", 0, 10)
	if total != 1 || len(library) != 1 || library[0].Name != "pack" || library[0].Enabled {
		t.Fatalf("Library(web) = (%+v, %d), want disabled pack", library, total)
	}
	if got := m.Count(); got != 1 {
		t.Fatalf("Count() = %d, want only active enabled", got)
	}
	prompt := m.PromptBlock(0)
	if !strings.Contains(prompt, "active: active description") || strings.Contains(prompt, "legacy") || strings.Contains(prompt, "imported") || strings.Contains(prompt, "pack") {
		t.Fatalf("PromptBlock() did not reflect effective state: %q", prompt)
	}

	for path, snapshot := range before {
		afterBytes, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		afterInfo, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(afterBytes, snapshot.bytes) {
			t.Fatalf("SetDisabled changed source bytes for %q:\n%s", path, afterBytes)
		}
		if afterInfo.Mode() != snapshot.info.Mode() || afterInfo.Size() != snapshot.info.Size() || !afterInfo.ModTime().Equal(snapshot.info.ModTime()) {
			t.Fatalf("SetDisabled changed source stat for %q: before=%+v after=%+v", path, snapshot.info, afterInfo)
		}
	}
}

func TestLegacyDisabledUsesConfiguredWinnerAndSortsNames(t *testing.T) {
	base := t.TempDir()
	first := filepath.Join(base, "first")
	second := filepath.Join(base, "second")
	pack := filepath.Join(base, "pack")
	for _, dir := range []string{first, second, pack} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeStateSkill(t, first, "duplicate.md", "duplicate", "enabled: false\n", "losing legacy body")
	writeStateSkill(t, second, "duplicate.md", "duplicate", "enabled: true\n", "winning body")
	writeStateSkill(t, second, "z.md", "z-name", "enabled: false\n", "z body")
	writeStateSkill(t, second, "a.md", "a-name", "enabled: false\n", "a body")
	writeStateSkill(t, pack, "pack.md", "pack-legacy", "enabled: false\n", "pack body")

	m := NewManager(Options{Dirs: []string{first, second}, PackDirs: []string{pack}})
	if err := m.Reload(); err != nil {
		t.Fatal(err)
	}
	if got := m.LegacyDisabled(); !reflect.DeepEqual(got, []string{"a-name", "z-name"}) {
		t.Fatalf("LegacyDisabled() = %v, want sorted configured winners only", got)
	}
	winner, ok := m.Get("duplicate")
	if !ok || winner.Body != "winning body" || !winner.Enabled {
		t.Fatalf("selected duplicate = (%+v, %v), want enabled later-directory winner", winner, ok)
	}
	if packSkill := requireSkill(t, m, "pack-legacy"); !packSkill.Enabled {
		t.Fatalf("legacy pack header affected runtime state: %+v", packSkill)
	}
}

func TestDisabledPreferenceSurvivesReloadSaveDeleteAndRecreate(t *testing.T) {
	dir := t.TempDir()
	path := writeStateSkill(t, dir, "persistent.md", "persistent", "description: old\nenabled: false\n", "old body")
	m := NewManager(Options{Dirs: []string{dir}})
	if err := m.Reload(); err != nil {
		t.Fatal(err)
	}
	m.SetDisabled([]string{"persistent"})

	if err := m.Reload(); err != nil {
		t.Fatal(err)
	}
	assertSkillDisabled(t, m, "persistent")

	saved, err := m.Save("persistent", "saved", "saved body", []string{"state"})
	if err != nil {
		t.Fatal(err)
	}
	if saved.Enabled {
		t.Fatalf("Save returned enabled skill despite retained preference: %+v", saved)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "enabled:") {
		t.Fatalf("Save persisted retired enabled header:\n%s", raw)
	}

	if err := m.Delete("persistent"); err != nil {
		t.Fatal(err)
	}
	if _, ok := m.Get("persistent"); ok {
		t.Fatal("deleted skill remains present")
	}
	writeStateSkill(t, dir, "persistent.md", "persistent", "description: recreated\n", "recreated body")
	if err := m.Reload(); err != nil {
		t.Fatal(err)
	}
	assertSkillDisabled(t, m, "persistent")
}

func TestSetDisabledClonesInputAndReplacesState(t *testing.T) {
	dir := t.TempDir()
	writeStateSkill(t, dir, "one.md", "one", "", "one body")
	writeStateSkill(t, dir, "two.md", "two", "", "two body")
	m := NewManager(Options{Dirs: []string{dir}})
	if err := m.Reload(); err != nil {
		t.Fatal(err)
	}

	names := []string{"one"}
	m.SetDisabled(names)
	names[0] = "two"
	assertSkillDisabled(t, m, "one")
	if two, _ := m.Get("two"); !two.Enabled {
		t.Fatal("mutating SetDisabled input changed manager state")
	}

	m.SetDisabled(nil)
	if m.Count() != 2 {
		t.Fatalf("SetDisabled(nil) did not clear state: count=%d", m.Count())
	}
}

func TestReloadReportsErrorAndPublishesReadableSkills(t *testing.T) {
	configured := t.TempDir()
	writeStateSkill(t, configured, "good.md", "good", "description: readable\n", "good body")
	writeStateSkill(t, configured, "malformed.md", "malformed", "tags: [unterminated\n", "bad body")

	m := NewManager(Options{Dirs: []string{configured}})
	err := m.Reload()
	if err == nil {
		t.Fatal("Reload() error = nil, want configured parse error")
	}
	if !strings.Contains(err.Error(), "invalid front matter") {
		t.Fatalf("Reload() error = %q, want invalid front matter", err)
	}
	good, ok := m.Get("good")
	if !ok || good.Body != "good body" {
		t.Fatalf("readable skill was not published with partial scan: (%+v, %v)", good, ok)
	}
	if _, ok := m.Get("malformed"); ok {
		t.Fatal("malformed skill was published")
	}
}

func enabledByName(skills []Skill) map[string]bool {
	out := make(map[string]bool, len(skills))
	for _, skill := range skills {
		out[skill.Name] = skill.Enabled
	}
	return out
}

func assertSkillDisabled(t *testing.T, m *Manager, name string) {
	t.Helper()
	skill, ok := m.Get(name)
	if !ok || skill.Enabled {
		t.Fatalf("Get(%q) = (%+v, %v), want disabled", name, skill, ok)
	}
}
