package tui

// sidebar.go is the side column: the SESSION card (how full the context is,
// tokens, cost, tool calls) and the detail card, whose tabs Agents · Tools ·
// Skills · Log sit in its top edge and whose pager sits in its bottom edge.
//
// The figures come from state the model already keeps (blocks, token and
// context counters) plus a few per-session counters fed by observe, which
// sees each agent event before applyEvent folds it into the transcript.

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"

	"github.com/enowdev/antares/internal/agent"
	"github.com/enowdev/antares/internal/providers"
)

// sideTabs are the detail card's tabs, in the order F1–F4 and Alt+1–4 pick.
var sideTabs = [4]string{"Agents", "Tools", "Skills", "Log"}

const (
	tabAgents = iota
	tabTools
	tabSkills
	tabLog
)

// labelColumn is the SESSION card's label width, so the figures start on one
// column and read down as a block.
const labelColumn = 9

// chromeState is the layout's own state: what the person chose (side column
// shown, tab, page, highlighted answer) and where things were drawn, for the
// mouse.
type chromeState struct {
	sideHidden bool
	tab        int
	page       int
	pages      int
	askSel     int
	askID      string // the question askSel belongs to

	// Recorded while drawing.
	sideRect  rect
	chatInner rect // where the transcript viewport sits on screen
	tabHits   []tabHit
	pagerHit  rect

	stats sessionStats

	skillCount   int
	skillCountAt time.Time
}

type tabHit struct {
	r   rect
	tab int
}

type logEntry struct {
	at   time.Time
	kind string // notice | error | retry
	text string
}

// sessionStats are the per-session counters the side column shows that no
// block records: tokens and cost summed over every turn, the skills read and
// the notices, errors and retries.
type sessionStats struct {
	session string
	run     chan tea.Msg // the turn the run totals belong to
	runIn   int
	runOut  int
	in, out int
	cost    float64
	priced  bool
	skills  []string
	log     []logEntry
}

// syncSession drops the counters when the conversation changes: another
// session, or a new one after /new.
func (m *Model) syncSession() {
	st := &m.chrome.stats
	if st.session == m.sessionID {
		return
	}
	// The first turn of a new conversation names its session; that is the
	// same conversation, not a new one.
	if st.session == "" && len(m.blocks) > 0 {
		st.session = m.sessionID
		return
	}
	m.chrome.stats = sessionStats{session: m.sessionID}
	m.chrome.page = 0
}

// observe counts one agent event. It never changes the transcript.
func (m *Model) observe(e agent.Event) {
	m.syncSession()
	st := &m.chrome.stats
	switch e.Type {
	case agent.EventUsage:
		// A turn reports its running totals; the session's are the sum of
		// every turn's last.
		if st.run != m.msgCh {
			st.run, st.runIn, st.runOut = m.msgCh, 0, 0
		}
		dIn, dOut := e.InputTokens-st.runIn, e.OutputTokens-st.runOut
		if dIn < 0 || dOut < 0 {
			dIn, dOut = e.InputTokens, e.OutputTokens
		}
		st.runIn, st.runOut = e.InputTokens, e.OutputTokens
		st.in += dIn
		st.out += dOut
		if in, out, ok := m.pricing(); ok {
			st.cost += (float64(dIn)*in + float64(dOut)*out) / 1_000_000
			st.priced = true
		}
	case agent.EventToolCall:
		if e.Name == "skill" {
			var a struct{ Action, Name string }
			if json.Unmarshal([]byte(e.Arguments), &a) == nil && a.Action == "read" && a.Name != "" {
				st.addSkill(a.Name)
			}
		}
	case agent.EventNotice:
		st.addLog("notice", e.Message)
	case agent.EventError:
		st.addLog("error", e.Err)
	case agent.EventReset:
		st.addLog("retry", "Retrying after a provider error")
	}
}

func (st *sessionStats) addSkill(name string) {
	for _, s := range st.skills {
		if s == name {
			return
		}
	}
	st.skills = append(st.skills, name)
}

