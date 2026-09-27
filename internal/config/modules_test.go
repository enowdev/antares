package config

import (
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestValidateModules(t *testing.T) {
	cases := []struct {
		name    string
		modules []string
		wantErr string
	}{
		{name: "nil", modules: nil},
		{name: "empty is the General preset", modules: []string{}},
		{name: "one", modules: []string{"automation"}},
		{name: "all", modules: []string{"automation", "security", "studio"}},
		{name: "any order", modules: []string{"studio", "automation"}},
		{name: "unknown", modules: []string{"automation", "telepathy"}, wantErr: `unknown module "telepathy"`},
		{name: "case matters", modules: []string{"Security"}, wantErr: `unknown module "Security"`},
		{name: "blank", modules: []string{""}, wantErr: `unknown module ""`},
		{name: "duplicate", modules: []string{"studio", "security", "studio"}, wantErr: `duplicate module "studio"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateModules(tc.modules)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("ValidateModules(%q) = %v, want nil", tc.modules, err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("ValidateModules(%q) = %v, want error containing %q", tc.modules, err, tc.wantErr)
			}
		})
	}
}

func TestValidatePreset(t *testing.T) {
	for _, p := range []string{"general", "coding", "security", "creator", "full", "custom"} {
		if err := ValidatePreset(p); err != nil {
			t.Errorf("ValidatePreset(%q) = %v, want nil", p, err)
		}
	}
	for _, p := range []string{"", "Full", "everything"} {
		if err := ValidatePreset(p); err == nil {
			t.Errorf("ValidatePreset(%q) = nil, want error", p)
		}
	}
}

func TestNormalizeModulesUsesKnownOrder(t *testing.T) {
	got := NormalizeModules([]string{"studio", "automation"})
	if want := []string{"automation", "studio"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("NormalizeModules = %q, want %q", got, want)
	}
	if got := NormalizeModules(nil); got == nil || len(got) != 0 {
		t.Fatalf("NormalizeModules(nil) = %#v, want non-nil empty", got)
	}
}

func TestConfigValidateChecksModulesOnlyWhenPresent(t *testing.T) {
	cfg := Default()
	if err := cfg.Validate(); err != nil {
		t.Fatalf("absent modules: Validate = %v", err)
	}
	empty := []string{}
	cfg.Display.Modules = &empty
	if err := cfg.Validate(); err != nil {
		t.Fatalf("empty modules: Validate = %v", err)
	}
	bad := []string{"automation", "nope"}
	cfg.Display.Modules = &bad
	if err := cfg.Validate(); err == nil {
		t.Fatal("unknown module: Validate = nil, want error")
	}
}

// loadSaveReload runs a config file through the ordinary Load → Save → Reload
// cycle any unrelated settings change would trigger, returning the reloaded
// config and the bytes that landed on disk.
func loadSaveReload(t *testing.T, body string) (*Config, string) {
	t.Helper()
	home := isolateConfigHome(t)
	path := filepath.Join(home, "config.yaml")
	writeSkillConfigFixture(t, path, body)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	cfg.Display.BellOnComplete = true // an unrelated edit
	if err := Save(cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}
	resetLoadedForTest(t)
	reloaded, err := Reload()
	if err != nil {
		t.Fatalf("Reload: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return reloaded, string(raw)
}

func TestDisplayModulesAbsentStaysAbsentAcrossSave(t *testing.T) {
	cfg, raw := loadSaveReload(t, "display:\n  theme: dark\n")
	if cfg.Display.Modules != nil {
		t.Fatalf("display.modules = %#v after re-save, want nil (absent means every module on)", *cfg.Display.Modules)
	}
	if cfg.Display.Preset != "" {
		t.Fatalf("display.preset = %q after re-save, want empty", cfg.Display.Preset)
	}
	var disk struct {
		Display map[string]any `yaml:"display"`
	}
	if err := yaml.Unmarshal([]byte(raw), &disk); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"modules", "preset"} {
		if _, ok := disk.Display[key]; ok {
			t.Fatalf("re-saved config wrote display.%s for an install that never set it:\n%s", key, raw)
		}
	}
	if !cfg.Display.BellOnComplete {
		t.Fatal("unrelated edit was not saved")
	}
}

func TestDisplayModulesEmptyStaysEmptyAcrossSave(t *testing.T) {
	cfg, raw := loadSaveReload(t, "display:\n  modules: []\n  preset: general\n")
	if cfg.Display.Modules == nil {
		t.Fatalf("display.modules = nil after re-save, want empty list (General preset):\n%s", raw)
	}
	if len(*cfg.Display.Modules) != 0 {
		t.Fatalf("display.modules = %q, want empty", *cfg.Display.Modules)
	}
	if cfg.Display.Preset != "general" {
		t.Fatalf("display.preset = %q, want general", cfg.Display.Preset)
	}
	if !strings.Contains(raw, "modules: []") {
		t.Fatalf("re-saved config lost the explicit empty list:\n%s", raw)
	}
}

func TestCloneDeepCopiesDisplayModules(t *testing.T) {
	cfg := Default()
	mods := []string{"automation", "security"}
	cfg.Display.Modules = &mods

	clone := cfg.Clone()
	if clone.Display.Modules == cfg.Display.Modules {
		t.Fatal("Clone shared the display.modules pointer")
	}
	(*clone.Display.Modules)[0] = "studio"
	*clone.Display.Modules = append(*clone.Display.Modules, "studio")
	if want := []string{"automation", "security"}; !reflect.DeepEqual(*cfg.Display.Modules, want) {
		t.Fatalf("mutating the clone changed the original: %q", *cfg.Display.Modules)
	}

	if Default().Clone().Display.Modules != nil {
		t.Fatal("Clone turned an absent display.modules into a value")
	}
}

func TestEffectiveAppliesDisplayModulesLive(t *testing.T) {
	current := Default()
	desired := current.Clone()
	mods := []string{"studio"}
	desired.Display.Modules = &mods
	desired.Display.Preset = "custom"

	next, pending := Effective(current, desired)
	if len(pending) != 0 {
		t.Fatalf("pending = %v, want none for a display change", pending)
	}
	if next.Display.Modules == nil || !reflect.DeepEqual(*next.Display.Modules, mods) {
		t.Fatalf("effective display.modules = %v, want %q", next.Display.Modules, mods)
	}
	if next.Display.Modules == desired.Display.Modules {
		t.Fatal("Effective returned a config sharing desired's display.modules")
	}

	// And back again: a pointer on the running side, nil on the desired side.
	next, pending = Effective(desired, Default())
	if len(pending) != 0 || next.Display.Modules != nil {
		t.Fatalf("effective after clearing = %v (pending %v), want nil", next.Display.Modules, pending)
	}
}

func TestSchemaOmitsDisplayModules(t *testing.T) {
	for _, f := range Schema() {
		if f.Path == "display.modules" || f.Path == "display.preset" {
			t.Fatalf("Schema exposes %s; it has a dedicated Modules UI", f.Path)
		}
	}
}

func TestSetModulesPersistsWithoutEnvironmentOverrides(t *testing.T) {
	home := isolateConfigHome(t)
	path := filepath.Join(home, "config.yaml")
	writeSkillConfigFixture(t, path, `server:
  host: 127.0.0.1
agent:
  workspace: $MODULES_WORKSPACE
display:
  theme: dark
`)
	t.Setenv("ANTARES_HOST", "0.0.0.0")
	t.Setenv("MODULES_WORKSPACE", filepath.Join(home, "runtime-workspace"))

	cfg, err := SetModules([]string{"studio", "automation"}, "creator")
	if err != nil {
		t.Fatalf("SetModules: %v", err)
	}
	want := []string{"automation", "studio"}
	if cfg.Display.Modules == nil || !reflect.DeepEqual(*cfg.Display.Modules, want) {
		t.Fatalf("runtime display.modules = %v, want %q", cfg.Display.Modules, want)
	}
	if cfg.Server.Host != "0.0.0.0" {
		t.Fatalf("runtime server.host = %q, want the environment override", cfg.Server.Host)
	}

	var disk struct {
		Server struct {
			Host string `yaml:"host"`
		} `yaml:"server"`
		Agent struct {
			Workspace string `yaml:"workspace"`
		} `yaml:"agent"`
		Display struct {
			Theme   string    `yaml:"theme"`
			Modules *[]string `yaml:"modules"`
			Preset  string    `yaml:"preset"`
		} `yaml:"display"`
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := yaml.Unmarshal(raw, &disk); err != nil {
		t.Fatal(err)
	}
	if disk.Server.Host != "127.0.0.1" {
		t.Fatalf("persisted server.host = %q, want the operator's value", disk.Server.Host)
	}
	if disk.Agent.Workspace != "$MODULES_WORKSPACE" {
		t.Fatalf("persisted agent.workspace = %q, want unexpanded input", disk.Agent.Workspace)
	}
	if disk.Display.Theme != "dark" {
		t.Fatalf("persisted display.theme = %q, want untouched", disk.Display.Theme)
	}
	if disk.Display.Modules == nil || !reflect.DeepEqual(*disk.Display.Modules, want) {
		t.Fatalf("persisted display.modules = %v, want %q", disk.Display.Modules, want)
	}
	if disk.Display.Preset != "creator" {
		t.Fatalf("persisted display.preset = %q, want creator", disk.Display.Preset)
	}

	// An empty list persists as the explicit General preset.
	cfg, err = SetModules([]string{}, "general")
	if err != nil {
		t.Fatalf("SetModules(empty): %v", err)
	}
	if cfg.Display.Modules == nil || len(*cfg.Display.Modules) != 0 {
		t.Fatalf("display.modules after General = %v, want non-nil empty", cfg.Display.Modules)
	}
}

func TestSetModulesRejectsInvalidInputWithoutWriting(t *testing.T) {
	home := isolateConfigHome(t)
	path := filepath.Join(home, "config.yaml")
	writeSkillConfigFixture(t, path, "display:\n  theme: dark\n")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		modules []string
		preset  string
	}{
		{[]string{"nope"}, "custom"},
		{[]string{"studio", "studio"}, "custom"},
		{[]string{"studio"}, "bogus"},
		{[]string{"studio"}, ""},
	} {
		if _, err := SetModules(tc.modules, tc.preset); err == nil {
			t.Errorf("SetModules(%q, %q) = nil error, want rejection", tc.modules, tc.preset)
		}
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatalf("rejected SetModules rewrote the file:\n%s", after)
	}
}

func TestSaveRawRejectsUnknownModules(t *testing.T) {
	isolateConfigHome(t)
	if err := SaveRaw("display:\n  modules: [automation, holodeck]\n"); err == nil {
		t.Fatal("SaveRaw accepted an unknown module id")
	}
	if err := SaveRaw("display:\n  modules: [studio]\n  preset: nonsense\n"); err == nil {
		t.Fatal("SaveRaw accepted an unknown preset")
	}
	if err := SaveRaw("display:\n  modules: [studio]\n  preset: custom\n"); err != nil {
		t.Fatalf("SaveRaw rejected a valid module list: %v", err)
	}
}

// TestKnownModulesMatchDashboard keeps the Go module ids in lockstep with the
// dashboard's MODULE_IDS; either side drifting would hide or strand a module.
func TestKnownModulesMatchDashboard(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate this test file")
	}
	tsPath := filepath.Join(filepath.Dir(file), "..", "..", "web", "src", "lib", "modules.ts")
	src, err := os.ReadFile(tsPath)
	if err != nil {
		t.Fatalf("read %s: %v", tsPath, err)
	}
	m := regexp.MustCompile(`(?s)export const MODULE_IDS\s*=\s*\[(.*?)\]`).FindSubmatch(src)
	if m == nil {
		t.Fatalf("MODULE_IDS array literal not found in web/src/lib/modules.ts")
	}
	var ts []string
	for _, id := range regexp.MustCompile(`['"]([^'"]+)['"]`).FindAllSubmatch(m[1], -1) {
		ts = append(ts, string(id[1]))
	}
	if !reflect.DeepEqual(ts, KnownModules) {
		t.Fatalf("module ids drifted: web/src/lib/modules.ts MODULE_IDS = %q, internal/config/modules.go KnownModules = %q; update both", ts, KnownModules)
	}
}
