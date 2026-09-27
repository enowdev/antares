package config

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

// persistedEditMu serialises read-modify-write edits of the persisted file
// (skill state, dashboard modules) so one cannot overwrite the other's change.
var persistedEditMu sync.Mutex

// MigrateSkillState imports legacy frontmatter opt-outs exactly once. Imported
// names are merged with preferences already present in the active profile.
func MigrateSkillState(disabled []string) (*Config, error) {
	persistedEditMu.Lock()
	defer persistedEditMu.Unlock()

	cfg, err := readPersistedConfig()
	if err != nil {
		return nil, err
	}
	if cfg.Skills.FrontmatterMigrated {
		return Reload()
	}

	cfg.Skills.Disabled = sortedUniqueNames(append(cfg.Skills.Disabled, disabled...))
	cfg.Skills.FrontmatterMigrated = true
	return persistSkillState(cfg)
}

// SetSkillEnabled updates one exact skill name in the active profile. Skill
// state must be migrated before requests can alter it, so a later migration
// cannot unexpectedly reapply stale frontmatter state.
func SetSkillEnabled(name string, enabled bool) (*Config, error) {
	if strings.TrimSpace(name) == "" {
		return nil, errors.New("skill name is required")
	}

	persistedEditMu.Lock()
	defer persistedEditMu.Unlock()

	cfg, err := readPersistedConfig()
	if err != nil {
		return nil, err
	}
	if !cfg.Skills.FrontmatterMigrated {
		return nil, errors.New("skill preferences have not been migrated")
	}

	if enabled {
		kept := cfg.Skills.Disabled[:0]
		for _, disabled := range cfg.Skills.Disabled {
			if disabled != name {
				kept = append(kept, disabled)
			}
		}
		cfg.Skills.Disabled = kept
	} else {
		cfg.Skills.Disabled = append(cfg.Skills.Disabled, name)
	}
	cfg.Skills.Disabled = sortedUniqueNames(cfg.Skills.Disabled)
	return persistSkillState(cfg)
}

// readPersistedConfig deliberately bypasses Load and Reload: their result has
// environment overrides and normalized paths that must never be written back.
func readPersistedConfig() (*Config, error) {
	path := ConfigFile()
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	cfg := Default()
	if err := yaml.Unmarshal(raw, cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return cfg, nil
}

func persistSkillState(cfg *Config) (*Config, error) {
	if err := writeFile(ConfigFile(), cfg); err != nil {
		return nil, err
	}
	return Reload()
}

func sortedUniqueNames(names []string) []string {
	if len(names) == 0 {
		return nil
	}
	sort.Strings(names)
	write := 1
	for _, name := range names[1:] {
		if name == names[write-1] {
			continue
		}
		names[write] = name
		write++
	}
	return names[:write]
}
