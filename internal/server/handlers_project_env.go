package server

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/enowdev/antares/internal/config"
)

// projectEnvFiles are the dotenv files a project may carry, most-specific first.
var projectEnvFiles = []string{".env", ".env.local", ".env.development", ".env.dev", ".env.example"}

// resolveProjectDir validates the ?dir= query as an absolute path that exists
// and is a directory. It is the shared guard for project handlers that inspect
// or read and write inside a caller-selected project folder.
func resolveProjectDir(w http.ResponseWriter, r *http.Request) (string, bool) {
	dir := strings.TrimSpace(r.URL.Query().Get("dir"))
	if dir == "" {
		writeError(w, http.StatusBadRequest, errors.New("dir is required"))
		return "", false
	}
	dir = filepath.Clean(config.Expand(dir))
	if !filepath.IsAbs(dir) {
		writeError(w, http.StatusBadRequest, errors.New("dir must be an absolute path"))
		return "", false
	}
	fi, err := os.Stat(dir)
	if err != nil || !fi.IsDir() {
		writeError(w, http.StatusBadRequest, errors.New("not a directory"))
		return "", false
	}
	return dir, true
}

// handleProjectEnv lists the dotenv files in a project and their parsed
// key/value pairs, for the sidebar's Environment viewer. It is gated behind the
// dashboard password because .env files routinely hold live credentials.
func (s *Server) handleProjectEnv(w http.ResponseWriter, r *http.Request) {
	if s.requireDashboardPassword(w, r) {
		return
	}
	dir, ok := resolveProjectDir(w, r)
	if !ok {
		return
	}

	type kv struct {
		Key   string `json:"key"`
		Value string `json:"value"`
	}
	type envFile struct {
		Name    string `json:"name"`
		Entries []kv   `json:"entries"`
		Raw     string `json:"raw"`
	}
	var files []envFile
	for _, name := range projectEnvFiles {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			continue
		}
		entries := make([]kv, 0)
		for _, k := range parseDotenv(string(data)) {
			entries = append(entries, kv{Key: k[0], Value: k[1]})
		}
		files = append(files, envFile{Name: name, Entries: entries, Raw: string(data)})
	}
	writeJSON(w, http.StatusOK, map[string]any{"files": files})
}

// handleSaveProjectEnv rewrites one dotenv file from an edited set of pairs.
// Only files already recognised as project env files may be written, and only
// inside the given project dir — a write cannot escape to an arbitrary path.
func (s *Server) handleSaveProjectEnv(w http.ResponseWriter, r *http.Request) {
	if s.requireDashboardPassword(w, r) {
		return
	}
	var body struct {
		Dir     string `json:"dir"`
		File    string `json:"file"`
		Entries []struct {
			Key   string `json:"key"`
			Value string `json:"value"`
		} `json:"entries"`
	}
	if err := decodeBody(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	dir := filepath.Clean(config.Expand(strings.TrimSpace(body.Dir)))
	if !filepath.IsAbs(dir) {
		writeError(w, http.StatusBadRequest, errors.New("dir must be an absolute path"))
		return
	}
	// The file name must be one of the known env files — no path separators, no
	// traversal — so a save can only ever touch the project's own dotenv files.
	name := strings.TrimSpace(body.File)
	if !slices.Contains(projectEnvFiles, name) {
		writeError(w, http.StatusBadRequest, errors.New("unknown env file"))
		return
	}
	var b strings.Builder
	for _, e := range body.Entries {
		key := strings.TrimSpace(e.Key)
		if key == "" {
			continue
		}
		b.WriteString(key)
		b.WriteString("=")
		b.WriteString(dotenvQuote(e.Value))
		b.WriteString("\n")
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(b.String()), 0o600); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"saved": true})
}

// handleProjectPlan returns the project's .antares/plan.md — raw text plus a
// light parse into checklist tasks and section headings for the sidebar's Plan
// viewer. Absent file is not an error: it returns exists=false.
func (s *Server) handleProjectPlan(w http.ResponseWriter, r *http.Request) {
	dir, ok := resolveProjectDir(w, r)
	if !ok {
		return
	}
	path := filepath.Join(dir, ".antares", "plan.md")
	data, err := os.ReadFile(path)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"exists": false})
		return
	}
	raw := string(data)
	writeJSON(w, http.StatusOK, map[string]any{
		"exists":   true,
		"raw":      raw,
		"tasks":    parsePlanTasks(raw),
		"sections": parsePlanSections(raw),
	})
}

