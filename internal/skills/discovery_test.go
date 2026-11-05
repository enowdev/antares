package skills

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

var expectedUserSkillRoots = [][]string{
	{".agent", "skills"},
	{".agents", "skills"},
	{".claude", "skills"},
	{".codex", "skills"},
	{".config", "opencode", "skills"},
	{".omp", "agent", "managed-skills"},
}

var expectedProjectSkillRoots = [][]string{
	{".agent", "skills"},
	{".agents", "skills"},
	{".claude", "skills"},
	{".codex", "skills"},
	{".opencode", "skills"},
	{".github", "skills"},
}

func makeDir(t *testing.T, path string) string {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	makeDir(t, filepath.Dir(path))
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func skillDocument(name, description, body string, enabled bool) string {
	enabledText := "true"
	if !enabled {
		enabledText = "false"
	}
	header := "---\n"
	if name != "" {
		header += "name: " + name + "\n"
	}
	return header + "description: " + description + "\nenabled: " + enabledText + "\nsource: fixture\n" +
		"tags: [discovery]\ntriggers: [on demand]\ncategory: testing\n" +
		"tech_stack: [go]\ncwe_ids: [CWE-1]\nowasp_id: A01\nchains_with: [next]\n---\n\n" + body + "\n"
}

func mustReload(t *testing.T, m *Manager) {
	t.Helper()
	if err := m.Reload(); err != nil {
		t.Fatal(err)
	}
}

func requireSkill(t *testing.T, m *Manager, name string) *Skill {
	t.Helper()
	skill, ok := m.Get(name)
	if !ok {
		t.Fatalf("skill %q was not discovered; got %v", name, names(m.List()))
	}
	return skill
}

func TestDiscoveryRootsAndFormats(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	configured := makeDir(t, filepath.Join(t.TempDir(), "native"))
	missing := filepath.Join(t.TempDir(), "must-stay-missing")
	missingHome := filepath.Join(t.TempDir(), "missing-home")
	missingAutomatic := filepath.Join(missingHome, ".agent", "skills")
	empty := NewManager(Options{UserHome: missingHome})
	mustReload(t, empty)
	if _, err := os.Stat(missingAutomatic); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing automatic root was created or returned an unexpected error: %v", err)
	}

	for i, parts := range expectedUserSkillRoots {
		name := "user-root-" + string(rune('1'+i))
		path := filepath.Join(append([]string{home}, parts...)...)
		writeFile(t, filepath.Join(path, name, "SKILL.md"), skillDocument(name, name+" description", name+" body", true))
	}
	for i, parts := range expectedProjectSkillRoots {
		name := "project-root-" + string(rune('1'+i))
		path := filepath.Join(append([]string{project}, parts...)...)
		writeFile(t, filepath.Join(path, name, "sKiLl.Md"), skillDocument(name, name+" description", name+" body", true))
	}

	fallbackPath := filepath.Join(home, ".agent", "skills", "logical-fallback", "nested", "SKILL.md")
	writeFile(t, fallbackPath, skillDocument("", "fallback description", "fallback body", true))
	supportRoot := filepath.Join(home, ".agent", "skills", "support")
	writeFile(t, filepath.Join(supportRoot, "README.md"), skillDocument("support-readme", "ignored", "ignored", true))
	writeFile(t, filepath.Join(supportRoot, "DESIGN.md"), skillDocument("support-design", "ignored", "ignored", true))
	writeFile(t, filepath.Join(supportRoot, "references", "help.md"), skillDocument("support-help", "ignored", "ignored", true))
	writeFile(t, filepath.Join(supportRoot, ".hidden", "SKILL.md"), skillDocument("hidden-skill", "ignored", "ignored", true))
	writeFile(t, filepath.Join(configured, "flat.md"), skillDocument("native-flat", "native description", "native body", true))
	writeFile(t, filepath.Join(configured, "disabled.md"), skillDocument("disabled-native", "disabled description", "disabled body", false))
	writeFile(t, filepath.Join(configured, "broken.md"), "---\nname: [not valid\n---\nbroken")
	writeFile(t, filepath.Join(configured, "valid.md"), skillDocument("valid-beside-broken", "valid", "valid body", true))

	configuredFallbackPath := filepath.Join(configured, "configured-parent", "SKILL.md")
	writeFile(t, configuredFallbackPath, skillDocument("", "configured fallback", "configured fallback body", true))
	hiddenConfiguredRoot := makeDir(t, filepath.Join(t.TempDir(), ".explicit-hidden-root"))
	writeFile(t, filepath.Join(hiddenConfiguredRoot, "visible.md"), skillDocument("hidden-root-visible", "visible", "VISIBLE", true))
	m := NewManager(Options{Dirs: []string{missing, configured, hiddenConfiguredRoot}, UserHome: home, ProjectDir: project})
	err := m.Reload()
	if err == nil || !strings.Contains(err.Error(), "invalid front matter") {
		t.Fatalf("Reload error = %v, want malformed front matter error", err)
	}
	for i := 1; i <= 6; i++ {
		requireSkill(t, m, "user-root-"+string(rune('0'+i)))
		requireSkill(t, m, "project-root-"+string(rune('0'+i)))
	}
	fallback := requireSkill(t, m, "nested")
	if fallback.Path != fallbackPath || !fallback.ReadOnly || fallback.Description != "fallback description" {
		t.Fatalf("fallback skill = %+v, want logical parent name/path, metadata, and read-only provenance", fallback)
	}
	metadata := requireSkill(t, m, "user-root-1")
	if !metadata.ReadOnly || metadata.Pack || metadata.Source != "fixture" || metadata.UpdatedAt.IsZero() || metadata.Category != "testing" || metadata.OWASPID != "A01" || len(metadata.Tags) != 1 || len(metadata.Triggers) != 1 || len(metadata.TechStack) != 1 || len(metadata.CWEIDs) != 1 || len(metadata.ChainsWith) != 1 {
		t.Fatalf("front matter metadata or automatic provenance was not preserved: %+v", metadata)
	}
	metadata.Tags[0] = "mutated"
	if got := requireSkill(t, m, "user-root-1"); got.Tags[0] != "discovery" {
		t.Fatalf("caller mutation leaked into manager state: %+v", got.Tags)
	}
	for _, absent := range []string{"support-readme", "support-design", "support-help", "hidden-skill"} {
		if _, ok := m.Get(absent); ok {
			t.Fatalf("support/hidden document %q was loaded", absent)
		}
	}
	configuredFallback := requireSkill(t, m, "configured-parent")
	if configuredFallback.Path != configuredFallbackPath || configuredFallback.ReadOnly {
		t.Fatalf("configured SKILL.md fallback = %+v, want writable parent fallback", configuredFallback)
	}
	if requireSkill(t, m, "native-flat").ReadOnly {
		t.Fatal("configured flat Markdown must remain writable")
	}
	if disabled := requireSkill(t, m, "disabled-native"); !disabled.Enabled {
		t.Fatalf("legacy enabled header affected runtime state: %+v", disabled)
	}
	if got := m.LegacyDisabled(); len(got) != 1 || got[0] != "disabled-native" {
		t.Fatalf("LegacyDisabled() = %v, want configured legacy input", got)
	}
	m.SetDisabled([]string{"disabled-native"})
	if strings.Contains(m.PromptBlock(0), "disabled-native") {
		t.Fatal("config-disabled skill appeared in PromptBlock")
	}
	requireSkill(t, m, "valid-beside-broken")
	requireSkill(t, m, "hidden-root-visible")
	if _, err := os.Stat(missing); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing root was created or returned an unexpected error: %v", err)
	}
}

