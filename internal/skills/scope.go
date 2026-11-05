package skills

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
)

// managerState owns the immutable source snapshots shared by every scoped
// Manager handle. Project maps contain only project-source entries; effective
// catalogues are resolved while holding mu rather than copied per scope.
type managerState struct {
	mu     sync.RWMutex
	scanMu sync.Mutex

	opts Options

	bundled     map[string]*Skill
	user        map[string]*Skill
	configured  map[string]*Skill
	projects    map[string]map[string]*Skill
	projectErrs map[string]error
	scopes      map[string]*Manager
	usage       map[string]int
	disabled    map[string]struct{}
	cache       map[string]cachedSkillFile
	root        *Manager
	sharedOnly  *Manager

	// startupBase never changes: relative session bindings remain anchored to
	// the process's originally captured startup directory after reconfiguration.
	startupBase    string
	defaultProject string
	defaultErr     error
	sharedErr      error
}

// Manager is a lightweight view over shared skill source snapshots. An empty
// projectDir denotes the root handle, whose selected project follows the
// state's current default. A bound handle keeps its logical project path.
type Manager struct {
	state      *managerState
	projectDir string
	sharedOnly bool
}

// NewManager builds a manager over independent writable, bundled, user, and
// project sources. Options are cloned so caller mutations cannot reconfigure it.
func NewManager(opts Options) *Manager {
	opts = cloneOptions(opts)
	defaultProject, defaultErr := normalizeStartupProject(opts.ProjectDir)
	opts.ProjectDir = defaultProject
	state := &managerState{
		opts:           opts,
		bundled:        map[string]*Skill{},
		user:           map[string]*Skill{},
		configured:     map[string]*Skill{},
		projects:       map[string]map[string]*Skill{},
		projectErrs:    map[string]error{},
		scopes:         map[string]*Manager{},
		usage:          map[string]int{},
		disabled:       map[string]struct{}{},
		cache:          map[string]cachedSkillFile{},
		startupBase:    defaultProject,
		defaultProject: defaultProject,
		defaultErr:     defaultErr,
	}
	state.root = &Manager{state: state}
	state.sharedOnly = &Manager{state: state, sharedOnly: true}
	return state.root
}

func cloneOptions(opts Options) Options {
	opts.Dirs = append([]string(nil), opts.Dirs...)
	opts.PackDirs = append([]string(nil), opts.PackDirs...)
	return opts
}

func normalizeStartupProject(projectDir string) (string, error) {
	if strings.TrimSpace(projectDir) == "" {
		return "", nil
	}
	if strings.IndexByte(projectDir, 0) >= 0 {
		return "", fmt.Errorf("normalize startup project directory: path contains NUL")
	}
	logical, err := filepath.Abs(projectDir)
	if err != nil {
		return "", fmt.Errorf("normalize startup project directory %q: %w", projectDir, err)
	}
	return filepath.Clean(logical), nil
}

func normalizeReconfiguredProject(projectDir, startupBase string) (string, error) {
	if strings.TrimSpace(projectDir) == "" {
		return "", nil
	}
	if strings.IndexByte(projectDir, 0) >= 0 {
		return "", fmt.Errorf("normalize project directory: path contains NUL")
	}
	if filepath.IsAbs(projectDir) {
		return filepath.Clean(projectDir), nil
	}
	if startupBase == "" {
		return "", fmt.Errorf("normalize relative project directory %q: startup project directory is unavailable", projectDir)
	}
	return filepath.Clean(filepath.Join(startupBase, projectDir)), nil
}

