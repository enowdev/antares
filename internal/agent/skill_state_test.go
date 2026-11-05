package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/enowdev/antares/internal/config"
	"github.com/enowdev/antares/internal/llm"
	"github.com/enowdev/antares/internal/skills"
	"github.com/enowdev/antares/internal/store"
	"github.com/enowdev/antares/internal/tools"
)

func skillStateAgent(t *testing.T) (*Agent, *skills.Manager, string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("ANTARES_HOME", home)
	t.Setenv("ANTARES_CONFIG", filepath.Join(home, "config.yaml"))
	t.Setenv("ANTARES_PROFILE", "default")
	cfg := config.Default()
	cfg.Skills.Enabled = true
	cfg.Skills.FrontmatterMigrated = true
	cfg.Memory.Enabled = false
	cfg.Memory.UserProfileEnabled = false
	cfg.RAG.Enabled = false
	cfg.Agent.Workspace = home
	cfg.Tools.ApprovalMode = "auto"
	m := skills.NewManager(skills.Options{Dirs: []string{home}})
	a := agentWithConfig(cfg)
	a.SetSkills(m)
	return a, m, home
}

func agentSkillSource(t *testing.T, dir, name, description, extra, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name+".md"), []byte("---\nname: "+name+"\ndescription: "+description+"\n"+extra+"---\n"+body+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func runStateSkill(t *testing.T, a *Agent, args map[string]any) (string, bool) {
	t.Helper()
	if len(args) <= 2 {
		body, err := executeSessionSkill(a, &store.Session{ID: "state-session", Workspace: a.Config().Agent.Workspace}, args["action"].(string), fmt.Sprint(args["name"]))
		if err != nil {
			return err.Error(), true
		}
		return body, false
	}
	tool, ok := tools.Default().Get("skill")
	if !ok {
		t.Fatal("skill tool unavailable")
	}
	raw, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	out := a.executeTools(context.Background(), []llm.ToolCall{{ID: "state", Name: "skill", Arguments: string(raw)}}, map[string]tools.Tool{"skill": tool}, Request{Platform: "web"}, &store.Session{ID: "state-session", Workspace: a.Config().Agent.Workspace}, func(Event) error { return nil })
	if len(out) != 1 {
		t.Fatalf("tool outcomes = %d", len(out))
	}
	return out[0].message.Content, out[0].isError
}

func TestDisabledSkillReadAndUsage(t *testing.T) {
	a, m, dir := skillStateAgent(t)
	agentSkillSource(t, dir, "secret", "SECRET_DESCRIPTION", "", "SECRET_BODY")
	if err := m.Reload(); err != nil {
		t.Fatal(err)
	}
	cfg := a.Config().Clone()
	cfg.Skills.Disabled = []string{"secret"}
	a.SetConfig(cfg)
	body, isError := runStateSkill(t, a, map[string]any{"action": "read", "name": "secret"})
	if !isError || strings.Contains(body, "SECRET_BODY") || !strings.Contains(body, "not found") {
		t.Fatalf("disabled read leaked or succeeded: error=%v body=%q", isError, body)
	}
	if s, _ := m.Get("secret"); s.UsageCount != 0 {
		t.Fatalf("disabled read incremented usage to %d", s.UsageCount)
	}
	cfg = cfg.Clone()
	cfg.Skills.Disabled = nil
	a.SetConfig(cfg)
	body, isError = runStateSkill(t, a, map[string]any{"action": "read", "name": "secret"})
	if isError || !strings.Contains(body, "SECRET_BODY") {
		t.Fatalf("re-enabled read failed: error=%v body=%q", isError, body)
	}
	if s, _ := m.Get("secret"); s.UsageCount != 1 {
		t.Fatalf("enabled read usage = %d", s.UsageCount)
	}
}

func TestDisabledSkillSearchBeforeLimit(t *testing.T) {
	a, m, dir := skillStateAgent(t)
	cfg := a.Config().Clone()
	for i := range 35 {
		name := fmt.Sprintf("needle-%02d", i)
		agentSkillSource(t, dir, name, "DISABLED_DESCRIPTION", "tech_stack: [web]\ncwe_ids: [CWE-89]\n", "DISABLED_BODY")
		cfg.Skills.Disabled = append(cfg.Skills.Disabled, name)
	}
	agentSkillSource(t, dir, "available", "needle ENABLED_DESCRIPTION", "tech_stack: [web]\ncwe_ids: [CWE-89]\n", "ENABLED_BODY")
	if err := m.Reload(); err != nil {
		t.Fatal(err)
	}
	a.SetConfig(cfg)
	for _, args := range []map[string]any{{"action": "search", "name": "needle"}, {"action": "search", "name": "needle", "tech": "web", "cwe": "89"}, {"action": "list"}} {
		body, isError := runStateSkill(t, a, args)
		if isError || strings.Contains(body, "DISABLED_DESCRIPTION") || !strings.Contains(body, "ENABLED_DESCRIPTION") {
			t.Fatalf("disabled search/list displaced enabled match: args=%v error=%v body=%q", args, isError, body)
		}
	}
	handle := a.skillLibrary(nil)
	hits := handle.Search("needle", 30)
	if len(hits) != 1 || hits[0].Name != "available" {
		t.Fatalf("adapter search failed enabled-only limit: %+v", hits)
	}
	if hits := m.Search("needle", 100); len(hits) != 36 {
		t.Fatalf("administrative search omitted disabled entries: %d", len(hits))
	}
	if prompt := m.PromptBlock(0); strings.Contains(prompt, "DISABLED_DESCRIPTION") || !strings.Contains(prompt, "ENABLED_DESCRIPTION") {
		t.Fatalf("prompt exposed disabled names: %s", prompt)
	}
}

func TestDisabledSkillChainsAndPackAccess(t *testing.T) {
	a, m, dir := skillStateAgent(t)
	packDir := t.TempDir()
	m = skills.NewManager(skills.Options{Dirs: []string{dir}, PackDirs: []string{packDir}})
	a.SetSkills(m)
	agentSkillSource(t, dir, "origin", "ORIGIN_DESCRIPTION", "chains_with: [off, on, pack]\n", "ORIGIN_BODY")
	agentSkillSource(t, dir, "off", "OFF_DESCRIPTION", "", "OFF_BODY")
	agentSkillSource(t, dir, "on", "ON_DESCRIPTION", "", "ON_BODY")
	agentSkillSource(t, packDir, "pack", "PACK_DESCRIPTION", "", "PACK_BODY")
	if err := m.Reload(); err != nil {
		t.Fatal(err)
	}
	cfg := a.Config().Clone()
	cfg.Skills.Disabled = []string{"off"}
	a.SetConfig(cfg)
	body, isError := runStateSkill(t, a, map[string]any{"action": "chains", "name": "origin"})
	if isError || strings.Contains(body, "OFF_DESCRIPTION") || !strings.Contains(body, "ON_DESCRIPTION") || !strings.Contains(body, "PACK_DESCRIPTION") {
		t.Fatalf("chain target filtering: error=%v body=%q", isError, body)
	}
	cfg = cfg.Clone()
	cfg.Skills.Disabled = append(cfg.Skills.Disabled, "origin")
	a.SetConfig(cfg)
	body, _ = runStateSkill(t, a, map[string]any{"action": "chains", "name": "origin"})
	if strings.Contains(body, "ON_DESCRIPTION") || strings.Contains(body, "PACK_DESCRIPTION") {
		t.Fatalf("disabled origin exposed targets: %q", body)
	}
	if len(m.Chains("origin")) != 3 {
		t.Fatal("administrative chain targets were filtered")
	}
	for _, action := range []string{"read", "search"} {
		body, isError = runStateSkill(t, a, map[string]any{"action": action, "name": "pack"})
		if isError || !strings.Contains(body, "PACK_") {
			t.Fatalf("enabled pack %s failed: %q", action, body)
		}
	}
	body, _ = runStateSkill(t, a, map[string]any{"action": "list"})
	if strings.Contains(body, "PACK_DESCRIPTION") {
		t.Fatalf("pack leaked into everyday list: %q", body)
	}
}

func TestSkillHandleObservesConfigAndReplacement(t *testing.T) {
	a, m, dir := skillStateAgent(t)
	agentSkillSource(t, dir, "same", "DESCRIPTION", "", "ORIGINAL_BODY")
	if err := m.Reload(); err != nil {
		t.Fatal(err)
	}
	handle := a.skillLibrary(nil)
	cfg := a.Config().Clone()
	cfg.Skills.Disabled = []string{"same"}
	a.SetConfig(cfg)
	if _, body, ok := handle.Read("same"); ok || body != "" {
		t.Fatalf("retained handle missed config: ok=%v body=%q", ok, body)
	}
	cfg = cfg.Clone()
	cfg.Skills.Disabled = nil
	a.SetConfig(cfg)
	if _, body, ok := handle.Read("same"); !ok || body != "ORIGINAL_BODY" {
		t.Fatalf("retained handle did not re-enable: ok=%v body=%q", ok, body)
	}
	replacementDir := t.TempDir()
	agentSkillSource(t, replacementDir, "same", "DESCRIPTION", "", "REPLACEMENT_BODY")
	replacement := skills.NewManager(skills.Options{Dirs: []string{replacementDir}})
	if err := replacement.Reload(); err != nil {
		t.Fatal(err)
	}
	a.SetSkills(replacement)
	body, isError := runStateSkill(t, a, map[string]any{"action": "read", "name": "same"})
	if isError || !strings.Contains(body, "REPLACEMENT_BODY") {
		t.Fatalf("new tool retained retired manager: %q", body)
	}
	cfg = cfg.Clone()
	cfg.Skills.Disabled = []string{"same"}
	a.SetConfig(cfg)
	body, isError = runStateSkill(t, a, map[string]any{"action": "save", "name": "same", "description": "edited", "body": "EDITED_BODY with enough procedure details to be accepted by the real tool."})
	if isError {
		t.Fatalf("content editing disabled skill failed: %q", body)
	}
	if s, _ := replacement.Get("same"); s.Enabled || !strings.Contains(s.Body, "EDITED_BODY") {
		t.Fatalf("save reset preference or lost edit: %+v", s)
	}
}

func TestConcurrentSkillReadsAndConfigPublication(t *testing.T) {
	a, m, dir := skillStateAgent(t)
	agentSkillSource(t, dir, "same", "DESCRIPTION", "", "BODY")
	if err := m.Reload(); err != nil {
		t.Fatal(err)
	}
	handle := a.skillLibrary(nil)
	on := a.Config().Clone()
	off := on.Clone()
	off.Skills.Disabled = []string{"same"}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for range 200 {
			a.SetConfig(off)
			a.SetConfig(on)
		}
	}()
	go func() {
		defer wg.Done()
		for range 200 {
			handle.List()
			handle.Read("same")
			handle.Search("same", 1)
			handle.Chains("same")
		}
	}()
	wg.Wait()
	a.SetConfig(off)
	if _, _, ok := handle.Read("same"); ok {
		t.Fatal("final disabled publication invisible to retained handle")
	}
}

