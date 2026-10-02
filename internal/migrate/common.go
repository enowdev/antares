package migrate

// Shared helpers for sources. Every source_<id>.go reads a different layout,
// but the pieces are the same: a .env file, a JSON5 or YAML config, a
// MEMORY.md to split, a folder of SKILL.md skills, a vendor name to map to an
// Antares provider, and Items to build with the right status. Keep these
// source-neutral; anything specific to one agent belongs in its own file.

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode"

	"github.com/enowdev/antares/internal/cron"
	"gopkg.in/yaml.v3"
)

// ErrNotFound is what Detect returns when the agent is not installed at the
// default locations (or at root). DetectAll also treats any other error, or a
// Detection with an empty Root, as "not installed".
var ErrNotFound = errors.New("not installed")

// MultiDetector is an optional Source extension for agents that can have
// several installs side by side (Hermes profiles, OpenClaw agents). DetectAll
// prefers it over Detect; each Detection's Profile tells them apart, and Plan
// receives the chosen one.
type MultiDetector interface {
	DetectAll(ctx context.Context, root string) ([]Detection, error)
}

// ---- Optional Env extensions -------------------------------------------------
//
// Env is the frozen contract; these optional interfaces let a richer Env (the
// real one in env.go) answer finer questions. Builders below type-assert for
// them and fall back to the coarse answer when absent.

// ProviderMatcher reports whether provider id already holds exactly this base
// URL and key, so re-importing it is a no-op rather than a conflict.
type ProviderMatcher interface {
	ProviderMatches(id, baseURL, apiKey string) bool
}

// DefaultModelChecker reports whether Antares already has a working default
// model (a model plus a provider that can answer).
type DefaultModelChecker interface {
	HasDefaultModel() bool
}

// RAGChecker reports whether RAG is enabled, so knowledge items can be marked
// unsupported ("RAG is off") at plan time.
type RAGChecker interface {
	RAGEnabled() bool
}

// ---- Small file helpers -------------------------------------------------------

// HomeDir is the user's home directory; tests may override it.
var HomeDir = func() string {
	h, _ := os.UserHomeDir()
	return h
}

// ExpandHome turns a leading "~" into the user's home directory.
func ExpandHome(p string) string {
	p = strings.TrimSpace(p)
	if p == "~" {
		return HomeDir()
	}
	if strings.HasPrefix(p, "~/") {
		return filepath.Join(HomeDir(), p[2:])
	}
	return p
}

// IsDir reports whether path exists and is a directory.
func IsDir(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.IsDir()
}

// IsFile reports whether path exists and is a regular file.
func IsFile(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.Mode().IsRegular()
}

// maxReadSize bounds every file a source reads; nothing an agent keeps as
// config or memory is legitimately bigger.
const maxReadSize = 8 << 20