func (st *sessionStats) addLog(kind, text string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	st.log = append(st.log, logEntry{at: time.Now(), kind: kind, text: text})
	if len(st.log) > 100 {
		st.log = st.log[len(st.log)-100:]
	}
}

// pricing is the active model's price per million tokens in and out, from
// the same models.dev metadata the dashboard reads.
func (m *Model) pricing() (in, out float64, ok bool) {
	if m.cfg == nil || m.cfg.Model.Default == "" {
		return 0, 0, false
	}
	meta, found := providers.MetaByProvider(m.cfg.Model.Provider, m.cfg.Model.Default)
	if !found || !meta.Cost.HasCost() {
		return 0, 0, false
	}
	return meta.Cost.Input, meta.Cost.Output, true
}

// sessionCost is what the session's turns cost, when the model has a price.
func (m *Model) sessionCost() (float64, bool) {
	m.syncSession()
	st := m.chrome.stats
	if st.priced {
		return st.cost, true
	}
	if _, _, ok := m.pricing(); ok {
		return 0, true
	}
	return 0, false
}

func formatCost(c float64) string {
	if c >= 1 {
		return fmt.Sprintf("$%.2f", c)
	}
	return fmt.Sprintf("$%.4f", c)
}

// toolCalls is the session's tool calls, newest first, and how many failed.
func (m *Model) toolCalls() (calls []block, failed int) {
	for i := len(m.blocks) - 1; i >= 0; i-- {
		b := m.blocks[i]
		if b.kind != blockTool {
			continue
		}
		calls = append(calls, b)
		if b.isError {
			failed++
		}
	}
	return calls, failed
}

// ---- keys and mouse ------------------------------------------------------------

// chromeKey handles the layout's own keys: Ctrl+B shows or hides the side
// column, F1–F4 and Alt+1–4 pick a tab, and ↑↓ Enter work the QUESTION box.
// It reports whether it used the key.
func (m *Model) chromeKey(msg tea.KeyMsg) bool {
	switch msg.Type {
	case tea.KeyCtrlB:
		m.chrome.sideHidden = !m.chrome.sideHidden
		m.layout()
		m.refreshTranscript()
		return true
	case tea.KeyF1, tea.KeyF2, tea.KeyF3, tea.KeyF4:
		m.selectTab(int(tea.KeyF1 - msg.Type))
		return true
	case tea.KeyRunes:
		if msg.Alt && len(msg.Runes) == 1 && msg.Runes[0] >= '1' && msg.Runes[0] <= '4' {
			m.selectTab(int(msg.Runes[0] - '1'))
			return true
		}
	}
	return m.askKey(msg)
}

func (m *Model) selectTab(i int) {
	if i < 0 || i >= len(sideTabs) {
		return
	}
	m.chrome.tab, m.chrome.page = i, 0
}

// chromeMouse turns the side card's pages with the wheel and takes clicks on
// its tabs and pager. It reports whether it used the event.
func (m *Model) chromeMouse(msg tea.MouseMsg) bool {
	if m.isHome() || !m.chrome.sideRect.contains(msg.X, msg.Y) {
		return false
	}
	switch msg.Button {
	case tea.MouseButtonWheelUp:
		m.turnPage(-1)
		return true
	case tea.MouseButtonWheelDown:
		m.turnPage(1)
		return true
	case tea.MouseButtonLeft:
		if msg.Action != tea.MouseActionPress {
			return true
		}
		for _, h := range m.chrome.tabHits {
			if h.r.contains(msg.X, msg.Y) {
				m.selectTab(h.tab)
				return true
			}
		}
		if p := m.chrome.pagerHit; p.contains(msg.X, msg.Y) {
			if msg.X < p.x+p.w/2 {
				m.turnPage(-1)
			} else {
				m.turnPage(1)
			}
		}
		return true
	}
	return true
}

func (m *Model) turnPage(d int) {
	m.chrome.page = clampi(m.chrome.page+d, 0, maxi(m.chrome.pages-1, 0))
}

// ---- drawing ---------------------------------------------------------------------

