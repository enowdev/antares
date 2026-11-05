// Package skills manages the agent's learned procedures: Markdown files with
// YAML front matter that are injected into the prompt when relevant.
package skills

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Skill is one learned procedure loaded from disk.
type Skill struct {
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Path        string    `json:"path"`
	Enabled     bool      `json:"enabled"`
	Source      string    `json:"source"`
	Tags        []string  `json:"tags"`
	Triggers    []string  `json:"triggers"`
	Category    string    `json:"category,omitempty"`
	Body        string    `json:"-"`
	UpdatedAt   time.Time `json:"updated_at"`
	UsageCount  int       `json:"usage_count"`
	// Security-library metadata, used for filtered search and chaining. Empty
	// for everyday skills.
	TechStack  []string `json:"tech_stack,omitempty"`
	CWEIDs     []string `json:"cwe_ids,omitempty"`
	OWASPID    string   `json:"owasp_id,omitempty"`
	ChainsWith []string `json:"chains_with,omitempty"`
	// Pack marks a skill from the bundled security library: searchable and
	// loadable, but kept out of the prompt catalogue so thousands of them do
	// not bury the conversation.
	Pack bool `json:"pack,omitempty"`
	// ReadOnly marks automatically discovered user/project skills. Pack roots
	// preserve their existing management behavior.
	ReadOnly       bool `json:"read_only"`
	legacyDisabled bool
}

// frontMatter is the YAML header of a skill file.
type frontMatter struct {
	Name        string   `yaml:"name"`
	Description string   `yaml:"description"`
	Enabled     *bool    `yaml:"enabled,omitempty"`
	Source      string   `yaml:"source"`
	Category    string   `yaml:"category"`
	Tags        []string `yaml:"tags"`
	Triggers    []string `yaml:"triggers"`
	TechStack   []string `yaml:"tech_stack"`
	CWEIDs      []string `yaml:"cwe_ids"`
	OWASPID     string   `yaml:"owasp_id"`
	ChainsWith  []string `yaml:"chains_with"`
}

// Options describes the independent skill sources. Configured and pack
// directories retain existing mutation behavior; conventional roots are read-only.
type Options struct {
	Dirs       []string
	PackDirs   []string
	UserHome   string
	ProjectDir string
}

// ErrReadOnly is returned when a mutation targets an imported skill.
var ErrReadOnly = errors.New("automatically discovered skills are read-only")

// parseFile reads one skill file, tolerating a missing front matter block. Its
// result is source-neutral: the scanner attaches logical path, fallback name,
// modification time, and provenance for each occurrence.
func parseFile(path string) (*Skill, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	text := strings.ReplaceAll(string(raw), "\r\n", "\n")

	s := &Skill{Enabled: true}
	body := text
	if strings.HasPrefix(text, "---\n") {
		if end := strings.Index(text[4:], "\n---"); end >= 0 {
			header := text[4 : 4+end]
			body = strings.TrimPrefix(text[4+end+4:], "\n")

			var fm frontMatter
			if err := yaml.Unmarshal([]byte(header), &fm); err != nil {
				return nil, fmt.Errorf("%s: invalid front matter: %w", path, err)
			}
			s.Name = fm.Name
			s.Description = fm.Description
			s.Tags, s.Triggers = fm.Tags, fm.Triggers
			s.Category = fm.Category
			s.TechStack, s.CWEIDs, s.ChainsWith = fm.TechStack, fm.CWEIDs, fm.ChainsWith
			s.OWASPID = fm.OWASPID
			s.Source = fm.Source
			if fm.Enabled != nil && !*fm.Enabled {
				s.legacyDisabled = true
			}
		}
	}
	s.Body = strings.TrimSpace(body)

	if s.Description == "" {
		// Fall back to the first non-heading line so the list is never blank.
		for _, line := range strings.Split(s.Body, "\n") {
			line = strings.TrimSpace(line)
			if line != "" && !strings.HasPrefix(line, "#") {
				s.Description = truncate(line, 160)
				break
			}
		}
	}
	return s, nil
}

// Everyday returns the skills for the everyday catalogue — everything except
// the bundled library, which is thousands strong and reached by search.
func (m *Manager) Everyday() []Skill {
	out := m.List()
	kept := make([]Skill, 0, len(out))
	for _, s := range out {
		if !s.Pack {
			kept = append(kept, s)
		}
	}
	return kept
}