// ReadText returns a file's contents trimmed, or "" when it is missing,
// unreadable or larger than 8 MB.
func ReadText(path string) string {
	st, err := os.Stat(path)
	if err != nil || !st.Mode().IsRegular() || st.Size() > maxReadSize {
		return ""
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// ReadDotEnv parses a KEY=VALUE file (blank lines and # comments ignored,
// optional "export " prefix, single/double quotes stripped, inline " #"
// comments dropped from unquoted values). A missing file yields an empty map.
func ReadDotEnv(path string) map[string]string {
	out := map[string]string{}
	f, err := os.Open(path)
	if err != nil {
		return out
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		if len(v) >= 2 && (v[0] == '"' && v[len(v)-1] == '"' || v[0] == '\'' && v[len(v)-1] == '\'') {
			v = v[1 : len(v)-1]
		} else if i := strings.Index(v, " #"); i >= 0 {
			v = strings.TrimSpace(v[:i])
		}
		if k != "" {
			out[k] = v
		}
	}
	return out
}

// ExpandVars replaces ${VAR} and $VAR references with values from env, then
// from the process environment. ok is false when a referenced variable is
// unset anywhere (the caller then marks the item needs_input).
func ExpandVars(s string, env map[string]string) (string, bool) {
	ok := true
	out := os.Expand(s, func(name string) string {
		if v, found := env[name]; found && v != "" {
			return v
		}
		if v, found := os.LookupEnv(name); found && v != "" {
			return v
		}
		ok = false
		return ""
	})
	return out, ok
}

// ReadYAML decodes a YAML file into v.
func ReadYAML(path string, v any) error {
	b, err := readLimited(path)
	if err != nil {
		return err
	}
	return yaml.Unmarshal(b, v)
}

// ReadJSON decodes a JSON file into v.
func ReadJSON(path string, v any) error {
	b, err := readLimited(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

// ReadJSON5 decodes a JSON5 file (comments, trailing commas, unquoted keys,
// single-quoted strings, hex numbers) into v via JSON5ToJSON.
func ReadJSON5(path string, v any) error {
	b, err := readLimited(path)
	if err != nil {
		return err
	}
	j, err := JSON5ToJSON(b)
	if err != nil {
		return fmt.Errorf("%s: %w", filepath.Base(path), err)
	}
	return json.Unmarshal(j, v)
}

func readLimited(path string) ([]byte, error) {
	st, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if st.Size() > maxReadSize {
		return nil, fmt.Errorf("%s is too large", filepath.Base(path))
	}
	return os.ReadFile(path)
}

// JSON5ToJSON rewrites JSON5 text as strict JSON. It handles the JSON5 that
// config files actually use: // and /* */ comments, trailing commas, unquoted
// identifier keys, single-quoted strings, backslash line continuations,
// hexadecimal integers, leading '+' and bare leading/trailing decimal points.
// Infinity and NaN have no JSON form and become null.
func JSON5ToJSON(src []byte) ([]byte, error) {
	var out bytes.Buffer
	out.Grow(len(src))
	pendingComma := false
	n := len(src)
	i := 0
	flushComma := func(next byte) {
		if pendingComma && next != '}' && next != ']' {
			out.WriteByte(',')
		}
		pendingComma = false
	}
	isIdentStart := func(c byte) bool {
		return c == '_' || c == '$' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= 0x80
	}
	isIdent := func(c byte) bool { return isIdentStart(c) || c >= '0' && c <= '9' }
	for i < n {
		c := src[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			out.WriteByte(c)
			i++
		case c == '/' && i+1 < n && src[i+1] == '/':
			for i < n && src[i] != '\n' {
				i++
			}
		case c == '/' && i+1 < n && src[i+1] == '*':
			end := bytes.Index(src[i+2:], []byte("*/"))
			if end < 0 {
				return nil, fmt.Errorf("unterminated comment")
			}
			i += end + 4
		case c == ',':
			if pendingComma {
				return nil, fmt.Errorf("unexpected comma at byte %d", i)
			}
			pendingComma = true
			i++
		case c == '"' || c == '\'':
			flushComma(c)
			quote := c
			out.WriteByte('"')
			i++
			for {
				if i >= n {
					return nil, fmt.Errorf("unterminated string")
				}
				ch := src[i]
				if ch == quote {
					i++
					out.WriteByte('"')
					break
				}
				if ch == '\\' && i+1 < n {
					nx := src[i+1]
					switch {
					case nx == '\n':
						i += 2
						continue
					case nx == '\r':
						i += 2
						if i < n && src[i] == '\n' {
							i++
						}
						continue
					case nx == '\'':
						out.WriteByte('\'')
						i += 2
						continue
					case nx == 'x' && i+3 < n:
						out.WriteString(`\u00` + string(src[i+2:i+4]))
						i += 4
						continue
					}
					out.WriteByte('\\')
					out.WriteByte(nx)
					i += 2
					continue
				}
				if ch == '"' {
					out.WriteString(`\"`)
				} else if ch == '\n' {
					out.WriteString(`\n`)
				} else if ch == '\t' {
					out.WriteString(`\t`)
				} else {
					out.WriteByte(ch)
				}
				i++
			}
		case c == '{' || c == '[' || c == ':':
			flushComma(c)
			out.WriteByte(c)
			i++
		case c == '}' || c == ']':
			pendingComma = false
			out.WriteByte(c)
			i++
		case isIdentStart(c):
			flushComma(c)
			j := i
			for j < n && isIdent(src[j]) {
				j++
			}
			word := string(src[i:j])
			k := j
			for k < n && (src[k] == ' ' || src[k] == '\t' || src[k] == '\n' || src[k] == '\r') {
				k++
			}
			switch {
			case k < n && src[k] == ':':
				out.WriteString(`"` + word + `"`)
			case word == "true" || word == "false" || word == "null":
				out.WriteString(word)
			case word == "Infinity" || word == "NaN":
				out.WriteString("null")
			default:
				return nil, fmt.Errorf("unexpected identifier %q", word)
			}
			i = j
		case c == '+' || c == '-' || c == '.' || c >= '0' && c <= '9':
			flushComma(c)
			j := i
			neg := false
			if src[j] == '+' || src[j] == '-' {
				neg = src[j] == '-'
				j++
			}
			if j+1 < n && src[j] == '0' && (src[j+1] == 'x' || src[j+1] == 'X') {
				k := j + 2
				var v int64
				for k < n && isHex(src[k]) {
					v = v*16 + int64(hexVal(src[k]))
					k++
				}
				if neg {
					v = -v
				}
				fmt.Fprintf(&out, "%d", v)
				i = k
				continue
			}
			if j+8 <= n && string(src[j:j+8]) == "Infinity" {
				out.WriteString("null")
				i = j + 8
				continue
			}
			k := j
			for k < n && (src[k] >= '0' && src[k] <= '9' || src[k] == '.' || src[k] == 'e' || src[k] == 'E' ||
				(src[k] == '+' || src[k] == '-') && k > j && (src[k-1] == 'e' || src[k-1] == 'E')) {
				k++
			}
			num := string(src[j:k])
			if strings.HasPrefix(num, ".") {
				num = "0" + num
			}
			if strings.HasSuffix(num, ".") {
				num += "0"
			}
			num = strings.Replace(num, ".e", ".0e", 1)
			num = strings.Replace(num, ".E", ".0E", 1)
			if neg {
				out.WriteByte('-')
			}
			out.WriteString(num)
			i = k
		default:
			return nil, fmt.Errorf("unexpected %q at byte %d", c, i)
		}
	}
	return out.Bytes(), nil
}

func isHex(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F'
}

func hexVal(c byte) int {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0')
	case c >= 'a' && c <= 'f':
		return int(c-'a') + 10
	default:
		return int(c-'A') + 10
	}
}

// ---- Memory splitting -----------------------------------------------------------

// HermesMemorySeparator is the delimiter Hermes writes between entries in
// memories/MEMORY.md and memories/USER.md (tools/memory_tool.py,
// ENTRY_DELIMITER = "\n§\n").
const HermesMemorySeparator = "\n§\n"

// MemoryEntry is one fact split out of a memory file.
type MemoryEntry struct {
	Content string
	// Heading is the nearest Markdown heading above the entry, if any; useful
	// as a tag.
	Heading string
}

// SplitBySeparator splits text on sep, trimming entries and dropping empty
// ones. Line endings are normalised to \n first.
func SplitBySeparator(text, sep string) []MemoryEntry {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	var out []MemoryEntry
	for _, part := range strings.Split(text, sep) {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, MemoryEntry{Content: p})
		}
	}
	return out
}

// SplitMarkdownEntries splits a Markdown memory file into entries:
//
//   - each top-level bullet ("- ", "* ", "+ ", "1. ") with its indented
//     continuation lines is one entry, and each plain paragraph
//     (blank-line separated) is one entry;
//   - a lead line ending with ":" or "：" ("Key rules:") is grouped with the
//     bullets and code blocks that follow it into one entry;
//   - fenced code blocks are kept whole;
//   - headings are not entries; they become the Heading of the entries
//     beneath them. Front matter and HTML comments are skipped;
//   - entries that are only a code fence, a lone label, or have fewer than
//     ~12 meaningful characters (letters and digits; CJK counts 1.5) are
//     dropped.
func SplitMarkdownEntries(text string) []MemoryEntry {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = stripFrontMatter(text)
	blocks := mdBlocks(text)
	var out []MemoryEntry
	emit := func(content, heading string) {
		content = strings.TrimSpace(content)
		if mdMeaningful(content) >= 24 { // ~12 characters, in half-units
			out = append(out, MemoryEntry{Content: content, Heading: heading})
		}
	}
	for i := 0; i < len(blocks); i++ {
		b := blocks[i]
		if !mdIsLead(b) {
			emit(b.render(false), b.heading)
			continue
		}
		group := []string{b.render(false)}
		j := i + 1
		for ; j < len(blocks) && blocks[j].heading == b.heading && blocks[j].kind != mdPara; j++ {
			group = append(group, blocks[j].render(true))
		}
		if j > i+1 { // a lone label (nothing under it) is dropped
			emit(strings.Join(group, "\n"), b.heading)
		}
		i = j - 1
	}
	return out
}

const (
	mdPara = iota
	mdBullet
	mdFence
)

// mdBlock is one paragraph, bullet item (with continuation) or fenced code
// block, with the heading it sits under.
type mdBlock struct {
	kind    int
	lines   []string
	heading string
}

// render returns the block's text; a bullet keeps its "- " marker when it is
// shown under a lead line (inGroup), and loses it when it stands alone.
func (b mdBlock) render(inGroup bool) string {
	lines := append([]string(nil), b.lines...)
	if b.kind == mdBullet {
		first := strings.TrimPrefix(lines[0], bulletMarker)
		if inGroup {
			first = "- " + first
		}
		lines[0] = first
	}
	return strings.TrimRight(strings.Join(lines, "\n"), "\n ")
}

func mdIsLead(b mdBlock) bool {
	if b.kind == mdFence || len(b.lines) == 0 {
		return false
	}
	last := strings.TrimSpace(b.lines[len(b.lines)-1])
	last = strings.TrimRight(last, "*_ ")
	return strings.HasSuffix(last, ":") || strings.HasSuffix(last, "：")
}

func mdBlocks(text string) []mdBlock {
	var (
		blocks  []mdBlock
		cur     *mdBlock
		heading string
		inHTML  bool
		fenceIn *mdBlock // block a fence is being collected into
		fence   string   // the opening fence marker
	)
	closeCur := func() {
		if cur != nil {
			for len(cur.lines) > 0 && strings.TrimSpace(cur.lines[len(cur.lines)-1]) == "" {
				cur.lines = cur.lines[:len(cur.lines)-1]
			}
			if len(cur.lines) > 0 {
				blocks = append(blocks, *cur)
			}
		}
		cur = nil
	}
	for _, line := range strings.Split(text, "\n") {
		trim := strings.TrimSpace(line)
		if fenceIn != nil {
			fenceIn.lines = append(fenceIn.lines, line)
			if strings.HasPrefix(trim, fence) {
				fenceIn = nil
				if cur != nil && cur.kind == mdFence {
					closeCur()
				}
			}
			continue
		}
		if inHTML {
			if strings.Contains(trim, "-->") {
				inHTML = false
			}
			continue
		}
		if strings.HasPrefix(trim, "<!--") {
			if !strings.Contains(trim, "-->") {
				inHTML = true
			}
			continue
		}
		indented := strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t")
		if strings.HasPrefix(trim, "```") || strings.HasPrefix(trim, "~~~") {
			fence = trim[:3]
			if cur != nil && cur.kind == mdBullet && indented {
				cur.lines = append(cur.lines, line) // code inside a bullet
				fenceIn = cur
				continue
			}
			closeCur()
			cur = &mdBlock{kind: mdFence, lines: []string{line}, heading: heading}
			fenceIn = cur
			continue
		}
		switch {
		case strings.HasPrefix(line, "#"):
			closeCur()
			heading = strings.TrimSpace(strings.TrimLeft(trim, "#"))
		case trim == "":
			if cur != nil && cur.kind == mdBullet {
				cur.lines = append(cur.lines, "") // kept only if continuation follows
			} else {
				closeCur()
			}
		case isBullet(line):
			closeCur()
			cur = &mdBlock{kind: mdBullet, lines: []string{stripBullet(line)}, heading: heading}
		case cur != nil && cur.kind == mdBullet && indented:
			cur.lines = append(cur.lines, line)
		case cur != nil && cur.kind == mdPara:
			cur.lines = append(cur.lines, line)
		default:
			closeCur()
			cur = &mdBlock{kind: mdPara, lines: []string{line}, heading: heading}
		}
	}
	closeCur()
	return blocks
}

// mdMeaningful counts letters and digits in half-units: 2 per character,
// 3 per CJK character (which carries more meaning than a Latin letter).
func mdMeaningful(s string) int {
	n := 0
	inFence := false
	for _, line := range strings.Split(s, "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~") {
			inFence = !inFence
			continue
		}
		for _, r := range t {
			switch {
			case unicode.Is(unicode.Han, r) || unicode.Is(unicode.Hiragana, r) || unicode.Is(unicode.Katakana, r) || unicode.Is(unicode.Hangul, r):
				n += 3
			case unicode.IsLetter(r) || unicode.IsDigit(r):
				n += 2
			}
		}
	}
	return n
}

