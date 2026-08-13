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
	"strconv"
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
	return "", fmt.Errorf("path %q is outside this project — writes are only allowed inside %s (reads and copies from elsewhere are fine)",
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
	return "Read a text file from the workspace. Returns NUMBER|CONTENT lines; only text after | belongs in edit_file.old_string. Use offset/limit for large files."
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
		// The cut can land inside a multi-byte rune; trimming up to three
		// trailing bytes keeps a genuine text file from reading as binary.
		for i := 0; i < 3 && len(data) > 0 && !utf8.Valid(data); i++ {
			data = data[:len(data)-1]
		}
	}
	if !utf8.Valid(data) {
		return Errorf("%s appears to be a binary file (%d bytes)", args.Path, fi.Size())
	}

	normalized := strings.ReplaceAll(string(data), "\r", "\n")
	if fileEOL(string(data)) != "\r" {
		// Only a genuinely CR-terminated (classic Mac) file splits on a lone
		// CR. In an LF or CRLF file a bare CR is data — a control character
		// inside a string literal, say — and splitting on it would number the
		// displayed lines differently from the file's real lines, handing the
		// model an anchor that edit_file then cannot find.
		normalized = strings.ReplaceAll(string(data), "\r\n", "\n")
	}
	lines := strings.Split(normalized, "\n")
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
	return "Replace an exact string in a file. The old_string must appear exactly once unless replace_all is set. Copy old_string from read_file output using only the content after the NUMBER| separator (never the line number). Preserve tabs/spaces exactly; line endings are matched automatically."
}
func (editFileTool) RequiresApproval() bool { return true }
func (editFileTool) Schema() map[string]any {
	return schema(map[string]any{
		"path":        prop("string", "File to edit."),
		"old_string":  prop("string", "Exact text to find, including indentation (tabs/spaces). Do not include read_file line numbers."),
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
	oldString, newString, count, how := resolveEditMatch(content, args.OldString, args.NewString)
	switch {
	case count == 0:
		// Last-resort recovery for one narrow shape: a stale single-line anchor
		// whose new_string only inserts adjacent text. Spliced by line index so
		// it can never touch any other occurrence; never combined with
		// replace_all, whose contract is "every exact occurrence".
		if !args.ReplaceAll {
			if updated, ok := spliceAdjacentInsertion(content, args.OldString, args.NewString); ok {
				if err := writeWithCheckpoint(in, path, []byte(updated), "edit_file"); err != nil {
					return Errorf("cannot write %s: %v", args.Path, err)
				}
				rel := relTo(in.Workspace, path)
				return Result{
					Content: fmt.Sprintf("Edited %s (1 replacement(s)) [matched unique near line for adjacent insertion]", rel),
					Meta:    map[string]any{"path": rel, "replacements": 1},
				}
			}
		}
		return Errorf("%s", editNotFoundMessage(args.Path, content, args.OldString))
	case count > 1 && !args.ReplaceAll:
		return Errorf("%s", editAmbiguousMessage(args.Path, content, oldString, count))
	}

	var updated string
	if args.ReplaceAll {
		updated = strings.ReplaceAll(content, oldString, newString)
	} else {
		updated = strings.Replace(content, oldString, newString, 1)
	}
	if err := writeWithCheckpoint(in, path, []byte(updated), "edit_file"); err != nil {
		return Errorf("cannot write %s: %v", args.Path, err)
	}
	replaced := count
	if !args.ReplaceAll {
		replaced = 1
	}
	rel := relTo(in.Workspace, path)
	msg := fmt.Sprintf("Edited %s (%d replacement(s))", rel, replaced)
	if how != "" {
		msg += " [" + how + "]"
	}
	return Result{
		Content: msg,
		Meta:    map[string]any{"path": rel, "replacements": replaced},
	}
}

// fileEOL returns the dominant newline sequence used in s, by majority. A
// single stray CRLF or CR in an otherwise-LF file must not decide the flavor:
// that used to convert every multi-line old_string away from what the file
// actually contains and permanently break edits on such files.
func fileEOL(s string) string {
	crlf := strings.Count(s, "\r\n")
	lf := strings.Count(s, "\n") - crlf
	cr := strings.Count(s, "\r") - crlf
	if crlf > 0 && crlf >= lf && crlf >= cr {
		return "\r\n"
	}
	if cr > lf {
		return "\r"
	}
	return "\n"
}

// eolOf reports the single newline flavor used in s, or "" when s has no
// newlines or mixes flavors.
func eolOf(s string) string {
	crlf := strings.Count(s, "\r\n")
	lf := strings.Count(s, "\n") - crlf
	cr := strings.Count(s, "\r") - crlf
	switch {
	case crlf > 0 && lf == 0 && cr == 0:
		return "\r\n"
	case lf > 0 && crlf == 0 && cr == 0:
		return "\n"
	case cr > 0 && crlf == 0 && lf == 0:
		return "\r"
	}
	return ""
}

// toEOL rewrites every newline in s to the given eol sequence.
func toEOL(s, eol string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	if eol == "\n" {
		return s
	}
	return strings.ReplaceAll(s, "\n", eol)
}

// stripReadFileLinePrefixes removes a NUMBER| prefix from every line when the
// whole block looks like a paste of read_file output. Returns ok=false when the
// string should be left alone (mixed or missing prefixes).
func stripReadFileLinePrefixes(s string) (string, bool) {
	if s == "" {
		return s, false
	}
	// Work on LF so CR in a pasted block does not hide the prefix.
	normalized := strings.ReplaceAll(s, "\r\n", "\n")
	normalized = strings.ReplaceAll(normalized, "\r", "\n")
	// Preserve whether the input ended with a newline so join stays faithful.
	trimTrailing := strings.HasSuffix(normalized, "\n")
	body := normalized
	if trimTrailing {
		body = strings.TrimSuffix(body, "\n")
	}
	if body == "" {
		return s, false
	}
	lines := strings.Split(body, "\n")
	out := make([]string, 0, len(lines))
	nums := make([]int, 0, len(lines))
	for _, line := range lines {
		i := strings.IndexByte(line, '|')
		if i <= 0 {
			return s, false
		}
		for _, c := range line[:i] {
			if c < '0' || c > '9' {
				return s, false
			}
		}
		n, err := strconv.Atoi(line[:i])
		if err != nil {
			return s, false
		}
		nums = append(nums, n)
		out = append(out, line[i+1:])
	}
	// read_file prefixes are always consecutive. A multi-line block whose
	// numbers are not is real pipe-delimited data — stripping it could make a
	// stale old_string match somewhere else entirely.
	for k := 1; k < len(nums); k++ {
		if nums[k] != nums[k-1]+1 {
			return s, false
		}
	}
	joined := strings.Join(out, "\n")
	if trimTrailing {
		joined += "\n"
	}
	return joined, true
}

// resolveEditMatch finds old/new strings that match content, recovering from
// the two failure modes that read_file → edit_file commonly hits:
//  1. LF vs CRLF (read_file always displays LF)
//  2. pasted NUMBER| line prefixes from read_file output
//
// The verbatim input is always tried first: when old_string already matches
// the file bytes exactly, no newline heuristic may reject or rewrite it.
// how is a short note for the success message when recovery was used; empty on
// a plain exact match.
func resolveEditMatch(content, oldIn, newIn string) (oldString, newString string, count int, how string) {
	// 0. Verbatim bytes. Mixed-EOL files and stray CR bytes made the old
	// normalize-first order fail edits whose old_string was byte-perfect.
	if c := strings.Count(content, oldIn); c > 0 {
		flav := eolOf(oldIn)
		if flav == "" {
			flav = fileEOL(content)
		}
		return oldIn, toEOL(newIn, flav), c, ""
	}

	// Candidate flavors for normalized matching: the file's dominant flavor
	// first, then the alternatives a mixed-EOL file may need.
	flavors := []string{fileEOL(content), "\n", "\r\n"}

	try := func(oldCand, newCand, label string) bool {
		tried := map[string]bool{oldIn: true} // verbatim already attempted
		for _, flav := range flavors {
			o := toEOL(oldCand, flav)
			if o == "" || tried[o] {
				continue
			}
			tried[o] = true
			c := strings.Count(content, o)
			if c == 0 {
				continue
			}
			oldString, newString, count, how = o, toEOL(newCand, flav), c, label
			return true
		}
		return false
	}

	// 1. EOL-normalized (covers LF paste against a CRLF file and vice versa).
	if try(oldIn, newIn, "normalized line endings to match file") {
		return
	}

	// 2. Strip NUMBER| prefixes from a full paste of read_file output.
	oldStripped, oldOK := stripReadFileLinePrefixes(oldIn)
	newStripped, newOK := stripReadFileLinePrefixes(newIn)
	if oldOK {
		newCand := newIn
		if newOK {
			newCand = newStripped
		}
		if try(oldStripped, newCand, "stripped read_file NUMBER| prefixes") {
			return
		}
	}

	return oldIn, newIn, 0, ""
}

// lineSpan is the [start,end) byte range of one line's text in the original
// content, excluding its \n, \r\n, or lone \r terminator.
type lineSpan struct{ start, end int }

func lineSpans(content string) []lineSpan {
	// A lone CR terminates a line only in a genuinely CR-based file. Anywhere
	// else it is data, and treating it as a break here would number lines
	// differently from what read_file displayed to the model.
	crIsTerminator := fileEOL(content) == "\r"
	var spans []lineSpan
	start := 0
	i := 0
	for i < len(content) {
		switch content[i] {
		case '\n':
			spans = append(spans, lineSpan{start, i})
			i++
			start = i
		case '\r':
			if i+1 < len(content) && content[i+1] == '\n' {
				spans = append(spans, lineSpan{start, i})
				i += 2
				start = i
				continue
			}
			if !crIsTerminator {
				i++
				continue
			}
			spans = append(spans, lineSpan{start, i})
			i++
			start = i
		default:
			i++
		}
	}
	if start < len(content) {
		spans = append(spans, lineSpan{start, len(content)})
	}
	return spans
}

// spliceAdjacentInsertion recovers one narrow failure shape: a common
// README/table operation copies a line from an earlier read, abbreviates one
// phrase, and adds a new row immediately before or after it. old_string is a
// single stale line, new_string only wraps it with inserted text, and exactly
// one file line is a clear similarity match. The inserted text is spliced at
// that line's byte range: the anchor line is kept byte-for-byte, and no other
// occurrence of similar text can be touched. Ordinary replacements remain
// exact-only.
func spliceAdjacentInsertion(content, oldIn, newIn string) (string, bool) {
	oldNorm := toEOL(oldIn, "\n")
	newNorm := toEOL(newIn, "\n")
	if oldNorm == "" || strings.Contains(oldNorm, "\n") {
		return "", false
	}
	// A NUMBER| line-prefix paste must never reach the fuzzy path. The anchor
	// carries the prefix, so its token set still scores high against the real
	// line, and the inserted text would be written to the file WITH its "13|"
	// prefix while the tool reported success. Exact matching handles prefixed
	// pastes properly via stripReadFileLinePrefixes; guessing must not.
	if prefixed, total := readFileLinePrefixCounts(oldIn); prefixed > 0 && total > 0 {
		return "", false
	}
	if prefixed, total := readFileLinePrefixCounts(newIn); prefixed > 0 && total > 0 {
		return "", false
	}

	insertAfter := false
	insert := ""
	switch {
	case strings.HasPrefix(newNorm, oldNorm+"\n"):
		insertAfter = true
		insert = strings.TrimPrefix(newNorm, oldNorm)
	case strings.HasSuffix(newNorm, "\n"+oldNorm):
		insert = strings.TrimSuffix(newNorm, oldNorm)
	default:
		return "", false
	}

	spans := lineSpans(content)
	best, second := -1.0, -1.0
	bestIdx := -1
	for i, sp := range spans {
		score := editLineSimilarity(oldNorm, content[sp.start:sp.end])
		if score > best {
			second, best = best, score
			bestIdx = i
		} else if score > second {
			second = score
		}
	}
	if bestIdx < 0 || best < 0.78 || (second >= 0 && best-second < 0.12) {
		return "", false
	}
	// Token overlap alone is not enough to claim "this is the same line,
	// lightly reworded". Sibling rows of one table share almost every token by
	// construction, so a deleted anchor row scores 0.8+ against a surviving row
	// while the runner-up (a heading, a paragraph) sits far below and clears
	// the margin gate too.
	//
	// Edit distance does not separate those cases either: "2026-02-01" vs
	// "2026-03-01" is a one-character difference, exactly like a typo. What
	// actually distinguishes them is WHICH characters differ. Digits are a
	// line's identifying detail — dates, ids, versions, counts — and a
	// rewording never changes them, while a different row almost always does.
	// Requiring identical digits admits the abbreviations this recovery exists
	// for and rejects the sibling-row confusion that silently corrupts files.
	anchor := content[spans[bestIdx].start:spans[bestIdx].end]
	if digitsOfLine(oldNorm) != digitsOfLine(anchor) {
		return "", false
	}
	// Belt and braces: even with matching digits, the line must still be a
	// light edit rather than a wholesale rewrite.
	if !nearEditDistance(oldNorm, anchor, 0.34) {
		return "", false
	}

	insert = toEOL(insert, fileEOL(content))
	sp := spans[bestIdx]
	if insertAfter {
		return content[:sp.end] + insert + content[sp.end:], true
	}
	return content[:sp.start] + insert + content[sp.start:], true
}

// digitsOfLine returns just the digits of s, in order. Two renderings of the
// same line keep the same digits; two different rows of one table almost never
// do.
func digitsOfLine(s string) string {
	out := make([]byte, 0, 16)
	for i := 0; i < len(s); i++ {
		if s[i] >= '0' && s[i] <= '9' {
			out = append(out, s[i])
		}
	}
	return string(out)
}

// nearEditDistance reports whether b is within maxRatio of a's length in
// Levenshtein distance — i.e. b looks like a lightly edited a rather than a
// different line that merely reuses the same vocabulary. Long lines are capped
// so the O(n*m) table stays small on an error-recovery path.
func nearEditDistance(a, b string, maxRatio float64) bool {
	const cap = 512
	if len(a) > cap {
		a = a[:cap]
	}
	if len(b) > cap {
		b = b[:cap]
	}
	if a == b {
		return true
	}
	longest := len(a)
	if len(b) > longest {
		longest = len(b)
	}
	if longest == 0 {
		return false
	}
	budget := int(float64(longest) * maxRatio)
	// A length gap alone can already exceed the budget; skip the table then.
	if diff := len(a) - len(b); diff > budget || -diff > budget {
		return false
	}

	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			del, ins, sub := prev[j]+1, cur[j-1]+1, prev[j-1]+cost
			best := del
			if ins < best {
				best = ins
			}
			if sub < best {
				best = sub
			}
			cur[j] = best
		}
		prev, cur = cur, prev
	}
	return prev[len(b)] <= budget
}

