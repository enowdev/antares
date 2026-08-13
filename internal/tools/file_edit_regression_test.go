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

func TestEditFileAmbiguousListsCurrentMatchLines(t *testing.T) {
	workspace := t.TempDir()
	path := filepath.Join(workspace, "repeat.go")
	content := "func a() {\n\treturn value\n}\nfunc b() {\n\treturn value\n}\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	args, _ := json.Marshal(map[string]any{"path": "repeat.go", "old_string": "\treturn value", "new_string": "\treturn other"})
	result := (editFileTool{}).Execute(context.Background(), Input{Workspace: workspace, Args: args})
	if !result.IsError || !strings.Contains(result.Content, "Current match line(s): 2, 5") {
		t.Fatalf("unexpected ambiguity result: %+v", result)
	}
}

func TestEditFileNotFoundShowsNearMiss(t *testing.T) {
	workspace := t.TempDir()
	path := filepath.Join(workspace, "names.go")
	if err := os.WriteFile(path, []byte("func attachEntity() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	args, _ := json.Marshal(map[string]any{"path": "names.go", "old_string": "func attachEntit() {}", "new_string": "func attachEntity2() {}"})
	result := (editFileTool{}).Execute(context.Background(), Input{Workspace: workspace, Args: args})
	if !result.IsError || !strings.Contains(result.Content, "attachEntity") {
		t.Fatalf("near-miss missing from result: %+v", result)
	}
}

func TestEditFileRecoversUniqueNearInsertionWithoutChangingExistingLine(t *testing.T) {
	workspace := t.TempDir()
	path := filepath.Join(workspace, "README.md")
	actual := "| **pool39v2** | 14 hand + 25 vision-audited | 33 train / 6 val | T4 | 85 (early stop @55) | **0.009** | `artifacts/pool39v2/` |\n"
	if err := os.WriteFile(path, []byte(actual), 0o644); err != nil {
		t.Fatal(err)
	}
	old := "| **pool39v2** | 14 hand + 25 vision-audited | 33 train / 6 val | T4 | 85 (ES@55) | **0.009** | `artifacts/pool39v2/` |"
	newString := old + "\n| **pool39v2_sc** | single-class icon | 33 train / 6 val | T4 | 70 | **0.519** | `artifacts/pool39v2_sc/` |"
	args, _ := json.Marshal(map[string]any{"path": "README.md", "old_string": old, "new_string": newString})
	result := (editFileTool{}).Execute(context.Background(), Input{Workspace: workspace, Args: args})
	if result.IsError {
		t.Fatalf("unique adjacent insertion should recover: %s", result.Content)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := actual + "| **pool39v2_sc** | single-class icon | 33 train / 6 val | T4 | 70 | **0.519** | `artifacts/pool39v2_sc/` |\n"
	if string(got) != want {
		t.Fatalf("recovery changed the existing line:\n%s\nwant:\n%s", got, want)
	}
}

// The similarity search picks a unique best line, so the insertion must land
// at that line — not at an earlier occurrence of the same text inside a longer
// line, which strings.Replace-based recovery corrupted mid-line.
func TestEditFileAdjacentInsertionSplicesAtMatchedLine(t *testing.T) {
	workspace := t.TempDir()
	path := filepath.Join(workspace, "f.txt")
	content := "start\nreturn nil // TODO cleanup\nmiddle\nreturn nil\nend\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	// Stale old_string (double space) matches nothing exactly; similarity must
	// pick line 4 ("return nil", score 1.0) over line 2 (score 0.5).
	old := "return  nil"
	args, _ := json.Marshal(map[string]any{
		"path": "f.txt", "old_string": old, "new_string": old + "\nINSERTED",
	})
	result := (editFileTool{}).Execute(context.Background(), Input{Workspace: workspace, Args: args})
	if result.IsError {
		t.Fatalf("unique near-line insertion should recover: %s", result.Content)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := "start\nreturn nil // TODO cleanup\nmiddle\nreturn nil\nINSERTED\nend\n"
	if string(got) != want {
		t.Fatalf("insertion landed at the wrong place:\n%s\nwant:\n%s", got, want)
	}
}

// replace_all promises "replace every exact occurrence"; a similarity-based
// recovery must never piggyback on it and multiply insertions.
func TestEditFileAdjacentInsertionIgnoredWithReplaceAll(t *testing.T) {
	workspace := t.TempDir()
	path := filepath.Join(workspace, "ra.txt")
	content := "return nil // TODO cleanup\nmiddle\nreturn nil\nend\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	old := "return  nil"
	args, _ := json.Marshal(map[string]any{
		"path": "ra.txt", "old_string": old, "new_string": old + "\nINSERTED", "replace_all": true,
	})
	result := (editFileTool{}).Execute(context.Background(), Input{Workspace: workspace, Args: args})
	if !result.IsError {
		t.Fatalf("replace_all must not trigger similarity recovery: %s", result.Content)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != content {
		t.Fatalf("file modified by rejected recovery:\n%s", got)
	}
}

// A file with mixed line endings must never reject an old_string whose bytes
// match the file exactly. (fileEOL used to pick CRLF because one line used it,
// then converted the LF old_string so it matched nothing.)
func TestEditFileExactMatchOnMixedEOLFile(t *testing.T) {
	workspace := t.TempDir()
	path := filepath.Join(workspace, "mixed.txt")
	content := "alpha\r\nbeta\nGAMMA\ndelta\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	args, _ := json.Marshal(map[string]any{
		"path": "mixed.txt", "old_string": "beta\nGAMMA", "new_string": "beta\nGAMMA2",
	})
	result := (editFileTool{}).Execute(context.Background(), Input{Workspace: workspace, Args: args})
	if result.IsError {
		t.Fatalf("exact byte match rejected on mixed-EOL file: %s", result.Content)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := "alpha\r\nbeta\nGAMMA2\ndelta\n"
	if string(got) != want {
		t.Fatalf("edited = %q, want %q", got, want)
	}
}

// One stray lone CR byte anywhere in an LF file used to flip fileEOL to "\r"
// and permanently break every multi-line edit in that file.
func TestEditFileExactMatchDespiteStrayCR(t *testing.T) {
	workspace := t.TempDir()
	path := filepath.Join(workspace, "cr.txt")
	content := "one\ntwo\nnote ends\rrest\nfour\nfive\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	args, _ := json.Marshal(map[string]any{
		"path": "cr.txt", "old_string": "four\nfive", "new_string": "four\nFIVE",
	})
	result := (editFileTool{}).Execute(context.Background(), Input{Workspace: workspace, Args: args})
	if result.IsError {
		t.Fatalf("stray CR poisoned an exact match: %s", result.Content)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := "one\ntwo\nnote ends\rrest\nfour\nFIVE\n"
	if string(got) != want {
		t.Fatalf("edited = %q, want %q", got, want)
	}
}

// read_file line numbers are always consecutive, so a multi-line block whose
// numeric prefixes are not sequential is real pipe-delimited data, not a paste.
func TestStripReadFileLinePrefixesRequiresSequentialNumbers(t *testing.T) {
	if _, ok := stripReadFileLinePrefixes("3|a\n7|b"); ok {
		t.Fatal("non-sequential numeric prefixes must not strip")
	}
	if _, ok := stripReadFileLinePrefixes("5|x\n5|y"); ok {
		t.Fatal("repeated numeric prefixes must not strip")
	}
	got, ok := stripReadFileLinePrefixes("9|a\n10|b\n11|c")
	if !ok || got != "a\nb\nc" {
		t.Fatalf("sequential prefixes should strip, got %q ok=%v", got, ok)
	}
}

// Truncating at the byte cap must not cut a multi-byte rune in half and then
// misreport the whole file as binary.
func TestReadFileTruncationDoesNotSplitRune(t *testing.T) {
	workspace := t.TempDir()
	path := filepath.Join(workspace, "big.txt")
	// "é" is 2 bytes; place it so the maxReadBytes cut lands inside it.
	content := strings.Repeat("a", maxReadBytes-1) + "é" + strings.Repeat("b", 16)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	in := Input{Workspace: workspace, Args: []byte(`{"path":"big.txt"}`)}
	result := (readFileTool{}).Execute(context.Background(), in)
	if result.IsError {
		t.Fatalf("truncated UTF-8 file misread as binary: %s", result.Content)
	}
	if !strings.Contains(result.Content, "file truncated") {
		t.Fatalf("missing truncation notice: %s", result.Content)
	}
}

// Classic-Mac style lone CR line endings must display as separate lines, not
// one giant line with embedded CR bytes.
func TestReadFileDisplaysLoneCRLines(t *testing.T) {
	workspace := t.TempDir()
	path := filepath.Join(workspace, "old.txt")
	if err := os.WriteFile(path, []byte("a\rb\rc"), 0o644); err != nil {
		t.Fatal(err)
	}
	in := Input{Workspace: workspace, Args: []byte(`{"path":"old.txt"}`)}
	result := (readFileTool{}).Execute(context.Background(), in)
	if result.IsError {
		t.Fatalf("read failed: %s", result.Content)
	}
	if !strings.Contains(result.Content, "1|a\n2|b\n3|c") {
		t.Fatalf("lone-CR file not split into lines: %q", result.Content)
	}
}

func TestEditFileDoesNotRecoverAmbiguousNearInsertion(t *testing.T) {
	workspace := t.TempDir()
	path := filepath.Join(workspace, "README.md")
	content := "| **pool39v2** | 85 (early stop @55) | artifacts/a |\n| **pool39v2** | 85 (early stop @55) | artifacts/b |\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	old := "| **pool39v2** | 85 (ES@55) | artifacts/c |"
	args, _ := json.Marshal(map[string]any{
		"path": "README.md", "old_string": old, "new_string": old + "\n| new row |",
	})
	result := (editFileTool{}).Execute(context.Background(), Input{Workspace: workspace, Args: args})
	if !result.IsError || !strings.Contains(result.Content, "old_string not found") {
		t.Fatalf("ambiguous near insertion must remain exact-only: %+v", result)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != content {
		t.Fatalf("ambiguous recovery modified file: %q", got)
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

func TestEditFileDiagnosesMixedReadPrefixes(t *testing.T) {
	workspace := t.TempDir()
	path := filepath.Join(workspace, "mixed.go")
	if err := os.WriteFile(path, []byte("func run() {\n\treturn\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	args, _ := json.Marshal(map[string]any{
		"path": "mixed.go", "old_string": "1|func run() {\n\treturn\n}", "new_string": "1|func run() {\n\treturn nil\n}",
	})
	result := (editFileTool{}).Execute(context.Background(), Input{Workspace: workspace, Args: args})
	if !result.IsError || !strings.Contains(result.Content, "Some old_string lines still include") {
		t.Fatalf("mixed-prefix diagnostic missing: %+v", result)
	}
}