// bulletMarker prefixes a bullet's first line in the working buffer (after
// stripBullet removes the "- ") so the splitter still knows the entry is a
// bullet; it is removed on output.
const bulletMarker = "\x00"

func isBullet(line string) bool {
	if strings.HasPrefix(line, bulletMarker) {
		return true
	}
	if len(line) < 2 {
		return false
	}
	switch {
	case (line[0] == '-' || line[0] == '*' || line[0] == '+') && line[1] == ' ':
		return true
	}
	j := 0
	for j < len(line) && line[j] >= '0' && line[j] <= '9' {
		j++
	}
	return j > 0 && j+1 < len(line) && (line[j] == '.' || line[j] == ')') && line[j+1] == ' '
}

func stripBullet(line string) string {
	if line[0] == '-' || line[0] == '*' || line[0] == '+' {
		return bulletMarker + strings.TrimSpace(line[2:])
	}
	j := 0
	for j < len(line) && line[j] >= '0' && line[j] <= '9' {
		j++
	}
	return bulletMarker + strings.TrimSpace(line[j+2:])
}

func stripFrontMatter(text string) string {
	if !strings.HasPrefix(text, "---\n") {
		return text
	}
	end := strings.Index(text[4:], "\n---")
	if end < 0 {
		return text
	}
	rest := text[4+end+4:]
	return strings.TrimPrefix(rest, "\n")
}

// SplitMemory splits memory text by sep when non-empty, else by Markdown
// bullets/paragraphs (SplitMarkdownEntries).
func SplitMemory(text, sep string) []MemoryEntry {
	if sep != "" {
		return SplitBySeparator(text, sep)
	}
	return SplitMarkdownEntries(text)
}

// ContentHash is a short stable hash of normalised text, used for memory ids
// and dedupe.
func ContentHash(s string) string {
	norm := strings.Join(strings.Fields(strings.ToLower(s)), " ")
	sum := sha256.Sum256([]byte(norm))
	return hex.EncodeToString(sum[:])[:12]
}

// ---- Skills -----------------------------------------------------------------------

// SkillDir is one folder holding a SKILL.md.
type SkillDir struct {
	Name        string // from front matter "name", else the folder name; sanitised
	Description string
	Dir         string // absolute folder path
	Category    string // parent folder relative to the scan root ("" at top level)
}

// ScanSkills finds SKILL.md folders under root, up to maxDepth levels deep
// (Hermes uses skills/<category>/<name>/SKILL.md, so 2; flat layouts need 1).
// Hidden folders and node_modules are skipped, and a skill folder's own
// subfolders are not searched. On a name clash the first found (sorted by
// path) wins. Missing root yields nil.
func ScanSkills(root string, maxDepth int) []SkillDir {
	if !IsDir(root) {
		return nil
	}
	var out []SkillDir
	seen := map[string]bool{}
	var walk func(dir string, depth int)
	walk = func(dir string, depth int) {
		if depth > maxDepth {
			return
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
		for _, e := range entries {
			if !e.IsDir() || strings.HasPrefix(e.Name(), ".") || e.Name() == "node_modules" {
				continue
			}
			sub := filepath.Join(dir, e.Name())
			if md := filepath.Join(sub, "SKILL.md"); IsFile(md) {
				name, desc := skillFrontMatter(md)
				if name == "" {
					name = e.Name()
				}
				name = Slug(name)
				if name == "" || seen[name] {
					continue
				}
				seen[name] = true
				cat, _ := filepath.Rel(root, dir)
				if cat == "." {
					cat = ""
				}
				out = append(out, SkillDir{Name: name, Description: desc, Dir: sub, Category: filepath.ToSlash(cat)})
				continue
			}
			walk(sub, depth+1)
		}
	}
	walk(root, 1)
	return out
}

func skillFrontMatter(path string) (name, desc string) {
	text := strings.ReplaceAll(ReadText(path), "\r\n", "\n")
	if !strings.HasPrefix(text, "---\n") {
		return "", ""
	}
	end := strings.Index(text[4:], "\n---")
	if end < 0 {
		return "", ""
	}
	var fm struct {
		Name        string `yaml:"name"`
		Description string `yaml:"description"`
	}
	_ = yaml.Unmarshal([]byte(text[4:4+end]), &fm)
	return strings.TrimSpace(fm.Name), strings.TrimSpace(fm.Description)
}

// Slug reduces a name to lowercase kebab-case ASCII (the form Antares uses for
// skill, role, MCP and provider ids).
func Slug(name string) string {
	var b strings.Builder
	prevDash := true
	for _, r := range strings.ToLower(name) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
			prevDash = false
			continue
		}
		if !prevDash {
			b.WriteByte('-')
			prevDash = true
		}
	}
	return strings.Trim(b.String(), "-")
}