func editLineSimilarity(a, b string) float64 {
	aSet := editTokenSet(a)
	bSet := editTokenSet(b)
	if len(aSet) == 0 || len(bSet) == 0 {
		return 0
	}
	common := 0
	for token := range aSet {
		if _, ok := bSet[token]; ok {
			common++
		}
	}
	return float64(common) / float64(len(aSet)+len(bSet)-common)
}

func editTokenSet(s string) map[string]struct{} {
	set := make(map[string]struct{})
	start := -1
	flush := func(end int) {
		if start >= 0 && end-start >= 2 {
			set[strings.ToLower(s[start:end])] = struct{}{}
		}
		start = -1
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		isToken := (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_'
		if isToken {
			if start < 0 {
				start = i
			}
		} else {
			flush(i)
		}
	}
	flush(len(s))
	return set
}

// editNotFoundMessage explains why an edit missed, with actionable recovery
// hints for the model (line prefixes, tabs vs spaces, re-read).
func editNotFoundMessage(path, content, oldString string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "old_string not found in %s.", path)

	if stripped, ok := stripReadFileLinePrefixes(oldString); ok {
		if strings.Count(content, toEOL(stripped, fileEOL(content))) > 0 {
			b.WriteString(" Your old_string still includes read_file line numbers (NUMBER|). Call edit_file again with only the content after each |.")
			return b.String()
		}
	}

	if strings.Contains(content, "\t") && strings.Contains(oldString, " ") && !strings.Contains(oldString, "\t") {
		// Spaces in old_string might still be inter-word; only flag when a
		// detabbed view of the file contains the old_string.
		for _, width := range []int{2, 4, 8} {
			detabbed := expandTabs(content, width)
			if strings.Contains(detabbed, toEOL(oldString, "\n")) || strings.Contains(detabbed, oldString) {
				fmt.Fprintf(&b, " The file indents with TAB characters, but old_string uses spaces (tab width ~%d). Re-read the file and copy the content after NUMBER| without expanding tabs.", width)
				return b.String()
			}
		}
	}

	// A mixed paste usually means the model copied the display prefix from only
	// one or two read_file lines. Do not silently strip it: the unprefixed lines
	// may contain literal pipe characters.
	if prefixed, total := readFileLinePrefixCounts(oldString); prefixed > 0 && prefixed < total {
		b.WriteString(" Some old_string lines still include read_file line numbers (NUMBER|) while others do not. Remove every numeric prefix and keep only the text after each |, then retry from a fresh read.")
		return b.String()
	}
	if hint := nearMissHint(content, oldString); hint != "" {
		b.WriteByte(' ')
		b.WriteString(hint)
		return b.String()
	}

	b.WriteString(" Read the file first and copy only the content after the NUMBER| separator; preserve tabs, spaces, and indentation exactly.")
	return b.String()
}

