package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// grep and glob must follow the same read boundary as read_file/list_files:
// confined to the workspace in an ordinary session, free to search anywhere in
// a project session (WriteRoots set).
func TestGrepAndGlobFollowProjectReadBoundary(t *testing.T) {
	project := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "ref.txt"), []byte("needle content\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	grepArgs, _ := json.Marshal(map[string]any{"pattern": "needle", "path": outside})
	projectIn := Input{Workspace: project, WriteRoots: []string{project}, Args: grepArgs}
	result := (grepTool{}).Execute(context.Background(), projectIn)
	if result.IsError || !strings.Contains(result.Content, "needle content") {
		t.Fatalf("project-session grep outside workspace should match, got: %+v", result)
	}

	globArgs, _ := json.Marshal(map[string]any{"pattern": "*.txt", "path": outside})
	result = (globTool{}).Execute(context.Background(), Input{Workspace: project, WriteRoots: []string{project}, Args: globArgs})
	if result.IsError || !strings.Contains(result.Content, "ref.txt") {
		t.Fatalf("project-session glob outside workspace should match, got: %+v", result)
	}

	// Ordinary sessions keep the old confinement.
	result = (grepTool{}).Execute(context.Background(), Input{Workspace: project, Args: grepArgs})
	if !result.IsError {
		t.Fatalf("ordinary-session grep outside workspace must be refused, got: %+v", result)
	}
	result = (globTool{}).Execute(context.Background(), Input{Workspace: project, Args: globArgs})
	if !result.IsError {
		t.Fatalf("ordinary-session glob outside workspace must be refused, got: %+v", result)
	}
}

// A line longer than the scanner buffer used to stop the file scan silently:
// no matches after it, no report. The tool must surface that the file scan
// stopped early.
func TestGrepReportsOverlongLineInsteadOfSilentStop(t *testing.T) {
	workspace := t.TempDir()
	content := strings.Repeat("x", 2*1024*1024) + "\nNEEDLE line\n"
	if err := os.WriteFile(filepath.Join(workspace, "big.txt"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	args, _ := json.Marshal(map[string]any{"pattern": "NEEDLE", "path": "."})
	result := (grepTool{}).Execute(context.Background(), Input{Workspace: workspace, Args: args})
	if result.IsError {
		t.Fatalf("grep errored: %s", result.Content)
	}
	if !strings.Contains(result.Content, "big.txt") || !strings.Contains(result.Content, "stopped") {
		t.Fatalf("overlong line not reported: %q", result.Content)
	}
}
