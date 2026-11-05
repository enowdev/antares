package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/enowdev/antares/internal/agent"
	"github.com/enowdev/antares/internal/config"
	"github.com/enowdev/antares/internal/skills"
)

func TestSkillToggleDoesNotMutateWritableSource(t *testing.T) {
	s, manager, _, sources := newSkillToggleServer(t, []string{"writable"}, nil)
	path := sources["writable"]
	before := snapshotSkillSource(t, path)

	if rr := postSkillToggle(s, "writable", false); rr.Code != http.StatusOK {
		t.Fatalf("disable status = %d, want 200 (body=%s)", rr.Code, rr.Body.String())
	}
	if sk, ok := manager.Get("writable"); !ok || sk.Enabled {
		t.Fatalf("live skill after disable = %#v, found=%v", sk, ok)
	}
	assertSkillSourceUnchanged(t, path, before)
}

func TestSkillTogglePersistsWithoutMutatingReadOnlySourceAndRestarts(t *testing.T) {
	s, manager, cfgPath, sources := newSkillToggleServer(t, []string{"toggle-me"}, nil)
	path := sources["toggle-me"]
	if err := os.Chmod(path, 0o444); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Dir(path), 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Chmod(filepath.Dir(path), 0o755)
		_ = os.Chmod(path, 0o644)
	})
	before := snapshotSkillSource(t, path)

	if rr := postSkillToggle(s, "toggle-me", false); rr.Code != http.StatusOK {
		t.Fatalf("disable status = %d, want 200 (body=%s)", rr.Code, rr.Body.String())
	}
	if sk, ok := manager.Get("toggle-me"); !ok || sk.Enabled {
		t.Fatalf("live skill after disable = %#v, found=%v", sk, ok)
	}
	persisted, err := config.Reload()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(persisted.Skills.Disabled, []string{"toggle-me"}) {
		t.Fatalf("persisted disabled = %#v, want [toggle-me]", persisted.Skills.Disabled)
	}
	assertSkillSourceUnchanged(t, path, before)

	restartedManager := skills.NewManager(skills.Options{Dirs: persisted.Skills.Dirs})
	if err := restartedManager.Reload(); err != nil {
		t.Fatal(err)
	}
	restarted := New(Options{Config: persisted, Skills: restartedManager})
	if sk, ok := restarted.currentSkills().Get("toggle-me"); !ok || sk.Enabled {
		t.Fatalf("restarted skill = %#v, found=%v; want disabled", sk, ok)
	}
	if rr := postSkillToggle(restarted, "toggle-me", true); rr.Code != http.StatusOK {
		t.Fatalf("re-enable status = %d, want 200 (body=%s)", rr.Code, rr.Body.String())
	}
	if sk, ok := restarted.currentSkills().Get("toggle-me"); !ok || !sk.Enabled {
		t.Fatalf("live skill after re-enable = %#v, found=%v", sk, ok)
	}
	persisted, err = config.Reload()
	if err != nil {
		t.Fatal(err)
	}
	if len(persisted.Skills.Disabled) != 0 {
		t.Fatalf("persisted disabled after re-enable = %#v, want empty", persisted.Skills.Disabled)
	}
	if cfgPath != config.ConfigFile() {
		t.Fatalf("fixture config path changed from %q to %q", cfgPath, config.ConfigFile())
	}
	assertSkillSourceUnchanged(t, path, before)
}

func TestSkillToggleConcurrentDifferentNamesPersistsUnion(t *testing.T) {
	s, manager, _, _ := newSkillToggleServer(t, []string{"alpha", "bravo"}, nil)

	var wg sync.WaitGroup
	results := make(chan *httptest.ResponseRecorder, 2)
	for _, name := range []string{"alpha", "bravo"} {
		name := name
		wg.Add(1)
		go func() {
			defer wg.Done()
			results <- postSkillToggle(s, name, false)
		}()
	}
	wg.Wait()
	close(results)
	for rr := range results {
		if rr.Code != http.StatusOK {
			t.Fatalf("concurrent toggle status = %d, want 200 (body=%s)", rr.Code, rr.Body.String())
		}
	}
	persisted, err := config.Reload()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"alpha", "bravo"}
	if !reflect.DeepEqual(persisted.Skills.Disabled, want) {
		t.Fatalf("persisted disabled = %#v, want %#v", persisted.Skills.Disabled, want)
	}
	for _, name := range want {
		if sk, ok := manager.Get(name); !ok || sk.Enabled {
			t.Fatalf("live %s = %#v, found=%v; want disabled", name, sk, ok)
		}
	}
}

