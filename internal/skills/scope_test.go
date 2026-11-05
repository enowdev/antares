package skills

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

func writeProjectSkill(t *testing.T, projectDir, name, body string) string {
	t.Helper()
	path := filepath.Join(projectDir, ".agent", "skills", name, "SKILL.md")
	writeFile(t, path, skillDocument(name, name+" description", body, true))
	return path
}

func TestProjectScopeIsolationAndPrecedence(t *testing.T) {
	base := t.TempDir()
	home := filepath.Join(base, "home")
	startup := filepath.Join(base, "startup")
	projectA := filepath.Join(base, "project-a")
	projectB := filepath.Join(base, "project-b")
	configured := filepath.Join(base, "configured")
	pack := filepath.Join(base, "pack")

	writeFile(t, filepath.Join(home, ".agent", "skills", "global", "SKILL.md"), skillDocument("global", "global", "GLOBAL", true))
	writeFile(t, filepath.Join(pack, "pack-only.md"), skillDocument("pack-only", "pack", "PACK", true))
	writeFile(t, filepath.Join(pack, "collision.md"), skillDocument("collision", "pack", "PACK_COLLISION", true))
	writeProjectSkill(t, startup, "startup-only", "STARTUP")
	writeProjectSkill(t, startup, "collision", "STARTUP_COLLISION")
	writeProjectSkill(t, projectA, "a-only", "A_ONLY")
	writeProjectSkill(t, projectA, "collision", "A_COLLISION")
	writeProjectSkill(t, projectB, "b-only", "B_ONLY")
	writeProjectSkill(t, projectB, "collision", "B_COLLISION")

	manager := NewManager(Options{Dirs: []string{configured}, PackDirs: []string{pack}, UserHome: home, ProjectDir: startup})
	mustReload(t, manager)
	a, err := manager.ForProject(projectA)
	if err != nil {
		t.Fatal(err)
	}
	b, err := manager.ForProject(projectB)
	if err != nil {
		t.Fatal(err)
	}
	relative, err := manager.ForProject("../project-a")
	if err != nil {
		t.Fatal(err)
	}

	for label, scoped := range map[string]*Manager{"startup": manager, "a": a, "b": b, "relative-a": relative} {
		if got := requireSkill(t, scoped, "global"); got.Body != "GLOBAL" {
			t.Fatalf("%s global body = %q", label, got.Body)
		}
		if got := requireSkill(t, scoped, "pack-only"); got.Body != "PACK" || !got.Pack {
			t.Fatalf("%s pack skill = %+v", label, got)
		}
	}
	if got := requireSkill(t, manager, "collision"); got.Body != "STARTUP_COLLISION" {
		t.Fatalf("startup collision = %q", got.Body)
	}
	if got := requireSkill(t, a, "collision"); got.Body != "A_COLLISION" {
		t.Fatalf("A collision = %q", got.Body)
	}
	if got := requireSkill(t, b, "collision"); got.Body != "B_COLLISION" {
		t.Fatalf("B collision = %q", got.Body)
	}
	if got := requireSkill(t, relative, "collision"); got.Body != "A_COLLISION" {
		t.Fatalf("relative A collision = %q", got.Body)
	}
	manager.SetDisabled([]string{"collision"})
	for label, scoped := range map[string]*Manager{"startup": manager, "a": a, "b": b, "relative-a": relative} {
		if requireSkill(t, scoped, "collision").Enabled {
			t.Fatalf("%s did not observe shared disabled state", label)
		}
	}
	search := a.Search("collision", 1)
	if len(search) != 1 || search[0].Body != "A_COLLISION" {
		t.Fatalf("A search winner = %+v", search)
	}
	if got := a.Count(); got != len(a.List())-1 {
		t.Fatalf("A enabled count = %d, list length = %d with one disabled", got, len(a.List()))
	}
	var readers sync.WaitGroup
	for range 8 {
		readers.Add(2)
		go func() {
			defer readers.Done()
			got, ok := a.Get("collision")
			if !ok || got.Body != "A_COLLISION" {
				t.Errorf("concurrent A collision = %+v, present=%v", got, ok)
			}
		}()
		go func() {
			defer readers.Done()
			got, ok := b.Get("collision")
			if !ok || got.Body != "B_COLLISION" {
				t.Errorf("concurrent B collision = %+v, present=%v", got, ok)
			}
		}()
	}
	readers.Wait()
	for _, check := range []struct {
		scope  *Manager
		absent string
	}{{manager, "a-only"}, {manager, "b-only"}, {a, "startup-only"}, {a, "b-only"}, {b, "startup-only"}, {b, "a-only"}} {
		if _, ok := check.scope.Get(check.absent); ok {
			t.Fatalf("scope unexpectedly exposed %q", check.absent)
		}
	}

	writeFile(t, filepath.Join(configured, "collision.md"), skillDocument("collision", "configured", "CONFIGURED_COLLISION", true))
	mustReload(t, a)
	for label, scoped := range map[string]*Manager{"startup": manager, "a": a, "b": b} {
		if got := requireSkill(t, scoped, "collision"); got.Body != "CONFIGURED_COLLISION" || got.ReadOnly || got.Pack || got.Enabled {
			t.Fatalf("%s configured disabled winner = %+v", label, got)
		}
	}
	if err := os.Remove(filepath.Join(configured, "collision.md")); err != nil {
		t.Fatal(err)
	}
	mustReload(t, b)
	for label, scoped := range map[string]*Manager{"startup": manager, "a": a, "b": b} {
		got := requireSkill(t, scoped, "collision")
		want := map[string]string{"startup": "STARTUP_COLLISION", "a": "A_COLLISION", "b": "B_COLLISION"}[label]
		if got.Body != want || got.Enabled {
			t.Fatalf("%s fallback after configured removal = %+v, want body %q still disabled", label, got, want)
		}
	}

	saved, err := a.Save("shared saved", "saved", "SAVED_BODY", []string{"saved"})
	if err != nil {
		t.Fatal(err)
	}
	if saved.Path != filepath.Join(configured, "shared-saved.md") {
		t.Fatalf("scoped Save path = %q", saved.Path)
	}
	for label, scoped := range map[string]*Manager{"startup": manager, "a": a, "b": b} {
		if got := requireSkill(t, scoped, "shared-saved"); got.Body != "SAVED_BODY" {
			t.Fatalf("%s did not observe scoped Save: %+v", label, got)
		}
	}
	b.SetDisabled([]string{"collision", "shared-saved"})
	for label, scoped := range map[string]*Manager{"startup": manager, "a": a, "b": b} {
		if requireSkill(t, scoped, "shared-saved").Enabled {
			t.Fatalf("%s did not observe scoped SetDisabled", label)
		}
	}
	if err := manager.Delete("shared-saved"); err != nil {
		t.Fatal(err)
	}
	for label, scoped := range map[string]*Manager{"startup": manager, "a": a, "b": b} {
		if _, ok := scoped.Get("shared-saved"); ok {
			t.Fatalf("%s retained scoped deletion", label)
		}
	}
}