// ForProject returns a lightweight catalogue view bound to projectDir. Relative
// paths resolve against the originally captured startup project. A normalization
// failure returns a shared-only view rather than another project's catalogue.
func (m *Manager) ForProject(projectDir string) (*Manager, error) {
	if m == nil {
		return nil, nil
	}
	if strings.TrimSpace(projectDir) == "" {
		m.state.mu.RLock()
		root := m.state.root
		err := m.state.defaultErr
		if err == nil {
			err = m.state.sharedErr
		}
		if err == nil && m.state.defaultProject != "" {
			err = m.state.projectErrs[m.state.defaultProject]
		}
		m.state.mu.RUnlock()
		return root, err
	}

	logical, err := m.normalizeProject(projectDir)
	if err != nil {
		return m.state.sharedOnly, err
	}

	// Registered scopes are the common path. Avoid queueing behind an active
	// filesystem scan when their immutable snapshot can be returned immediately.
	m.state.mu.RLock()
	view, registered := m.state.scopes[logical]
	registeredErr := m.state.sharedErr
	if registeredErr == nil {
		registeredErr = m.state.projectErrs[logical]
	}
	m.state.mu.RUnlock()
	if registered {
		return view, registeredErr
	}

	m.state.scanMu.Lock()
	defer m.state.scanMu.Unlock()
	// Another first-use registration may have completed while this caller waited.
	m.state.mu.RLock()
	view, registered = m.state.scopes[logical]
	registeredErr = m.state.sharedErr
	if registeredErr == nil {
		registeredErr = m.state.projectErrs[logical]
	}
	previous := m.state.cache
	m.state.mu.RUnlock()
	if registered {
		return view, registeredErr
	}

	view = &Manager{state: m.state, projectDir: logical}
	scan := newDiscoveryScan(context.Background(), false, previous, true)
	found, scanErr := discoverProject(scan, logical)
	m.state.mu.Lock()
	m.state.projects[logical] = found
	m.state.projectErrs[logical] = scanErr
	m.state.scopes[logical] = view
	m.state.cache = scan.cache()
	sharedErr := m.state.sharedErr
	m.state.mu.Unlock()
	if sharedErr != nil {
		return view, sharedErr
	}
	return view, scanErr
}

func (m *Manager) normalizeProject(projectDir string) (string, error) {
	if strings.IndexByte(projectDir, 0) >= 0 {
		return "", fmt.Errorf("normalize project directory: path contains NUL")
	}
	if filepath.IsAbs(projectDir) {
		return filepath.Clean(projectDir), nil
	}
	m.state.mu.RLock()
	startupBase := m.state.startupBase
	m.state.mu.RUnlock()
	if startupBase == "" {
		return "", fmt.Errorf("normalize relative project directory %q: startup project directory is unavailable", projectDir)
	}
	return filepath.Clean(filepath.Join(startupBase, projectDir)), nil
}

func (m *Manager) selectedProjectLocked() map[string]*Skill {
	if m.sharedOnly {
		return nil
	}
	projectDir := m.projectDir
	if projectDir == "" {
		projectDir = m.state.defaultProject
	}
	if projectDir == "" {
		return nil
	}
	return m.state.projects[projectDir]
}

func (m *Manager) effectiveSkillLocked(name string) (*Skill, bool) {
	if skill, ok := m.state.configured[name]; ok {
		return skill, true
	}
	if skill, ok := m.selectedProjectLocked()[name]; ok {
		return skill, true
	}
	if skill, ok := m.state.user[name]; ok {
		return skill, true
	}
	skill, ok := m.state.bundled[name]
	return skill, ok
}

func (m *Manager) effectiveSkillsLocked(yield func(*Skill) bool) {
	layers := [4]map[string]*Skill{m.state.configured, m.selectedProjectLocked(), m.state.user, m.state.bundled}
	for i, layer := range layers {
		for name, skill := range layer {
			shadowed := false
			for _, higher := range layers[:i] {
				if _, exists := higher[name]; exists {
					shadowed = true
					break
				}
			}
			if !shadowed && !yield(skill) {
				return
			}
		}
	}
}

func (m *Manager) effectiveListLocked() []Skill {
	capacity := len(m.state.configured) + len(m.state.user) + len(m.state.bundled) + len(m.selectedProjectLocked())
	out := make([]Skill, 0, capacity)
	for skill := range m.effectiveSkillsLocked {
		out = append(out, m.cloneEffectiveSkillLocked(skill))
	}
	return out
}

func (m *Manager) cloneEffectiveSkillLocked(skill *Skill) Skill {
	clone := cloneSkill(skill)
	clone.UsageCount = m.state.usage[skill.Name]
	_, disabled := m.state.disabled[skill.Name]
	clone.Enabled = !disabled
	return clone
}