// ---- Providers -------------------------------------------------------------------

// VendorInfo is the Antares provider a source vendor name maps to.
type VendorInfo struct {
	ID      string // Antares provider id (a catalogue id where one exists)
	Label   string
	Kind    string // Antares provider kind
	BaseURL string // default endpoint; empty means the kind's own default
	// OAuth marks a subscription/OAuth login that cannot be migrated as a key.
	OAuth bool
}

// vendors maps the names other agents use for providers to Antares ids and
// kinds. Catalogue ids match the setup catalogue in
// internal/server/handlers_setup.go and internal/providers/catalog.go.
var vendors = map[string]VendorInfo{
	"openrouter":     {ID: "openrouter", Label: "OpenRouter", Kind: "openai-compatible", BaseURL: "https://openrouter.ai/api/v1"},
	"anthropic":      {ID: "anthropic", Label: "Anthropic", Kind: "anthropic", BaseURL: "https://api.anthropic.com/v1"},
	"claude":         {ID: "anthropic", Label: "Anthropic", Kind: "anthropic", BaseURL: "https://api.anthropic.com/v1"},
	"openai":         {ID: "openai", Label: "OpenAI", Kind: "openai", BaseURL: "https://api.openai.com/v1"},
	"gemini":         {ID: "gemini", Label: "Google Gemini", Kind: "gemini", BaseURL: "https://generativelanguage.googleapis.com/v1beta"},
	"google":         {ID: "gemini", Label: "Google Gemini", Kind: "gemini", BaseURL: "https://generativelanguage.googleapis.com/v1beta"},
	"zai":            {ID: "zai", Label: "Z.ai GLM", Kind: "anthropic", BaseURL: "https://api.z.ai/api/anthropic/v1"},
	"z-ai":           {ID: "zai", Label: "Z.ai GLM", Kind: "anthropic", BaseURL: "https://api.z.ai/api/anthropic/v1"},
	"glm":            {ID: "zai", Label: "Z.ai GLM", Kind: "anthropic", BaseURL: "https://api.z.ai/api/anthropic/v1"},
	"zhipu":          {ID: "zai", Label: "Z.ai GLM", Kind: "anthropic", BaseURL: "https://api.z.ai/api/anthropic/v1"},
	"opencode":       {ID: "opencode", Label: "OpenCode Go (Zen)", Kind: "opencode", BaseURL: "https://opencode.ai/zen/go/v1"},
	"ollama":         {ID: "ollama", Label: "Ollama", Kind: "openai-compatible", BaseURL: "http://127.0.0.1:11434/v1"},
	"lmstudio":       {ID: "lmstudio", Label: "LM Studio", Kind: "openai-compatible", BaseURL: "http://127.0.0.1:1234/v1"},
	"lm-studio":      {ID: "lmstudio", Label: "LM Studio", Kind: "openai-compatible", BaseURL: "http://127.0.0.1:1234/v1"},
	"azure":          {ID: "azure", Label: "Azure OpenAI", Kind: "azure"},
	"azure-openai":   {ID: "azure", Label: "Azure OpenAI", Kind: "azure"},
	"bedrock":        {ID: "bedrock", Label: "AWS Bedrock", Kind: "bedrock"},
	"amazon-bedrock": {ID: "bedrock", Label: "AWS Bedrock", Kind: "bedrock"},
	"vertex":         {ID: "vertex", Label: "Google Vertex AI", Kind: "vertex"},
	"google-vertex":  {ID: "vertex", Label: "Google Vertex AI", Kind: "vertex"},
	"deepseek":       {ID: "deepseek", Label: "DeepSeek", Kind: "openai-compatible", BaseURL: "https://api.deepseek.com"},
	"groq":           {ID: "groq", Label: "Groq", Kind: "openai-compatible", BaseURL: "https://api.groq.com/openai/v1"},
	"xai":            {ID: "xai", Label: "xAI Grok", Kind: "openai-compatible", BaseURL: "https://api.x.ai/v1"},
	"grok":           {ID: "xai", Label: "xAI Grok", Kind: "openai-compatible", BaseURL: "https://api.x.ai/v1"},
	"mistral":        {ID: "mistral", Label: "Mistral", Kind: "openai-compatible", BaseURL: "https://api.mistral.ai/v1"},
	"together":       {ID: "together", Label: "Together AI", Kind: "openai-compatible", BaseURL: "https://api.together.xyz/v1"},
	"fireworks":      {ID: "fireworks", Label: "Fireworks", Kind: "openai-compatible", BaseURL: "https://api.fireworks.ai/inference/v1"},
	"moonshot":       {ID: "moonshot", Label: "Moonshot (Kimi)", Kind: "openai-compatible", BaseURL: "https://api.moonshot.ai/v1"},
	"kimi":           {ID: "moonshot", Label: "Moonshot (Kimi)", Kind: "openai-compatible", BaseURL: "https://api.moonshot.ai/v1"},
	"dashscope":      {ID: "dashscope", Label: "Qwen (DashScope)", Kind: "openai-compatible", BaseURL: "https://dashscope-intl.aliyuncs.com/compatible-mode/v1"},
	"qwen":           {ID: "dashscope", Label: "Qwen (DashScope)", Kind: "openai-compatible", BaseURL: "https://dashscope-intl.aliyuncs.com/compatible-mode/v1"},
	"minimax":        {ID: "minimax", Label: "MiniMax", Kind: "anthropic", BaseURL: "https://api.minimax.io/anthropic/v1"},
	"nvidia":         {ID: "nvidia", Label: "NVIDIA NIM", Kind: "openai-compatible", BaseURL: "https://integrate.api.nvidia.com/v1"},
	"huggingface":    {ID: "huggingface", Label: "Hugging Face", Kind: "openai-compatible", BaseURL: "https://router.huggingface.co/v1"},
	"cerebras":       {ID: "cerebras", Label: "Cerebras", Kind: "openai-compatible", BaseURL: "https://api.cerebras.ai/v1"},
	"vllm":           {ID: "vllm", Label: "vLLM", Kind: "openai-compatible"},
	// Subscription/OAuth logins: no portable key; the user signs in again.
	"copilot":           {ID: "copilot", Label: "GitHub Copilot", Kind: "copilot", OAuth: true},
	"github-copilot":    {ID: "copilot", Label: "GitHub Copilot", Kind: "copilot", OAuth: true},
	"codex":             {ID: "codex", Label: "OpenAI Codex", Kind: "codex", OAuth: true},
	"openai-codex":      {ID: "codex", Label: "OpenAI Codex", Kind: "codex", OAuth: true},
	"nous":              {ID: "nous", Label: "Nous Portal", Kind: "openai-compatible", OAuth: true},
	"qwen-oauth":        {ID: "qwen-oauth", Label: "Qwen OAuth", Kind: "openai-compatible", OAuth: true},
	"qwen-portal":       {ID: "qwen-oauth", Label: "Qwen OAuth", Kind: "openai-compatible", OAuth: true},
	"google-gemini-cli": {ID: "gemini-cli", Label: "Gemini CLI login", Kind: "gemini", OAuth: true},
	"anthropic-oauth":   {ID: "anthropic", Label: "Claude subscription", Kind: "anthropic", OAuth: true},
}

