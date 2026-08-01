package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadFileSeparatesLineNumbersFromTabbedUnicodeContent(t *testing.T) {
	workspace := t.TempDir()
	path := filepath.Join(workspace, "remoteplayer.cpp")
	original := "\t\t\tif (!m_pPlayerPed->IsInVehicle())\n\t\t\t{\n\t\t\t\t// preserve root motion — exactly\n\t\t\t}\n"
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	readArgs, _ := json.Marshal(map[string]any{"path": "remoteplayer.cpp"})
	read := (readFileTool{}).Execute(context.Background(), Input{Args: readArgs, Workspace: workspace})
	if read.IsError {
		t.Fatalf("read_file failed: %s", read.Content)
	}
	wantRead := "1|\t\t\tif (!m_pPlayerPed->IsInVehicle())\n2|\t\t\t{\n3|\t\t\t\t// preserve root motion — exactly\n4|\t\t\t}\n5|\n"
	if read.Content != wantRead {
		t.Fatalf("read output = %q, want %q", read.Content, wantRead)
	}

	var copied []string
	for _, line := range strings.Split(strings.TrimSuffix(read.Content, "\n"), "\n")[:4] {
		_, content, ok := strings.Cut(line, "|")
		if !ok {
			t.Fatalf("line lacks NUMBER| separator: %q", line)
		}
		copied = append(copied, content)
	}
	oldString := strings.Join(copied, "\n")
	newString := strings.Replace(oldString, "exactly", "safely", 1)
	editArgs, _ := json.Marshal(map[string]any{
		"path": "remoteplayer.cpp", "old_string": oldString, "new_string": newString,
	})
	edited := (editFileTool{}).Execute(context.Background(), Input{Args: editArgs, Workspace: workspace})
	if edited.IsError {
		t.Fatalf("edit_file failed with content copied after separator: %s", edited.Content)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != strings.Replace(original, "exactly", "safely", 1) {
		t.Fatalf("edited content = %q", got)
	}
}

func TestProjectWriteErrorSuggestsInWorkspaceTemporaryScript(t *testing.T) {
	project := t.TempDir()
	workspace := t.TempDir()
	_, err := resolveWrite(Input{Workspace: project, WriteRoots: []string{project, workspace}}, "/tmp/fix_remoteplayer.py")
	if err == nil {
		t.Fatal("outside write unexpectedly allowed")
	}
	if !strings.Contains(err.Error(), "inside the project or Antares workspace instead of /tmp") {
		t.Fatalf("write error lacks actionable fallback: %v", err)
	}
}
