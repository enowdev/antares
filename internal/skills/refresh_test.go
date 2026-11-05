package skills

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

const refreshTestInterval = 15 * time.Millisecond

func startTestWatch(t *testing.T, manager *Manager) (context.CancelFunc, <-chan struct{}) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		manager.Watch(ctx, refreshTestInterval)
	}()
	return cancel, done
}

func stopTestWatch(t *testing.T, cancel context.CancelFunc, done <-chan struct{}) {
	t.Helper()
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Watch did not stop after context cancellation")
	}
}

func eventually(t *testing.T, condition func() bool, detail string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		runtime.Gosched()
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("deadline waiting for %s", detail)
}

func eventuallySkill(t *testing.T, manager *Manager, name string, condition func(*Skill) bool) {
	t.Helper()
	eventually(t, func() bool {
		skill, ok := manager.Get(name)
		return ok && condition(skill)
	}, "skill "+name)
}

func TestWatchRefreshTransitions(t *testing.T) {
	base := t.TempDir()
	home := filepath.Join(base, "home")
	startup := filepath.Join(base, "startup")
	projectA := filepath.Join(base, "project-a")
	projectB := filepath.Join(base, "project-b")
	configuredA := filepath.Join(base, "configured-a")
	configuredB := filepath.Join(base, "configured-b")
	manager := NewManager(Options{Dirs: []string{configuredA}, UserHome: home, ProjectDir: startup})
	mustReload(t, manager)
	a, err := manager.ForProject(projectA)
	if err != nil {
		t.Fatal(err)
	}
	b, err := manager.ForProject(projectB)
	if err != nil {
		t.Fatal(err)
	}
	writeProjectSkill(t, projectB, "b-only", "B_STABLE")
	mustReload(t, manager)

	cancel, done := startTestWatch(t, manager)
	defer func() { stopTestWatch(t, cancel, done) }()

	// The automatic root is absent when the watcher starts and is enumerated on
	// every tick, so creating the root and its first procedure needs no Reload.
	livePath := filepath.Join(home, ".agent", "skills", "live", "SKILL.md")
	writeFile(t, livePath, skillDocument("live", "initial description", "INITIAL_BODY", true))
	eventuallySkill(t, manager, "live", func(skill *Skill) bool {
		return skill.Body == "INITIAL_BODY" && skill.ReadOnly
	})
	if got := manager.Search("initial description", 5); len(got) != 1 || got[0].Name != "live" {
		t.Fatalf("Search after automatic addition = %+v", got)
	}
	if !strings.Contains(manager.PromptBlock(0), "live") {
		t.Fatal("PromptBlock did not expose enabled watched skill")
	}
	manager.SetDisabled([]string{"live"})
	assertSkillDisabled(t, manager, "live")

	writeFile(t, livePath, skillDocument("live", "changed description", "EDITED_BODY_LONGER", false))
	eventually(t, func() bool {
		updated, ok := manager.Get("live")
		return ok && updated.Body == "EDITED_BODY_LONGER" && !updated.Enabled &&
			len(manager.Search("changed description", 5)) == 1 && !strings.Contains(manager.PromptBlock(0), "live")
	}, "metadata/body edit to refresh while config-disabled state survives")

	atomicTemp := filepath.Join(filepath.Dir(livePath), "replacement.tmp")
	writeFile(t, atomicTemp, skillDocument("live", "atomic description", "ATOMIC_BODY", true))
	if err := os.Rename(atomicTemp, livePath); err != nil {
		t.Fatal(err)
	}
	eventually(t, func() bool {
		atomic, ok := manager.Get("live")
		return ok && atomic.Body == "ATOMIC_BODY" && !atomic.Enabled && !strings.Contains(manager.PromptBlock(0), "live")
	}, "atomic replacement preserving disabled state")

	projectAPath := writeProjectSkill(t, projectA, "a-only", "A_INITIAL")
	eventuallySkill(t, a, "a-only", func(skill *Skill) bool { return skill.Body == "A_INITIAL" })
	writeFile(t, projectAPath, skillDocument("a-only", "a changed", "A_CHANGED_LONGER", true))
	eventuallySkill(t, a, "a-only", func(skill *Skill) bool { return skill.Body == "A_CHANGED_LONGER" })
	if got := requireSkill(t, b, "b-only"); got.Body != "B_STABLE" {
		t.Fatalf("A refresh changed B scope: %+v", got)
	}
	if _, ok := b.Get("a-only"); ok {
		t.Fatal("A watched skill leaked into B")
	}

	writeFile(t, filepath.Join(configuredA, "old.md"), skillDocument("old-configured", "old", "OLD", true))
	eventuallySkill(t, manager, "old-configured", func(skill *Skill) bool { return skill.Body == "OLD" })
	newStartup := filepath.Join(base, "elsewhere", "new-startup")
	writeProjectSkill(t, newStartup, "new-startup-only", "NEW_STARTUP")
	writeFile(t, filepath.Join(configuredB, "new.md"), skillDocument("new-configured", "new", "NEW", true))
	newDirs := []string{configuredB}
	if err := manager.Reconfigure(Options{Dirs: newDirs, UserHome: home, ProjectDir: newStartup}); err != nil {
		t.Fatal(err)
	}
	for label, scoped := range map[string]*Manager{"root": manager, "A": a, "B": b} {
		if requireSkill(t, scoped, "live").Enabled {
			t.Fatalf("%s reconfigure cleared shared disabled state", label)
		}
	}
	newDirs[0] = configuredA
	if _, ok := manager.Get("old-configured"); ok {
		t.Fatal("root handle retained removed configured source after Reconfigure")
	}
	for label, scoped := range map[string]*Manager{"root": manager, "A": a, "B": b} {
		if got := requireSkill(t, scoped, "new-configured"); got.Body != "NEW" {
			t.Fatalf("%s existing handle missed reconfigured source: %+v", label, got)
		}
	}
	if got := requireSkill(t, manager, "new-startup-only"); got.Body != "NEW_STARTUP" {
		t.Fatalf("root handle did not follow reconfigured default project: %+v", got)
	}
	writeFile(t, filepath.Join(configuredB, "after.md"), skillDocument("after-reconfigure", "after", "AFTER", true))
	eventuallySkill(t, a, "after-reconfigure", func(skill *Skill) bool { return skill.Body == "AFTER" })
	relative, err := manager.ForProject("../project-a")
	if err != nil {
		t.Fatal(err)
	}
	if got := requireSkill(t, relative, "a-only"); got.Body != "A_CHANGED_LONGER" {
		t.Fatalf("relative binding no longer uses original startup directory: %+v", got)
	}

	if err := os.Remove(livePath); err != nil {
		t.Fatal(err)
	}
	eventually(t, func() bool {
		_, ok := manager.Get("live")
		return !ok && len(manager.Search("atomic description", 5)) == 0 && !strings.Contains(manager.PromptBlock(0), "live")
	}, "watched removal")
	var readers sync.WaitGroup
	for range 12 {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for range 30 {
				_ = manager.List()
				_, _ = a.Get("a-only")
				_ = b.Search("B_STABLE", 2)
			}
		}()
	}
	if _, err := manager.Save("concurrent", "concurrent", "CONCURRENT", nil); err != nil {
		t.Fatal(err)
	}
	readers.Wait()
}