func TestDiscoveryPrecedenceAndReadOnly(t *testing.T) {
	base := t.TempDir()
	home, project := filepath.Join(base, "home"), filepath.Join(base, "project")
	pack := makeDir(t, filepath.Join(base, "security-skills"))
	packSibling := makeDir(t, filepath.Join(base, "security-skills-copy"))
	configured1 := makeDir(t, filepath.Join(base, "configured-1"))
	configured2 := makeDir(t, filepath.Join(base, "configured-2"))
	user1 := filepath.Join(home, ".agent", "skills")
	user2 := filepath.Join(home, ".agents", "skills")
	project1 := filepath.Join(project, ".agent", "skills")
	project2 := filepath.Join(project, ".agents", "skills")
	// Within one root lexical traversal is the final tie-breaker.
	writeFile(t, filepath.Join(configured2, "a-collision.md"), skillDocument("lexical-winner", "a", "LEXICAL_A", true))
	writeFile(t, filepath.Join(configured2, "z-collision.md"), skillDocument("lexical-winner", "z", "LEXICAL_Z", true))

	type fixture struct {
		path string
		body string
	}
	fixtures := []fixture{
		{filepath.Join(pack, "winner.md"), "pack"},
		{filepath.Join(user1, "winner", "SKILL.md"), "user-1"},
		{filepath.Join(user2, "winner", "SKILL.md"), "user-2"},
		{filepath.Join(project1, "winner", "SKILL.md"), "project-1"},
		{filepath.Join(project2, "winner", "SKILL.md"), "project-2"},
		{filepath.Join(configured1, "winner.md"), "configured-1"},
		{filepath.Join(configured2, "winner.md"), "configured-2"},
	}
	for _, fixture := range fixtures {
		writeFile(t, fixture.path, skillDocument("winner", fixture.body, fixture.body, true))
	}
	writeFile(t, filepath.Join(packSibling, "sibling.md"), skillDocument("pack-sibling", "sibling", "sibling", true))

	dirs := []string{" ", configured1, configured2, configured2, packSibling}
	packs := []string{pack}
	m := NewManager(Options{Dirs: dirs, PackDirs: packs, UserHome: home, ProjectDir: project})
	dirs[1] = filepath.Join(base, "mutated-caller")
	packs[0] = packSibling
	mustReload(t, m)
	winner := requireSkill(t, m, "winner")
	if winner.Body != "configured-2" || winner.ReadOnly || winner.Pack {
		t.Fatalf("initial winner = %+v, want second configured root", winner)
	}
	if sibling := requireSkill(t, m, "pack-sibling"); sibling.Pack || sibling.ReadOnly {
		t.Fatalf("configured sibling was mislabeled as pack/imported: %+v", sibling)
	}
	if got := requireSkill(t, m, "lexical-winner"); got.Body != "LEXICAL_Z" {
		t.Fatalf("within-root winner body = %q, want lexically later file", got.Body)
	}

	// Equivalent roots are retained only once per kind, using the last logical
	// spelling, and nonblank directory bytes are not trimmed.
	dedupeTarget := makeDir(t, filepath.Join(base, "dedupe-target"))
	writeFile(t, filepath.Join(dedupeTarget, "deduped.md"), skillDocument("deduped", "deduped", "DEDUPED", true))
	alias1, alias2 := filepath.Join(base, "alias-1"), filepath.Join(base, "alias-2")
	if runtime.GOOS == "windows" {
		t.Log("skipping equivalent-symlink root assertion on Windows")
	} else {
		if err := os.Symlink(dedupeTarget, alias1); err != nil {
			t.Fatalf("directory symlinks unavailable after discovery assertions: %v", err)
		}
		if err := os.Symlink(dedupeTarget, alias2); err != nil {
			t.Fatal(err)
		}
	}
	spacedRoot := makeDir(t, filepath.Join(base, " spaced root "))
	writeFile(t, filepath.Join(spacedRoot, "spaced.md"), skillDocument("spaced-root", "spaced", "SPACED", true))
	crossKind := makeDir(t, filepath.Join(base, "cross-kind"))
	writeFile(t, filepath.Join(crossKind, "shared.md"), skillDocument("cross-kind", "cross", "CROSS", true))
	dedupeDirs := []string{spacedRoot, crossKind}
	if runtime.GOOS != "windows" {
		dedupeDirs = []string{alias1, spacedRoot, alias2, crossKind}
	}
	dedupeManager := NewManager(Options{Dirs: dedupeDirs, PackDirs: []string{crossKind}})
	mustReload(t, dedupeManager)
	if runtime.GOOS != "windows" {
		if got := requireSkill(t, dedupeManager, "deduped"); got.Path != filepath.Join(alias2, "deduped.md") {
			t.Fatalf("deduplicated root path = %q, want last logical alias", got.Path)
		}
	}
	if got := requireSkill(t, dedupeManager, "spaced-root"); got.Path != filepath.Join(spacedRoot, "spaced.md") {
		t.Fatalf("spaced configured root path = %q, want bytes preserved", got.Path)
	}
	if got := requireSkill(t, dedupeManager, "cross-kind"); got.Pack || got.ReadOnly {
		t.Fatalf("explicit occurrence was suppressed by equivalent pack root: %+v", got)
	}
	m.MarkUsed("winner")
	m.SetDisabled([]string{"winner"})
	for i := len(fixtures) - 1; i > 0; i-- {
		if err := os.Remove(fixtures[i].path); err != nil {
			t.Fatal(err)
		}
		mustReload(t, m)
		got := requireSkill(t, m, "winner")
		want := fixtures[i-1].body
		if got.Body != want || got.UsageCount != 1 || got.Enabled {
			t.Fatalf("after removing %q winner = body %q usage %d enabled %v, want %q usage 1 disabled", fixtures[i].body, got.Body, got.UsageCount, got.Enabled, want)
		}
		wantPack := i-1 == 0
		wantReadOnly := i-1 >= 1 && i-1 <= 4
		if got.Pack != wantPack || got.ReadOnly != wantReadOnly {
			t.Fatalf("winner provenance after removing %q = pack %v readonly %v, want %v/%v", fixtures[i].body, got.Pack, got.ReadOnly, wantPack, wantReadOnly)
		}
	}
	if got := requireSkill(t, m, "winner"); !got.Pack || got.ReadOnly {
		t.Fatalf("pack winner provenance = %+v, want pack and mutable", got)
	}
	if err := os.Remove(fixtures[0].path); err != nil {
		t.Fatal(err)
	}
	mustReload(t, m)
	if _, ok := m.Get("winner"); ok {
		t.Fatal("winner remained after every source was removed")
	}

	packMutablePath := filepath.Join(pack, "pack-mutable.md")
	writeFile(t, packMutablePath, skillDocument("pack-mutable", "pack", "PACK_MUTABLE", true))
	mustReload(t, m)
	packOriginal, err := os.ReadFile(packMutablePath)
	if err != nil {
		t.Fatal(err)
	}
	m.SetDisabled([]string{"pack-mutable"})
	if requireSkill(t, m, "pack-mutable").Enabled {
		t.Fatal("pack preference did not update the effective skill")
	}
	if raw, err := os.ReadFile(packMutablePath); err != nil || string(raw) != string(packOriginal) {
		t.Fatalf("SetDisabled changed pack source: err=%v bytes=%q", err, raw)
	}
	importedPath := filepath.Join(user1, "imported", "SKILL.md")
	original := skillDocument("imported", "imported", "ORIGINAL", true)
	writeFile(t, importedPath, original)
	mixedPath := filepath.Join(user1, "mixed", "SKILL.md")
	mixedOriginal := skillDocument("Mixed Name", "mixed", "MIXED_ORIGINAL", true)
	normalizedPath := filepath.Join(user1, "normalized", "SKILL.md")
	normalizedOriginal := skillDocument("normalized-name", "normalized", "NORMALIZED_ORIGINAL", true)
	writeFile(t, normalizedPath, normalizedOriginal)
	writeFile(t, mixedPath, mixedOriginal)
	mustReload(t, m)
	operations := []struct {
		name string
		run  func() error
	}{
		{"save", func() error { _, err := m.Save("imported", "changed", "changed", nil); return err }},
		{"delete", func() error { return m.Delete("imported") }},
	}
	for _, operation := range operations {
		if err := operation.run(); !errors.Is(err, ErrReadOnly) {
			t.Fatalf("%s error = %v, want ErrReadOnly", operation.name, err)
		}
	}
	m.SetDisabled([]string{"imported"})
	assertSkillDisabled(t, m, "imported")
	if _, err := m.Save("Mixed Name", "changed", "changed", nil); !errors.Is(err, ErrReadOnly) {
		t.Fatalf("Save using unsanitized imported metadata name error = %v, want ErrReadOnly", err)
	}
	if _, err := m.Save("Normalized Name", "changed", "changed", nil); !errors.Is(err, ErrReadOnly) {
		t.Fatalf("Save using a name that normalizes to an imported name error = %v, want ErrReadOnly", err)
	}
	if raw, err := os.ReadFile(normalizedPath); err != nil || string(raw) != normalizedOriginal {
		t.Fatalf("normalized-name read-only source changed: err=%v bytes=%q", err, raw)
	}
	if raw, err := os.ReadFile(mixedPath); err != nil || string(raw) != mixedOriginal {
		t.Fatalf("mixed-name read-only source changed: err=%v bytes=%q", err, raw)
	}
	if raw, err := os.ReadFile(importedPath); err != nil || string(raw) != original {
		t.Fatalf("read-only source changed: err=%v bytes=%q", err, raw)
	}
	if _, err := os.Stat(filepath.Join(configured1, "imported.md")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("read-only Save created a configured shadow: %v", err)
	}
	saved, err := m.Save("new skill", "new", "new body", nil)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Path != filepath.Join(configured1, "new-skill.md") {
		t.Fatalf("new skill path = %q, want first nonempty configured root", saved.Path)
	}

	override := NewManager(Options{Dirs: []string{user1}, UserHome: home})
	mustReload(t, override)
	if requireSkill(t, override, "imported").ReadOnly {
		t.Fatal("explicit configuration of an automatic root must make its winner writable")
	}
	override.SetDisabled([]string{"imported"})
	assertSkillDisabled(t, override, "imported")

	noWritable := NewManager(Options{Dirs: []string{"", "  "}, UserHome: home})
	mustReload(t, noWritable)
	if _, err := noWritable.Save("new", "", "", nil); err == nil || err.Error() != "no skills directory configured" {
		t.Fatalf("Save without a configured directory error = %v", err)
	}
}

func TestDiscoverySymlinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink and FIFO matrix is exercised on Unix")
	}
	base := t.TempDir()
	home := filepath.Join(base, "home")
	root := filepath.Join(home, ".agent", "skills")
	targetA := makeDir(t, filepath.Join(base, "target-a"))
	targetB := makeDir(t, filepath.Join(base, "target-b"))
	writeFile(t, filepath.Join(targetA, "SKILL.md"), skillDocument("linked-root", "target a", "A_BODY", true))
	writeFile(t, filepath.Join(targetB, "SKILL.md"), skillDocument("linked-root", "target b", "B_BODY", true))
	makeDir(t, filepath.Dir(root))
	if err := os.Symlink(targetA, root); err != nil {
		t.Skipf("directory symlinks unavailable: %v", err)
	}

	folderTarget := makeDir(t, filepath.Join(base, "folder-target"))
	writeFile(t, filepath.Join(folderTarget, "SKILL.md"), skillDocument("", "folder alias", "FOLDER_BODY", true))
	if err := os.Symlink(folderTarget, filepath.Join(targetA, "logical-folder")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(folderTarget, filepath.Join(targetA, "other-folder")); err != nil {
		t.Fatal(err)
	}
	fileTarget := filepath.Join(base, "file-target.md")
	writeFile(t, fileTarget, skillDocument("linked-file", "file alias", "FILE_BODY", true))
	if err := os.Symlink(fileTarget, filepath.Join(targetA, "SKILL-LINK.md")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(fileTarget, filepath.Join(targetA, "SKILL.md.link")); err != nil {
		t.Fatal(err)
	}
	fileAliasDir := makeDir(t, filepath.Join(targetA, "file-alias"))
	if err := os.Symlink(fileTarget, filepath.Join(fileAliasDir, "SKILL.md")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(base, "absent"), filepath.Join(targetA, "broken")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(targetA, filepath.Join(targetA, "cycle")); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(targetA, "README.md"), skillDocument("support-link", "ignored", "ignored", true))
	if err := os.Symlink(filepath.Join(targetA, "README.md"), filepath.Join(targetA, "support-copy.md")); err != nil {
		t.Fatal(err)
	}
	fifoDir := makeDir(t, filepath.Join(targetA, "fifo"))
	fifo := filepath.Join(fifoDir, "SKILL.md")
	if _, err := exec.LookPath("mkfifo"); err != nil {
		t.Skipf("FIFO creation tool unavailable: %v", err)
	}
	if output, err := exec.Command("mkfifo", "-m", "600", fifo).CombinedOutput(); err != nil {
		t.Fatalf("FIFO creation failed: %v: %s", err, output)
	}

	m := NewManager(Options{UserHome: home})
	mustReload(t, m)
	linked := requireSkill(t, m, "linked-root")
	if linked.Path != filepath.Join(root, "SKILL.md") || linked.Body != "A_BODY" {
		t.Fatalf("linked root = %+v, want logical path and target A body", linked)
	}
	otherFolder := requireSkill(t, m, "other-folder")
	if otherFolder.Path != filepath.Join(root, "other-folder", "SKILL.md") || otherFolder.Body != "FOLDER_BODY" {
		t.Fatalf("second logical alias fallback/path = %+v, want independent logical name/path", otherFolder)
	}
	folder := requireSkill(t, m, "logical-folder")
	if folder.Path != filepath.Join(root, "logical-folder", "SKILL.md") {
		t.Fatalf("symlinked folder fallback/path = %+v, want logical alias", folder)
	}
	file := requireSkill(t, m, "linked-file")
	if file.Path != filepath.Join(root, "file-alias", "SKILL.md") {
		t.Fatalf("symlinked SKILL.md path = %q, want logical path", file.Path)
	}
	for _, absent := range []string{"support-link", "fifo"} {
		if _, ok := m.Get(absent); ok {
			t.Fatalf("non-procedure %q was loaded", absent)
		}
	}

	if err := os.Remove(root); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(targetB, root); err != nil {
		t.Fatal(err)
	}
	mustReload(t, m)
	if got := requireSkill(t, m, "linked-root"); got.Body != "B_BODY" || got.Path != filepath.Join(root, "SKILL.md") {
		t.Fatalf("retargeted root = %+v, want target B through same logical path", got)
	}
	if err := os.Remove(root); err != nil {
		t.Fatal(err)
	}
	mustReload(t, m)
	if _, ok := m.Get("linked-root"); ok {
		t.Fatal("removed symlink root remained in the catalog")
	}
}