// List returns all effective skills for this scope, sorted by name.
func (m *Manager) List() []Skill {
	if m == nil {
		return nil
	}
	m.state.mu.RLock()
	defer m.state.mu.RUnlock()
	out := m.effectiveListLocked()
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Get returns one effective skill by name.
func (m *Manager) Get(name string) (*Skill, bool) {
	if m == nil {
		return nil, false
	}
	m.state.mu.RLock()
	defer m.state.mu.RUnlock()
	skill, ok := m.effectiveSkillLocked(name)
	if !ok {
		return nil, false
	}
	clone := m.cloneEffectiveSkillLocked(skill)
	return &clone, true
}

func cloneSkill(s *Skill) Skill {
	clone := *s
	clone.Tags = append([]string(nil), s.Tags...)
	clone.Triggers = append([]string(nil), s.Triggers...)
	clone.TechStack = append([]string(nil), s.TechStack...)
	clone.CWEIDs = append([]string(nil), s.CWEIDs...)
	clone.ChainsWith = append([]string(nil), s.ChainsWith...)
	return clone
}

// SetDisabled replaces the profile-wide set of disabled logical skill names.
// Names are matched exactly after source precedence has selected a winner.
func (m *Manager) SetDisabled(names []string) {
	if m == nil {
		return
	}
	disabled := make(map[string]struct{}, len(names))
	for _, name := range names {
		disabled[name] = struct{}{}
	}
	m.state.mu.Lock()
	m.state.disabled = disabled
	m.state.mu.Unlock()
}

// LegacyDisabled returns selected configured-source names carrying the retired
// enabled: false header. The header is migration input only and never affects
// the manager's effective state.
func (m *Manager) LegacyDisabled() []string {
	if m == nil {
		return nil
	}
	m.state.mu.RLock()
	defer m.state.mu.RUnlock()
	out := make([]string, 0)
	for name, skill := range m.state.configured {
		if skill.legacyDisabled {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

// Save writes (or overwrites) a skill file in the first nonempty configured
// directory. Imported effective names cannot be shadowed through this API.
func (m *Manager) Save(name, description, body string, tags []string) (*Skill, error) {
	if m == nil {
		return nil, errors.New("skills manager is unavailable")
	}
	m.state.scanMu.Lock()
	defer m.state.scanMu.Unlock()
	if existing, ok := m.Get(name); ok && existing.ReadOnly {
		return nil, fmt.Errorf("%w: %q", ErrReadOnly, name)
	}
	name = sanitizeName(name)
	if name == "" {
		return nil, errors.New("skill name is required")
	}
	if existing, ok := m.Get(name); ok && existing.ReadOnly {
		return nil, fmt.Errorf("%w: %q", ErrReadOnly, name)
	}
	dir := m.writeDir()
	if dir == "" {
		return nil, errors.New("no skills directory configured")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}

	header := frontMatter{Name: name, Description: description, Source: "agent", Tags: tags}
	headerYAML, err := yaml.Marshal(header)
	if err != nil {
		return nil, err
	}
	content := "---\n" + string(headerYAML) + "---\n\n" + strings.TrimSpace(body) + "\n"
	path := filepath.Join(dir, name+".md")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return nil, err
	}
	if err := m.reloadLocked(); err != nil {
		return nil, err
	}
	s, _ := m.Get(name)
	return s, nil
}

func (m *Manager) writeDir() string {
	m.state.mu.RLock()
	defer m.state.mu.RUnlock()
	for _, dir := range m.state.opts.Dirs {
		if strings.TrimSpace(dir) != "" {
			return dir
		}
	}
	return ""
}

// Delete removes a writable skill file.
func (m *Manager) Delete(name string) error {
	if m == nil {
		return errors.New("skills manager is unavailable")
	}
	m.state.scanMu.Lock()
	defer m.state.scanMu.Unlock()
	s, ok := m.Get(name)
	if !ok {
		return fmt.Errorf("skill %q not found", name)
	}
	if s.ReadOnly {
		return fmt.Errorf("%w: %q", ErrReadOnly, name)
	}
	if err := os.Remove(s.Path); err != nil {
		return err
	}
	return m.reloadLocked()
}

// MarkUsed increments the in-memory usage counter shown in the dashboard.
func (m *Manager) MarkUsed(name string) {
	if m == nil {
		return
	}
	m.state.mu.Lock()
	defer m.state.mu.Unlock()
	m.state.usage[name]++
}

// PromptBlock renders the enabled skills as a compact catalogue for the system
// prompt: name, description, and triggers only. Full bodies are fetched on
// demand with the skill tool so the prompt stays small.
func (m *Manager) PromptBlock(limit int) string {
	list := m.List()
	var b strings.Builder
	n := 0
	for _, s := range list {
		if !s.Enabled || s.Pack {
			continue
		}
		if limit > 0 && n >= limit {
			break
		}
		fmt.Fprintf(&b, "- %s: %s", s.Name, s.Description)
		if len(s.Triggers) > 0 {
			fmt.Fprintf(&b, " (use when: %s)", strings.Join(s.Triggers, "; "))
		}
		b.WriteString("\n")
		n++
	}
	return b.String()
}

// Filter narrows a skill search by security metadata. Empty fields are ignored.
type Filter struct {
	// CWE matches a CWE id, with or without the "CWE-" prefix (e.g. "89").
	CWE string
	// Tech matches a tech_stack entry (e.g. "web", "api", "cloud").
	Tech string
	// Category matches the skill category exactly.
	Category string
	// EnabledOnly excludes disabled names before ranking and limiting results.
	EnabledOnly bool
}

// Search finds skills by keyword, ranked by relevance. It matches across the
// name, description, tags, triggers, and — for the security library — the CWE
// ids, tech stack, OWASP id, and category, so "CWE-89" or "graphql" find the
// right skills even when the words are not in the prose.
func (m *Manager) Search(query string, limit int) []Skill {
	return m.SearchFiltered(query, Filter{}, limit)
}

// SearchFiltered is Search with an optional metadata filter applied first.
func (m *Manager) SearchFiltered(query string, f Filter, limit int) []Skill {
	q := strings.ToLower(strings.TrimSpace(query))
	words := strings.Fields(q)
	list := m.List()
	if limit <= 0 {
		limit = 30
	}

	type scored struct {
		s     Skill
		score int
	}
	var hits []scored
	for _, s := range list {
		if (f.EnabledOnly && !s.Enabled) || !passesFilter(s, f) {
			continue
		}
		hay := strings.ToLower(strings.Join([]string{
			s.Name, s.Description, strings.Join(s.Tags, " "), strings.Join(s.Triggers, " "),
			strings.Join(s.TechStack, " "), strings.Join(s.CWEIDs, " "), s.OWASPID, s.Category,
		}, " "))
		if q != "" && !matchesAll(hay, q) {
			continue
		}
		hits = append(hits, scored{s: s, score: scoreSkill(s, words)})
	}
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].score != hits[j].score {
			return hits[i].score > hits[j].score
		}
		if hits[i].s.UsageCount != hits[j].s.UsageCount {
			return hits[i].s.UsageCount > hits[j].s.UsageCount
		}
		return hits[i].s.Name < hits[j].s.Name
	})
	out := make([]Skill, 0, limit)
	for _, h := range hits {
		out = append(out, h.s)
		if len(out) >= limit {
			break
		}
	}
	return out
}