func TestWatchDetectsPreservedMetadata(t *testing.T) {
	configured := t.TempDir()
	path := filepath.Join(configured, "stable.md")
	oldDoc := skillDocument("stable", "same", "AAAA", true)
	newDoc := skillDocument("stable", "same", "BBBB", true)
	if len(oldDoc) != len(newDoc) {
		t.Fatal("fixture documents must be the same size")
	}
	writeFile(t, path, oldDoc)
	manager := NewManager(Options{Dirs: []string{configured}})
	mustReload(t, manager)
	originalInfo, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}

	writeFile(t, path, newDoc)
	if err := os.Chtimes(path, originalInfo.ModTime(), originalInfo.ModTime()); err != nil {
		t.Fatal(err)
	}
	cancel, done := startTestWatch(t, manager)
	defer func() { stopTestWatch(t, cancel, done) }()
	// Cached ticks may preserve the old bytes; the twelfth tick must not.
	eventuallySkill(t, manager, "stable", func(skill *Skill) bool { return skill.Body == "BBBB" })
}

func TestReloadBypassesParseCache(t *testing.T) {
	configured := t.TempDir()
	path := filepath.Join(configured, "stable.md")
	oldDoc := skillDocument("stable", "same", "AAAA", true)
	newDoc := skillDocument("stable", "same", "BBBB", true)
	if len(oldDoc) != len(newDoc) {
		t.Fatal("fixture documents must be the same size")
	}
	writeFile(t, path, oldDoc)
	manager := NewManager(Options{Dirs: []string{configured}})
	mustReload(t, manager)
	originalInfo, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, path, newDoc)
	if err := os.Chtimes(path, originalInfo.ModTime(), originalInfo.ModTime()); err != nil {
		t.Fatal(err)
	}
	mustReload(t, manager)
	if got := requireSkill(t, manager, "stable"); got.Body != "BBBB" {
		t.Fatalf("forced Reload reused cached parser output: %+v", got)
	}
}