func TestProjectScopeErrorsPartialAndSharedOnly(t *testing.T) {
	base := t.TempDir()
	home := filepath.Join(base, "home")
	configured := filepath.Join(base, "configured")
	project := filepath.Join(base, "partial")
	writeFile(t, filepath.Join(home, ".agent", "skills", "global", "SKILL.md"), skillDocument("global", "global", "GLOBAL", true))
	writeFile(t, filepath.Join(configured, "configured.md"), skillDocument("configured", "configured", "CONFIGURED", true))
	writeProjectSkill(t, project, "valid", "VALID")
	writeFile(t, filepath.Join(project, ".agent", "skills", "broken", "SKILL.md"), "---\nname: [broken\n---\nbody")

	manager := NewManager(Options{Dirs: []string{configured}, UserHome: home})
	mustReload(t, manager)
	partial, err := manager.ForProject(project)
	if err == nil || !strings.Contains(err.Error(), "invalid front matter") {
		t.Fatalf("partial scope error = %v", err)
	}
	if requireSkill(t, partial, "valid").Body != "VALID" {
		t.Fatal("valid project skill was hidden by malformed neighbor")
	}
	for _, name := range []string{"global", "configured"} {
		requireSkill(t, partial, name)
	}

	sharedOnly, err := manager.ForProject("relative-project")
	if err == nil || !strings.Contains(err.Error(), "startup project directory is unavailable") {
		t.Fatalf("relative scope error = %v", err)
	}
	for _, name := range []string{"global", "configured"} {
		requireSkill(t, sharedOnly, name)
	}
	if _, ok := sharedOnly.Get("valid"); ok {
		t.Fatal("normalization fallback leaked another project's skill")
	}
	malformed, err := manager.ForProject("bad\x00path")
	if err == nil {
		t.Fatal("NUL project path unexpectedly normalized")
	}
	if _, ok := malformed.Get("valid"); ok {
		t.Fatal("malformed path fallback leaked registered project skills")
	}

	var nilManager *Manager
	if got, err := nilManager.ForProject(project); got != nil || err != nil {
		t.Fatalf("nil manager ForProject = %#v, %v", got, err)
	}
}