// parseDotenv splits a dotenv file into [key, value] pairs, ignoring blanks,
// comments, and an optional leading `export`.
func parseDotenv(s string) [][2]string {
	var out [][2]string
	for _, line := range strings.Split(s, "\n") {
		t := strings.TrimSpace(line)
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		t = strings.TrimPrefix(t, "export ")
		eq := strings.IndexByte(t, '=')
		if eq <= 0 {
			continue
		}
		key := strings.TrimSpace(t[:eq])
		val := strings.TrimSpace(t[eq+1:])
		val = dotenvValue(val)
		out = append(out, [2]string{key, val})
	}
	return out
}

// dotenvValue turns the raw right-hand side of a dotenv line into the actual
// value: a quoted value keeps its content verbatim (a "#" inside quotes is
// literal); an unquoted value has any trailing inline comment (" # …") stripped.
func dotenvValue(v string) string {
	if len(v) >= 2 {
		if (v[0] == '"' && v[len(v)-1] == '"') || (v[0] == '\'' && v[len(v)-1] == '\'') {
			return v[1 : len(v)-1]
		}
	}
	// Unquoted: a "#" that follows whitespace begins a comment. A "#" with no
	// space before it is part of the value (e.g. a URL fragment or colour hex).
	for i := 1; i < len(v); i++ {
		if v[i] == '#' && (v[i-1] == ' ' || v[i-1] == '\t') {
			v = v[:i]
			break
		}
	}
	return strings.TrimSpace(v)
}

// dotenvQuote wraps a value in double quotes when it contains characters that
// would otherwise break parsing (spaces, #, quotes, newlines).
func dotenvQuote(v string) string {
	if v == "" {
		return ""
	}
	if strings.ContainsAny(v, " \t#\"'\n") {
		v = strings.ReplaceAll(v, "\\", "\\\\")
		v = strings.ReplaceAll(v, "\"", "\\\"")
		v = strings.ReplaceAll(v, "\n", "\\n")
		return "\"" + v + "\""
	}
	return v
}

// planTask is one checklist item parsed from plan.md.
type planTask struct {
	Text string `json:"text"`
	Done bool   `json:"done"`
	// Section is the nearest preceding heading, so the viewer can group tasks.
	Section string `json:"section,omitempty"`
}

// parsePlanTasks pulls Markdown checkboxes (- [ ] / - [x]) out of the plan,
// tagging each with the heading it falls under.
func parsePlanTasks(md string) []planTask {
	var out []planTask
	section := ""
	for _, line := range strings.Split(md, "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "#") {
			section = strings.TrimSpace(strings.TrimLeft(t, "# "))
			continue
		}
		low := strings.ToLower(t)
		switch {
		case strings.HasPrefix(low, "- [x]") || strings.HasPrefix(low, "* [x]"):
			out = append(out, planTask{Text: strings.TrimSpace(t[5:]), Done: true, Section: section})
		case strings.HasPrefix(low, "- [ ]") || strings.HasPrefix(low, "* [ ]"):
			out = append(out, planTask{Text: strings.TrimSpace(t[5:]), Done: false, Section: section})
		}
	}
	return out
}

// planSection is a heading plus the body under it, for the structured view.
type planSection struct {
	Title string `json:"title"`
	Level int    `json:"level"`
	Body  string `json:"body"`
}

// parsePlanSections splits the plan by Markdown headings so the viewer can show
// PRD/TRD/etc. as collapsible blocks.
func parsePlanSections(md string) []planSection {
	var out []planSection
	var cur *planSection
	var body []string
	flush := func() {
		if cur != nil {
			cur.Body = strings.TrimSpace(strings.Join(body, "\n"))
			out = append(out, *cur)
		}
		body = nil
	}
	for _, line := range strings.Split(md, "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "#") {
			level := len(t) - len(strings.TrimLeft(t, "#"))
			flush()
			cur = &planSection{Title: strings.TrimSpace(strings.TrimLeft(t, "# ")), Level: level}
			continue
		}
		if cur != nil {
			body = append(body, line)
		}
	}
	flush()
	return out
}