func TestWatchCancellationStopsRefresh(t *testing.T) {
	configured := t.TempDir()
	path := filepath.Join(configured, "skill.md")
	writeFile(t, path, skillDocument("skill", "initial", "INITIAL", true))
	manager := NewManager(Options{Dirs: []string{configured}})
	mustReload(t, manager)
	cancel, done := startTestWatch(t, manager)
	stopTestWatch(t, cancel, done)

	writeFile(t, path, skillDocument("skill", "changed", "CHANGED_AFTER_CANCEL", true))
	deadline := time.Now().Add(4 * refreshTestInterval)
	for time.Now().Before(deadline) {
		if got := requireSkill(t, manager, "skill"); got.Body != "INITIAL" {
			t.Fatalf("catalog changed after Watch stopped: %+v", got)
		}
		runtime.Gosched()
	}
}

func TestCanceledRefreshDoesNotPublishPartialScan(t *testing.T) {
	configured := t.TempDir()
	writeFile(t, filepath.Join(configured, "first.md"), skillDocument("first", "first", "FIRST", true))
	writeFile(t, filepath.Join(configured, "second.md"), skillDocument("second", "second", "SECOND", true))
	manager := NewManager(Options{Dirs: []string{configured}})
	mustReload(t, manager)

	writeFile(t, filepath.Join(configured, "first.md"), skillDocument("first-new", "changed", "CHANGED", true))
	// Cancellation occurs after the first replacement was parsed but while later
	// directory entries remain, proving publication is all-or-nothing mid-scan.
	ctx := &cancelAfterChecks{Context: context.Background(), remaining: 6}
	manager.state.scanMu.Lock()
	err := manager.refreshLocked(ctx, true)
	manager.state.scanMu.Unlock()
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled refresh error = %v", err)
	}
	if got := requireSkill(t, manager, "first"); got.Body != "FIRST" {
		t.Fatalf("canceled refresh published partial replacement: %+v", got)
	}
	if _, ok := manager.Get("first-new"); ok {
		t.Fatal("canceled refresh published newly parsed entry")
	}
	requireSkill(t, manager, "second")
}

type cancelAfterChecks struct {
	context.Context
	remaining int
}

func (ctx *cancelAfterChecks) Err() error {
	ctx.remaining--
	if ctx.remaining <= 0 {
		return context.Canceled
	}
	return nil
}

