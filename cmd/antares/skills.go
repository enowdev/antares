package main

import (
	"fmt"

	"github.com/enowdev/antares/internal/config"
	"github.com/enowdev/antares/internal/skills"
)

// migrateSkillState imports only the selected configured sources, once per profile.
func migrateSkillState(cfg *config.Config) (*config.Config, error) {
	if cfg.Skills.FrontmatterMigrated {
		return cfg, nil
	}
	manager := skills.NewManager(skills.Options{Dirs: expandAll(cfg.Skills.Dirs)})
	if err := manager.Reload(); err != nil {
		return nil, fmt.Errorf("migrate skill preferences: %w", err)
	}
	fresh, err := config.MigrateSkillState(manager.LegacyDisabled())
	if err != nil {
		return nil, fmt.Errorf("migrate skill preferences: %w", err)
	}
	return fresh, nil
}