func TestScopedSkillHandlesObserveDisabledReconfigure(t *testing.T) {
	a, _, configured := skillStateAgent(t)
	projectA, projectB := t.TempDir(), t.TempDir()
	writeSessionSkill(t, projectA, "collision", "A_DESCRIPTION", "A_BODY")
	writeSessionSkill(t, projectB, "collision", "B_DESCRIPTION", "B_BODY")
	m := skills.NewManager(skills.Options{Dirs: []string{configured}})
	if err := m.Reload(); err != nil {
		t.Fatal(err)
	}
	a.SetSkills(m)
	sessions := []*store.Session{
		{ID: "a", Workspace: projectA, Meta: store.Meta{"project_dir": projectA}},
		{ID: "b", Workspace: projectB, Meta: store.Meta{"project_dir": projectB}},
	}
	handles := []tools.SkillLibrary{a.skillLibrary(sessions[0]), a.skillLibrary(sessions[1])}
	cfg := a.Config().Clone()
	cfg.Skills.Disabled = []string{"collision"}
	a.SetConfig(cfg)
	if err := m.Reconfigure(skills.Options{Dirs: []string{configured}}); err != nil {
		t.Fatal(err)
	}
	for i, sess := range sessions {
		if _, body, ok := handles[i].Read("collision"); ok || body != "" {
			t.Fatalf("bound handle leaked disabled body: %q", body)
		}
		if body, err := executeSessionSkill(a, sess, "read", "collision"); err == nil || strings.Contains(body, "_BODY") {
			t.Fatalf("scoped disabled read: %q %v", body, err)
		}
	}
	cfg = cfg.Clone()
	cfg.Skills.Disabled = nil
	a.SetConfig(cfg)
	for i, sess := range sessions {
		want := []string{"A_BODY", "B_BODY"}[i]
		if _, body, ok := handles[i].Read("collision"); !ok || body != want {
			t.Fatalf("retained scoped handle = %q want %q", body, want)
		}
		if body, err := executeSessionSkill(a, sess, "read", "collision"); err != nil || !strings.Contains(body, want) {
			t.Fatalf("scoped enabled read: %q %v", body, err)
		}
	}
}