// sideLines draws the side column: the SESSION card when there is room for
// it, then the detail card down to the row above the status bar.
func (m *Model) sideLines(r rect, sessionCard bool) []string {
	t := themeByName(m.themeName)
	m.syncSession()
	m.chrome.tabHits = m.chrome.tabHits[:0]
	m.chrome.pagerHit = rect{}
	out := make([]string, 0, r.h)
	detail := r
	if sessionCard {
		cw, _, _, _ := contentSize(r.w, 10, true)
		rows := m.sessionRows(cw)
		h := lesser(len(rows)+2+2*padY, r.h)
		out = append(out, drawBox(box{
			w: r.w, h: h, border: t.Border, fill: t.Panel,
			title: fg(t.Muted).Bold(true).Render("SESSION"), body: rows, vpad: true,
		})...)
		detail = rect{r.x, r.y + h, r.w, r.h - h}
	}
	if detail.h >= 3 {
		out = append(out, m.detailLines(detail)...)
	}
	for len(out) < r.h {
		out = append(out, paint("", r.w, t.Canvas))
	}
	return out
}

// sessionRows are the figures the side column is glanced at for.
func (m *Model) sessionRows(w int) []string {
	t := themeByName(m.themeName)
	label := func(s string) string { return fg(t.Muted).Render(padRight(s, labelColumn)) }
	value := func(s string) string { return fg(t.Text).Render(s) }
	var rows []string

	// How full the context is leads: it decides whether to compact. The bar
	// turns red once little room is left, and the figure says the same in
	// words for a terminal without colour.
	if pct, ok := m.contextPercent(); ok {
		col := t.Accent
		if pct >= 85 {
			col = t.Red
		}
		figure := fmt.Sprintf("%d%%", pct)
		track := maxi(w-labelColumn-5, 4)
		filled := clampi((track*lesser(pct, 100)+50)/100, 0, track)
		rows = append(rows, label("context")+fg(col).Bold(true).Render(padRight(figure, 5))+
			fg(col).Render(strings.Repeat("▰", filled))+fg(t.Faint).Render(strings.Repeat("▱", track-filled)))
		rows = append(rows, label("window")+value(thousands(m.ctxUsed)+" / "+thousands(m.contextWindow())))
	} else {
		rows = append(rows, label("context")+fg(t.Faint).Render("window unknown"))
	}

	st := m.chrome.stats
	in, out := st.in, st.out
	if in == 0 && out == 0 {
		in, out = m.tokensIn, m.tokensOut
	}
	rows = append(rows, label("tokens")+value(thousands(in)+" in · "+thousands(out)+" out"))
	if cost, ok := m.sessionCost(); ok {
		rows = append(rows, label("cost")+value(formatCost(cost)))
	} else {
		rows = append(rows, label("cost")+fg(t.Faint).Render("no price for this model"))
	}

	calls, failed := m.toolCalls()
	noun := "calls"
	if len(calls) == 1 {
		noun = "call"
	}
	tools := label("tools") + value(fmt.Sprintf("%d %s", len(calls), noun))
	if failed > 0 {
		tools += fg(t.Red).Render(fmt.Sprintf("  %d failed", failed))
	}
	rows = append(rows, tools)
	if m.sessionID != "" {
		rows = append(rows, label("session")+fg(t.Faint).Render(shortID(m.sessionID)))
	}
	return rows
}

// detailLines draws the tabbed card and pages its content.
func (m *Model) detailLines(r rect) []string {
	t := themeByName(m.themeName)
	cw, ch, _, py := contentSize(r.w, r.h, true)
	lines := m.tabLines(cw)
	page := maxi(ch, 1)
	m.chrome.pages = maxi((len(lines)+page-1)/page, 1)
	m.chrome.page = clampi(m.chrome.page, 0, m.chrome.pages-1)
	start := m.chrome.page * page
	body := lines[lesser(start, len(lines)):lesser(start+page, len(lines))]

	pager := ""
	if m.chrome.pages > 1 {
		pager = fg(t.Muted).Render(fmt.Sprintf("◀ %d/%d ▶", m.chrome.page+1, m.chrome.pages))
		pw := ansi.StringWidth(pager) + 2
		m.chrome.pagerHit = rect{r.x + r.w - 2 - pw, r.y + r.h - 1, pw, 1}
	}
	_ = py
	return drawBox(box{
		w: r.w, h: r.h, border: t.Border, fill: t.Panel,
		top: m.tabsEdge(r), pager: pager, body: body, vpad: true,
	})
}