func TestSkillToggleRejectsUnknownAndUnavailableManager(t *testing.T) {
	s, _, _, _ := newSkillToggleServer(t, []string{"known"}, []string{"existing"})
	if rr := postSkillToggle(s, "missing", false); rr.Code != http.StatusBadRequest {
		t.Fatalf("unknown skill status = %d, want 400 (body=%s)", rr.Code, rr.Body.String())
	}
	persisted, err := config.Reload()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(persisted.Skills.Disabled, []string{"existing"}) {
		t.Fatalf("unknown skill created preference: %#v", persisted.Skills.Disabled)
	}

	nilServer := &Server{}
	if rr := postSkillToggle(nilServer, "known", false); rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("nil manager status = %d, want 503 (body=%s)", rr.Code, rr.Body.String())
	}
}

func TestSkillToggleSaveFailureDoesNotPublish(t *testing.T) {
	_, manager, cfgPath, _ := newSkillToggleServer(t, []string{"known"}, nil)
	before, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	procPath := "/proc/self/fd/" + strconv.Itoa(int(file.Fd()))
	if _, err := os.Stat(procPath); err != nil {
		t.Skipf("proc fd paths unavailable: %v", err)
	}
	t.Setenv("ANTARES_CONFIG", procPath)
	cfg := config.Get()
	s := New(Options{Config: cfg, Skills: manager})

	rr := postSkillToggle(s, "known", false)
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("save failure status = %d, want 500 (body=%s)", rr.Code, rr.Body.String())
	}
	if sk, ok := manager.Get("known"); !ok || !sk.Enabled {
		t.Fatalf("failed save published disabled state: %#v, found=%v", sk, ok)
	}
	after, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, before) {
		t.Fatal("failed config save changed the persisted file")
	}
}

func TestSkillToggleReloadFailurePersistsWithoutPublishing(t *testing.T) {
	_, manager, _, _ := newSkillToggleServer(t, []string{"known"}, nil)
	cfg := config.Get()
	s := New(Options{
		Config: cfg,
		Skills: manager,
		Reload: func() error { return errors.New("reload failed") },
	})

	rr := postSkillToggle(s, "known", false)
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("reload failure status = %d, want 500 (body=%s)", rr.Code, rr.Body.String())
	}
	persisted, err := config.Reload()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(persisted.Skills.Disabled, []string{"known"}) {
		t.Fatalf("saved preference after reload failure = %#v, want [known]", persisted.Skills.Disabled)
	}
	if len(s.config().Skills.Disabled) != 0 {
		t.Fatalf("reload failure published server config: %#v", s.config().Skills.Disabled)
	}
	if sk, ok := manager.Get("known"); !ok || !sk.Enabled {
		t.Fatalf("reload failure published state despite callback error: %#v, found=%v", sk, ok)
	}
}