func TestProjectScopeReloadAliasUsageChainsAndCopies(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink retarget coverage is exercised on Unix")
	}
	base := t.TempDir()
	startup := filepath.Join(base, "startup")
	alias := filepath.Join(base, "alias")
	targetA := filepath.Join(base, "target-a")
	targetB := filepath.Join(base, "target-b")
	pack := filepath.Join(base, "pack")

	writeProjectSkill(t, targetA, "collision", "ALIAS_A")
	writeProjectSkill(t, targetB, "collision", "ALIAS_B")
	writeProjectSkill(t, targetA, "chain-start", "CHAIN_A")
	chainPath := filepath.Join(targetA, ".agent", "skills", "chain-start", "SKILL.md")
	writeFile(t, chainPath, strings.Replace(skillDocument("chain-start", "chain", "CHAIN_A", true), "chains_with: [next]", "chains_with: [collision]", 1))
	writeFile(t, filepath.Join(pack, "collision.md"), skillDocument("collision", "pack", "PACK_COLLISION", true))
	if err := os.Symlink(targetA, alias); err != nil {
		t.Skipf("directory symlinks unavailable: %v", err)
	}

	manager := NewManager(Options{PackDirs: []string{pack}, ProjectDir: startup})
	mustReload(t, manager)
	scoped, err := manager.ForProject(alias)
	if err != nil {
		t.Fatal(err)
	}
	if categories := scoped.Categories(); len(categories) != 0 {
		t.Fatalf("project collision should hide bundled categories: %+v", categories)
	}
	manager.MarkUsed("collision")
	got := requireSkill(t, scoped, "collision")
	if got.Body != "ALIAS_A" || got.UsageCount != 1 || got.Pack {
		t.Fatalf("alias A effective skill = %+v", got)
	}
	if got.Path != filepath.Join(alias, ".agent", "skills", "collision", "SKILL.md") {
		t.Fatalf("alias scope path = %q, want logical alias path", got.Path)
	}
	chains := scoped.Chains("chain-start")
	if len(chains) != 1 || chains[0].Body != "ALIAS_A" || chains[0].UsageCount != 1 {
		t.Fatalf("scoped chain resolution = %+v", chains)
	}
	if scoped.PackCount() != 0 {
		t.Fatalf("project collision should hide effective pack count, got %d", scoped.PackCount())
	}
	library, total := scoped.Library("", 0, 10)
	if total != 0 || len(library) != 0 {
		t.Fatalf("project collision should hide pack browsing entry: total=%d items=%+v", total, library)
	}

	listed := scoped.List()
	for i := range listed {
		if listed[i].Name == "collision" {
			listed[i].Tags[0] = "caller-mutated"
			listed[i].ChainsWith = append(listed[i].ChainsWith, "caller-mutated")
		}
	}
	if got := requireSkill(t, scoped, "collision"); got.Tags[0] != "discovery" || len(got.ChainsWith) != 1 || got.ChainsWith[0] != "next" {
		t.Fatalf("List slice mutation leaked into shared snapshot: %+v", got)
	}

	if err := os.Remove(alias); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(targetB, alias); err != nil {
		t.Fatal(err)
	}
	mustReload(t, manager)
	if got := requireSkill(t, scoped, "collision"); got.Body != "ALIAS_B" || got.UsageCount != 1 {
		t.Fatalf("retargeted bound scope = %+v", got)
	}
	if _, ok := scoped.Get("chain-start"); ok {
		t.Fatal("bound alias retained a skill from its previous target")
	}
	root, err := manager.ForProject("")
	if err != nil {
		t.Fatal(err)
	}
	if got := requireSkill(t, root, "collision"); got.Body != "PACK_COLLISION" {
		t.Fatalf("startup collision = %q, want bundled fallback", got.Body)
	}
}
