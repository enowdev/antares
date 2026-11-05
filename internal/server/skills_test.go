package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/enowdev/antares/internal/agent"
	"github.com/enowdev/antares/internal/config"
	"github.com/enowdev/antares/internal/skills"
)

func writeServerSkill(t *testing.T, dir, name, category, body string) {
	t.Helper()
	content := fmt.Sprintf("---\nname: %s\ndescription: %s description\nenabled: true\ncategory: %s\n---\n%s\n", name, name, category, body)
	if err := os.WriteFile(filepath.Join(dir, name+".md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func decodeServerJSON(t *testing.T, rr *httptest.ResponseRecorder, dst any) {
	t.Helper()
	if err := json.NewDecoder(rr.Body).Decode(dst); err != nil {
		t.Fatalf("decode response status %d: %v; body=%q", rr.Code, err, rr.Body.String())
	}
}

func TestSkillHTTPReadOnly(t *testing.T) {
	home := t.TempDir()
	state := t.TempDir()
	t.Setenv("ANTARES_HOME", state)
	t.Setenv("ANTARES_PROFILE", "default")
	t.Setenv("ANTARES_CONFIG", filepath.Join(state, "config.yaml"))
	configured := t.TempDir()
	borrowedDir := filepath.Join(home, ".agent", "skills", "borrowed")
	if err := os.MkdirAll(borrowedDir, 0o755); err != nil {
		t.Fatal(err)
	}
	borrowedPath := filepath.Join(borrowedDir, "SKILL.md")
	borrowedBytes := []byte("---\nname: borrowed\ndescription: Borrowed description\nenabled: true\n---\nBORROWED_BODY\n")
	if err := os.WriteFile(borrowedPath, borrowedBytes, 0o644); err != nil {
		t.Fatal(err)
	}
	borrowedBefore := snapshotSkillSource(t, borrowedPath)
	mgr := skills.NewManager(skills.Options{Dirs: []string{configured}, UserHome: home})
	if err := mgr.Reload(); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Skills.Enabled = true
	cfg.Skills.Dirs = []string{configured}
	cfg.Skills.FrontmatterMigrated = true
	if err := config.SaveAt(config.ConfigFile(), cfg); err != nil {
		t.Fatal(err)
	}
	s := New(Options{Config: cfg, Skills: mgr})

	list := httptest.NewRecorder()
	s.handleListSkills(list, httptest.NewRequest(http.MethodGet, "/api/skills", nil))
	if list.Code != http.StatusOK {
		t.Fatalf("list status = %d, want 200; body=%q", list.Code, list.Body.String())
	}
	var listed struct {
		Skills []skills.Skill `json:"skills"`
	}
	decodeServerJSON(t, list, &listed)
	if len(listed.Skills) != 1 || listed.Skills[0].Name != "borrowed" || !listed.Skills[0].ReadOnly {
		t.Fatalf("list did not expose borrowed skill as read-only: %+v", listed.Skills)
	}

	getSkill := func(name string) (*httptest.ResponseRecorder, struct {
		Skill skills.Skill `json:"skill"`
		Body  string       `json:"body"`
	}) {
		t.Helper()
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/skills/"+name, nil)
		req.SetPathValue("name", name)
		s.handleGetSkill(rr, req)
		var response struct {
			Skill skills.Skill `json:"skill"`
			Body  string       `json:"body"`
		}
		if rr.Code == http.StatusOK {
			decodeServerJSON(t, rr, &response)
		}
		return rr, response
	}

	get, fetched := getSkill("borrowed")
	if get.Code != http.StatusOK {
		t.Fatalf("get borrowed status = %d, want 200; body=%q", get.Code, get.Body.String())
	}
	if fetched.Skill.Name != "borrowed" || !fetched.Skill.ReadOnly || fetched.Body != "BORROWED_BODY" {
		t.Fatalf("get did not return borrowed body and read-only metadata: %+v body=%q", fetched.Skill, fetched.Body)
	}

	readonlyRequests := []struct {
		name string
		run  func(*httptest.ResponseRecorder)
	}{
		{
			name: "save",
			run: func(rr *httptest.ResponseRecorder) {
				req := httptest.NewRequest(http.MethodPost, "/api/skills", strings.NewReader(`{"name":"borrowed","description":"changed","body":"CHANGED","tags":[]}`))
				s.handleSaveSkill(rr, req)
			},
		},
		{
			name: "delete",
			run: func(rr *httptest.ResponseRecorder) {
				req := httptest.NewRequest(http.MethodDelete, "/api/skills/borrowed", nil)
				req.SetPathValue("name", "borrowed")
				s.handleDeleteSkill(rr, req)
			},
		},
	}
	for _, mutation := range readonlyRequests {
		rr := httptest.NewRecorder()
		mutation.run(rr)
		if rr.Code != http.StatusForbidden {
			t.Fatalf("%s borrowed status = %d, want 403; body=%q", mutation.name, rr.Code, rr.Body.String())
		}
	}

	toggle := httptest.NewRecorder()
	s.handleToggleSkill(toggle, httptest.NewRequest(http.MethodPost, "/api/skills/toggle", strings.NewReader(`{"name":"borrowed","enabled":false}`)))
	if toggle.Code != http.StatusOK {
		t.Fatalf("toggle borrowed status = %d, want 200; body=%q", toggle.Code, toggle.Body.String())
	}
	toggled, toggledBody := getSkill("borrowed")
	if toggled.Code != http.StatusOK || toggledBody.Skill.Enabled || !toggledBody.Skill.ReadOnly {
		t.Fatalf("toggle borrowed did not update effective state: status=%d skill=%+v", toggled.Code, toggledBody.Skill)
	}
	persisted, err := config.Reload()
	if err != nil {
		t.Fatal(err)
	}
	if len(persisted.Skills.Disabled) != 1 || persisted.Skills.Disabled[0] != "borrowed" {
		t.Fatalf("persisted disabled = %#v, want [borrowed]", persisted.Skills.Disabled)
	}
	assertSkillSourceUnchanged(t, borrowedPath, borrowedBefore)
	configuredEntries, err := os.ReadDir(configured)
	if err != nil {
		t.Fatal(err)
	}
	if len(configuredEntries) != 0 {
		t.Fatalf("read-only save created a configured shadow: %+v", configuredEntries)
	}

	save := httptest.NewRecorder()
	s.handleSaveSkill(save, httptest.NewRequest(http.MethodPost, "/api/skills", strings.NewReader(`{"name":"writable","description":"Writable description","body":"WRITABLE_BODY","tags":["local"]}`)))
	if save.Code != http.StatusOK {
		t.Fatalf("create writable status = %d, want 200; body=%q", save.Code, save.Body.String())
	}
	created, createdBody := getSkill("writable")
	if created.Code != http.StatusOK || createdBody.Skill.ReadOnly || createdBody.Body != "WRITABLE_BODY" {
		t.Fatalf("created writable skill is not editable: status=%d skill=%+v body=%q", created.Code, createdBody.Skill, createdBody.Body)
	}

	edit := httptest.NewRecorder()
	s.handleSaveSkill(edit, httptest.NewRequest(http.MethodPost, "/api/skills", strings.NewReader(`{"name":"writable","description":"Edited description","body":"EDITED_BODY","tags":[]}`)))
	if edit.Code != http.StatusOK {
		t.Fatalf("edit writable status = %d, want 200; body=%q", edit.Code, edit.Body.String())
	}
	edited, editedBody := getSkill("writable")
	if edited.Code != http.StatusOK || editedBody.Body != "EDITED_BODY" {
		t.Fatalf("edit writable did not persist: status=%d body=%q", edited.Code, editedBody.Body)
	}

	writableToggle := httptest.NewRecorder()
	s.handleToggleSkill(writableToggle, httptest.NewRequest(http.MethodPost, "/api/skills/toggle", strings.NewReader(`{"name":"writable","enabled":false}`)))
	if writableToggle.Code != http.StatusOK {
		t.Fatalf("toggle writable status = %d, want 200; body=%q", writableToggle.Code, writableToggle.Body.String())
	}
	writableToggled, toggledBody := getSkill("writable")
	if writableToggled.Code != http.StatusOK || toggledBody.Skill.Enabled {
		t.Fatalf("toggle writable did not persist: status=%d skill=%+v", writableToggled.Code, toggledBody.Skill)
	}

	deleted := httptest.NewRecorder()
	deleteReq := httptest.NewRequest(http.MethodDelete, "/api/skills/writable", nil)
	deleteReq.SetPathValue("name", "writable")
	s.handleDeleteSkill(deleted, deleteReq)
	if deleted.Code != http.StatusOK {
		t.Fatalf("delete writable status = %d, want 200; body=%q", deleted.Code, deleted.Body.String())
	}
	missing, _ := getSkill("writable")
	if missing.Code != http.StatusNotFound {
		t.Fatalf("deleted writable status = %d, want 404; body=%q", missing.Code, missing.Body.String())
	}
}

func TestSkillConsumersSeeInPlaceReconfigure(t *testing.T) {
	t.Setenv("ANTARES_HOME", t.TempDir())

	oldDir, oldPack := t.TempDir(), t.TempDir()
	newDir, newPack := t.TempDir(), t.TempDir()
	fallbackDir := t.TempDir()
	writeServerSkill(t, oldDir, "old-catalog", "everyday", "OLD_BODY")
	writeServerSkill(t, oldPack, "old-library", "old-category", "OLD_LIBRARY_BODY")
	writeServerSkill(t, newDir, "new-catalog", "everyday", "NEW_BODY")
	writeServerSkill(t, newPack, "new-library", "new-category", "NEW_LIBRARY_BODY")
	writeServerSkill(t, fallbackDir, "fallback-only", "everyday", "FALLBACK_BODY")

	live := skills.NewManager(skills.Options{Dirs: []string{oldDir}, PackDirs: []string{oldPack}})
	if err := live.Reload(); err != nil {
		t.Fatal(err)
	}
	fallback := skills.NewManager(skills.Options{Dirs: []string{fallbackDir}})
	if err := fallback.Reload(); err != nil {
		t.Fatal(err)
	}

	cfg := config.Default()
	ag := &agent.Agent{}
	ag.SetConfig(cfg)
	ag.SetSkills(live)
	s := &Server{
		cfg:    cfg,
		agent:  ag,
		skills: fallback,
		reloadFn: func() error {
			return live.Reconfigure(skills.Options{Dirs: []string{newDir}, PackDirs: []string{newPack}})
		},
	}

	if err := s.applyReload(); err != nil {
		t.Fatal(err)
	}

	list := httptest.NewRecorder()
	s.handleListSkills(list, httptest.NewRequest(http.MethodGet, "/api/skills", nil))
	if list.Code != http.StatusOK {
		t.Fatalf("list status = %d, want 200; body=%q", list.Code, list.Body.String())
	}
	var listed struct {
		Skills []skills.Skill `json:"skills"`
	}
	decodeServerJSON(t, list, &listed)
	if len(listed.Skills) != 1 || listed.Skills[0].Name != "new-catalog" {
		t.Fatalf("list did not expose only the reconfigured everyday catalog: %+v", listed.Skills)
	}

	get := httptest.NewRecorder()
	getReq := httptest.NewRequest(http.MethodGet, "/api/skills/new-catalog", nil)
	getReq.SetPathValue("name", "new-catalog")
	s.handleGetSkill(get, getReq)
	if get.Code != http.StatusOK {
		t.Fatalf("get status = %d, want 200; body=%q", get.Code, get.Body.String())
	}
	var fetched struct {
		Skill skills.Skill `json:"skill"`
		Body  string       `json:"body"`
	}
	decodeServerJSON(t, get, &fetched)
	if fetched.Skill.Name != "new-catalog" || !strings.Contains(fetched.Body, "NEW_BODY") {
		t.Fatalf("get returned stale skill: %+v body=%q", fetched.Skill, fetched.Body)
	}

	oldGet := httptest.NewRecorder()
	oldGetReq := httptest.NewRequest(http.MethodGet, "/api/skills/old-catalog", nil)
	oldGetReq.SetPathValue("name", "old-catalog")
	s.handleGetSkill(oldGet, oldGetReq)
	if oldGet.Code != http.StatusNotFound {
		t.Fatalf("removed skill status = %d, want 404; body=%q", oldGet.Code, oldGet.Body.String())
	}

	library := httptest.NewRecorder()
	s.handleSkillLibrary(library, httptest.NewRequest(http.MethodGet, "/api/skills/library", nil))
	if library.Code != http.StatusOK {
		t.Fatalf("library status = %d, want 200; body=%q", library.Code, library.Body.String())
	}
	var libraryBody struct {
		Skills []skills.Skill `json:"skills"`
		Total  int            `json:"total"`
	}
	decodeServerJSON(t, library, &libraryBody)
	if libraryBody.Total != 1 || len(libraryBody.Skills) != 1 || libraryBody.Skills[0].Name != "new-library" {
		t.Fatalf("library did not expose only the reconfigured pack: %+v (total %d)", libraryBody.Skills, libraryBody.Total)
	}

	command := httptest.NewRecorder()
	commandReq := httptest.NewRequest(http.MethodPost, "/api/commands/run", strings.NewReader(`{"input":"/skills","surface":"web"}`))
	s.handleCommandRun(command, commandReq)
	if command.Code != http.StatusOK {
		t.Fatalf("command status = %d, want 200; body=%q", command.Code, command.Body.String())
	}
	var commandBody struct {
		OK     bool   `json:"ok"`
		Output string `json:"output"`
	}
	decodeServerJSON(t, command, &commandBody)
	// With no filter /skills lists the everyday catalogue, as the dashboard
	// does; the bundled library is only searched when a filter is given.
	if !commandBody.OK || !strings.Contains(commandBody.Output, "new-catalog") || strings.Contains(commandBody.Output, "new-library") ||
		strings.Contains(commandBody.Output, "old-catalog") || strings.Contains(commandBody.Output, "old-library") || strings.Contains(commandBody.Output, "fallback-only") {
		t.Fatalf("command did not use the reconfigured live catalog: %+v", commandBody)
	}

	filtered := httptest.NewRecorder()
	s.handleCommandRun(filtered, httptest.NewRequest(http.MethodPost, "/api/commands/run", strings.NewReader(`{"input":"/skills library","surface":"web"}`)))
	var filteredBody struct {
		OK     bool   `json:"ok"`
		Output string `json:"output"`
	}
	decodeServerJSON(t, filtered, &filteredBody)
	if !filteredBody.OK || !strings.Contains(filteredBody.Output, "new-library") || strings.Contains(filteredBody.Output, "old-library") {
		t.Fatalf("filtered command did not search the reconfigured library: %+v", filteredBody)
	}
}
