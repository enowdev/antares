package skills

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

type sourceKind uint8

const (
	sourcePack sourceKind = iota
	sourceUser
	sourceProject
	sourceConfigured
)

type sourceRoot struct {
	path string
	kind sourceKind
}

// cachedSkillFile holds only parser output and filesystem identity. Logical
// path, fallback name, modification time, and provenance belong to each source
// occurrence and are attached after a cache hit.
type cachedSkillFile struct {
	info   fs.FileInfo
	parsed *Skill
}
type discoveryScan struct {
	ctx              context.Context
	force            bool
	previous         map[string]cachedSkillFile
	next             map[string]cachedSkillFile
	failed           map[string]struct{}
	preservePrevious bool
}

var userSkillRoots = [][]string{
	{".agent", "skills"},
	{".agents", "skills"},
	{".claude", "skills"},
	{".codex", "skills"},
	{".config", "opencode", "skills"},
	{".omp", "agent", "managed-skills"},
}

var projectSkillRoots = [][]string{
	{".agent", "skills"},
	{".agents", "skills"},
	{".claude", "skills"},
	{".codex", "skills"},
	{".opencode", "skills"},
	{".github", "skills"},
}

func newDiscoveryScan(ctx context.Context, force bool, previous map[string]cachedSkillFile, preservePrevious bool) *discoveryScan {
	if ctx == nil {
		ctx = context.Background()
	}
	capacity := 0
	if !preservePrevious {
		capacity = len(previous)
	}
	return &discoveryScan{
		ctx: ctx, force: force, previous: previous,
		next:   make(map[string]cachedSkillFile, capacity),
		failed: make(map[string]struct{}), preservePrevious: preservePrevious,
	}
}

func (scan *discoveryScan) cache() map[string]cachedSkillFile {
	if !scan.preservePrevious {
		return scan.next
	}
	// First-project registration only adds its own cache entries. The caller
	// holds scanMu and the state write lock; parsed values remain immutable.
	for path := range scan.failed {
		delete(scan.previous, path)
	}
	for path, cached := range scan.next {
		scan.previous[path] = cached
	}
	return scan.previous
}

// discoverShared scans each shared source kind independently. Lower-priority
// entries remain in their layer so removing an override reveals them later.
func discoverShared(scan *discoveryScan, opts Options) (bundled, user, configured map[string]*Skill, firstErr error) {
	bundled, err := discoverRoots(scan, rootsForPaths(opts.PackDirs, sourcePack))
	firstErr = err
	userRoots := make([]string, 0, len(userSkillRoots))
	if strings.TrimSpace(opts.UserHome) != "" {
		for _, parts := range userSkillRoots {
			userRoots = append(userRoots, filepath.Join(append([]string{opts.UserHome}, parts...)...))
		}
	}
	user, err = discoverRoots(scan, rootsForPaths(userRoots, sourceUser))
	if firstErr == nil {
		firstErr = err
	}
	configured, err = discoverRoots(scan, rootsForPaths(opts.Dirs, sourceConfigured))
	if firstErr == nil {
		firstErr = err
	}
	return bundled, user, configured, firstErr
}

// discoverProject scans only the conventional roots beneath one normalized
// logical project directory.
func discoverProject(scan *discoveryScan, projectDir string) (map[string]*Skill, error) {
	paths := make([]string, 0, len(projectSkillRoots))
	for _, parts := range projectSkillRoots {
		paths = append(paths, filepath.Join(append([]string{projectDir}, parts...)...))
	}
	return discoverRoots(scan, rootsForPaths(paths, sourceProject))
}

func rootsForPaths(paths []string, kind sourceKind) []sourceRoot {
	return appendSourceRoots(nil, paths, kind)
}

