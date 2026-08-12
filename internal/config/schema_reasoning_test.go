package config

import (
	"os"
	"strings"
	"testing"
)

func TestSchemaMarksReasoningFieldsModelAware(t *testing.T) {
	schema := Schema()
	for _, path := range []string{"agent.reasoning_effort", "model.reasoning_effort"} {
		field := fieldByPath(t, schema, path)
		if len(field.Enum) != 0 || field.OptionsSource != "reasoning_capability" {
			t.Fatalf("%s = %+v", path, field)
		}
	}
}

func TestParseRawDoesNotWriteConfiguration(t *testing.T) {
	t.Setenv("ANTARES_HOME", t.TempDir())
	t.Setenv("ANTARES_CONFIG", "")
	t.Setenv("ANTARES_PROFILE", "default")
	if err := Save(Default()); err != nil {
		t.Fatal(err)
	}
	before := mustReadConfigFile(t)
	if _, err := ParseRaw("model:\n  default: gpt-5\n"); err != nil {
		t.Fatal(err)
	}
	if after := mustReadConfigFile(t); after != before {
		t.Fatal("ParseRaw changed the config file")
	}
}

func TestParseRawWithEnvAppliesProviderOverlaysWithoutWriting(t *testing.T) {
	t.Setenv("ANTARES_HOME", t.TempDir())
	t.Setenv("ANTARES_CONFIG", "")
	t.Setenv("ANTARES_PROFILE", "default")
	t.Setenv("ANTARES_MODEL", "")
	t.Setenv("ANTARES_PROVIDER", "")
	t.Setenv("ANTARES_BASE_URL", "")
	t.Setenv("ANTARES_API_KEY", "")
	t.Setenv("ROUND2_DECLARED_KEY", "declared-secret")
	t.Setenv("ANTARES_PROVIDER_DECLARED_API_KEY", "")
	t.Setenv("ANTARES_PROVIDER_DECLARED_BASE_URL", "http://env-declared.example/v1")
	t.Setenv("ANTARES_PROVIDER_EXPLICIT_API_KEY", "explicit-secret")
	t.Setenv("ANTARES_PROVIDER_EXPLICIT_BASE_URL", "http://env-explicit.example/v1")

	if err := Save(Default()); err != nil {
		t.Fatal(err)
	}
	liveBefore := Get()
	fileBefore := mustReadConfigFile(t)
	raw := "model:\n" +
		"  provider: declared\n" +
		"  default: model-a\n" +
		"providers:\n" +
		"  declared:\n" +
		"    kind: openai-compatible\n" +
		"    base_url: http://raw-declared.example/v1\n" +
		"    api_key_env: ROUND2_DECLARED_KEY\n" +
		"    enabled: true\n" +
		"  explicit:\n" +
		"    kind: openai-compatible\n" +
		"    base_url: http://raw-explicit.example/v1\n" +
		"    enabled: true\n"

	candidate, err := ParseRawWithEnv(raw)
	if err != nil {
		t.Fatal(err)
	}
	declared := candidate.Providers["declared"]
	if declared.APIKey != "declared-secret" ||
		declared.BaseURL != "http://env-declared.example/v1" {
		t.Fatalf("declared provider = %+v", declared)
	}
	explicit := candidate.Providers["explicit"]
	if explicit.APIKey != "explicit-secret" ||
		explicit.BaseURL != "http://env-explicit.example/v1" {
		t.Fatalf("explicit provider = %+v", explicit)
	}
	if after := mustReadConfigFile(t); after != fileBefore {
		t.Fatal("ParseRawWithEnv changed the config file")
	}
	if Get() != liveBefore {
		t.Fatal("ParseRawWithEnv replaced the live config")
	}
	if strings.Contains(fileBefore, "declared-secret") ||
		strings.Contains(fileBefore, "explicit-secret") {
		t.Fatal("environment credential was persisted")
	}
}

func fieldByPath(t *testing.T, fields []Field, path string) Field {
	t.Helper()
	for _, field := range fields {
		if field.Path == path {
			return field
		}
	}
	t.Fatalf("field %q not found", path)
	return Field{}
}

func mustReadConfigFile(t *testing.T) string {
	t.Helper()
	path := ConfigFile()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.WriteFile(path, raw, 0o600); err != nil {
			t.Errorf("restore config: %v", err)
		}
	})
	return string(raw)
}