func TestApplyReloadWithoutCallbackReturnsErrorsAndToleratesNilAgent(t *testing.T) {
	s, manager, cfgPath, _ := newSkillToggleServer(t, []string{"known"}, nil)
	if err := os.WriteFile(cfgPath, []byte("skills: [unterminated\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	before := s.config()
	if err := s.applyReload(); err == nil {
		t.Fatal("applyReload succeeded with malformed persisted configuration")
	}
	if s.config() != before {
		t.Fatal("failed applyReload replaced the server config")
	}
	if sk, ok := manager.Get("known"); !ok || !sk.Enabled {
		t.Fatalf("failed applyReload changed manager state: %#v, found=%v", sk, ok)
	}
}

func TestNewInitializesFallbackSkillPreferences(t *testing.T) {
	s, manager, _, _ := newSkillToggleServer(t, []string{"known"}, []string{"known"})
	if s.agent != nil {
		t.Fatal("fixture unexpectedly has an agent")
	}
	if sk, ok := manager.Get("known"); !ok || sk.Enabled {
		t.Fatalf("fallback skill = %#v, found=%v; want disabled at construction", sk, ok)
	}
}

func TestSkillHandlersObserveInPlaceReconfigure(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ANTARES_HOME", home)
	t.Setenv("ANTARES_PROFILE", "default")
	t.Setenv("ANTARES_CONFIG", filepath.Join(home, "config.yaml"))
	oldDir := filepath.Join(home, "old-skills")
	newDir := filepath.Join(home, "new-skills")
	packDir := filepath.Join(home, "pack-skills")
	writeToggleServerSkill(t, oldDir, "replace-me")
	writeToggleServerSkill(t, oldDir, "old-only")
	writeToggleServerSkill(t, newDir, "replace-me")
	writeToggleServerSkill(t, newDir, "replacement-only")
	writeToggleServerSkill(t, packDir, "code-review")

	cfg := config.Default()
	cfg.Skills.Enabled = true
	cfg.Skills.FrontmatterMigrated = true
	cfg.Skills.Dirs = []string{oldDir}
	if err := config.SaveAt(config.ConfigFile(), cfg); err != nil {
		t.Fatal(err)
	}
	liveManager := skills.NewManager(skills.Options{Dirs: []string{oldDir}, PackDirs: []string{packDir}})
	if err := liveManager.Reload(); err != nil {
		t.Fatal(err)
	}
	a := &agent.Agent{}
	a.SetConfig(cfg)
	a.SetSkills(liveManager)
	boundHandle, err := liveManager.ForProject(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s := New(Options{
		Config: cfg,
		Agent:  a,
		Skills: liveManager,
		Reload: func() error {
			reloaded, err := config.Reload()
			if err != nil {
				return err
			}
			if err := liveManager.Reconfigure(skills.Options{Dirs: []string{newDir}, PackDirs: []string{packDir}}); err != nil {
				return err
			}
			a.SetConfig(reloaded)
			return nil
		},
	})

	if rr := postSkillToggle(s, "replace-me", false); rr.Code != http.StatusOK {
		t.Fatalf("toggle status = %d, want 200 (body=%s)", rr.Code, rr.Body.String())
	}
	if s.currentSkills() != liveManager || a.Skills() != liveManager {
		t.Fatal("in-place reload detached the shared skill manager")
	}
	if s.skills != liveManager {
		t.Fatal("applyReload replaced the fallback manager")
	}
	if s.commandDeps().Skills != liveManager {
		t.Fatal("command dependencies detached from the shared skill manager")
	}
	if sk, ok := liveManager.Get("replace-me"); !ok || sk.Enabled {
		t.Fatalf("reconfigured manager preference = %#v, found=%v; want disabled", sk, ok)
	}
	if boundHandle == liveManager {
		t.Fatal("project scope did not return a bound handle")
	}
	if sk, ok := boundHandle.Get("replace-me"); !ok || sk.Enabled {
		t.Fatalf("bound handle preference = %#v, found=%v; want disabled", sk, ok)
	}

	getReq := httptest.NewRequest(http.MethodGet, "/api/skills/replacement-only", nil)
	getReq.SetPathValue("name", "replacement-only")
	getRR := httptest.NewRecorder()
	s.handleGetSkill(getRR, getReq)
	if getRR.Code != http.StatusOK {
		t.Fatalf("replacement get status = %d, want 200 (body=%s)", getRR.Code, getRR.Body.String())
	}

	listRR := httptest.NewRecorder()
	s.handleListSkills(listRR, httptest.NewRequest(http.MethodGet, "/api/skills", nil))
	if !strings.Contains(listRR.Body.String(), "replacement-only") || strings.Contains(listRR.Body.String(), "old-only") {
		t.Fatalf("list did not use replacement manager: %s", listRR.Body.String())
	}

	hubRR := httptest.NewRecorder()
	s.handleHubSkills(hubRR, httptest.NewRequest(http.MethodGet, "/api/hub/skills", nil))
	var hubBody struct {
		Skills []struct {
			Name      string `json:"name"`
			Installed bool   `json:"installed"`
		} `json:"skills"`
	}
	if err := json.Unmarshal(hubRR.Body.Bytes(), &hubBody); err != nil {
		t.Fatalf("decode hub response: %v (body=%s)", err, hubRR.Body.String())
	}
	hubInstalled := false
	for _, entry := range hubBody.Skills {
		if entry.Name == "code-review" {
			hubInstalled = entry.Installed
			break
		}
	}
	if !hubInstalled {
		t.Fatalf("hub did not mark replacement-manager skill installed: %s", hubRR.Body.String())
	}

	saveRR := httptest.NewRecorder()
	s.handleSaveSkill(saveRR, httptest.NewRequest(http.MethodPost, "/api/skills", strings.NewReader(`{"name":"saved-on-replacement","description":"new","body":"body"}`)))
	if saveRR.Code != http.StatusOK {
		t.Fatalf("replacement save status = %d, want 200 (body=%s)", saveRR.Code, saveRR.Body.String())
	}
	if _, err := os.Stat(filepath.Join(newDir, "saved-on-replacement.md")); err != nil {
		t.Fatalf("replacement manager did not receive save: %v", err)
	}
	if _, err := os.Stat(filepath.Join(oldDir, "saved-on-replacement.md")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stale manager received save: %v", err)
	}

	libraryRR := httptest.NewRecorder()
	s.handleSkillLibrary(libraryRR, httptest.NewRequest(http.MethodGet, "/api/skills/library", nil))
	if !strings.Contains(libraryRR.Body.String(), "code-review") || strings.Contains(libraryRR.Body.String(), "old-only") {
		t.Fatalf("library did not use replacement manager: %s", libraryRR.Body.String())
	}

	deleteReq := httptest.NewRequest(http.MethodDelete, "/api/skills/replacement-only", nil)
	deleteReq.SetPathValue("name", "replacement-only")
	deleteRR := httptest.NewRecorder()
	s.handleDeleteSkill(deleteRR, deleteReq)
	if deleteRR.Code != http.StatusOK {
		t.Fatalf("replacement delete status = %d, want 200 (body=%s)", deleteRR.Code, deleteRR.Body.String())
	}
	if _, err := os.Stat(filepath.Join(newDir, "replacement-only.md")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("replacement manager did not delete its source: %v", err)
	}
}

func newSkillToggleServer(t *testing.T, names, disabled []string) (*Server, *skills.Manager, string, map[string]string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("ANTARES_HOME", home)
	t.Setenv("ANTARES_PROFILE", "default")
	cfgPath := filepath.Join(home, "config.yaml")
	t.Setenv("ANTARES_CONFIG", cfgPath)
	dir := filepath.Join(home, "skills")
	sources := make(map[string]string, len(names))
	for _, name := range names {
		sources[name] = writeToggleServerSkill(t, dir, name)
	}
	cfg := config.Default()
	cfg.Skills.Enabled = true
	cfg.Skills.Dirs = []string{dir}
	cfg.Skills.Disabled = append([]string(nil), disabled...)
	cfg.Skills.FrontmatterMigrated = true
	if err := config.SaveAt(cfgPath, cfg); err != nil {
		t.Fatalf("seed config: %v", err)
	}
	manager := skills.NewManager(skills.Options{Dirs: []string{dir}})
	if err := manager.Reload(); err != nil {
		t.Fatalf("load skills: %v", err)
	}
	return New(Options{Config: cfg, Skills: manager}), manager, cfgPath, sources
}

func writeToggleServerSkill(t *testing.T, dir, name string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name+".md")
	body := "---\nname: " + name + "\ndescription: " + name + " description\n---\n\n" + name + " body\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	fixed := time.Unix(1_700_000_000, 0)
	if err := os.Chtimes(path, fixed, fixed); err != nil {
		t.Fatal(err)
	}
	return path
}

func postSkillToggle(s *Server, name string, enabled bool) *httptest.ResponseRecorder {
	body := `{"name":"` + name + `","enabled":` + strconv.FormatBool(enabled) + `}`
	rr := httptest.NewRecorder()
	s.handleToggleSkill(rr, httptest.NewRequest(http.MethodPost, "/api/skills/toggle", strings.NewReader(body)))
	return rr
}

type skillSourceSnapshot struct {
	body    []byte
	mode    os.FileMode
	size    int64
	modTime time.Time
}

func snapshotSkillSource(t *testing.T, path string) skillSourceSnapshot {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return skillSourceSnapshot{body: body, mode: info.Mode(), size: info.Size(), modTime: info.ModTime()}
}

func assertSkillSourceUnchanged(t *testing.T, path string, before skillSourceSnapshot) {
	t.Helper()
	after := snapshotSkillSource(t, path)
	if !bytes.Equal(after.body, before.body) {
		t.Fatalf("skill source bytes changed during config toggle:\nbefore: %q\nafter:  %q", before.body, after.body)
	}
	if after.mode != before.mode || after.size != before.size || !after.modTime.Equal(before.modTime) {
		t.Fatalf("skill source stat changed: before={mode:%v size:%d mtime:%s} after={mode:%v size:%d mtime:%s}",
			before.mode, before.size, before.modTime, after.mode, after.size, after.modTime)
	}
}
