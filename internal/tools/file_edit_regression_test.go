package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadAndEditPreserveTabbedCRLFContent(t *testing.T) {
	workspace := t.TempDir()
	path := filepath.Join(workspace, "sample.cpp")
	original := "if (ready) {\r\n\treturn !failed;\r\n}\r\n"
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	in := Input{Workspace: workspace, Args: []byte(`{"path":"sample.cpp"}`)}
	read := (readFileTool{}).Execute(context.Background(), in)
	if read.IsError {
		t.Fatalf("read_file: %s", read.Content)
	}
	if !strings.Contains(read.Content, "2|\treturn !failed;") {
		t.Fatalf("read output does not preserve indentation unambiguously: %q", read.Content)
	}

	edit := (editFileTool{}).Execute(context.Background(), Input{
		Workspace: workspace,
		Args:      []byte(`{"path":"sample.cpp","old_string":"if (ready) {\n\treturn !failed;\n}","new_string":"if (ready) {\n\treturn success;\n}"}`),
	})
	if edit.IsError {
		t.Fatalf("edit_file: %s", edit.Content)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := "if (ready) {\r\n\treturn success;\r\n}\r\n"
	if string(got) != want {
		t.Fatalf("edited bytes = %q, want %q", got, want)
	}
}

// Model copies old_string from read_file output, which always uses LF, even when
// the on-disk file is CRLF. edit_file must still match and preserve the file's
// original line endings on write.
func TestEditFileMatchesCRLFWhenCopiedFromRead(t *testing.T) {
	workspace := t.TempDir()
	path := filepath.Join(workspace, "win.go")
	original := "package main\r\n\r\nfunc main() {\r\n\tfmt.Println(\"hi\")\r\n}\r\n"
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	readArgs, _ := json.Marshal(map[string]any{"path": "win.go"})
	read := (readFileTool{}).Execute(context.Background(), Input{Args: readArgs, Workspace: workspace})
	if read.IsError {
		t.Fatalf("read: %s", read.Content)
	}

	var copied []string
	for _, line := range strings.Split(strings.TrimSuffix(read.Content, "\n"), "\n") {
		_, content, ok := strings.Cut(line, "|")
		if !ok {
			t.Fatalf("read line missing NUMBER| separator: %q", line)
		}
		copied = append(copied, content)
	}
	// Function body as the model would reassemble it from the LF display.
	oldString := strings.Join(copied[2:5], "\n")
	newString := strings.Replace(oldString, "hi", "bye", 1)

	editArgs, _ := json.Marshal(map[string]any{
		"path": "win.go", "old_string": oldString, "new_string": newString,
	})
	edited := (editFileTool{}).Execute(context.Background(), Input{Args: editArgs, Workspace: workspace})
	if edited.IsError {
		t.Fatalf("edit_file failed for CRLF file after read_file copy: %s", edited.Content)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := "package main\r\n\r\nfunc main() {\r\n\tfmt.Println(\"bye\")\r\n}\r\n"
	if string(got) != want {
		t.Fatalf("edited content = %q\nwant %q", got, want)
	}
}

// Models sometimes paste the whole NUMBER| line from read_file into old_string.
// edit_file should strip a consistent line-number prefix block and still match.
func TestEditFileStripsReadFileLineNumberPrefixes(t *testing.T) {
	workspace := t.TempDir()
	path := filepath.Join(workspace, "a.go")
	original := "package main\n\nfunc main() {\n\treturn\n}\n"
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	// Accidental paste of the read_file display format.
	oldWithPrefix := "3|func main() {\n4|\treturn\n5|}"
	newWithPrefix := "3|func main() {\n4|\treturn nil\n5|}"
	editArgs, _ := json.Marshal(map[string]any{
		"path": "a.go", "old_string": oldWithPrefix, "new_string": newWithPrefix,
	})
	edited := (editFileTool{}).Execute(context.Background(), Input{Args: editArgs, Workspace: workspace})
	if edited.IsError {
		t.Fatalf("edit_file should strip NUMBER| prefixes: %s", edited.Content)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := "package main\n\nfunc main() {\n\treturn nil\n}\n"
	if string(got) != want {
		t.Fatalf("edited = %q, want %q", got, want)
	}
}

// When the match still fails, the error must say what went wrong in a way the
// model can act on (tabs vs spaces is the common indentation trap).
func TestEditFileDiagnosesTabVsSpaceMismatch(t *testing.T) {
	workspace := t.TempDir()
	path := filepath.Join(workspace, "tabs.c")
	original := "\t\tif (x) {\n\t\t\tdo_work();\n\t\t}\n"
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	editArgs, _ := json.Marshal(map[string]any{
		"path":       "tabs.c",
		"old_string": "  if (x) {\n    do_work();\n  }",
		"new_string": "  if (x) {\n    do_work2();\n  }",
	})
	edited := (editFileTool{}).Execute(context.Background(), Input{Args: editArgs, Workspace: workspace})
	if !edited.IsError {
		t.Fatal("expected failure for tab/space mismatch")
	}
	if !strings.Contains(edited.Content, "tab") {
		t.Fatalf("error should diagnose tabs vs spaces, got: %s", edited.Content)
	}
}

func TestStripReadFileLinePrefixes(t *testing.T) {
	in := "10|\tfoo()\n11|\tbar()\n12|}"
	got, ok := stripReadFileLinePrefixes(in)
	if !ok {
		t.Fatal("expected strip success")
	}
	if got != "\tfoo()\n\tbar()\n}" {
		t.Fatalf("got %q", got)
	}
	// Not every line prefixed → leave alone (could be real pipe content).
	if _, ok := stripReadFileLinePrefixes("a|b\nc"); ok {
		t.Fatal("partial prefix block must not strip")
	}
	// Single line of real data that happens to contain a pipe stays intact.
	if s, ok := stripReadFileLinePrefixes("nope"); ok || s != "nope" {
		t.Fatalf("non-prefixed = %q ok=%v", s, ok)
	}
}