// Vendor maps a source's provider/vendor name (case-insensitive; "_" and " "
// treated as "-") to an Antares provider.
func Vendor(name string) (VendorInfo, bool) {
	key := strings.ReplaceAll(strings.ReplaceAll(strings.ToLower(strings.TrimSpace(name)), "_", "-"), " ", "-")
	v, ok := vendors[key]
	if !ok {
		v, ok = vendors[strings.ReplaceAll(key, "-", "")]
	}
	return v, ok
}

// VendorByBaseURL guesses the vendor from an endpoint's host, for sources that
// only store a base URL.
func VendorByBaseURL(baseURL string) (VendorInfo, bool) {
	u := strings.ToLower(baseURL)
	for _, h := range []struct{ host, vendor string }{
		{"openrouter.ai", "openrouter"}, {"api.anthropic.com", "anthropic"}, {"api.openai.com", "openai"},
		{"generativelanguage.googleapis.com", "gemini"}, {"api.z.ai", "zai"}, {"open.bigmodel.cn", "zai"},
		{"opencode.ai", "opencode"}, {"api.deepseek.com", "deepseek"}, {"api.groq.com", "groq"},
		{"api.x.ai", "xai"}, {"api.mistral.ai", "mistral"}, {"api.together.xyz", "together"},
		{"api.fireworks.ai", "fireworks"}, {"api.moonshot", "moonshot"}, {"dashscope", "dashscope"},
		{"api.minimax", "minimax"}, {"integrate.api.nvidia.com", "nvidia"}, {"api.cerebras.ai", "cerebras"},
		{":11434", "ollama"}, {":1234", "lmstudio"},
	} {
		if strings.Contains(u, h.host) {
			return Vendor(h.vendor)
		}
	}
	return VendorInfo{}, false
}

// CatalogueProviderIDs are the built-in provider ids of the setup catalogue
// (internal/server/handlers_setup.go setupProviderCatalogue). A custom
// provider must not take one. Duplicated here because migrate cannot import
// server; a server test asserts the two lists match.
var CatalogueProviderIDs = []string{
	"openrouter", "anthropic", "openai", "gemini", "zai", "opencode", "ollama",
	"lmstudio", "azure", "bedrock", "vertex", "copilot", "codex", "custom",
}

// CustomProviderID mints a config id for a user-named OpenAI-compatible
// provider, with the same rules as server.CustomProviderID: slug of the name,
// "custom-provider" when the slug is empty or "custom", never a catalogue id,
// and "-2", "-3"… until taken reports false.
func CustomProviderID(taken func(id string) bool, name string) string {
	slug := Slug(name)
	if slug == "" || slug == "custom" {
		slug = "custom-provider"
	}
	base := slug
	for i := 2; ; i++ {
		if !taken(slug) && !isCatalogueID(slug) {
			return slug
		}
		slug = fmt.Sprintf("%s-%d", base, i)
	}
}

func isCatalogueID(id string) bool {
	for _, c := range CatalogueProviderIDs {
		if c == id {
			return true
		}
	}
	return false
}

// NextFreeName returns name if free, else name-2, name-3… (the rename
// resolution).
func NextFreeName(name string, taken func(string) bool) string {
	if !taken(name) {
		return name
	}
	for i := 2; ; i++ {
		c := fmt.Sprintf("%s-%d", name, i)
		if !taken(c) {
			return c
		}
	}
}

// IsLocalURL reports whether an endpoint is on this machine (no key needed).
func IsLocalURL(u string) bool {
	u = strings.ToLower(u)
	return strings.Contains(u, "://localhost") || strings.Contains(u, "://127.") || strings.Contains(u, "://[::1]") || strings.Contains(u, "://0.0.0.0")
}

// ---- Channels ----------------------------------------------------------------

// SupportedPlatforms are the gateway.<platform> blocks Antares has.
var SupportedPlatforms = []string{"telegram", "discord", "slack", "matrix", "signal", "whatsapp", "feishu"}

// SupportedPlatform reports whether Antares has a gateway for platform.
func SupportedPlatform(p string) bool {
	for _, s := range SupportedPlatforms {
		if s == p {
			return true
		}
	}
	return false
}

// PlatformLabel is a display name for a channel platform.
func PlatformLabel(p string) string {
	switch p {
	case "whatsapp":
		return "WhatsApp"
	case "imessage":
		return "iMessage"
	case "wecom":
		return "WeCom"
	case "wechat", "weixin":
		return "WeChat"
	case "dingtalk":
		return "DingTalk"
	case "qq":
		return "QQ"
	case "":
		return ""
	}
	return strings.ToUpper(p[:1]) + p[1:]
}

// ---- Schedules ------------------------------------------------------------------

// EverySchedule renders an interval as an Antares "@every" schedule
// ("@every 30m", "@every 2h", "@every 1h30m").
func EverySchedule(d time.Duration) string {
	d = d.Round(time.Minute)
	s := d.String() // e.g. "1h30m0s"
	s = strings.TrimSuffix(s, "0s")
	s = strings.TrimSuffix(s, "h0m")
	if strings.HasSuffix(d.String(), "h0m0s") {
		s += "h"
	}
	return "@every " + s
}

// ValidSchedule reports whether Antares' scheduler accepts expr.
func ValidSchedule(expr string) error {
	_, err := cron.Parse(expr)
	return err
}

// ---- Item builders -----------------------------------------------------------
//
// Each builder sets ID, Category, Status, Reason, Secret and Selected
// consistently, so every source marks conflicts the same way.

// ItemID is "<category>:<key>".
func ItemID(cat Category, key string) string { return string(cat) + ":" + key }

func finish(it Item) Item {
	it.Selected = it.Status == StatusReady
	return it
}

// UnsupportedItem is an item that is listed but never applied.
func UnsupportedItem(cat Category, key, title, reason string) Item {
	return finish(Item{ID: ItemID(cat, key), Category: cat, Title: title, Status: StatusUnsupported, Reason: reason})
}

