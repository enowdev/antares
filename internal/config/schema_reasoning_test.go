package config

import (
	"os"
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
