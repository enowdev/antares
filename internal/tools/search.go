package tools

import (
	"bufio"
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"
)

// ---- glob -------------------------------------------------------------------

type globTool struct{}

func (globTool) Name() string { return "glob" }
func (globTool) Description() string {
	return "Find files by glob pattern (e.g. **/*.go, src/**/*.tsx). Results are sorted by modification time, newest first."
}
func (globTool) Schema() map[string]any {
	return schema(map[string]any{
		"pattern": prop("string", "Glob pattern. ** matches any depth."),
		"path":    propDefault("string", "Directory to search from.", "."),
		"limit":   propDefault("integer", "Maximum results.", 200),
	}, "pattern")
}

func (globTool) Execute(_ context.Context, in Input) Result {
	var args struct {
		Pattern string `json:"pattern"`
		Path    string `json:"path"`
		Limit   int    `json:"limit"`
	}
	if err := in.Bind(&args); err != nil {
		return Errorf("%v", err)
	}
	if strings.TrimSpace(args.Pattern) == "" {
		return Errorf("pattern is required")
	}
	if args.Path == "" {
		args.Path = "."
	}
	if args.Limit <= 0 || args.Limit > 2000 {
		args.Limit = 200
	}
	// Same read boundary as read_file: workspace-confined in an ordinary
	// session, anywhere in a project session.
	root, err := resolveRead(in, args.Path)
	if err != nil {
		return Errorf("%v", err)
	}
	re, err := globToRegexp(args.Pattern)
	if err != nil {
		return Errorf("invalid pattern: %v", err)
	}

	type hit struct {
		path string
		mod  int64
	}
	var hits []hit
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if ignoredDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if !re.MatchString(rel) {
			return nil
		}
		var mod int64
		if fi, err := d.Info(); err == nil {
			mod = fi.ModTime().UnixNano()
		}
		hits = append(hits, hit{path: rel, mod: mod})
		return nil
	})

	sort.Slice(hits, func(i, j int) bool { return hits[i].mod > hits[j].mod })
	truncated := false
	if len(hits) > args.Limit {
		hits = hits[:args.Limit]
		truncated = true
	}
	if len(hits) == 0 {
		return Text(fmt.Sprintf("No files match %q under %s", args.Pattern, relTo(in.Workspace, root)))
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d match(es) for %q:\n", len(hits), args.Pattern)
	for _, h := range hits {
		b.WriteString(h.path + "\n")
	}
	if truncated {
		b.WriteString("… truncated\n")
	}
	return Text(b.String())
}

// globToRegexp compiles a glob (with ** support) into an anchored regexp.
func globToRegexp(pattern string) (*regexp.Regexp, error) {
	var b strings.Builder
	b.WriteString("^")
	for i := 0; i < len(pattern); i++ {
		c := pattern[i]
		switch c {
		case '*':
			if i+1 < len(pattern) && pattern[i+1] == '*' {
				i++
				// **/ matches zero or more path segments.
				if i+1 < len(pattern) && pattern[i+1] == '/' {
					i++
					b.WriteString("(?:[^/]*/)*")
					continue
				}
				b.WriteString(".*")
				continue
			}
			b.WriteString("[^/]*")
		case '?':
			b.WriteString("[^/]")
		case '.', '+', '(', ')', '|', '^', '$', '{', '}', '\\':
			b.WriteByte('\\')
			b.WriteByte(c)
		case '[':
			j := i + 1
			for j < len(pattern) && pattern[j] != ']' {
				j++
			}
			if j >= len(pattern) {
				b.WriteString("\\[")
				continue
			}
			b.WriteString(pattern[i : j+1])
			i = j
		default:
			b.WriteByte(c)
		}
	}
	b.WriteString("$")
	return regexp.Compile(b.String())
}

// ---- grep -------------------------------------------------------------------

type grepTool struct{}

func (grepTool) Name() string { return "grep" }
func (grepTool) Description() string {
	return "Search file contents with a regular expression. Returns matching lines with file path and line number."
}
func (grepTool) Schema() map[string]any {
	return schema(map[string]any{
		"pattern":        prop("string", "Go/RE2 regular expression."),
		"path":           propDefault("string", "Directory or file to search.", "."),
		"include":        prop("string", "Only search files matching this glob, e.g. *.go"),
		"case_sensitive": propDefault("boolean", "Match case exactly.", false),
		"context":        propDefault("integer", "Lines of context around each match.", 0),
		"limit":          propDefault("integer", "Maximum matches to return.", 100),
	}, "pattern")
}