func TestWatchRetriesParseErrorsAndPrunesCache(t *testing.T) {
	configured := t.TempDir()
	path := filepath.Join(configured, "retry.md")
	neighbor := filepath.Join(configured, "neighbor.md")
	writeFile(t, path, skillDocument("retry", "valid", "VALID", true))
	writeFile(t, neighbor, skillDocument("neighbor", "initial", "NEIGHBOR_INITIAL", true))
	manager := NewManager(Options{Dirs: []string{configured}})
	mustReload(t, manager)
	cancel, done := startTestWatch(t, manager)
	defer func() { stopTestWatch(t, cancel, done) }()

	writeFile(t, path, "---\nname: [broken\n---\nBROKEN")
	writeFile(t, neighbor, skillDocument("neighbor", "changed", "NEIGHBOR_CHANGED_LONGER", true))
	eventually(t, func() bool {
		_, ok := manager.Get("retry")
		updated, neighborOK := manager.Get("neighbor")
		return !ok && neighborOK && updated.Body == "NEIGHBOR_CHANGED_LONGER"
	}, "failed parse removal with successful neighbor publication")
	writeFile(t, path, skillDocument("retry", "recovered", "RECOVERED_LONGER", true))
	eventuallySkill(t, manager, "retry", func(skill *Skill) bool { return skill.Body == "RECOVERED_LONGER" })
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	eventually(t, func() bool {
		_, ok := manager.Get("retry")
		return !ok
	}, "removed cached file to disappear")
}
func TestWatchCacheSourceNeutralAcrossAliases(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink cache coverage is exercised on Unix")
	}
	base := t.TempDir()
	home := filepath.Join(base, "home")
	target := filepath.Join(base, "shared-skill.md")
	writeFile(t, target, skillDocument("", "shared cache", "SHARED", true))
	for _, alias := range []string{
		filepath.Join(home, ".agent", "skills", "alpha", "SKILL.md"),
		filepath.Join(home, ".agents", "skills", "beta", "SKILL.md"),
	} {
		makeDir(t, filepath.Dir(alias))
		if err := os.Symlink(target, alias); err != nil {
			t.Skipf("file symlinks unavailable: %v", err)
		}
	}
	manager := NewManager(Options{UserHome: home})
	mustReload(t, manager)
	cancel, done := startTestWatch(t, manager)
	defer func() { stopTestWatch(t, cancel, done) }()
	writeFile(t, filepath.Join(home, ".agent", "skills", "tick-marker", "SKILL.md"), skillDocument("tick-marker", "tick", "TICK", true))
	eventuallySkill(t, manager, "tick-marker", func(skill *Skill) bool { return skill.Body == "TICK" })
	for name, wantSuffix := range map[string]string{
		"alpha": filepath.Join("alpha", "SKILL.md"),
		"beta":  filepath.Join("beta", "SKILL.md"),
	} {
		got := requireSkill(t, manager, name)
		if got.Body != "SHARED" || !strings.HasSuffix(got.Path, wantSuffix) {
			t.Fatalf("cached alias %q materialization = %+v", name, got)
		}
	}
}

func TestWatchRetargetsBoundAlias(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink retarget coverage is exercised on Unix")
	}
	base := t.TempDir()
	startup := filepath.Join(base, "startup")
	targetA := filepath.Join(base, "target-a")
	targetB := filepath.Join(base, "target-b")
	alias := filepath.Join(base, "alias")
	writeProjectSkill(t, targetA, "collision", "ALIAS_A")
	writeProjectSkill(t, targetB, "collision", "ALIAS_B")
	if err := os.Symlink(targetA, alias); err != nil {
		t.Skipf("directory symlinks unavailable: %v", err)
	}
	manager := NewManager(Options{ProjectDir: startup})
	mustReload(t, manager)
	scoped, err := manager.ForProject(alias)
	if err != nil {
		t.Fatal(err)
	}
	cancel, done := startTestWatch(t, manager)
	defer func() { stopTestWatch(t, cancel, done) }()
	if err := os.Remove(alias); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(targetB, alias); err != nil {
		t.Fatal(err)
	}
	eventuallySkill(t, scoped, "collision", func(skill *Skill) bool {
		return skill.Body == "ALIAS_B" && strings.HasPrefix(skill.Path, alias)
	})
}
