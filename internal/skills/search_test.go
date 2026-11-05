package skills

import (
	"os"
	"path/filepath"
	"testing"
)

func writeSkill(t *testing.T, dir, name, frontmatter string) {
	t.Helper()
	content := "---\nname: " + name + "\n" + frontmatter + "---\n# " + name + "\nbody text here for the skill\n"
	if err := os.WriteFile(filepath.Join(dir, name+".md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func loadManager(t *testing.T) *Manager {
	t.Helper()
	dir := t.TempDir()
	writeSkill(t, dir, "attack-sqli", "description: \"SQL injection\"\ncategory: web-application\ntags: [sqli, database]\ntech_stack: [web]\ncwe_ids: [CWE-89]\nchains_with: [attack-idor]\n")
	writeSkill(t, dir, "attack-idor", "description: \"IDOR\"\ncategory: web-application\ntags: [idor, authz]\ntech_stack: [web, api]\ncwe_ids: [CWE-639]\n")
	writeSkill(t, dir, "attack-jwt", "description: \"JWT attacks and token forgery\"\ncategory: web-application\ntags: [jwt, auth]\ntech_stack: [web]\ncwe_ids: [CWE-287, CWE-345]\n")
	m := NewManager(Options{Dirs: []string{dir}})
	if err := m.Reload(); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestSearchByCWEMetadata(t *testing.T) {
	m := loadManager(t)
	// A CWE id that appears nowhere in the prose must still find the skill.
	hits := m.Search("CWE-89", 10)
	if len(hits) == 0 || hits[0].Name != "attack-sqli" {
		t.Fatalf("CWE-89 should surface attack-sqli first, got %+v", names(hits))
	}
}

func TestSearchFilterByTech(t *testing.T) {
	m := loadManager(t)
	// tech=api should exclude the web-only skills.
	hits := m.SearchFiltered("", Filter{Tech: "api"}, 10)
	if len(hits) != 1 || hits[0].Name != "attack-idor" {
		t.Fatalf("tech=api should return only attack-idor, got %+v", names(hits))
	}
}

func TestSearchFilterByCWE(t *testing.T) {
	m := loadManager(t)
	hits := m.SearchFiltered("", Filter{CWE: "287"}, 10) // bare id, no prefix
	if len(hits) != 1 || hits[0].Name != "attack-jwt" {
		t.Fatalf("CWE 287 filter should return attack-jwt, got %+v", names(hits))
	}
}

func TestSearchRanksNameOverDescription(t *testing.T) {
	m := loadManager(t)
	// "jwt" is in one name and one description-ish spot; the name hit must win.
	hits := m.Search("jwt", 10)
	if len(hits) == 0 || hits[0].Name != "attack-jwt" {
		t.Fatalf("name match should rank first, got %+v", names(hits))
	}
}

func TestChainsResolveExistingSkills(t *testing.T) {
	m := loadManager(t)
	chains := m.Chains("attack-sqli")
	if len(chains) != 1 || chains[0].Name != "attack-idor" {
		t.Fatalf("attack-sqli should chain to attack-idor, got %+v", names(chains))
	}
	// A skill with no chains returns nothing, not an error.
	if got := m.Chains("attack-jwt"); len(got) != 0 {
		t.Fatalf("attack-jwt has no chains, got %+v", names(got))
	}
}

func names(list []Skill) []string {
	out := make([]string, len(list))
	for i, s := range list {
		out[i] = s.Name
	}
	return out
}