func readFileLinePrefixCounts(s string) (prefixed, total int) {
	lines := strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
	if len(lines) > 1 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	for _, line := range lines {
		total++
		i := strings.IndexByte(line, '|')
		if i > 0 {
			allDigits := true
			for _, c := range line[:i] {
				if c < '0' || c > '9' {
					allDigits = false
					break
				}
			}
			if allDigits {
				prefixed++
			}
		}
	}
	return prefixed, total
}

func editAmbiguousMessage(path, content, oldString string, count int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "old_string appears %d times in %s; add unique surrounding context or set replace_all only if every occurrence should change.", count, path)
	if lines := occurrenceLines(content, oldString, 12); len(lines) > 0 {
		b.WriteString(" Current match line(s): ")
		for i, line := range lines {
			if i > 0 {
				b.WriteString(", ")
			}
			fmt.Fprintf(&b, "%d", line)
		}
		b.WriteByte('.')
	}
	b.WriteString(" Re-read the current file and include enough neighbouring lines for exactly one match.")
	return b.String()
}

func occurrenceLines(content, needle string, max int) []int {
	if needle == "" || max <= 0 {
		return nil
	}
	var lines []int
	for from := 0; from < len(content) && len(lines) < max; {
		i := strings.Index(content[from:], needle)
		if i < 0 {
			break
		}
		at := from + i
		lines = append(lines, 1+strings.Count(content[:at], "\n"))
		from = at + len(needle)
	}
	return lines
}