func (grepTool) Execute(ctx context.Context, in Input) Result {
	var args struct {
		Pattern       string `json:"pattern"`
		Path          string `json:"path"`
		Include       string `json:"include"`
		CaseSensitive bool   `json:"case_sensitive"`
		Context       int    `json:"context"`
		Limit         int    `json:"limit"`
	}
	if err := in.Bind(&args); err != nil {
		return Errorf("%v", err)
	}
	if strings.TrimSpace(args.Pattern) == "" {
		return Errorf("pattern is required")
	}
	if args.Path == "" {
		args.Path = "."
	}
	if args.Limit <= 0 || args.Limit > 1000 {
		args.Limit = 100
	}
	expr := args.Pattern
	if !args.CaseSensitive {
		expr = "(?i)" + expr
	}
	re, err := regexp.Compile(expr)
	if err != nil {
		return Errorf("invalid regular expression: %v", err)
	}
	// Same read boundary as read_file: workspace-confined in an ordinary
	// session, anywhere in a project session.
	root, err := resolveRead(in, args.Path)
	if err != nil {
		return Errorf("%v", err)
	}
	var includeRe *regexp.Regexp
	if args.Include != "" {
		if includeRe, err = globToRegexp(args.Include); err != nil {
			return Errorf("invalid include pattern: %v", err)
		}
	}

	var (
		b        strings.Builder
		matches  int
		files    int
		stopped  bool
		warnings []string
	)

	searchFile := func(path, display string) error {
		if matches >= args.Limit {
			stopped = true
			return filepath.SkipAll
		}
		f, err := os.Open(path)
		if err != nil {
			return nil
		}
		defer f.Close()

		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		var window []string
		lineNo := 0
		fileMatched := false
		pending := 0

		for sc.Scan() {
			lineNo++
			line := sc.Text()
			if lineNo == 1 && !utf8.ValidString(line) {
				return nil // binary
			}
			if re.MatchString(line) {
				if !fileMatched {
					fileMatched = true
					files++
					fmt.Fprintf(&b, "\n%s\n", display)
				}
				for i, prev := range window {
					fmt.Fprintf(&b, "%6d-\t%s\n", lineNo-len(window)+i, prev)
				}
				window = nil
				fmt.Fprintf(&b, "%6d:\t%s\n", lineNo, truncateLine(line))
				matches++
				pending = args.Context
				if matches >= args.Limit {
					stopped = true
					return filepath.SkipAll
				}
				continue
			}
			if pending > 0 {
				fmt.Fprintf(&b, "%6d-\t%s\n", lineNo, truncateLine(line))
				pending--
				continue
			}
			if args.Context > 0 {
				window = append(window, truncateLine(line))
				if len(window) > args.Context {
					window = window[1:]
				}
			}
		}
		// A line longer than the scanner buffer aborts the scan; say so instead
		// of silently reporting the rest of the file as match-free.
		if err := sc.Err(); err != nil && len(warnings) < 8 {
			warnings = append(warnings, fmt.Sprintf("%s: search stopped at line %d: %v", display, lineNo+1, err))
		}
		return nil
	}

	fi, err := os.Stat(root)
	if err != nil {
		return Errorf("cannot search %s: %v", args.Path, err)
	}
	if fi.IsDir() {
		_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if ctx.Err() != nil {
				return filepath.SkipAll
			}
			if d.IsDir() {
				if ignoredDirs[d.Name()] {
					return filepath.SkipDir
				}
				return nil
			}
			rel, _ := filepath.Rel(root, p)
			rel = filepath.ToSlash(rel)
			if includeRe != nil && !includeRe.MatchString(rel) && !includeRe.MatchString(filepath.Base(rel)) {
				return nil
			}
			if info, err := d.Info(); err == nil && info.Size() > 8*1024*1024 {
				return nil
			}
			return searchFile(p, rel)
		})
	} else {
		_ = searchFile(root, relTo(in.Workspace, root))
	}

	warn := ""
	if len(warnings) > 0 {
		warn = "\nwarning: " + strings.Join(warnings, "\nwarning: ")
	}
	if matches == 0 {
		return Text(fmt.Sprintf("No matches for %q under %s%s", args.Pattern, relTo(in.Workspace, root), warn))
	}
	header := fmt.Sprintf("%d match(es) in %d file(s) for %q", matches, files, args.Pattern)
	if stopped {
		header += " (limit reached)"
	}
	return Text(header + "\n" + b.String() + warn)
}

func truncateLine(s string) string {
	if len(s) <= 400 {
		return s
	}
	return s[:400] + "…"
}