// ProviderItem builds a provider item. A missing key (for a kind that needs
// one and a non-local endpoint) is needs_input "api_key"; an existing id is a
// conflict unless the Env reports identical base URL and key (then ready, and
// Apply makes it a no-op).
func ProviderItem(env Env, p ProviderPayload, detail string) Item {
	if p.Label == "" {
		p.Label = p.ID
	}
	it := Item{ID: ItemID(CatProvider, p.ID), Category: CatProvider, Title: p.Label, Detail: detail,
		Status: StatusReady, Secret: true, Payload: Payload{Provider: &p}}
	needsKey := p.Kind != "bedrock" && !IsLocalURL(p.BaseURL)
	switch {
	case env != nil && env.ProviderExists(p.ID):
		if m, ok := env.(ProviderMatcher); ok && m.ProviderMatches(p.ID, p.BaseURL, p.APIKey) {
			it.Reason = "already configured with the same key"
		} else {
			it.Status = StatusConflict
			it.Reason = "Antares already has a provider \"" + p.ID + "\""
		}
	case p.APIKey == "" && needsKey:
		it.Status = StatusNeedsInput
		it.Input = "api_key"
		it.Reason = "no key found; enter it to import this provider"
	}
	if it.Status == StatusConflict && p.APIKey == "" && needsKey {
		it.Input = "api_key"
	}
	return finish(it)
}

// OAuthProviderItem lists a subscription login that must be redone in Antares.
func OAuthProviderItem(key, label string) Item {
	return UnsupportedItem(CatProvider, key, label, "subscription/OAuth login — sign in again in Antares")
}

// ModelItem builds the default-model item; a conflict when Antares already
// has a working default (as reported by a DefaultModelChecker Env).
func ModelItem(env Env, m ModelPayload) Item {
	title := m.Model
	if m.Provider != "" {
		title = m.Provider + " / " + m.Model
	}
	detail := ""
	if len(m.Fallback) > 0 {
		detail = fmt.Sprintf("%d fallback(s): %s", len(m.Fallback), strings.Join(m.Fallback, ", "))
	}
	it := Item{ID: ItemID(CatModel, "default"), Category: CatModel, Title: title, Detail: detail,
		Status: StatusReady, Payload: Payload{Model: &m}}
	if c, ok := env.(DefaultModelChecker); ok && c.HasDefaultModel() {
		it.Status = StatusConflict
		it.Reason = "Antares already has a default model"
	}
	return finish(it)
}

var textTitles = map[Category]string{CatSoul: "SOUL.md", CatAgentsMD: "AGENTS.md", CatUserMD: "USER.md"}

// TextItem builds a soul / agents_md / user_md item; a conflict when the
// current Antares file is custom. Empty content yields ok=false.
func TextItem(env Env, cat Category, content, from string) (Item, bool) {
	content = strings.TrimSpace(content)
	if content == "" {
		return Item{}, false
	}
	it := Item{ID: ItemID(cat, "file"), Category: cat, Title: textTitles[cat], Status: StatusReady,
		Detail:  fmt.Sprintf("from %s · %d chars", from, len(content)),
		Payload: Payload{Text: &TextPayload{Content: content, From: from}}}
	if env != nil && env.TextFileState(cat) == "custom" {
		it.Status = StatusConflict
		it.Reason = "Antares already has a custom " + textTitles[cat] + " (replace or append)"
	}
	return finish(it), true
}

// MemoryItems turns entries into memory items, deduped by content hash.
// tags are added to every entry (an entry's Heading is added as a tag too).
func MemoryItems(entries []MemoryEntry, scope string, tags ...string) []Item {
	if scope == "" {
		scope = "global"
	}
	seen := map[string]bool{}
	var out []Item
	for _, e := range entries {
		h := ContentHash(e.Content)
		if seen[h] {
			continue
		}
		seen[h] = true
		t := append([]string(nil), tags...)
		if e.Heading != "" {
			t = append(t, Slug(e.Heading))
		}
		it := finish(Item{ID: ItemID(CatMemory, h), Category: CatMemory,
			Title: Truncate(RedactSecrets(firstLine(e.Content)), 80), Status: StatusReady,
			Payload: Payload{Memory: &MemoryPayload{Scope: scope, Key: memoryKey(e.Content), Content: e.Content, Tags: t}}})
		out = append(out, flagSecret(it, e.Content))
	}
	return out
}

// People keep credentials in their agent's notes ("Bot Token: …"). Such an
// entry is still offered, but masked in every preview and left unselected, so
// a secret is never shown on screen and only copied when the user opts in.
var (
	secretTokenRe = regexp.MustCompile(`(?:sk|pk|rk)-[A-Za-z0-9_-]{16,}|gh[pousr]_[A-Za-z0-9]{20,}|github_pat_[A-Za-z0-9_]{20,}|xox[abposr]-[A-Za-z0-9-]{10,}|AKIA[0-9A-Z]{16}|AIza[0-9A-Za-z_-]{35}|eyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}|[MNO][A-Za-z0-9_-]{23,27}\.[A-Za-z0-9_-]{6}\.[A-Za-z0-9_-]{27,}|\b\d{8,10}:[A-Za-z0-9_-]{35}\b|atd_[0-9a-f]{48}|-----BEGIN [A-Z ]*PRIVATE KEY-----`)
	secretLabelRe = regexp.MustCompile(`(?i)\b(token|secret|password|passwd|pwd|api[ _-]?key|access[ _-]?key|private[ _-]?key|client[ _-]?secret|bearer)\b(\**\s*[:=]\s*\**\s*)(\S{6,})`)
)

// LooksSecret reports whether text appears to contain a credential.
func LooksSecret(text string) bool {
	return secretTokenRe.MatchString(text) || secretLabelRe.MatchString(text)
}

// RedactSecrets masks credential-looking values in text meant for display.
func RedactSecrets(text string) string {
	text = secretLabelRe.ReplaceAllString(text, "${1}${2}••••")
	return secretTokenRe.ReplaceAllString(text, "••••")
}

// flagSecret marks an item whose content looks like a credential: masked,
// deselected, and explained.
func flagSecret(it Item, content string) Item {
	if !LooksSecret(content) {
		return it
	}
	it.Secret = true
	it.Selected = false
	it.Title = RedactSecrets(it.Title)
	if it.Reason == "" {
		it.Reason = "Looks like it contains a credential — masked here and not selected by default."
	}
	return it
}

func memoryKey(content string) string {
	k := Slug(Truncate(firstLine(content), 48))
	if k == "" {
		k = "imported"
	}
	return k
}

// KnowledgeItem builds a RAG document item; unsupported when RAG is off.
func KnowledgeItem(env Env, relPath, content string) (Item, bool) {
	content = strings.TrimSpace(content)
	if content == "" {
		return Item{}, false
	}
	relPath = filepath.ToSlash(relPath)
	it := Item{ID: ItemID(CatKnowledge, relPath), Category: CatKnowledge, Title: relPath,
		Detail: fmt.Sprintf("%d chars", len(content)), Status: StatusReady,
		Payload: Payload{Knowledge: &KnowledgePayload{Path: relPath, Content: content}}}
	if c, ok := env.(RAGChecker); ok && !c.RAGEnabled() {
		it.Status = StatusUnsupported
		it.Reason = "RAG is off"
	}
	return flagSecret(finish(it), content), true
}

