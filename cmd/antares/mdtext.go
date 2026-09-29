package main

import (
	"os"
	"regexp"
	"strings"

	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/glamour/ansi"
	gstyles "github.com/charmbracelet/glamour/styles"
	"github.com/charmbracelet/lipgloss"
	"golang.org/x/term"
)

// Command output from the shared registry is markdown written for a chat
// transcript. A shell wants it readable: on a terminal it is rendered with
// Glamour (the same renderer the TUI uses); piped into a file or another
// program it is flattened to plain text so scripts never see ANSI escapes or
// stray markdown punctuation.

// stdoutIsTTY reports whether stdout is an interactive terminal that should get
// styled output. NO_COLOR opts out, per https://no-color.org.
func stdoutIsTTY() bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	return term.IsTerminal(int(os.Stdout.Fd()))
}

// renderForTerminal turns registry markdown into what the shell should print.
func renderForTerminal(md string, styled bool) string {
	if styled {
		if out, ok := glamourRender(md); ok {
			return out
		}
	}
	return plainMarkdown(md)
}

func glamourRender(md string) (string, bool) {
	width := 100
	if w, _, err := term.GetSize(int(os.Stdout.Fd())); err == nil && w > 20 {
		width = w - 2
	}
	// Pick the palette from COLORFGBG rather than querying the terminal: a
	// query writes escape sequences a one-shot command cannot wait to read.
	style := gstyles.DarkStyleConfig
	if lightBackground(os.Getenv("COLORFGBG")) {
		style = gstyles.LightStyleConfig
	}
	// Trim the default style the way the TUI does: no document margin, no
	// literal "## " heading prefixes, no heavy inline-code background.
	var zero uint
	style.Document.Margin = &zero
	style.Code.BackgroundColor = nil
	style.Code.Prefix, style.Code.Suffix = "", ""
	for _, h := range []*ansi.StyleBlock{&style.H1, &style.H2, &style.H3, &style.H4, &style.H5, &style.H6} {
		h.Prefix, h.Suffix = "", ""
		h.BackgroundColor = nil
	}
	r, err := glamour.NewTermRenderer(glamour.WithStyles(style), glamour.WithWordWrap(width))
	if err != nil {
		return "", false
	}
	out, err := r.Render(md)
	if err != nil {
		return "", false
	}
	// Glamour pads every line to the wrap width with styled spaces; drop them
	// so copied output carries no trailing whitespace.
	lines := strings.Split(strings.Trim(out, "\n"), "\n")
	for i, ln := range lines {
		lines[i] = trailingPad.ReplaceAllString(ln, "") + "\x1b[0m"
	}
	return strings.Join(lines, "\n") + "\n", true
}

var trailingPad = regexp.MustCompile(`(?:\x1b\[[0-9;]*m| )+$`)

// lightBackground reads the COLORFGBG convention ("fg;bg"): a background of 7
// or 15 is a light terminal.
func lightBackground(colorfgbg string) bool {
	parts := strings.Split(colorfgbg, ";")
	bg := strings.TrimSpace(parts[len(parts)-1])
	return bg == "7" || bg == "15"
}

var (
	mdBold     = regexp.MustCompile(`\*\*(.+?)\*\*`)
	mdBoldU    = regexp.MustCompile(`(^|[\s(])__(\S.*?\S|\S)__($|[\s).,;:!?])`)
	mdItalicU  = regexp.MustCompile(`(^|[\s(])_(\S[^_]*?\S|\S)_($|[\s).,;:!?])`)
	mdItalicS  = regexp.MustCompile(`(^|[\s(])\*(\S[^*]*?\S|\S)\*($|[\s).,;:!?])`)
	mdLink     = regexp.MustCompile(`\[([^\]]+)\]\(([^)\s]+)\)`)
	mdHeading  = regexp.MustCompile(`^\s{0,3}#{1,6}\s+(.*?)\s*#*\s*$`)
	mdTableSep = regexp.MustCompile(`^\|?\s*:?-{2,}:?\s*(\|\s*:?-{2,}:?\s*)*\|?$`)
)

