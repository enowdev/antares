package tui

import (
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// ---- generic list picker -----------------------------------------------------

// pickerItem is one selectable row. right is pre-rendered content shown on the
// right (a colour swatch, a provider name, a status). meta carries extra data
// (e.g. a model's provider) for the commit handler.
type pickerItem struct {
	id    string
	label string
	right string
	meta  string
}

// picker is a centred, clickable modal list. It previews live as the selection
// moves (keyboard or wheel) and commits on Enter or click; Esc or a click
// outside cancels. It scrolls when the list is taller than the screen.
type picker struct {
	active  bool
	title   string
	hint    string
	footer  string
	items   []pickerItem
	cursor  int // index into the *filtered* view
	query   string
	preview func(m *Model, it pickerItem)
	commit  func(m *Model, it pickerItem)
	cancel  func(m *Model)

	// layout recorded at render time for click hit-testing
	x, w      int
	rowY0     int
	rowRows   int
	viewStart int
}

func (m *Model) openPicker(p picker) {
	if p.cursor < 0 || p.cursor >= len(p.items) {
		p.cursor = 0
	}
	if p.footer == "" {
		p.footer = "type to filter · ↑↓ move · Enter select · Esc cancel"
	}
	p.active = true
	m.picker = p
}

// vis returns the indices of items matching the current search query.
func (p *picker) vis() []int {
	q := strings.ToLower(strings.TrimSpace(p.query))
	idx := make([]int, 0, len(p.items))
	for i, it := range p.items {
		if q == "" || strings.Contains(strings.ToLower(it.label), q) {
			idx = append(idx, i)
		}
	}
	return idx
}

func (p *picker) move(m *Model, d int) {
	v := p.vis()
	if len(v) == 0 {
		return
	}
	p.previewAt(m, (p.cursor+d+len(v))%len(v))
}

// previewAt takes a position within the filtered view.
func (p *picker) previewAt(m *Model, pos int) {
	v := p.vis()
	if pos < 0 || pos >= len(v) {
		return
	}
	p.cursor = pos
	if p.preview != nil {
		p.preview(m, p.items[v[pos]])
	}
}

func (p *picker) doCommit(m *Model) {
	v := p.vis()
	if len(v) == 0 || p.cursor >= len(v) {
		p.active = false
		return
	}
	it := p.items[v[p.cursor]]
	p.active = false
	if p.commit != nil {
		p.commit(m, it)
	}
}

// setQuery updates the filter text and resets the highlight to the first match.
func (p *picker) setQuery(m *Model, q string) {
	p.query = q
	p.cursor = 0
	p.previewAt(m, 0)
}

func (p *picker) doCancel(m *Model) {
	p.active = false
	if p.cancel != nil {
		p.cancel(m)
	}
}

func (p *picker) rowAt(x, y int) int {
	if x < p.x || x >= p.x+p.w {
		return -1
	}
	if y < p.rowY0 || y >= p.rowY0+p.rowRows {
		return -1
	}
	return p.viewStart + (y - p.rowY0)
}

func (p *picker) onMouse(m *Model, e tea.MouseMsg) {
	switch e.Button {
	case tea.MouseButtonWheelUp:
		p.move(m, -1)
		return
	case tea.MouseButtonWheelDown:
		p.move(m, 1)
		return
	}
	idx := p.rowAt(e.X, e.Y)
	switch e.Action {
	case tea.MouseActionMotion:
		if idx >= 0 {
			p.previewAt(m, idx)
		}
	case tea.MouseActionPress:
		if e.Button != tea.MouseButtonLeft {
			return
		}
		if idx >= 0 {
			p.previewAt(m, idx)
			p.doCommit(m)
		} else {
			p.doCancel(m)
		}
	}
}

func (p *picker) onKey(m *Model, msg tea.KeyMsg) {
	switch msg.Type {
	case tea.KeyUp, tea.KeyCtrlP:
		p.move(m, -1)
	case tea.KeyDown, tea.KeyCtrlN:
		p.move(m, 1)
	case tea.KeyEnter:
		p.doCommit(m)
	case tea.KeyEsc, tea.KeyCtrlC:
		p.doCancel(m)
	case tea.KeyBackspace, tea.KeyDelete:
		if r := []rune(p.query); len(r) > 0 {
			p.setQuery(m, string(r[:len(r)-1]))
		}
	case tea.KeySpace:
		p.setQuery(m, p.query+" ")
	case tea.KeyRunes:
		p.setQuery(m, p.query+string(msg.Runes)) // type to filter
	}
}

func (m *Model) renderPickerModal(base string) string {
	W, H := m.width, m.height
	t := themeByName(m.themeName)
	pk := &m.picker

	title := lipgloss.NewStyle().Foreground(t.Accent).Bold(true).Render(pk.title)
	if pk.hint != "" {
		title += "  " + lipgloss.NewStyle().Foreground(t.Faint).Render(pk.hint)
	}

	// Search line: shows the live query, or a placeholder when empty.
	var search string
	if pk.query != "" {
		search = lipgloss.NewStyle().Foreground(t.Accent).Render("⌕ "+pk.query) +
			lipgloss.NewStyle().Foreground(t.Faint).Render("▏")
	} else {
		search = lipgloss.NewStyle().Foreground(t.Faint).Render("⌕ type to filter")
	}

	vis := pk.vis()
	labelW := 0
	for _, i := range vis {
		if w := lipgloss.Width(pk.items[i].label); w > labelW {
			labelW = w
		}
	}

	// Window the filtered list so it never outgrows the screen.
	maxRows := H - 11
	if maxRows < 3 {
		maxRows = 3
	}
	start, end := 0, len(vis)
	if len(vis) > maxRows {
		start = pk.cursor - maxRows/2
		if start < 0 {
			start = 0
		}
		if start > len(vis)-maxRows {
			start = len(vis) - maxRows
		}
		end = start + maxRows
	}

	var rows []string
	if start > 0 {
		rows = append(rows, lipgloss.NewStyle().Foreground(t.Faint).Render("  ↑ more"))
	}
	if len(vis) == 0 {
		rows = append(rows, lipgloss.NewStyle().Foreground(t.Faint).Render("  no matches"))
	}
	for pos := start; pos < end; pos++ {
		it := pk.items[vis[pos]]
		var left string
		if pos == pk.cursor {
			left = lipgloss.NewStyle().Foreground(t.Accent).Bold(true).Render("❯ " + padRight(it.label, labelW))
		} else {
			left = lipgloss.NewStyle().Foreground(t.Muted).Render("  " + padRight(it.label, labelW))
		}
		if it.right != "" {
			left += "   " + it.right
		}
		rows = append(rows, left)
	}
	if end < len(vis) {
		rows = append(rows, lipgloss.NewStyle().Foreground(t.Faint).Render("  ↓ more"))
	}
	footer := lipgloss.NewStyle().Foreground(t.Faint).Render(pk.footer)

	lines := append([]string{title, search, ""}, rows...)
	lines = append(lines, "", footer)
	box := modalBox(t).Render(strings.Join(lines, "\n"))

	boxW, boxH := lipgloss.Width(box), lipgloss.Height(box)
	x, y := (W-boxW)/2, (H-boxH)/2
	if x < 0 {
		x = 0
	}
	if y < 0 {
		y = 0
	}

	// Rows begin after border(1)+pad-top(1)+title(1)+search(1)+blank(1), plus a
	// "↑ more" line when the list is scrolled down.
	firstRow := y + 5
	if start > 0 {
		firstRow++
	}
	pk.x, pk.w = x, boxW
	pk.rowY0 = firstRow
	pk.rowRows = end - start
	pk.viewStart = start

	return placeModal(base, box, x, y, W, H, t)
}

// ---- single-field input modal ------------------------------------------------

// inputModal is a centred prompt for one line of text (e.g. an API key).
type inputModal struct {
	active bool
	title  string
	hint   string
	ti     textinput.Model
	submit func(m *Model, value string)
	cancel func(m *Model)
}

func (m *Model) openInput(title, hint, placeholder string, mask bool, submit func(*Model, string), cancel func(*Model)) {
	ti := textinput.New()
	ti.Placeholder = placeholder
	ti.Prompt = "❯ "
	ti.CharLimit = 4000
	ti.Width = 44
	if mask {
		ti.EchoMode = textinput.EchoPassword
		ti.EchoCharacter = '•'
	}
	ti.PromptStyle = lipgloss.NewStyle().Foreground(themeByName(m.themeName).Accent)
	ti.Focus()
	m.input = inputModal{active: true, title: title, hint: hint, ti: ti, submit: submit, cancel: cancel}
}

func (im *inputModal) onKey(m *Model, msg tea.KeyMsg) tea.Cmd {
	switch msg.Type {
	case tea.KeyEnter:
		v := strings.TrimSpace(im.ti.Value())
		sub := im.submit
		im.active = false
		if sub != nil {
			sub(m, v)
		}
		return nil
	case tea.KeyEsc, tea.KeyCtrlC:
		c := im.cancel
		im.active = false
		if c != nil {
			c(m)
		}
		return nil
	}
	var cmd tea.Cmd
	im.ti, cmd = im.ti.Update(msg)
	return cmd
}

func (m *Model) renderInputModal(base string) string {
	W, H := m.width, m.height
	t := themeByName(m.themeName)
	im := &m.input

	title := lipgloss.NewStyle().Foreground(t.Accent).Bold(true).Render(im.title)
	field := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).BorderForeground(t.Border).
		Padding(0, 1).Width(48).Render(im.ti.View())
	var lines []string
	lines = append(lines, title)
	if im.hint != "" {
		lines = append(lines, lipgloss.NewStyle().Foreground(t.Faint).Render(im.hint))
	}
	lines = append(lines, "", field, "",
		lipgloss.NewStyle().Foreground(t.Faint).Render("Enter save · Esc cancel"))
	box := modalBox(t).Render(strings.Join(lines, "\n"))

	boxW, boxH := lipgloss.Width(box), lipgloss.Height(box)
	x, y := (W-boxW)/2, (H-boxH)/2
	if x < 0 {
		x = 0
	}
	if y < 0 {
		y = 0
	}
	return placeModal(base, box, x, y, W, H, t)
}

