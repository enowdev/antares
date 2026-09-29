package tui

import (
	"fmt"
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

// swatch renders a theme's palette as coloured dots for a visual preview.
func swatch(th Theme) string {
	dot := func(c lipgloss.TerminalColor) string {
		return lipgloss.NewStyle().Foreground(c).Render("●")
	}
	return dot(th.Accent) + dot(th.Green) + dot(th.Yellow) + dot(th.Red) + dot(th.Muted)
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

// ---- overlays ------------------------------------------------------------------

// placeOverlay centres a box of the given width around body and lays it over
// base: the same rounded box as the layout's own, with an accent edge because
// it has the keyboard, its title in the top edge and its keys in the bottom
// edge. The border and the padding are added here, so no caller counts them.
// It returns the screen and where the box's first content row landed.
func (m *Model) placeOverlay(base []string, width int, body []string, title, hint, pager string) (out []string, x, firstRow int) {
	t := themeByName(m.themeName)
	W, H := m.width, m.height
	width = lesser(width, maxi(W-4, 8))
	h := lesser(len(body)+2+2*padY, maxi(H-2, 3))
	lines := drawBox(box{
		w: width, h: h, border: t.Accent, fill: t.Panel,
		title: fg(t.Accent).Bold(true).Render(strings.ToUpper(strings.TrimSpace(title))),
		hint:  fg(t.Muted).Render(hint), pager: pager, body: body, vpad: true,
	})
	x, y := maxi((W-width)/2, 0), maxi((H-h)/2, 0)
	_, _, _, py := contentSize(width, h, true)
	return splice(base, lines, x, y), x, y + 1 + py
}

// renderPickerModal draws the open picker over the screen: a search row, then
// the list with the `›` marker and a background-only band on the selected
// row, so the colours inside the row survive.
func (m *Model) renderPickerModal(base []string) []string {
	t := themeByName(m.themeName)
	pk := &m.picker
	vis := pk.vis()
	labelW, rightW := 0, 0
	for _, i := range vis {
		labelW = maxi(labelW, lipgloss.Width(pk.items[i].label))
		rightW = maxi(rightW, lipgloss.Width(pk.items[i].right))
	}
	width := clampi(labelW+rightW+5+2*(1+padX), 60, 100)
	width = lesser(width, maxi(m.width-4, 8))
	cw := maxi(width-2-2*padX, 1)

	var head []string
	if pk.hint != "" {
		head = append(head, fg(t.Muted).Render(pk.hint), "")
	}
	query := fg(t.Muted).Render("search  ") + fg(t.Text).Render(pk.query) + fg(t.Accent).Render("_")
	count := fg(t.Faint).Render(fmt.Sprintf("%d of %d", len(vis), len(pk.items)))
	head = append(head, splitLine(query, count, cw), "")

	// Window the filtered list so the box never outgrows the screen.
	maxRows := maxi(m.height-2-2-2*padY-len(head), 3)
	start, end := 0, len(vis)
	if len(vis) > maxRows {
		start = clampi(pk.cursor-maxRows/2, 0, len(vis)-maxRows)
		end = start + maxRows
	}
	body := append([]string(nil), head...)
	if len(vis) == 0 {
		body = append(body, fg(t.Faint).Render("  no matches"))
	}
	for pos := start; pos < end; pos++ {
		it := pk.items[vis[pos]]
		row := "  " + fg(t.Text).Render(padRight(it.label, labelW))
		if pos == pk.cursor {
			row = fg(t.Accent).Bold(true).Render("›") + " " + fg(t.Text).Bold(true).Render(padRight(it.label, labelW))
		}
		if it.right != "" {
			row += "   " + it.right
		}
		if pos == pk.cursor {
			row = paint(row, cw, t.Band)
		}
		body = append(body, row)
	}
	pager := ""
	if len(vis) > maxRows {
		pager = fg(t.Muted).Render(fmt.Sprintf("%d/%d", pk.cursor+1, len(vis)))
	}
	out, x, first := m.placeOverlay(base, width, body, pk.title, pk.footer, pager)
	pk.x, pk.w = x, width
	pk.rowY0 = first + len(head)
	pk.rowRows = end - start
	pk.viewStart = start
	return out
}

// renderInputModal draws the one-line prompt over the screen.
func (m *Model) renderInputModal(base []string) []string {
	t := themeByName(m.themeName)
	im := &m.input
	width := lesser(68, maxi(m.width-4, 8))
	cw := maxi(width-2-2*padX, 1)
	im.ti.Width = maxi(cw-4, 4)
	var body []string
	if im.hint != "" {
		for _, ln := range strings.Split(wrapPlain(im.hint, cw), "\n") {
			body = append(body, fg(t.Muted).Render(ln))
		}
		body = append(body, "")
	}
	body = append(body, paint(im.ti.View(), cw, t.Subtle))
	out, _, _ := m.placeOverlay(base, width, body, im.title, "Enter save · Esc cancel", "")
	return out
}