// tabsEdge is the detail card's top edge with the tabs set into it. The
// selected tab is bold and in the accent colour, so it stays marked without
// colour too; each name is a click target.
func (m *Model) tabsEdge(r rect) string {
	t := themeByName(m.themeName)
	bs := fg(t.Border)
	var b strings.Builder
	b.WriteString(bs.Render("╭─"))
	x := 2
	limit := r.w - 2
	plain := lipgloss.ColorProfile() == termenv.Ascii
	for i, name := range sideTabs {
		text := " " + name + " "
		st := fg(t.Muted)
		if i == m.chrome.tab {
			st = fg(t.Accent).Bold(true)
			if plain {
				// Without colour, bold goes too; the › says which tab.
				text = "›" + name + " "
			}
		}
		w := ansi.StringWidth(text)
		if x+w > limit {
			break
		}
		b.WriteString(st.Render(text))
		m.chrome.tabHits = append(m.chrome.tabHits, tabHit{rect{r.x + x, r.y, w, 1}, i})
		x += w
		if i+1 < len(sideTabs) && x+1 < limit {
			b.WriteString(fg(t.Faint).Render("·"))
			x++
		}
	}
	b.WriteString(bs.Render(strings.Repeat("─", maxi(r.w-1-x, 0)) + "╮"))
	return b.String()
}

// tabLines is the selected tab's content, one line per row, w cells wide.
func (m *Model) tabLines(w int) []string {
	switch m.chrome.tab {
	case tabTools:
		return m.toolsTab(w)
	case tabSkills:
		return m.skillsTab(w)
	case tabLog:
		return m.logTab(w)
	}
	return m.agentsTab(w)
}

func (m *Model) heading(lines []string, title string) []string {
	t := themeByName(m.themeName)
	if len(lines) > 0 {
		lines = append(lines, "")
	}
	return append(lines, fg(t.Muted).Bold(true).Render(title))
}

func (m *Model) note(lines []string, text string, w int) []string {
	t := themeByName(m.themeName)
	for _, ln := range strings.Split(wrapPlain(text, w), "\n") {
		lines = append(lines, fg(t.Faint).Render(ln))
	}
	return lines
}

func (m *Model) agentsTab(w int) []string {
	t := themeByName(m.themeName)
	var lines []string
	lines = m.heading(lines, "ACTIVE")
	// The active agent carries › as well as the accent: colour alone
	// disappears under NO_COLOR.
	lines = append(lines, fg(t.Accent).Bold(true).Render("›"+m.roleLabel())+fg(t.Muted).Render("  "+truncate(m.modelLabel(), maxi(w-len(m.roleLabel())-3, 4))))
	if m.effort != "" {
		lines = append(lines, fg(t.Muted).Render("effort "+m.effort))
	}

	lines = m.heading(lines, "SUB-AGENTS")
	var subs []agent.ActiveAgent
	if m.ag != nil && m.sessionID != "" {
		subs = m.ag.ActiveAgentsFor(m.sessionID)
	}
	if len(subs) == 0 {
		lines = m.note(lines, "Nothing delegated yet.", w)
	}
	for _, s := range subs {
		lines = append(lines, fg(t.Yellow).Render("◆ ")+fg(t.Text).Bold(true).Render(s.Role)+
			fg(t.Muted).Render("  "+since(s.StartedAt)))
		lines = append(lines, fg(t.Muted).Render("  "+truncate(firstLine(s.Task), w-2)))
	}

	var bg []string
	if m.ag != nil {
		for _, task := range m.ag.BackgroundTasks() {
			if task.Status != "running" {
				continue
			}
			bg = append(bg, fg(t.Yellow).Render("● ")+fg(t.Text).Render(task.Role)+
				fg(t.Muted).Render("  "+since(task.StartedAt)), fg(t.Muted).Render("  "+truncate(firstLine(task.Task), w-2)))
		}
	}
	if len(bg) > 0 {
		lines = m.heading(lines, "BACKGROUND")
		lines = append(lines, bg...)
	}
	return lines
}

