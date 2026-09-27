package config

import (
	"errors"
	"fmt"
	"os"
	"slices"
)

// KnownModules are the optional dashboard modules, in display order. The
// dashboard keeps the same ids in web/src/lib/modules.ts (MODULE_IDS); a parity
// test fails if the two lists drift.
var KnownModules = []string{"automation", "security", "studio"}

// KnownPresets are the preset labels the dashboard may store alongside
// display.modules. "custom" marks a module set that matches no preset.
var KnownPresets = []string{"general", "coding", "security", "creator", "full", "custom"}

// ValidateModules rejects unknown module ids and duplicates.
func ValidateModules(modules []string) error {
	seen := make(map[string]bool, len(modules))
	for _, m := range modules {
		if !slices.Contains(KnownModules, m) {
			return fmt.Errorf("display.modules: unknown module %q (known: %v)", m, KnownModules)
		}
		if seen[m] {
			return fmt.Errorf("display.modules: duplicate module %q", m)
		}
		seen[m] = true
	}
	return nil
}

// ValidatePreset rejects a preset label the dashboard does not know.
func ValidatePreset(preset string) error {
	if !slices.Contains(KnownPresets, preset) {
		return fmt.Errorf("display.preset: unknown preset %q (known: %v)", preset, KnownPresets)
	}
	return nil
}

// NormalizeModules returns the modules in KnownModules order. The result is
// never nil, so it always persists as an explicit list. Callers validate first.
func NormalizeModules(modules []string) []string {
	out := make([]string, 0, len(modules))
	for _, known := range KnownModules {
		if slices.Contains(modules, known) {
			out = append(out, known)
		}
	}
	return out
}

// PersistedModules reports display.modules and display.preset as stored in
// the active profile's file. modules is nil when the key is absent.
func PersistedModules() (modules *[]string, preset string, err error) {
	cfg, err := readPersistedConfig()
	if errors.Is(err, os.ErrNotExist) {
		return nil, "", nil
	}
	if err != nil {
		return nil, "", err
	}
	return cfg.Display.Modules, cfg.Display.Preset, nil
}

// SetModules validates and persists the dashboard module set and preset label.
// Like SetSkillEnabled it edits the file as written, never the env-overridden
// runtime view, so environment overrides are not saved back, and it shares
// SetSkillEnabled's lock so neither edit can drop the other.
func SetModules(modules []string, preset string) (*Config, error) {
	if err := ValidateModules(modules); err != nil {
		return nil, err
	}
	if err := ValidatePreset(preset); err != nil {
		return nil, err
	}

	persistedEditMu.Lock()
	defer persistedEditMu.Unlock()

	cfg, err := readPersistedConfig()
	if err != nil {
		return nil, err
	}
	normalized := NormalizeModules(modules)
	cfg.Display.Modules = &normalized
	cfg.Display.Preset = preset
	if err := writeFile(ConfigFile(), cfg); err != nil {
		return nil, err
	}
	return Reload()
}