// ---- shared rendering --------------------------------------------------------

func modalBox(t Theme) lipgloss.Style {
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(t.Accent).
		Padding(1, 2)
}

// placeModal composites box at (x,y) over a dimmed copy of base, falling back to
// a plain centred placement when the box is larger than the screen.
func placeModal(base, box string, x, y, w, h int, t Theme) string {
	if lipgloss.Width(box) > w || lipgloss.Height(box) > h {
		return clampHeight(lipgloss.Place(w, h, lipgloss.Center, lipgloss.Center, box), h)
	}
	dim := lipgloss.NewStyle().Foreground(t.Faint)
	return clampHeight(overlayBox(base, box, x, y, w, h, dim), h)
}

// swatch renders a theme's palette as coloured dots for a visual preview.
func swatch(th Theme) string {
	dot := func(c lipgloss.TerminalColor) string {
		return lipgloss.NewStyle().Foreground(c).Render("●")
	}
	return dot(th.Accent) + dot(th.Green) + dot(th.Yellow) + dot(th.Red) + dot(th.Muted)
}

// overlayBox draws box at (x,y) over a dimmed, plain-text copy of base.
func overlayBox(base, box string, x, y, w, h int, dim lipgloss.Style) string {
	if x < 0 {
		x = 0
	}
	if y < 0 {
		y = 0
	}
	bp := dimPlain(base, w, h)
	bl := strings.Split(box, "\n")
	out := make([]string, h)
	for i := 0; i < h; i++ {
		r := []rune(bp[i])
		bi := i - y
		if bi >= 0 && bi < len(bl) {
			bw := lipgloss.Width(bl[bi])
			lx := x
			if lx > w {
				lx = w
			}
			rx := x + bw
			if rx > w {
				rx = w
			}
			out[i] = dim.Render(string(r[:lx])) + bl[bi] + dim.Render(string(r[rx:]))
		} else {
			out[i] = dim.Render(string(r))
		}
	}
	return strings.Join(out, "\n")
}

// dimPlain strips colour from base and normalises it to exactly H lines of W
// runes, so a modal can be spliced in without worrying about ANSI state.
func dimPlain(base string, w, h int) []string {
	src := strings.Split(base, "\n")
	out := make([]string, h)
	for i := 0; i < h; i++ {
		line := ""
		if i < len(src) {
			line = stripANSI(src[i])
		}
		r := []rune(line)
		if len(r) > w {
			r = r[:w]
		}
		for len(r) < w {
			r = append(r, ' ')
		}
		out[i] = string(r)
	}
	return out
}

func stripANSI(s string) string {
	var b strings.Builder
	esc := false
	for _, r := range s {
		if esc {
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
				esc = false
			}
			continue
		}
		if r == 0x1b {
			esc = true
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func padRight(s string, n int) string {
	for len([]rune(s)) < n {
		s += " "
	}
	return s
}

func indexOf(list []string, s string) int {
	for i, v := range list {
		if v == s {
			return i
		}
	}
	return -1
}