// nearMissHint reports a few real lines sharing a distinctive identifier with
// old_string. It is intentionally short and bounded: the tool should correct
// the model's stale context without dumping the file into an error response.
func nearMissHint(content, oldString string) string {
	for _, token := range identifierTokens(oldString) {
		if len(token) < 8 || strings.Contains(strings.ToLower(token), "read_file") {
			continue
		}
		var hits []string
		for i, line := range strings.Split(content, "\n") {
			if strings.Contains(line, token) {
				line = strings.TrimRight(line, "\r")
				if len(line) > 180 {
					line = line[:180] + "..."
				}
				hits = append(hits, fmt.Sprintf("line %d: %s", i+1, line))
				if len(hits) == 3 {
					break
				}
			}
		}
		if len(hits) > 0 {
			return "Near-miss lines sharing a token (re-read them; do not invent identifiers): " + strings.Join(hits, " ")
		}
	}
	return ""
}

func identifierTokens(s string) []string {
	var out []string
	start := -1
	flush := func(end int) {
		if start >= 0 && end-start >= 8 {
			out = append(out, s[start:end])
		}
		start = -1
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		isID := (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_'
		if isID {
			if start < 0 {
				start = i
			}
		} else {
			flush(i)
		}
	}
	flush(len(s))
	for i := 0; i < len(out); i++ {
		for j := i + 1; j < len(out); j++ {
			if len(out[j]) > len(out[i]) {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out
}

// expandTabs replaces leading and embedded tabs with spaces at the given width
// (stop-based), used only for mismatch diagnosis.
func expandTabs(s string, width int) string {
	if width <= 0 {
		width = 4
	}
	var b strings.Builder
	b.Grow(len(s))
	col := 0
	for _, r := range s {
		switch r {
		case '\t':
			spaces := width - (col % width)
			b.WriteString(strings.Repeat(" ", spaces))
			col += spaces
		case '\n':
			b.WriteByte('\n')
			col = 0
		case '\r':
			// Keep CR out of the comparison view; pair with LF handling above.
			continue
		default:
			b.WriteRune(r)
			col++
		}
	}
	return b.String()
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
