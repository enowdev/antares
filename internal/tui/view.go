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

func (m *Model) renderBlocks() string {
	if m.showWelcome() {
		return m.welcomeView(m.vp.Width, m.vp.Height)
	}
	var out []string
	for i := range m.blocks {
		bl := m.blocks[i]
		if bl.kind == blockReasoning && !m.showReasoning {
			continue
		}
		out = append(out, m.renderBlockCached(bl))
	}
	return strings.Join(out, "\n\n")
}

// renderBlockCached memoises rendered output for settled blocks so streaming
// stays smooth even with a long transcript (Glamour is the expensive part).
func (m *Model) renderBlockCached(bl block) string {
	if bl.streaming || (bl.kind == blockAssistant && !bl.done) {
		return m.renderBlock(bl)
	}
	key := fmt.Sprintf("%d|%d|%v|%v|%v|%s|%s", bl.kind, m.vp.Width, bl.done, bl.isError, bl.markdown, bl.title, bl.text)
	if m.cache == nil {
		m.cache = map[string]string{}
	}
	if s, ok := m.cache[key]; ok {
		return s
	}
	s := m.renderBlock(bl)
	m.cache[key] = s
	return s
}

func (m *Model) renderBlock(bl block) string {
	cw := m.vp.Width
	switch bl.kind {
	case blockUser:
		label := m.st.userLabel.Render(" You ")
		box := m.st.userBox.Width(cw - 2).Render(m.st.userText.Render(wrap(bl.text, cw-6)))
		return label + "\n" + box

	case blockAssistant:
		return m.st.asstLabel.Render("antares") + "\n" + m.markdown(bl.text, cw-1)

	case blockReasoning:
		return m.st.reasonLbl.Render("thinking") + "\n" +
			m.st.reasonBar.Render(m.st.reasoning.Render(wrap(bl.text, cw-3)))

	case blockTool:
		return m.renderTool(bl, cw)

	case blockNotice:
		return m.st.notice.Render(wrap(bl.text, cw))

	case blockError:
		return m.st.errLabel.Render("error") + "\n" +
			m.st.errBar.Render(m.st.errText.Render(wrap(bl.text, cw-3)))

	case blockSystem:
		if bl.markdown {
			return m.markdown(bl.text, cw-1)
		}
		return m.st.system.Render(wrap(bl.text, cw))
	}
	return bl.text
}

// renderTool draws a tool call as a single clean line — a status glyph, the tool
// name, and a dim argument summary — with its result under a faint left rule.
func (m *Model) renderTool(bl block, cw int) string {
	var icon string
	switch {
	case bl.isError:
		icon = m.st.stErr.Render("✗")
	case bl.done:
		icon = m.st.stDone.Render("✓")
	default:
		icon = m.st.stRunning.Render("●")
	}
	name, summary := bl.title, ""
	if i := strings.Index(bl.title, "  "); i >= 0 {
		name, summary = bl.title[:i], strings.TrimSpace(bl.title[i:])
	}
	head := icon + " " + m.st.toolName.Bold(true).Render(name)
	if summary != "" {
		head += "  " + m.st.toolArgs.Render(summary)
	}
	body := strings.TrimSpace(bl.text)
	if body == "" {
		return head
	}
	return head + "\n" + m.st.toolBar.Render(m.st.toolResult.Render(clampLines(body, 10, cw-3)))
}

func (m *Model) markdown(text string, w int) string {
	if m.renderer != nil {
		if s, err := m.renderer.Render(text); err == nil {
			return strings.TrimRight(s, "\n")
		}
	}
	return wrap(text, w)
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