func (m *Model) toolsTab(w int) []string {
	t := themeByName(m.themeName)
	calls, failed := m.toolCalls()
	var lines []string
	head := fmt.Sprintf("THIS SESSION · %d", len(calls))
	lines = m.heading(lines, head)
	if failed > 0 {
		lines[len(lines)-1] += fg(t.Red).Render(fmt.Sprintf("  %d failed", failed))
	}
	if len(calls) == 0 {
		return m.note(lines, "No tool calls yet.", w)
	}
	for _, b := range calls {
		mark := fg(t.Green).Render("✓")
		switch {
		case b.isError:
			mark = fg(t.Red).Render("✗")
		case !b.done:
			mark = fg(t.Yellow).Render("●")
		}
		name, summary := b.title, ""
		if i := strings.Index(b.title, "  "); i >= 0 {
			name, summary = b.title[:i], strings.TrimSpace(b.title[i:])
		}
		row := mark + " " + fg(t.Text).Render(name)
		if summary != "" {
			row += "  " + fg(t.Muted).Render(summary)
		}
		lines = append(lines, row)
	}
	return lines
}

func (m *Model) skillsTab(w int) []string {
	t := themeByName(m.themeName)
	var lines []string
	st := m.chrome.stats
	if len(st.skills) > 0 {
		lines = m.heading(lines, fmt.Sprintf("READ THIS SESSION · %d", len(st.skills)))
		for _, s := range st.skills {
			lines = append(lines, fg(t.Accent).Render("◆ ")+fg(t.Text).Render(s))
		}
	}
	lines = m.heading(lines, "EVERYDAY")
	if n := m.everydaySkills(); n > 0 {
		lines = append(lines, fg(t.Text).Bold(true).Render(fmt.Sprint(n))+fg(t.Muted).Render("  skills ready"))
	} else {
		lines = m.note(lines, "No everyday skills installed.", w)
	}
	return m.note(append(lines, ""), "The agent reads one when a task calls for it. /skills lists them.", w)
}

// everydaySkills counts the everyday catalogue. The library can hold
// thousands, so the count is kept for a few seconds rather than taken on
// every frame.
func (m *Model) everydaySkills() int {
	if m.ag == nil || m.ag.Skills() == nil {
		return 0
	}
	if time.Since(m.chrome.skillCountAt) > 5*time.Second {
		m.chrome.skillCount = len(m.ag.Skills().Everyday())
		m.chrome.skillCountAt = time.Now()
	}
	return m.chrome.skillCount
}

func (m *Model) logTab(w int) []string {
	t := themeByName(m.themeName)
	var lines []string
	lines = m.heading(lines, "THIS SESSION")
	entries := m.chrome.stats.log
	if len(entries) == 0 {
		return m.note(lines, "Nothing logged this session.", w)
	}
	for i := len(entries) - 1; i >= 0; i-- {
		e := entries[i]
		mark, col := fg(t.Yellow).Render("!"), t.Text
		switch e.kind {
		case "error":
			mark, col = fg(t.Red).Render("✗"), t.Red
		case "retry":
			mark, col = fg(t.Muted).Render("↻"), t.Muted
		}
		lines = append(lines, mark+" "+fg(t.Faint).Render(e.at.Format("15:04"))+" "+fg(col).Render(truncate(firstLine(e.text), maxi(w-8, 4))))
	}
	return lines
}

// ---- text helpers -------------------------------------------------------------------

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

func since(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	d := time.Since(t).Round(time.Second)
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
}

// wrapPlain wraps unstyled text at w cells.
func wrapPlain(s string, w int) string {
	if w < 4 {
		w = 4
	}
	return lipgloss.NewStyle().Width(w).Render(s)
}