// passesFilter applies the metadata filter to one skill.
func passesFilter(s Skill, f Filter) bool {
	if f.Category != "" && !strings.EqualFold(s.Category, f.Category) {
		return false
	}
	if f.Tech != "" && !containsFold(s.TechStack, f.Tech) {
		return false
	}
	if cwe := strings.TrimSpace(f.CWE); cwe != "" {
		want := "cwe-" + strings.TrimPrefix(strings.ToLower(cwe), "cwe-")
		found := false
		for _, id := range s.CWEIDs {
			if strings.ToLower(id) == want {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// scoreSkill ranks a skill against the query words: a name hit outweighs a tag,
// which outweighs metadata, which outweighs a description hit.
func scoreSkill(s Skill, words []string) int {
	if len(words) == 0 {
		return s.UsageCount
	}
	name := strings.ToLower(s.Name)
	desc := strings.ToLower(s.Description)
	score := 0
	for _, w := range words {
		switch {
		case name == w:
			score += 100
		case strings.Contains(name, w):
			score += 10
		}
		if containsFold(s.Tags, w) {
			score += 6
		}
		for _, tr := range s.Triggers {
			if strings.Contains(strings.ToLower(tr), w) {
				score += 5
				break
			}
		}
		if containsFold(s.CWEIDs, w) || strings.EqualFold(s.OWASPID, w) {
			score += 12
		}
		if containsFold(s.TechStack, w) {
			score += 6
		}
		if strings.Contains(strings.ToLower(s.Category), w) {
			score += 4
		}
		if strings.Contains(desc, w) {
			score += 2
		}
	}
	return score
}

// Chains resolves a skill's chains_with entries to the skills that exist, so
// the agent can see which follow-on techniques compound with this one.
func (m *Manager) Chains(name string) []Skill {
	if m == nil {
		return nil
	}
	m.state.mu.RLock()
	defer m.state.mu.RUnlock()
	skill, ok := m.effectiveSkillLocked(name)
	if !ok {
		return nil
	}
	out := make([]Skill, 0, len(skill.ChainsWith))
	for _, next := range skill.ChainsWith {
		if chained, ok := m.effectiveSkillLocked(next); ok {
			out = append(out, m.cloneEffectiveSkillLocked(chained))
		}
	}
	return out
}

func containsFold(list []string, want string) bool {
	for _, v := range list {
		if strings.EqualFold(v, want) || strings.Contains(strings.ToLower(v), strings.ToLower(want)) {
			return true
		}
	}
	return false
}

// matchesAll reports whether every word of the query is in the haystack.
func matchesAll(hay, query string) bool {
	for _, w := range strings.Fields(query) {
		if !strings.Contains(hay, w) {
			return false
		}
	}
	return true
}

// Categories lists the pack skill categories with their counts, for browsing.
func (m *Manager) Categories() map[string]int {
	out := map[string]int{}
	if m == nil {
		return out
	}
	m.state.mu.RLock()
	defer m.state.mu.RUnlock()
	for skill := range m.effectiveSkillsLocked {
		if !skill.Pack {
			continue
		}
		category := skill.Category
		if category == "" {
			category = "uncategorised"
		}
		out[category]++
	}
	return out
}

// Library returns pack skills for browsing: filtered by category when given,
// sorted by name, and paged. It returns the page and the total in that filter.
func (m *Manager) Library(category string, offset, limit int) ([]Skill, int) {
	if limit <= 0 {
		limit = 50
	}
	if m == nil {
		return nil, 0
	}
	m.state.mu.RLock()
	all := make([]Skill, 0)
	for skill := range m.effectiveSkillsLocked {
		if !skill.Pack {
			continue
		}
		if category != "" && !strings.EqualFold(skill.Category, category) {
			continue
		}
		all = append(all, m.cloneEffectiveSkillLocked(skill))
	}
	m.state.mu.RUnlock()

	sort.Slice(all, func(i, j int) bool { return all[i].Name < all[j].Name })
	total := len(all)
	if offset >= total {
		return nil, total
	}
	end := offset + limit
	if end > total {
		end = total
	}
	return all[offset:end], total
}

// PackCount reports how many skills came from the bundled library.
func (m *Manager) PackCount() int {
	if m == nil {
		return 0
	}
	m.state.mu.RLock()
	defer m.state.mu.RUnlock()
	n := 0
	for skill := range m.effectiveSkillsLocked {
		if skill.Pack {
			n++
		}
	}
	return n
}

// Count reports how many skills are enabled.
func (m *Manager) Count() int {
	if m == nil {
		return 0
	}
	m.state.mu.RLock()
	defer m.state.mu.RUnlock()
	n := 0
	for skill := range m.effectiveSkillsLocked {
		if _, disabled := m.state.disabled[skill.Name]; !disabled {
			n++
		}
	}
	return n
}

func sanitizeName(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	prevDash := false
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			prevDash = false
		case r == '-' || r == '_' || r == ' ':
			if !prevDash && b.Len() > 0 {
				b.WriteByte('-')
				prevDash = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
