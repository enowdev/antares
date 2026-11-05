package skills

import (
	"context"
	"errors"
	"log/slog"
	"sort"
	"time"
)

const (
	defaultRefreshInterval = 5 * time.Second
	forcedRefreshTicks     = 12
)

// Watch blocks in the caller's goroutine until cancellation. A forced parse every
// twelve ticks bounds detection latency for metadata-preserving edits.
func (m *Manager) Watch(ctx context.Context, interval time.Duration) {
	if m == nil {
		return
	}
	if interval <= 0 {
		interval = defaultRefreshInterval
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	ticks := 0
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			ticks++
			m.state.scanMu.Lock()
			err := ctx.Err()
			if err == nil {
				err = m.refreshLocked(ctx, ticks%forcedRefreshTicks == 0)
			}
			m.state.scanMu.Unlock()
			if err != nil && !isContextError(err) {
				slog.Warn("some skills failed to load", "error", err)
			}
		}
	}
}

// Reload synchronously reparses all shared sources and registered projects.
func (m *Manager) Reload() error {
	if m == nil {
		return nil
	}
	m.state.scanMu.Lock()
	defer m.state.scanMu.Unlock()
	return m.reloadLocked()
}

// reloadLocked is used after mutations already holding scanMu.
func (m *Manager) reloadLocked() error {
	return m.refreshLocked(context.Background(), true)
}

func (m *Manager) refreshLocked(ctx context.Context, force bool) error {
	m.state.mu.RLock()
	opts, defaultErr := m.state.opts, m.state.defaultErr
	m.state.mu.RUnlock()
	return m.scanAndPublishLocked(ctx, opts, defaultErr, force)
}

// Reconfigure publishes cloned options and their newly scanned layers together.
// Existing bound handles retain their logical project paths; only the root view
// follows the new default project. Relative paths keep the original startup base.
func (m *Manager) Reconfigure(opts Options) error {
	if m == nil {
		return nil
	}
	m.state.scanMu.Lock()
	defer m.state.scanMu.Unlock()
	opts = cloneOptions(opts)
	project, err := normalizeReconfiguredProject(opts.ProjectDir, m.state.startupBase)
	opts.ProjectDir = project
	return m.scanAndPublishLocked(context.Background(), opts, err, true)
}

// scanAndPublishLocked requires scanMu, never holding the state write lock while
// walking. Non-cancellation errors publish successful entries; canceled scans
// leave the prior options, layers and cache untouched.
func (m *Manager) scanAndPublishLocked(ctx context.Context, opts Options, defaultErr error, force bool) error {
	state := m.state
	state.mu.RLock()
	projectsToScan := make([]string, 0, len(state.scopes)+1)
	for path := range state.scopes {
		projectsToScan = append(projectsToScan, path)
	}
	if opts.ProjectDir != "" {
		if _, registered := state.scopes[opts.ProjectDir]; !registered {
			projectsToScan = append(projectsToScan, opts.ProjectDir)
		}
	}
	previous := state.cache
	state.mu.RUnlock()
	sort.Strings(projectsToScan)

	scan := newDiscoveryScan(ctx, force, previous, false)
	bundled, user, configured, sharedErr := discoverShared(scan, opts)
	projects := make(map[string]map[string]*Skill, len(projectsToScan))
	projectErrs := make(map[string]error, len(projectsToScan))
	firstErr := defaultErr
	if firstErr == nil {
		firstErr = sharedErr
	}
	for _, path := range projectsToScan {
		if err := ctx.Err(); err != nil {
			return err
		}
		found, err := discoverProject(scan, path)
		projects[path], projectErrs[path] = found, err
		if firstErr == nil {
			firstErr = err
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	state.opts = opts
	state.defaultProject, state.defaultErr = opts.ProjectDir, defaultErr
	state.bundled, state.user, state.configured = bundled, user, configured
	state.projects, state.projectErrs, state.sharedErr = projects, projectErrs, sharedErr
	state.cache = scan.cache()
	return firstErr
}

func isContextError(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}
