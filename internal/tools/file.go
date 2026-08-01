package tools

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"
)

// maxReadBytes caps a single read so a stray large file cannot blow the context.
const maxReadBytes = 400 * 1024

// resolvePath joins a user-supplied path against the workspace and refuses to
// escape it, which is the sandbox boundary for every file tool. It is the
// confined form: an ordinary (non-project) session uses it for both reads and
// writes. Project sessions use resolveRead / resolveWrite instead.
func resolvePath(workspace, p string) (string, error) {
	clean, err := cleanPath(workspace, p)
	if err != nil {
		return "", err
	}
	if withinRoot(workspace, clean) {
		return clean, nil
	}
	return "", fmt.Errorf("path %q is outside the workspace (%s)", p, workspace)
}

// cleanPath expands ~, joins a relative path onto workspace, and cleans it,
// without any boundary check.
func cleanPath(workspace, p string) (string, error) {
	if strings.TrimSpace(p) == "" {
		return "", errors.New("path is required")
	}
	if strings.HasPrefix(p, "~") {
		if home, err := os.UserHomeDir(); err == nil {
			p = filepath.Join(home, strings.TrimPrefix(p, "~"))
		}
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(workspace, p)
	}
	return filepath.Clean(p), nil
}

// within reports whether clean resolves inside root (through symlinks).
func withinRoot(root, clean string) bool {
	r, err := filepath.Abs(root)
	if err != nil {
		return false
	}
	abs, err := filepath.Abs(clean)
	if err != nil {
		return false
	}
	if real, err := filepath.EvalSymlinks(r); err == nil {
		r = real
	}
	// A write target frequently does not exist yet (creating a new file, or a
	// file in a new directory), so EvalSymlinks on the full path would fail and
	// leave symlinks in the parent unresolved — on macOS the temp/root dirs are
	// themselves symlinks, so an unresolved target then looks "outside" a
	// resolved root. Resolve the deepest existing ancestor instead and re-append
	// the missing tail, so the comparison is symlink-correct either way.
	abs = evalExisting(abs)
	rel, err := filepath.Rel(r, abs)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// evalExisting resolves symlinks over the longest existing prefix of p and
// re-joins the not-yet-created tail, so paths that point at files or dirs that
// do not exist yet still compare correctly against a symlink-resolved root.
func evalExisting(p string) string {
	tail := ""
	cur := p
	for {
		if real, err := filepath.EvalSymlinks(cur); err == nil {
			return filepath.Join(real, tail)
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			// Reached the root without finding an existing ancestor.
			return p
		}
		tail = filepath.Join(filepath.Base(cur), tail)
		cur = parent
	}
}

// resolveRead resolves a path for a READ. In a project session (WriteRoots set)
// reads are allowed anywhere on the machine, so the agent can read and copy
// files from outside the project. In an ordinary session reads stay confined to
// the workspace, exactly as before.
func resolveRead(in Input, p string) (string, error) {
	if len(in.WriteRoots) == 0 {
		return resolvePath(in.Workspace, p)
	}
	return cleanPath(in.Workspace, p)
}

// resolveWrite resolves a path for a WRITE and confines it to the allowed roots.
// An ordinary session confines to the workspace; a project session confines to
// its WriteRoots (the project folder plus the antares workspace). Writing
// anywhere else is refused — this is a hard boundary the model cannot cross.
func resolveWrite(in Input, p string) (string, error) {
	if len(in.WriteRoots) == 0 {
		return resolvePath(in.Workspace, p)
	}
	clean, err := cleanPath(in.Workspace, p)
	if err != nil {
		return "", err
	}
	for _, root := range in.WriteRoots {
		if strings.TrimSpace(root) != "" && withinRoot(root, clean) {
			return clean, nil
		}
	}
	return "", fmt.Errorf("path %q is outside this project — writes are only allowed inside %s (reads and copies from elsewhere are fine). Create temporary scripts inside the project or Antares workspace instead of /tmp",
		p, strings.Join(in.WriteRoots, ", "))
}

func relTo(workspace, p string) string {
	if rel, err := filepath.Rel(workspace, p); err == nil && !strings.HasPrefix(rel, "..") {
		return rel
	}
	return p
}

// ---- read_file --------------------------------------------------------------

type readFileTool struct{}

func (readFileTool) Name() string { return "read_file" }
func (readFileTool) Description() string {
	return "Read a text file from the workspace. Returns lines as NUMBER|CONTENT; the | separates metadata from exact file content and must not be copied into edits. Use offset/limit for large files."
}
func (readFileTool) Schema() map[string]any {
	return schema(map[string]any{
		"path":   prop("string", "File path, relative to the workspace or absolute inside it."),
		"offset": propDefault("integer", "1-indexed line to start from.", 1),
		"limit":  propDefault("integer", "Maximum number of lines to return.", 2000),
	}, "path")
}

func (readFileTool) Execute(_ context.Context, in Input) Result {
	var args struct {
		Path   string `json:"path"`
		Offset int    `json:"offset"`
		Limit  int    `json:"limit"`
	}
	if err := in.Bind(&args); err != nil {
		return Errorf("%v", err)
	}
	path, err := resolveRead(in, args.Path)
	if err != nil {
		return Errorf("%v", err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		return Errorf("cannot read %s: %v", args.Path, err)
	}
	if fi.IsDir() {
		return Errorf("%s is a directory; use list_files instead", args.Path)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return Errorf("cannot read %s: %v", args.Path, err)
	}
	truncatedBytes := false
	if len(data) > maxReadBytes {
		data = data[:maxReadBytes]
		truncatedBytes = true
	}
	if !utf8.Valid(data) {
		return Errorf("%s appears to be a binary file (%d bytes)", args.Path, fi.Size())
	}

	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	offset := args.Offset
	if offset <= 0 {
		offset = 1
	}
	limit := args.Limit
	if limit <= 0 {
		limit = 2000
	}
	start := offset - 1
	if start > len(lines) {
		return Errorf("offset %d is past end of file (%d lines)", offset, len(lines))
	}
	end := start + limit
	if end > len(lines) {
		end = len(lines)
	}

	var b strings.Builder
	for i := start; i < end; i++ {
		fmt.Fprintf(&b, "%d|%s\n", i+1, lines[i])
	}
	if end < len(lines) {
		fmt.Fprintf(&b, "\n… %d more lines (use offset=%d to continue)\n", len(lines)-end, end+1)
	}
	if truncatedBytes {
		b.WriteString("\n… file truncated at 400 KB\n")
	}
	return Result{Content: b.String(), Meta: map[string]any{"path": relTo(in.Workspace, path), "lines": len(lines)}}
}

// ---- write_file -------------------------------------------------------------

type writeFileTool struct{}

func (writeFileTool) Name() string { return "write_file" }
func (writeFileTool) Description() string {
	return "Create or overwrite a file with the given content. Parent directories are created automatically."
}
func (writeFileTool) RequiresApproval() bool { return true }
func (writeFileTool) Schema() map[string]any {
	return schema(map[string]any{
		"path":    prop("string", "Destination file path."),
		"content": prop("string", "Full file content to write."),
		"append":  propDefault("boolean", "Append instead of overwriting.", false),
	}, "path", "content")
}

func (writeFileTool) Execute(_ context.Context, in Input) Result {
	var args struct {
		Path    string `json:"path"`
		Content string `json:"content"`
		Append  bool   `json:"append"`
	}
	if err := in.Bind(&args); err != nil {
		return Errorf("%v", err)
	}
	path, err := resolveWrite(in, args.Path)
	if err != nil {
		return Errorf("%v", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return Errorf("cannot create parent directory: %v", err)
	}

	existed := false
	if _, err := os.Stat(path); err == nil {
		existed = true
	}
	if args.Append {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			return Errorf("cannot open %s: %v", args.Path, err)
		}
		defer f.Close()
		if _, err := f.WriteString(args.Content); err != nil {
			return Errorf("cannot append to %s: %v", args.Path, err)
		}
	} else if err := writeWithCheckpoint(in, path, []byte(args.Content), "write_file"); err != nil {
		return Errorf("cannot write %s: %v", args.Path, err)
	}

	verb := "Created"
	if existed {
		verb = "Updated"
	}
	if args.Append {
		verb = "Appended to"
	}
	rel := relTo(in.Workspace, path)
	return Result{
		Content: fmt.Sprintf("%s %s (%d bytes, %d lines)", verb, rel, len(args.Content), strings.Count(args.Content, "\n")+1),
		Meta:    map[string]any{"path": rel, "bytes": len(args.Content)},
	}
}

// ---- edit_file --------------------------------------------------------------

type editFileTool struct{}

func (editFileTool) Name() string { return "edit_file" }
func (editFileTool) Description() string {
	return "Replace an exact string in a file. The old_string must appear exactly once unless replace_all is set."
}
func (editFileTool) RequiresApproval() bool { return true }
func (editFileTool) Schema() map[string]any {
	return schema(map[string]any{
		"path":        prop("string", "File to edit."),
		"old_string":  prop("string", "Exact text to find, including indentation."),
		"new_string":  prop("string", "Replacement text."),
		"replace_all": propDefault("boolean", "Replace every occurrence.", false),
	}, "path", "old_string", "new_string")
}

func (editFileTool) Execute(_ context.Context, in Input) Result {
	var args struct {
		Path       string `json:"path"`
		OldString  string `json:"old_string"`
		NewString  string `json:"new_string"`
		ReplaceAll bool   `json:"replace_all"`
	}
	if err := in.Bind(&args); err != nil {
		return Errorf("%v", err)
	}
	if args.OldString == args.NewString {
		return Errorf("old_string and new_string are identical")
	}
	path, err := resolveWrite(in, args.Path)
	if err != nil {
		return Errorf("%v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Errorf("cannot read %s: %v", args.Path, err)
	}
	content := string(data)
	count := strings.Count(content, args.OldString)
	switch {
	case count == 0:
		return Errorf("old_string not found in %s. Read the file first and copy only the content after the NUMBER| separator; preserve indentation exactly.", args.Path)
	case count > 1 && !args.ReplaceAll:
		return Errorf("old_string appears %d times in %s; add more surrounding context or set replace_all", count, args.Path)
	}

	updated := content
	if args.ReplaceAll {
		updated = strings.ReplaceAll(content, args.OldString, args.NewString)
	} else {
		updated = strings.Replace(content, args.OldString, args.NewString, 1)
	}
	if err := writeWithCheckpoint(in, path, []byte(updated), "edit_file"); err != nil {
		return Errorf("cannot write %s: %v", args.Path, err)
	}
	replaced := count
	if !args.ReplaceAll {
		replaced = 1
	}
	rel := relTo(in.Workspace, path)
	return Result{
		Content: fmt.Sprintf("Edited %s (%d replacement(s))", rel, replaced),
		Meta:    map[string]any{"path": rel, "replacements": replaced},
	}
}

// ---- list_files -------------------------------------------------------------

type listFilesTool struct{}

func (listFilesTool) Name() string { return "list_files" }
func (listFilesTool) Description() string {
	return "List directory entries. Set recursive to walk subdirectories (depth limited)."
}
func (listFilesTool) Schema() map[string]any {
	return schema(map[string]any{
		"path":      propDefault("string", "Directory to list.", "."),
		"recursive": propDefault("boolean", "Walk subdirectories.", false),
		"depth":     propDefault("integer", "Maximum recursion depth.", 3),
		"all":       propDefault("boolean", "Include dotfiles.", false),
	})
}

var ignoredDirs = map[string]bool{
	".git": true, "node_modules": true, "__pycache__": true, ".venv": true,
	"venv": true, "dist": true, "build": true, ".next": true, "target": true,
	".cache": true, ".idea": true, ".DS_Store": true,
}

func (listFilesTool) Execute(_ context.Context, in Input) Result {
	var args struct {
		Path      string `json:"path"`
		Recursive bool   `json:"recursive"`
		Depth     int    `json:"depth"`
		All       bool   `json:"all"`
	}
	if err := in.Bind(&args); err != nil {
		return Errorf("%v", err)
	}
	if args.Path == "" {
		args.Path = "."
	}
	if args.Depth <= 0 {
		args.Depth = 3
	}
	root, err := resolveRead(in, args.Path)
	if err != nil {
		return Errorf("%v", err)
	}

	var out []string
	count := 0
	var walk func(dir string, depth int, prefix string) error
	walk = func(dir string, depth int, prefix string) error {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return err
		}
		sort.Slice(entries, func(i, j int) bool {
			if entries[i].IsDir() != entries[j].IsDir() {
				return entries[i].IsDir()
			}
			return entries[i].Name() < entries[j].Name()
		})
		for _, e := range entries {
			name := e.Name()
			if !args.All && strings.HasPrefix(name, ".") {
				continue
			}
			if e.IsDir() && ignoredDirs[name] {
				out = append(out, prefix+name+"/ (skipped)")
				continue
			}
			if count >= 2000 {
				return errTooMany
			}
			count++
			if e.IsDir() {
				out = append(out, prefix+name+"/")
				if args.Recursive && depth > 1 {
					if err := walk(filepath.Join(dir, name), depth-1, prefix+"  "); err != nil {
						return err
					}
				}
				continue
			}
			size := int64(0)
			if fi, err := e.Info(); err == nil {
				size = fi.Size()
			}
			out = append(out, fmt.Sprintf("%s%s (%s)", prefix, name, humanBytes(size)))
		}
		return nil
	}

	err = walk(root, args.Depth, "")
	if err != nil && !errors.Is(err, errTooMany) {
		return Errorf("cannot list %s: %v", args.Path, err)
	}
	if len(out) == 0 {
		return Text(fmt.Sprintf("%s is empty", relTo(in.Workspace, root)))
	}
	header := fmt.Sprintf("%s (%d entries)\n", relTo(in.Workspace, root), len(out))
	if errors.Is(err, errTooMany) {
		header += "… truncated at 2000 entries\n"
	}
	return Text(header + strings.Join(out, "\n"))
}

var errTooMany = errors.New("too many entries")

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for n/div >= unit && exp < 3 {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGT"[exp])
}

// writeWithCheckpoint keeps a copy of what is there before overwriting it, so
// the change can be undone. A missing checkpoint store is not an error — it
// only means there is nothing to roll back to.
func writeWithCheckpoint(in Input, path string, content []byte, tool string) error {
	if in.Deps != nil && in.Deps.Checkpoint != nil {
		in.Deps.Checkpoint(in.SessionID, path, tool)
	}
	if err := os.WriteFile(path, content, 0o644); err != nil {
		return err
	}
	// Record what we just wrote, so an edit-message rollback can distinguish our
	// own output from a later manual edit of the same file.
	if in.Deps != nil && in.Deps.RecordResult != nil {
		sum := sha256.Sum256(content)
		in.Deps.RecordResult(in.SessionID, path, hex.EncodeToString(sum[:]))
	}
	return nil
}
