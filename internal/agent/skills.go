package agent

import (
	"log/slog"
	"strings"

	"github.com/enowdev/antares/internal/skills"
	"github.com/enowdev/antares/internal/store"
	"github.com/enowdev/antares/internal/tools"
)

// skillAdapter bridges the skills manager to the narrow interface tools use,
// keeping the tools package free of a dependency on skills.
type skillAdapter struct{ m *skills.Manager }

func (a skillAdapter) List() []tools.SkillInfo {
	items := a.m.List()
	out := make([]tools.SkillInfo, 0, len(items))
	for _, s := range items {
		// Pack skills are not listed — there are thousands. They are reached
		// with Search instead.
		if !s.Enabled || s.Pack {
			continue
		}
		out = append(out, tools.SkillInfo{
			Name: s.Name, Description: s.Description,
			Tags: s.Tags, Triggers: s.Triggers, Enabled: s.Enabled,
		})
	}
	return out
}

func (a skillAdapter) Search(query string, limit int) []tools.SkillInfo {
	return infos(a.m.SearchFiltered(query, skills.Filter{EnabledOnly: true}, limit))
}

func (a skillAdapter) SearchFiltered(query, cwe, tech, category string, limit int) []tools.SkillInfo {
	return infos(a.m.SearchFiltered(query, skills.Filter{CWE: cwe, Tech: tech, Category: category, EnabledOnly: true}, limit))
}

func (a skillAdapter) Chains(name string) []tools.SkillInfo {
	origin, ok := a.m.Get(name)
	if !ok || !origin.Enabled {
		return nil
	}
	items := a.m.Chains(name)
	out := make([]tools.SkillInfo, 0, len(items))
	for _, s := range items {
		if s.Enabled {
			out = append(out, toInfo(s))
		}
	}
	return out
}

func (a skillAdapter) Read(name string) (tools.SkillInfo, string, bool) {
	s, ok := a.m.Get(name)
	if !ok || !s.Enabled {
		return tools.SkillInfo{}, "", false
	}
	return toInfo(*s), s.Body, true
}

// toInfo maps a skills.Skill to the narrow tools.SkillInfo, carrying the
// security metadata through.
func toInfo(s skills.Skill) tools.SkillInfo {
	return tools.SkillInfo{
		Name: s.Name, Description: s.Description,
		Tags: s.Tags, Triggers: s.Triggers, Enabled: s.Enabled,
		CWEIDs: s.CWEIDs, TechStack: s.TechStack, OWASPID: s.OWASPID, ChainsWith: s.ChainsWith,
	}
}

func infos(items []skills.Skill) []tools.SkillInfo {
	out := make([]tools.SkillInfo, 0, len(items))
	for _, s := range items {
		out = append(out, toInfo(s))
	}
	return out
}

func (a skillAdapter) Write(name, description, body string, tags []string) error {
	_, err := a.m.Save(name, description, body, tags)
	return err
}

func (a skillAdapter) MarkUsed(name string) { a.m.MarkUsed(name) }

// skillsForSession snapshots the live manager and binds its catalogue to the
// persisted project selection. Scope failures are nonfatal: ForProject returns
// the partial scope, or a shared-only view when the path cannot be normalized.
func (a *Agent) skillsForSession(sess *store.Session) *skills.Manager {
	m := a.Skills()
	if m == nil {
		return nil
	}

	var projectDir string
	if sess != nil && sess.Meta != nil {
		projectDir, _ = sess.Meta["project_dir"].(string)
	}
	if strings.TrimSpace(projectDir) == "" {
		projectDir = ""
	}
	scoped, err := m.ForProject(projectDir)
	if err != nil {
		slog.Warn("some skills failed to load", "error", err)
	}
	return scoped
}

// skillLibrary exposes the session's scoped manager to tools, or nil when
// skills are off.
func (a *Agent) skillLibrary(sess *store.Session) tools.SkillLibrary {
	if !a.config().Skills.Enabled {
		return nil
	}
	m := a.skillsForSession(sess)
	if m == nil {
		return nil
	}
	return skillAdapter{m: m}
}