// SkillItem builds a skill item; a conflict when Antares has the name.
func SkillItem(env Env, s SkillDir) Item {
	it := Item{ID: ItemID(CatSkill, s.Name), Category: CatSkill, Title: s.Name,
		Detail: Truncate(s.Description, 120), Status: StatusReady,
		Payload: Payload{Skill: &SkillPayload{Name: s.Name, Dir: s.Dir}}}
	if env != nil && env.SkillExists(s.Name) {
		it.Status = StatusConflict
		it.Reason = "Antares already has a skill named \"" + s.Name + "\""
	}
	return finish(it)
}

// NormalizeTransport maps a source's MCP transport name to Antares'
// "stdio" or "http" (sse, streamable_http, streamable-http and http → http;
// empty → http when a URL is set, else stdio).
func NormalizeTransport(t, url string) string {
	switch strings.ToLower(strings.TrimSpace(t)) {
	case "sse", "http", "https", "streamable_http", "streamable-http", "streamablehttp":
		return "http"
	case "stdio":
		return "stdio"
	}
	if url != "" {
		return "http"
	}
	return "stdio"
}

// MCPItem builds an MCP server item; transport is normalised; a conflict when
// the name exists. Env/header values count as secrets.
func MCPItem(env Env, m MCPPayload) Item {
	m.Transport = NormalizeTransport(m.Transport, m.URL)
	detail := m.URL
	if m.Transport == "stdio" {
		detail = strings.TrimSpace(m.Command + " " + strings.Join(m.Args, " "))
	}
	it := Item{ID: ItemID(CatMCP, m.Name), Category: CatMCP, Title: m.Name, Detail: Truncate(detail, 120),
		Status: StatusReady, Secret: len(m.Env) > 0 || len(m.Headers) > 0, Payload: Payload{MCP: &m}}
	if m.Transport == "stdio" && m.Command == "" || m.Transport == "http" && m.URL == "" {
		it.Status = StatusUnsupported
		it.Reason = "no command or URL"
	} else if env != nil && env.MCPServerExists(m.Name) {
		it.Status = StatusConflict
		it.Reason = "Antares already has an MCP server \"" + m.Name + "\""
	}
	return finish(it)
}

// CronItem builds a schedule item. The schedule must already be in Antares
// form; one cron.Parse rejects becomes unsupported. Imported jobs are always
// disabled (Enabled=false), whatever the source said.
func CronItem(key string, c CronPayload, sourceEnabled bool) Item {
	c.Enabled = false
	detail := c.Schedule
	if !sourceEnabled {
		detail += " · paused in source"
	}
	it := Item{ID: ItemID(CatCron, key), Category: CatCron, Title: c.Name, Detail: detail,
		Status: StatusReady, Payload: Payload{Cron: &c}}
	if strings.TrimSpace(c.Prompt) == "" {
		it.Status, it.Reason = StatusUnsupported, "the job has no prompt"
	} else if err := ValidSchedule(c.Schedule); err != nil {
		it.Status, it.Reason = StatusUnsupported, "schedule not supported: "+err.Error()
	}
	return finish(it)
}

// ChannelItem builds a chat-channel item. Unsupported platforms get a reason;
// a missing required field (missingInput, e.g. "bot_token") is needs_input; a
// platform Antares already has a token for is a conflict.
func ChannelItem(env Env, platform, title string, fields map[string]any, missingInput string) Item {
	key := platform
	if title == "" {
		title = PlatformLabel(platform)
	}
	if !SupportedPlatform(platform) {
		return UnsupportedItem(CatChannel, key, title, "Antares has no "+PlatformLabel(platform)+" gateway")
	}
	it := Item{ID: ItemID(CatChannel, key), Category: CatChannel, Title: title, Status: StatusReady, Secret: true,
		Payload: Payload{Channel: &ChannelPayload{Platform: platform, Fields: fields}}}
	switch {
	case env != nil && env.ChannelConfigured(platform):
		it.Status = StatusConflict
		it.Reason = "Antares already has a " + PlatformLabel(platform) + " token"
	case missingInput != "":
		it.Status = StatusNeedsInput
		it.Input = missingInput
		it.Reason = "the " + missingInput + " could not be read"
	}
	return finish(it)
}

// RoleItem builds a role item; a conflict when the name exists (including a
// built-in role of the same name).
func RoleItem(env Env, r RolePayload) Item {
	r.Name = Slug(r.Name)
	if r.Title == "" {
		r.Title = r.Name
	}
	it := Item{ID: ItemID(CatRole, r.Name), Category: CatRole, Title: r.Title, Detail: Truncate(r.Summary, 120),
		Status: StatusReady, Payload: Payload{Role: &r}}
	if strings.TrimSpace(r.Prompt) == "" {
		it.Status, it.Reason = StatusUnsupported, "the agent has no instructions"
	} else if env != nil && env.RoleExists(r.Name) {
		it.Status = StatusConflict
		it.Reason = "Antares already has a role \"" + r.Name + "\""
	}
	return finish(it)
}

// ---- Presentation ----------------------------------------------------------------

// Truncate shortens s to n runes with an ellipsis.
func Truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return strings.TrimSpace(string(r[:n-1])) + "…"
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return s
}

// Redact masks a secret for display: "••••" plus the last 4 characters when
// the value is long enough to keep them anonymous, else just "••••".
func Redact(s string) string {
	if s == "" {
		return ""
	}
	if len(s) >= 12 {
		return "••••" + s[len(s)-4:]
	}
	return "••••"
}

// Summarize renders the picker's count line ("2 providers · 14 skills ·
// Telegram") from a plan's items; channels are named, the rest counted.
func Summarize(items []Item) string {
	counts := map[Category]int{}
	var channels []string
	for _, it := range items {
		if it.Category == CatChannel {
			if it.Status != StatusUnsupported && it.Payload.Channel != nil {
				channels = append(channels, PlatformLabel(it.Payload.Channel.Platform))
			}
			continue
		}
		counts[it.Category]++
	}
	var parts []string
	for _, c := range []struct {
		cat       Category
		one, many string
	}{
		{CatProvider, "provider", "providers"}, {CatModel, "model", "models"}, {CatSoul, "soul", "soul"},
		{CatAgentsMD, "instructions", "instructions"}, {CatUserMD, "user profile", "user profile"},
		{CatMemory, "memory", "memories"}, {CatKnowledge, "note", "notes"}, {CatSkill, "skill", "skills"},
		{CatMCP, "MCP server", "MCP servers"}, {CatCron, "schedule", "schedules"}, {CatRole, "role", "roles"},
	} {
		n := counts[c.cat]
		switch {
		case n == 0:
		case c.cat == CatSoul || c.cat == CatAgentsMD || c.cat == CatUserMD || c.cat == CatModel:
			parts = append(parts, c.one)
		case n == 1:
			parts = append(parts, "1 "+c.one)
		default:
			parts = append(parts, fmt.Sprintf("%d %s", n, c.many))
		}
	}
	parts = append(parts, channels...)
	if len(parts) == 0 {
		return "nothing to import"
	}
	return strings.Join(parts, " · ")
}