func discoverRoots(scan *discoveryScan, roots []sourceRoot) (map[string]*Skill, error) {
	found := make(map[string]*Skill)
	var firstErr error
	for _, root := range roots {
		if err := scan.ctx.Err(); err != nil {
			return found, err
		}
		if err := scanRoot(scan, root, func(skill *Skill) {
			found[skill.Name] = skill
		}); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return found, firstErr
}

// appendSourceRoots drops equivalent roots only within one source kind. Walking
// backwards preserves the last, highest-priority spelling of each root.
func appendSourceRoots(dst []sourceRoot, paths []string, kind sourceKind) []sourceRoot {
	kept := make([]string, 0, len(paths))
	for i := len(paths) - 1; i >= 0; i-- {
		path := paths[i]
		if strings.TrimSpace(path) == "" {
			continue
		}
		logical, err := filepath.Abs(path)
		if err != nil {
			logical = filepath.Clean(path)
		}
		duplicate := false
		for _, prior := range kept {
			if sameRoot(logical, prior) {
				duplicate = true
				break
			}
		}
		if duplicate {
			continue
		}
		kept = append(kept, logical)
	}
	for i := len(kept) - 1; i >= 0; i-- {
		dst = append(dst, sourceRoot{path: kept[i], kind: kind})
	}
	return dst
}

func sameRoot(left, right string) bool {
	leftInfo, leftErr := os.Stat(left)
	rightInfo, rightErr := os.Stat(right)
	if leftErr == nil && rightErr == nil {
		return os.SameFile(leftInfo, rightInfo)
	}
	return left == right
}

func scanRoot(scan *discoveryScan, root sourceRoot, publish func(*Skill)) error {
	var firstErr error
	ancestors := make(map[string]struct{})
	var walk func(string) error
	walk = func(logical string) error {
		if err := scan.ctx.Err(); err != nil {
			return err
		}
		info, err := os.Stat(logical)
		if err != nil {
			if !errors.Is(err, fs.ErrNotExist) && firstErr == nil {
				firstErr = err
			}
			return nil
		}
		if info.IsDir() {
			canonical, err := canonicalPath(logical)
			if err != nil {
				if !errors.Is(err, fs.ErrNotExist) && firstErr == nil {
					firstErr = err
				}
				return nil
			}
			if _, cycle := ancestors[canonical]; cycle {
				return nil
			}
			ancestors[canonical] = struct{}{}
			entries, err := os.ReadDir(logical)
			if err != nil {
				delete(ancestors, canonical)
				if !errors.Is(err, fs.ErrNotExist) && firstErr == nil {
					firstErr = err
				}
				return nil
			}
			for _, entry := range entries { // os.ReadDir returns lexical order.
				if err := scan.ctx.Err(); err != nil {
					delete(ancestors, canonical)
					return err
				}
				if strings.HasPrefix(entry.Name(), ".") {
					continue
				}
				if err := walk(filepath.Join(logical, entry.Name())); err != nil {
					delete(ancestors, canonical)
					return err
				}
			}
			delete(ancestors, canonical)
			return nil
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		base := filepath.Base(logical)
		if root.kind == sourceUser || root.kind == sourceProject {
			if !strings.EqualFold(base, "SKILL.md") {
				return nil
			}
		} else if !strings.EqualFold(filepath.Ext(base), ".md") {
			return nil
		}

		parsed, err := scan.parse(logical, info)
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return err
			}
			if !errors.Is(err, fs.ErrNotExist) && firstErr == nil {
				firstErr = err
			}
			return nil
		}
		skill := *parsed
		if strings.TrimSpace(skill.Name) == "" {
			if strings.EqualFold(base, "SKILL.md") {
				skill.Name = filepath.Base(filepath.Dir(logical))
			} else {
				skill.Name = strings.TrimSuffix(base, filepath.Ext(base))
			}
		}
		if skill.Source == "" {
			skill.Source = "local"
		}
		skill.Path = logical
		skill.UpdatedAt = info.ModTime()
		skill.Pack = root.kind == sourcePack
		skill.ReadOnly = root.kind == sourceUser || root.kind == sourceProject
		publish(&skill)
		return nil
	}
	if err := walk(root.path); err != nil {
		return err
	}
	return firstErr
}

func (scan *discoveryScan) parse(logical string, info fs.FileInfo) (*Skill, error) {
	if err := scan.ctx.Err(); err != nil {
		return nil, err
	}
	canonical, err := canonicalPath(logical)
	if err != nil {
		return nil, err
	}
	if cached, ok := scan.next[canonical]; ok && sameCachedFile(cached, info) {
		return cached.parsed, nil
	}
	if !scan.force {
		if cached, ok := scan.previous[canonical]; ok && sameCachedFile(cached, info) {
			scan.next[canonical] = cached
			return cached.parsed, nil
		}
	}
	parsed, err := parseFile(logical)
	if err != nil {
		delete(scan.next, canonical)
		scan.failed[canonical] = struct{}{}
		return nil, err
	}
	scan.next[canonical] = cachedSkillFile{info: info, parsed: parsed}
	delete(scan.failed, canonical)
	return parsed, nil
}

func canonicalPath(path string) (string, error) {
	canonical, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	return filepath.Abs(canonical)
}

func sameCachedFile(cached cachedSkillFile, info fs.FileInfo) bool {
	return cached.parsed != nil && cached.info != nil && os.SameFile(cached.info, info) &&
		cached.info.Size() == info.Size() && cached.info.ModTime().Equal(info.ModTime())
}