// plainMarkdown flattens markdown to plain text: emphasis markers and inline
// code ticks are dropped, links become "text (url)", headings lose their
// hashes, fenced code is kept verbatim, and tables are re-laid out as aligned
// columns.
func plainMarkdown(md string) string {
	lines := strings.Split(strings.ReplaceAll(md, "\r\n", "\n"), "\n")
	var out []string
	var table [][]string
	flush := func() {
		if table != nil {
			out = append(out, alignTable(table)...)
			table = nil
		}
	}
	inFence := false
	for _, ln := range lines {
		t := strings.TrimSpace(ln)
		if strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~") {
			flush()
			inFence = !inFence
			continue
		}
		if inFence {
			out = append(out, ln)
			continue
		}
		if strings.HasPrefix(t, "|") && strings.Count(t, "|") >= 2 {
			if mdTableSep.MatchString(t) {
				continue
			}
			table = append(table, splitTableRow(t))
			continue
		}
		flush()
		if m := mdHeading.FindStringSubmatch(ln); m != nil {
			out = append(out, inlinePlain(m[1]))
			continue
		}
		out = append(out, inlinePlain(ln))
	}
	flush()
	return strings.Trim(strings.Join(out, "\n"), "\n") + "\n"
}

// inlinePlain strips inline markdown from one line, leaving code spans literal.
func inlinePlain(s string) string {
	parts := strings.Split(s, "`")
	if len(parts)%2 == 0 {
		// An unmatched backtick: leave the line alone rather than guess.
		return stripEmphasis(s)
	}
	for i := range parts {
		if i%2 == 0 {
			parts[i] = stripEmphasis(parts[i])
		}
	}
	return strings.Join(parts, "")
}

func stripEmphasis(s string) string {
	s = mdLink.ReplaceAllStringFunc(s, func(m string) string {
		sub := mdLink.FindStringSubmatch(m)
		if sub[1] == sub[2] {
			return sub[2]
		}
		return sub[1] + " (" + sub[2] + ")"
	})
	s = mdBold.ReplaceAllString(s, "$1")
	s = mdBoldU.ReplaceAllString(s, "$1$2$3")
	s = mdItalicU.ReplaceAllString(s, "$1$2$3")
	s = mdItalicS.ReplaceAllString(s, "$1$2$3")
	return s
}

func splitTableRow(row string) []string {
	row = strings.TrimSpace(row)
	row = strings.TrimPrefix(row, "|")
	row = strings.TrimSuffix(row, "|")
	cells := strings.Split(row, "|")
	for i, c := range cells {
		cells[i] = inlinePlain(strings.TrimSpace(c))
	}
	return cells
}

// alignTable pads every column to its widest cell, with a rule under the first
// row (the header).
func alignTable(rows [][]string) []string {
	cols := 0
	for _, r := range rows {
		if len(r) > cols {
			cols = len(r)
		}
	}
	widths := make([]int, cols)
	for _, r := range rows {
		for i, c := range r {
			if w := lipgloss.Width(c); w > widths[i] {
				widths[i] = w
			}
		}
	}
	format := func(r []string) string {
		var b strings.Builder
		for i := 0; i < cols; i++ {
			c := ""
			if i < len(r) {
				c = r[i]
			}
			b.WriteString(c)
			if i < cols-1 {
				b.WriteString(strings.Repeat(" ", widths[i]-lipgloss.Width(c)+2))
			}
		}
		return strings.TrimRight(b.String(), " ")
	}
	out := make([]string, 0, len(rows)+1)
	for i, r := range rows {
		out = append(out, format(r))
		if i == 0 && len(rows) > 1 {
			rule := make([]string, cols)
			for j, w := range widths {
				rule[j] = strings.Repeat("-", w)
			}
			out = append(out, format(rule))
		}
	}
	return out
}
