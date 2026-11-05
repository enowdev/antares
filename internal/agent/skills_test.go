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

func writeSessionSkill(t *testing.T, root, name, description, body string, chains ...string) {
	t.Helper()
	dir := filepath.Join(root, ".agent", "skills", name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	doc := "---\nname: " + name + "\ndescription: " + description + "\nenabled: true\n"
	if len(chains) > 0 {
		doc += "chains_with:\n"
		for _, chain := range chains {
			doc += "  - " + chain + "\n"
		}
	}
	doc += "---\n\n" + body + "\n"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
}

func executeSessionSkill(a *Agent, sess *store.Session, action, name string) (string, error) {
	t, ok := tools.Default().Get("skill")
	if !ok {
		return "", fmt.Errorf("registered skill tool is missing")
	}
	args, err := json.Marshal(map[string]string{"action": action, "name": name})
	if err != nil {
		return "", err
	}
	outcomes := a.executeTools(
		context.Background(),
		[]llm.ToolCall{{ID: "scope-probe-" + sess.ID, Name: "skill", Arguments: string(args)}},
		map[string]tools.Tool{"skill": t},
		Request{Platform: "web"},
		sess,
		func(Event) error { return nil },
	)
	if len(outcomes) != 1 {
		return "", fmt.Errorf("skill tool returned %d outcomes", len(outcomes))
	}
	if outcomes[0].isError {
		return "", fmt.Errorf("skill tool failed: %s", outcomes[0].message.Content)
	}
	return outcomes[0].message.Content, nil
}

func requireScopedText(got string, wants, rejects []string) error {
	for _, want := range wants {
		if !strings.Contains(got, want) {
			return fmt.Errorf("missing %q in:\n%s", want, got)
		}
	}
	for _, reject := range rejects {
		if strings.Contains(got, reject) {
			return fmt.Errorf("unexpected %q in:\n%s", reject, got)
		}
	}
	return nil
}

func TestSkillSessionIsolation(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ANTARES_HOME", t.TempDir())
	startup := t.TempDir()
	projectA := t.TempDir()
	projectB := t.TempDir()

	writeSessionSkill(t, home, "session-shared", "SHARED_DESCRIPTION", "SHARED_BODY")
	writeSessionSkill(t, startup, "session-startup-only", "STARTUP_ONLY_DESCRIPTION", "STARTUP_ONLY_BODY")
	writeSessionSkill(t, startup, "session-collision", "STARTUP_COLLISION_DESCRIPTION", "STARTUP_COLLISION_BODY", "session-startup-follow")
	writeSessionSkill(t, startup, "session-startup-follow", "STARTUP_FOLLOW_DESCRIPTION", "STARTUP_FOLLOW_BODY")
	writeSessionSkill(t, projectA, "session-a-only", "A_ONLY_DESCRIPTION", "A_ONLY_BODY")
	writeSessionSkill(t, projectA, "session-collision", "A_COLLISION_DESCRIPTION", "A_COLLISION_BODY", "session-a-follow")
	writeSessionSkill(t, projectA, "session-a-follow", "A_FOLLOW_DESCRIPTION", "A_FOLLOW_BODY")
	writeSessionSkill(t, projectB, "session-b-only", "B_ONLY_DESCRIPTION", "B_ONLY_BODY")
	writeSessionSkill(t, projectB, "session-collision", "B_COLLISION_DESCRIPTION", "B_COLLISION_BODY", "session-b-follow")
	writeSessionSkill(t, projectB, "session-b-follow", "B_FOLLOW_DESCRIPTION", "B_FOLLOW_BODY")

	manager := skills.NewManager(skills.Options{UserHome: home, ProjectDir: startup})
	if err := manager.Reload(); err != nil {
		t.Fatalf("reload skills: %v", err)
	}

	a, startupSession := errorAgent(t)
	cfg := config.Default()
	cfg.Memory.Enabled = false
	cfg.Skills.Enabled = true
	a.SetConfig(cfg)
	a.SetSkills(manager)
	a.reg = tools.Default()

	ctx := context.Background()
	sessionA := &store.Session{ID: "session-a", Platform: "web", Workspace: projectA, Meta: store.Meta{"project_dir": projectA}}
	sessionB := &store.Session{ID: "session-b", Platform: "web", Workspace: projectB, Meta: store.Meta{"project_dir": projectB}}
	for _, sess := range []*store.Session{sessionA, sessionB} {
		if err := a.db.CreateSession(ctx, sess); err != nil {
			t.Fatalf("create %s: %v", sess.ID, err)
		}
	}

	startupPrompt := a.buildSystemPrompt(ctx, Request{}, startupSession, nil)
	if err := requireScopedText(startupPrompt,
		[]string{"SHARED_DESCRIPTION", "STARTUP_ONLY_DESCRIPTION", "STARTUP_COLLISION_DESCRIPTION"},
		[]string{"A_ONLY_DESCRIPTION", "A_COLLISION_DESCRIPTION", "B_ONLY_DESCRIPTION", "B_COLLISION_DESCRIPTION"},
	); err != nil {
		t.Fatalf("startup prompt: %v", err)
	}

	resumeReq := Request{SessionID: sessionA.ID, Message: "resume without a project_dir request"}
	resumedA, err := a.resolveSession(ctx, &resumeReq)
	if err != nil {
		t.Fatalf("resume A: %v", err)
	}

	promptCases := []struct {
		name    string
		sess    *store.Session
		want    []string
		rejects []string
	}{
		{
			name: "A resumed from persisted metadata", sess: resumedA,
			want:    []string{"SHARED_DESCRIPTION", "A_ONLY_DESCRIPTION", "A_COLLISION_DESCRIPTION"},
			rejects: []string{"STARTUP_ONLY_DESCRIPTION", "STARTUP_COLLISION_DESCRIPTION", "STARTUP_FOLLOW_DESCRIPTION", "B_ONLY_DESCRIPTION", "B_COLLISION_DESCRIPTION", "B_FOLLOW_DESCRIPTION"},
		},
		{
			name: "B", sess: sessionB,
			want:    []string{"SHARED_DESCRIPTION", "B_ONLY_DESCRIPTION", "B_COLLISION_DESCRIPTION"},
			rejects: []string{"STARTUP_ONLY_DESCRIPTION", "STARTUP_COLLISION_DESCRIPTION", "STARTUP_FOLLOW_DESCRIPTION", "A_ONLY_DESCRIPTION", "A_COLLISION_DESCRIPTION", "A_FOLLOW_DESCRIPTION"},
		},
	}
	for _, tc := range promptCases {
		t.Run("prompt "+tc.name, func(t *testing.T) {
			prompt := a.buildSystemPrompt(ctx, Request{}, tc.sess, nil)
			if err := requireScopedText(prompt, tc.want, tc.rejects); err != nil {
				t.Fatal(err)
			}
		})
	}

	toolCases := []struct {
		name             string
		sess             *store.Session
		onlyDescription  string
		collisionDesc    string
		collisionBody    string
		followName       string
		foreignFragments []string
	}{
		{
			name: "A", sess: resumedA, onlyDescription: "A_ONLY_DESCRIPTION",
			collisionDesc: "A_COLLISION_DESCRIPTION", collisionBody: "A_COLLISION_BODY", followName: "session-a-follow",
			foreignFragments: []string{"STARTUP_ONLY_DESCRIPTION", "STARTUP_COLLISION_DESCRIPTION", "STARTUP_COLLISION_BODY", "session-startup-follow", "B_ONLY_DESCRIPTION", "B_COLLISION_DESCRIPTION", "B_COLLISION_BODY", "session-b-follow"},
		},
		{
			name: "B", sess: sessionB, onlyDescription: "B_ONLY_DESCRIPTION",
			collisionDesc: "B_COLLISION_DESCRIPTION", collisionBody: "B_COLLISION_BODY", followName: "session-b-follow",
			foreignFragments: []string{"STARTUP_ONLY_DESCRIPTION", "STARTUP_COLLISION_DESCRIPTION", "STARTUP_COLLISION_BODY", "session-startup-follow", "A_ONLY_DESCRIPTION", "A_COLLISION_DESCRIPTION", "A_COLLISION_BODY", "session-a-follow"},
		},
	}
	for _, tc := range toolCases {
		t.Run("tool "+tc.name, func(t *testing.T) {
			checks := []struct {
				action string
				name   string
				wants  []string
			}{
				{action: "list", wants: []string{"SHARED_DESCRIPTION", tc.onlyDescription, tc.collisionDesc}},
				{action: "search", name: "session-collision", wants: []string{tc.collisionDesc}},
				{action: "read", name: "session-collision", wants: []string{tc.collisionDesc, tc.collisionBody}},
				{action: "read", name: "session-shared", wants: []string{"SHARED_DESCRIPTION", "SHARED_BODY"}},
				{action: "chains", name: "session-collision", wants: []string{tc.followName}},
			}
			for _, check := range checks {
				got, err := executeSessionSkill(a, tc.sess, check.action, check.name)
				if err != nil {
					t.Fatalf("%s: %v", check.action, err)
				}
				if err := requireScopedText(got, check.wants, tc.foreignFragments); err != nil {
					t.Fatalf("%s: %v", check.action, err)
				}
			}
		})
	}

	for i, sess := range []*store.Session{
		nil,
		{Meta: nil},
		{Meta: store.Meta{}},
		{Meta: store.Meta{"project_dir": 42}},
		{Meta: store.Meta{"project_dir": " \t "}},
	} {
		scoped := a.skillsForSession(sess)
		if scoped == nil {
			t.Fatalf("default scope case %d returned nil", i)
		}
		if _, ok := scoped.Get("session-startup-only"); !ok {
			t.Fatalf("default scope case %d did not select startup catalogue", i)
		}
	}

	invalid := &store.Session{Meta: store.Meta{"project_dir": "invalid\x00project"}}
	partial := a.skillsForSession(invalid)
	if partial == nil {
		t.Fatal("invalid project binding returned nil instead of a partial safe view")
	}
	if _, ok := partial.Get("session-shared"); !ok {
		t.Fatal("invalid project binding hid the shared user catalogue")
	}
	if _, ok := partial.Get("session-startup-only"); ok {
		t.Fatal("invalid project binding leaked the startup project catalogue")
	}

	start := make(chan struct{})
	errCh := make(chan error, 32)
	var wg sync.WaitGroup
	for i := 0; i < cap(errCh); i++ {
		sess := resumedA
		bodyWant, bodyReject := "A_COLLISION_BODY", "B_COLLISION_BODY"
		descriptionWant, descriptionReject := "A_COLLISION_DESCRIPTION", "B_COLLISION_DESCRIPTION"
		if i%2 == 1 {
			sess = sessionB
			bodyWant, bodyReject = "B_COLLISION_BODY", "A_COLLISION_BODY"
			descriptionWant, descriptionReject = "B_COLLISION_DESCRIPTION", "A_COLLISION_DESCRIPTION"
		}
		wg.Add(1)
		go func(sess *store.Session, bodyWant, bodyReject, descriptionWant, descriptionReject string) {
			defer wg.Done()
			<-start
			prompt := a.buildSystemPrompt(ctx, Request{}, sess, nil)
			if err := requireScopedText(prompt, []string{descriptionWant}, []string{descriptionReject}); err != nil {
				errCh <- fmt.Errorf("concurrent prompt: %w", err)
				return
			}
			got, err := executeSessionSkill(a, sess, "read", "session-collision")
			if err != nil {
				errCh <- err
				return
			}
			if err := requireScopedText(got, []string{bodyWant}, []string{bodyReject}); err != nil {
				errCh <- fmt.Errorf("concurrent read: %w", err)
			}
		}(sess, bodyWant, bodyReject, descriptionWant, descriptionReject)
	}
	close(start)
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Error(err)
	}
}