// RenderPlan prints a plan as stable text — one line per item plus its
// payload with every secret redacted. Used for golden tests and the CLI's
// --dry-run --verbose output.
func RenderPlan(p Plan) string {
	var b strings.Builder
	d := p.Detection
	fmt.Fprintf(&b, "source=%s name=%q profile=%q running=%v\nsummary: %s\n", d.Source, d.Name, d.Profile, d.Running, d.Summary)
	for _, w := range p.Warnings {
		fmt.Fprintf(&b, "warning: %s\n", w)
	}
	for _, it := range p.Items {
		fmt.Fprintf(&b, "\n[%s] %s %q status=%s selected=%v", it.Category, it.ID, it.Title, it.Status, it.Selected)
		if it.Secret {
			b.WriteString(" secret")
		}
		if it.Input != "" {
			fmt.Fprintf(&b, " input=%s", it.Input)
		}
		b.WriteString("\n")
		if it.Detail != "" {
			fmt.Fprintf(&b, "  detail: %s\n", it.Detail)
		}
		if it.Reason != "" {
			fmt.Fprintf(&b, "  reason: %s\n", it.Reason)
		}
		renderPayload(&b, it.Payload)
	}
	return b.String()
}

func renderPayload(b *strings.Builder, p Payload) {
	kv := func(k string, v any) { fmt.Fprintf(b, "  %s: %v\n", k, v) }
	redactMap := func(m map[string]string) string {
		keys := make([]string, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		parts := make([]string, 0, len(keys))
		for _, k := range keys {
			parts = append(parts, k+"="+Redact(m[k]))
		}
		return "{" + strings.Join(parts, ", ") + "}"
	}
	switch {
	case p.Provider != nil:
		x := p.Provider
		kv("provider", fmt.Sprintf("id=%s kind=%s base_url=%s key=%s models=%v headers=%s", x.ID, x.Kind, x.BaseURL, Redact(x.APIKey), x.Models, redactMap(x.Headers)))
	case p.Model != nil:
		kv("model", fmt.Sprintf("provider=%s model=%s fallback=%v", p.Model.Provider, p.Model.Model, p.Model.Fallback))
	case p.Text != nil:
		kv("text", fmt.Sprintf("from=%s sha=%s", p.Text.From, ContentHash(p.Text.Content)))
	case p.Memory != nil:
		kv("memory", fmt.Sprintf("scope=%s key=%s tags=%v", p.Memory.Scope, p.Memory.Key, p.Memory.Tags))
	case p.Knowledge != nil:
		kv("knowledge", fmt.Sprintf("path=%s sha=%s", p.Knowledge.Path, ContentHash(p.Knowledge.Content)))
	case p.Skill != nil:
		kv("skill", fmt.Sprintf("name=%s dir=%s", p.Skill.Name, filepath.Base(p.Skill.Dir)))
	case p.MCP != nil:
		x := p.MCP
		kv("mcp", fmt.Sprintf("name=%s transport=%s command=%s args=%v url=%s env=%s headers=%s", x.Name, x.Transport, x.Command, x.Args, x.URL, redactMap(x.Env), redactMap(x.Headers)))
	case p.Cron != nil:
		x := p.Cron
		kv("cron", fmt.Sprintf("name=%s schedule=%q tz=%s enabled=%v prompt_sha=%s", x.Name, x.Schedule, x.Timezone, x.Enabled, ContentHash(x.Prompt)))
	case p.Channel != nil:
		keys := make([]string, 0, len(p.Channel.Fields))
		for k := range p.Channel.Fields {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		parts := make([]string, 0, len(keys))
		for _, k := range keys {
			v := p.Channel.Fields[k]
			if s, ok := v.(string); ok && isSecretField(k) {
				v = Redact(s)
			}
			parts = append(parts, fmt.Sprintf("%s=%v", k, v))
		}
		kv("channel", p.Channel.Platform+" {"+strings.Join(parts, ", ")+"}")
	case p.Role != nil:
		kv("role", fmt.Sprintf("name=%s title=%q model=%s prompt_sha=%s", p.Role.Name, p.Role.Title, p.Role.Model, ContentHash(p.Role.Prompt)))
	}
}

// isSecretField reports whether a gateway field name holds a credential.
func isSecretField(k string) bool {
	k = strings.ToLower(k)
	return strings.Contains(k, "token") || strings.Contains(k, "secret") || strings.Contains(k, "password") || strings.HasSuffix(k, "key")
}

// ---- Running-process detection --------------------------------------------------

// RunningCheck reports whether any of the named processes (matched against
// the full command line) or launchd labels (macOS) is running. Sources call
// it from Detect; tests replace it.
var RunningCheck = func(processPatterns, launchdLabels []string) bool {
	if runtime.GOOS == "darwin" {
		for _, l := range launchdLabels {
			if out, err := exec.Command("launchctl", "list", l).Output(); err == nil && bytes.Contains(out, []byte(`"PID"`)) {
				return true
			}
		}
	}
	if runtime.GOOS == "windows" {
		return false
	}
	for _, p := range processPatterns {
		if err := exec.Command("pgrep", "-f", p).Run(); err == nil {
			return true
		}
	}
	return false
}

// ---- Loose value helpers (for sources decoding YAML/JSON into map[string]any) ----

func str(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(x)
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case int:
		return strconv.Itoa(x)
	case bool:
		return strconv.FormatBool(x)
	}
	return ""
}

func firstStr(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if s := str(m[k]); s != "" {
			return s
		}
	}
	return ""
}

func strList(v any) []string {
	xs, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(xs))
	for _, x := range xs {
		if s := str(x); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func splitList(v string) []string {
	var out []string
	for _, s := range strings.FieldsFunc(v, func(r rune) bool { return r == ',' || r == ' ' || r == ';' }) {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func firstNonEmpty(vs ...string) string {
	for _, v := range vs {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// hostLabel names an endpoint by its host ("llm.example.com").
func hostLabel(u string) string {
	h := u
	if i := strings.Index(h, "://"); i >= 0 {
		h = h[i+3:]
	}
	if i := strings.IndexAny(h, "/?#"); i >= 0 {
		h = h[:i]
	}
	if i := strings.LastIndex(h, "@"); i >= 0 {
		h = h[i+1:]
	}
	if h == "" {
		return "custom"
	}
	return h
}

func humanTitle(name string) string {
	words := strings.FieldsFunc(name, func(r rune) bool { return r == '-' || r == '_' || r == ' ' })
	for i, w := range words {
		words[i] = strings.ToUpper(w[:1]) + w[1:]
	}
	return strings.Join(words, " ")
}

// PIDAlive reports whether pid is a live process (never on Windows, where
// signal 0 is not available). Sources use it for PID/lock files; tests
// replace it.
var PIDAlive = func(pid int) bool {
	if pid <= 0 || runtime.GOOS == "windows" {
		return false
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return p.Signal(syscall.Signal(0)) == nil
}
