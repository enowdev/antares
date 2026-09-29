package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// paletteWindow is the [lo, hi) slice of n palette rows to show so that sel
// stays visible, at most rows tall.
func paletteWindow(n, sel, rows int) (lo, hi int) {
	if n <= rows {
		return 0, n
	}
	lo = sel - rows/2
	if lo < 0 {
		lo = 0
	}
	if lo+rows > n {
		lo = n - rows
	}
	return lo, lo + rows
}

// refreshTranscript rebuilds the viewport content, keeping it pinned to the
// newest line unless the user scrolled up.
func (m *Model) refreshTranscript() {
	if !m.ready {
		return
	}
	atBottom := m.vp.AtBottom()
	m.resizeViewport()
	m.vp.SetContent(m.renderBlocks())
	if atBottom || m.busy {
		m.vp.GotoBottom()
	}
}

// ---- helpers ----------------------------------------------------------------

func wrap(s string, w int) string {
	if w < 4 {
		w = 4
	}
	return lipgloss.NewStyle().Width(w).Render(s)
}

func indent(s, prefix string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = prefix + l
	}
	return strings.Join(lines, "\n")
}

// clampHeight caps the composed view at the terminal height so a degenerate
// tiny terminal can never push content past the bottom and make the alt-screen
// scroll. In normal sizes the view is already exactly h rows, so this is a no-op.
func clampHeight(s string, h int) string {
	if h < 1 {
		h = 1
	}
	lines := strings.Split(s, "\n")
	if len(lines) > h {
		lines = lines[:h]
	}
	return strings.Join(lines, "\n")
}

func clampLines(s string, maxLines, w int) string {
	s = wrap(s, w)
	lines := strings.Split(s, "\n")
	if len(lines) > maxLines {
		lines = lines[:maxLines]
		return strings.Join(lines, "\n") + "\n…"
	}
	return strings.Join(lines, "\n")
}

func pad(s string, n int) string {
	for len([]rune(s)) < n {
		s += " "
	}
	return s
}

func shortID(id string) string {
	if len(id) > 10 {
		return id[:10]
	}
	return id
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	if n < 1 {
		return ""
	}
	return string(r[:n-1]) + "…"
}

func firstNon(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}

func maxi(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
