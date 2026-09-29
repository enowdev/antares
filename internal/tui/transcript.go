package tui

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// The transcript: every block laid out on two columns. The marker column
// (`▌` for the person, `✓ ✗ ›` for a tool call, `✻` for a thought, `✗ ↻ ·`
// for errors and notices) is column 0; text starts two columns in, on the
// text column. Tool calls and thoughts are one packed list of rows; a blank
// row separates that list from the conversation around it. The width is the
// viewport's, which the layout sizes to the chat box's padded interior, so
// nothing here draws an outer border or padding.

const (
	gutter      = 2  // the marker and its space; text starts after it
	verbColumn  = 10 // a tool row's verb column, so the targets line up
	bodyPreview = 12 // an opened body folds after this many rows
)

// now is the clock thoughts and tool calls are timed with; tests replace it.
var now = time.Now

// ---- styled runs -------------------------------------------------------------

type tone uint8

const (
	tText tone = iota
	tMuted
	tFaint
	tAccent
	tGreen
	tRed
	tYellow
	tEdge // frames, table borders, rules
)

type sty struct {
	tone                        tone
	bold, italic, strike, under bool
}

// seg is a run of text in one style; an mdLine is one row of them.
type seg struct {
	s  string
	st sty
}

type mdLine []seg

// tstyles turns runs into ANSI for one theme, memoising the lipgloss styles.
type tstyles struct {
	theme Theme
	mu    sync.Mutex
	cache map[sty]lipgloss.Style
}

var (
	tsMu     sync.Mutex
	tsByName = map[string]*tstyles{}
)

func transcriptStyles(name string) *tstyles {
	tsMu.Lock()
	defer tsMu.Unlock()
	if ts, ok := tsByName[name]; ok {
		return ts
	}
	ts := &tstyles{theme: themeByName(name), cache: map[sty]lipgloss.Style{}}
	tsByName[name] = ts
	return ts
}

func (t *tstyles) color(tn tone) lipgloss.AdaptiveColor {
	switch tn {
	case tMuted:
		return t.theme.Muted
	case tFaint, tEdge:
		return t.theme.Faint
	case tAccent:
		return t.theme.Accent
	case tGreen:
		return t.theme.Green
	case tRed:
		return t.theme.Red
	case tYellow:
		return t.theme.Yellow
	}
	return t.theme.Text
}

func (t *tstyles) style(st sty) lipgloss.Style {
	t.mu.Lock()
	defer t.mu.Unlock()
	if s, ok := t.cache[st]; ok {
		return s
	}
	s := lipgloss.NewStyle().Foreground(t.color(st.tone)).
		Bold(st.bold).Italic(st.italic).Strikethrough(st.strike).Underline(st.under)
	t.cache[st] = s
	return s
}

// paint renders a row to a string, merging neighbouring runs of one style.
func (t *tstyles) paint(line mdLine) string {
	var b strings.Builder
	for i := 0; i < len(line); {
		st := line[i].st
		var run strings.Builder
		for ; i < len(line) && line[i].st == st; i++ {
			run.WriteString(line[i].s)
		}
		s := run.String()
		if strings.Trim(s, " ") == "" && !st.under && !st.strike {
			b.WriteString(s)
			continue
		}
		b.WriteString(t.style(st).Render(s))
	}
	return b.String()
}

func strWidth(s string) int { return ansi.StringWidth(s) }

func lineWidth(l mdLine) int {
	n := 0
	for _, s := range l {
		n += strWidth(s.s)
	}
	return n
}

// clip cuts a row to w columns, ending it with `…` when anything was cut.
func clip(line mdLine, w int) mdLine {
	if w < 1 {
		return nil
	}
	if lineWidth(line) <= w {
		return line
	}
	budget := w - 1
	var out mdLine
	used := 0
	for _, s := range line {
		sw := strWidth(s.s)
		if used+sw <= budget {
			out = append(out, s)
			used += sw
			continue
		}
		var b strings.Builder
		for _, r := range s.s {
			rw := strWidth(string(r))
			if used+rw > budget {
				break
			}
			used += rw
			b.WriteRune(r)
		}
		out = append(out, seg{b.String(), s.st})
		break
	}
	st := sty{tone: tMuted}
	if len(line) > 0 {
		st = line[len(line)-1].st
	}
	return append(out, seg{"…", st})
}

// padTo makes a row exactly w columns, cutting or padding it.
func padTo(line mdLine, w int) mdLine {
	line = clip(line, w)
	if gap := w - lineWidth(line); gap > 0 {
		line = append(line, seg{strings.Repeat(" ", gap), sty{}})
	}
	return line
}

// wrapSegs word-wraps runs to w columns. A newline in a run breaks the row;
// a word longer than a row breaks after a `/` or `-` when it can, anywhere
// otherwise. Spaces at the start of a wrapped row are dropped.
func wrapSegs(segs []seg, w int) []mdLine {
	if w < 1 {
		w = 1
	}
	type tok struct {
		seg
		space bool
		w     int
	}
	var out []mdLine
	var line mdLine
	lw := 0
	var pending []tok
	flush := func() {
		out = append(out, line)
		line, lw, pending = nil, 0, nil
	}
	put := func(t tok) {
		for _, p := range pending {
			line = append(line, p.seg)
			lw += p.w
		}
		pending = nil
		line = append(line, t.seg)
		lw += t.w
	}
	word := func(t tok) {
		pw := 0
		for _, p := range pending {
			pw += p.w
		}
		if lw+pw+t.w <= w {
			put(t)
			return
		}
		if lw > 0 {
			flush()
		}
		for t.w > w {
			head, rest := splitWord(t.s, w)
			line = append(line, seg{head, t.st})
			lw = strWidth(head)
			flush()
			t = tok{seg: seg{rest, t.st}, w: strWidth(rest)}
		}
		if t.s != "" {
			put(t)
		}
	}
	for _, s := range segs {
		parts := strings.Split(s.s, "\n")
		for pi, part := range parts {
			if pi > 0 {
				flush()
			}
			start := 0
			rs := []rune(part)
			for start < len(rs) {
				end := start
				space := rs[start] == ' '
				for end < len(rs) && (rs[end] == ' ') == space {
					end++
				}
				chunk := string(rs[start:end])
				t := tok{seg: seg{chunk, s.st}, space: space, w: strWidth(chunk)}
				if space {
					if lw > 0 || len(line) > 0 {
						pending = append(pending, t)
					}
				} else {
					word(t)
				}
				start = end
			}
		}
	}
	if len(line) > 0 || len(out) == 0 {
		flush()
	}
	return out
}

// splitWord cuts a word to at most w columns, preferring a cut after `/` or
// `-` in the second half.
func splitWord(s string, w int) (string, string) {
	rs := []rune(s)
	used, cut := 0, 0
	for i, r := range rs {
		rw := strWidth(string(r))
		if used+rw > w {
			break
		}
		used += rw
		cut = i + 1
	}
	if cut == 0 {
		cut = 1
	}
	for i := cut; i > cut/2 && i > 0; i-- {
		if rs[i-1] == '/' || rs[i-1] == '-' {
			if i < len(rs) {
				cut = i
			}
			break
		}
	}
	return string(rs[:cut]), string(rs[cut:])
}

// hardWrap cuts a row into pieces of at most w columns, ignoring words (code).
func hardWrap(line mdLine, w int) []mdLine {
	if lineWidth(line) <= w {
		return []mdLine{line}
	}
	var out []mdLine
	var cur mdLine
	used := 0
	for _, s := range line {
		var b strings.Builder
		for _, r := range s.s {
			rw := strWidth(string(r))
			if used+rw > w && used > 0 {
				if b.Len() > 0 {
					cur = append(cur, seg{b.String(), s.st})
					b.Reset()
				}
				out = append(out, cur)
				cur, used = nil, 0
			}
			b.WriteRune(r)
			used += rw
		}
		if b.Len() > 0 {
			cur = append(cur, seg{b.String(), s.st})
		}
	}
	return append(out, cur)
}

// shift puts n columns (or a marker) in front of every non-blank row.
func shift(rows []mdLine, n int) []mdLine {
	lead := seg{strings.Repeat(" ", n), sty{}}
	out := make([]mdLine, len(rows))
	for i, r := range rows {
		if len(r) == 0 {
			continue
		}
		out[i] = append(mdLine{lead}, r...)
	}
	return out
}

func plainRows(text string, w int, st sty) []mdLine {
	return wrapSegs([]seg{{sanitizeText(expandTabs(text)), st}}, w)
}

// ---- assembly --------------------------------------------------------------------

// unit is one rendered piece of the transcript. Units in the list (tool
// rows, thoughts) stack without a blank row; any other neighbour gets one.
type unit struct {
	lines []string
	list  bool
}

// renderBlocks is the transcript for the viewport's width.
func (m *Model) renderBlocks() string {
	if m.showWelcome() {
		return m.welcomeView(m.vp.Width, m.vp.Height)
	}
	w := maxi(m.vp.Width, 1)
	ts := transcriptStyles(m.themeName)
	roles := groupRoles(m.blocks)
	var units []unit
	for i := 0; i < len(m.blocks); i++ {
		b := &m.blocks[i]
		if b.kind == blockReasoning && !m.showReasoning {
			continue
		}
		// The same error or notice arriving again is counted, not stacked.
		repeat := 1
		if b.kind == blockError || b.kind == blockNotice {
			for i+repeat < len(m.blocks) && m.blocks[i+repeat].kind == b.kind && m.blocks[i+repeat].text == b.text {
				repeat++
			}
		}
		role := roles[i]
		switch role.kind {
		case roleMember:
			if !m.blocks[role.head].groupOpen {
				continue
			}
			if lines := m.renderBlockCached(i, w-gutter, 1); len(lines) > 0 {
				units = append(units, unit{lines: indentLines(lines, gutter), list: true})
			}
		case roleHead:
			open := b.groupOpen
			chev := "▸"
			if open {
				chev = "▾"
			}
			row := toolRow(seg{"✓", sty{tone: tGreen}}, fmt.Sprintf("%d calls", role.calls), role.summary, nil, chev, w)
			units = append(units, unit{lines: []string{ts.paint(clip(row, w))}, list: true})
			if open {
				if lines := m.renderBlockCached(i, w-gutter, 1); len(lines) > 0 {
					units = append(units, unit{lines: indentLines(lines, gutter), list: true})
				}
			}
		default:
			if lines := m.renderBlockCached(i, w, repeat); len(lines) > 0 {
				units = append(units, unit{lines: lines, list: b.kind == blockTool || b.kind == blockReasoning})
			}
		}
		i += repeat - 1
	}
	var out []string
	for i, u := range units {
		if i > 0 && !(u.list && units[i-1].list) {
			out = append(out, "")
		}
		out = append(out, u.lines...)
	}
	return strings.Join(out, "\n")
}

func indentLines(lines []string, n int) []string {
	lead := strings.Repeat(" ", n)
	out := make([]string, len(lines))
	for i, l := range lines {
		if l != "" {
			l = lead + l
		}
		out[i] = l
	}
	return out
}

// renderBlockCached memoises a settled block's rows, keyed by what it draws
// from — its content's length and ends, its state, the width and theme — so a
// streaming turn re-renders only the block that is still changing.
func (m *Model) renderBlockCached(i, w, repeat int) []string {
	b := m.blocks[i]
	if w < 1 {
		w = 1
	}
	key := blockKey(b, w, m.themeName, repeat)
	if s, ok := m.cache[key]; ok {
		if s == "" {
			return nil
		}
		return strings.Split(s, "\n")
	}
	ts := transcriptStyles(m.themeName)
	rows := m.renderBlock(b, w, repeat)
	lines := make([]string, len(rows))
	for k, r := range rows {
		lines[k] = ts.paint(clip(r, w))
	}
	if !b.streaming {
		if m.cache == nil || len(m.cache) > 4096 {
			m.cache = map[string]string{}
		}
		m.cache[key] = strings.Join(lines, "\n")
	}
	return lines
}

// blockKey names a render. Long text is keyed by its length and ends rather
// than hashed whole: a tool result can be megabytes, and a stream only grows.
func blockKey(b block, w int, theme string, repeat int) string {
	h := fnv.New64a()
	for _, s := range []string{b.title, b.name, b.args, b.text} {
		if len(s) > 512 {
			h.Write([]byte(s[:256]))
			h.Write([]byte(s[len(s)-256:]))
		} else {
			h.Write([]byte(s))
		}
		h.Write([]byte{0, byte(len(s)), byte(len(s) >> 8), byte(len(s) >> 16), byte(len(s) >> 24)})
	}
	return fmt.Sprintf("%d|%d|%s|%d|%v%v%v%v%v|%s|%x",
		b.kind, w, theme, repeat, b.streaming, b.done, b.isError, b.markdown, b.open, thoughtLabel(b), h.Sum64())
}

// renderBlock lays one block out as rows no wider than w.
func (m *Model) renderBlock(b block, w, repeat int) []mdLine {
	ts := transcriptStyles(m.themeName)
	tw := maxi(w-gutter, 1)
	switch b.kind {
	case blockUser:
		mark := seg{"▌ ", sty{tone: tAccent}}
		var out mdLine
		rows := plainRows(strings.TrimRight(b.text, "\n "), tw, sty{tone: tText, bold: true})
		res := make([]mdLine, 0, len(rows))
		for _, r := range rows {
			out = append(mdLine{mark}, r...)
			res = append(res, out)
		}
		return res

	case blockAssistant:
		return shift(renderMarkdown(b.text, tw, ts, false), gutter)

	case blockReasoning:
		return m.renderThought(b, w, ts)

	case blockTool:
		return m.renderTool(b, w, ts)

	case blockNotice:
		return renderNotice(b.text, repeat, w)

	case blockError:
		label := "error"
		if repeat > 1 {
			label = fmt.Sprintf("error ×%d", repeat)
		}
		out := []mdLine{{{"✗ ", sty{tone: tRed}}, {label, sty{tone: tRed, bold: true}}}}
		return append(out, shift(plainRows(strings.TrimSpace(b.text), tw, sty{tone: tRed}), gutter)...)

	case blockSystem:
		if b.markdown {
			return shift(renderMarkdown(b.text, tw, ts, false), gutter)
		}
		return shift(plainRows(strings.TrimRight(b.text, "\n "), tw, sty{tone: tMuted}), gutter)
	}
	return shift(plainRows(b.text, tw, sty{tone: tText}), gutter)
}

// renderNotice: a retry is `↻ retry N` with the message under it; any other
// notice is one `·` row, `×N` when it repeated.
func renderNotice(text string, repeat, w int) []mdLine {
	text = strings.TrimSpace(text)
	tw := maxi(w-gutter, 1)
	if strings.Contains(strings.ToLower(text), "retry") {
		out := []mdLine{{{"↻ ", sty{tone: tYellow}}, {fmt.Sprintf("retry %d", repeat), sty{tone: tYellow, bold: true}}}}
		return append(out, shift(plainRows(text, tw, sty{tone: tMuted}), gutter)...)
	}
	if repeat > 1 {
		text += fmt.Sprintf("  ×%d", repeat)
	}
	rows := plainRows(text, tw, sty{tone: tMuted})
	out := make([]mdLine, len(rows))
	for i, r := range rows {
		lead := seg{"  ", sty{}}
		if i == 0 {
			lead = seg{"· ", sty{tone: tFaint}}
		}
		out[i] = append(mdLine{lead}, r...)
	}
	return out
}

// ---- thoughts -----------------------------------------------------------------

func liveThought(b block) bool {
	return b.kind == blockReasoning && b.streaming && b.ended.IsZero()
}

// thoughtLabel is what a thought's row says: its latest sentence while it
// streams, then how long it took.
func thoughtLabel(b block) string {
	if b.kind != blockReasoning {
		return ""
	}
	if liveThought(b) {
		if s := latestSentence(b.text); s != "" {
			return "Thinking… " + s
		}
		return "Thinking…"
	}
	if !b.started.IsZero() && !b.ended.IsZero() {
		return "Thought for " + fmtDur(b.ended.Sub(b.started))
	}
	return "Thought"
}

func (m *Model) renderThought(b block, w int, ts *tstyles) []mdLine {
	label := thoughtLabel(b)
	hasText := strings.TrimSpace(b.text) != ""
	icon := seg{"✻", sty{tone: tAccent}}
	if b.open && hasText {
		if liveThought(b) {
			label = "Thinking…"
		}
		top := frameTop(icon, seg{label, sty{tone: tMuted}}, "", nil, "▾", w)
		return frameBody(top, renderMarkdown(b.text, maxi(w-4, 1), ts, true), w)
	}
	chev := " "
	if hasText {
		chev = "▸"
	}
	room := maxi(w-4, 1)
	label = fitText(label, room)
	return []mdLine{{
		{"✻ ", icon.st},
		{label, sty{tone: tMuted}},
		{strings.Repeat(" ", maxi(room-strWidth(label), 0)), sty{}},
		{" " + chev, sty{tone: tFaint}},
	}}
}

// latestSentence is the last finished sentence of streaming text, so the
// live row reads as a thought rather than the fragment the stream stopped
// on; before any sentence finishes, the fragment.
func latestSentence(text string) string {
	text = strings.TrimSpace(text)
	rs := []rune(text)
	var last string
	start := 0
	for i, r := range rs {
		ends := r == '\n'
		if r == '.' || r == '!' || r == '?' {
			ends = i+1 == len(rs) || rs[i+1] == ' ' || rs[i+1] == '\n'
		}
		if ends {
			s := strings.TrimSpace(strings.TrimRight(string(rs[start:i+1]), ".\n"))
			if s != "" {
				last = s
			}
			start = i + 1
		}
	}
	if last != "" {
		return strings.Join(strings.Fields(last), " ")
	}
	return strings.Join(strings.Fields(string(rs[start:])), " ")
}

func fmtDur(d time.Duration) string {
	s := int((d + time.Second/2) / time.Second)
	if s < 1 {
		s = 1
	}
	if s < 60 {
		return fmt.Sprintf("%ds", s)
	}
	return fmt.Sprintf("%dm %ds", s/60, s%60)
}

// ---- tool rows -------------------------------------------------------------------

// toolRow is a closed row: `✓ verb       target ……… metric ▸`. The verb has
// a fixed column so targets line up; the metric is flush right before the
// chevron's own column, which is blank when there is nothing to open.
func toolRow(icon seg, verb, target string, right mdLine, chevron string, w int) mdLine {
	vc := verbColumn
	if w < 32 {
		vc = maxi(w/4, 3) // a narrow row keeps room for the target
	}
	verb = fitText(verb, vc)
	used := 2 + vc + 2
	const tail = 2 // a space and the chevron
	rw := lineWidth(right)
	if used+rw+1+tail > w {
		right, rw = nil, 0
	}
	room := w - used - tail - 1
	if rw > 0 {
		room -= rw + 1
	}
	if room < 4 {
		target = ""
	} else {
		target = fitText(target, room)
	}
	row := mdLine{
		{icon.s + " ", icon.st},
		{verb, sty{tone: tMuted}},
		{strings.Repeat(" ", vc-strWidth(verb)+2), sty{}},
		{target, sty{tone: tText}},
	}
	gap := w - used - strWidth(target) - rw - tail
	row = append(row, seg{strings.Repeat(" ", maxi(gap, 1)), sty{}})
	row = append(row, right...)
	return append(row, seg{" " + chevron, sty{tone: tFaint}})
}

// frameTop is an opened row as the top edge of the frame around what it
// opened: `╭─ ✓ edit  path ──────── +6 −2 ▾ ─╮`.
func frameTop(icon, label seg, target string, right mdLine, chevron string, w int) mdLine {
	edge := sty{tone: tEdge}
	iw := strWidth(icon.s)
	rw := lineWidth(right)
	rpart := 0
	if rw > 0 {
		rpart = rw + 1
	}
	if 10+iw+rpart+1+1 > w {
		right, rw, rpart = nil, 0, 0
	}
	label.s = fitText(label.s, maxi(w-(10+iw+rpart)-1, 1))
	lw := strWidth(label.s)
	argRoom := w - (10 + iw + lw + rpart) - 1 - 2
	if target == "" || argRoom < 4 {
		target = ""
	} else {
		target = fitText(target, argRoom)
	}
	aw := strWidth(target)
	used := 10 + iw + lw + rpart
	if aw > 0 {
		used += 2 + aw
	}
	row := mdLine{{"╭─ ", edge}, {icon.s + " ", icon.st}, label}
	if aw > 0 {
		row = append(row, seg{"  ", sty{}}, seg{target, sty{tone: tText}})
	}
	row = append(row, seg{" " + strings.Repeat("─", maxi(w-used, 1)), edge})
	if rw > 0 {
		row = append(row, seg{" ", sty{}})
		row = append(row, right...)
	}
	return append(row, seg{" " + chevron, sty{tone: tFaint}}, seg{" ─╮", edge})
}

// frameBody walls body in under top and closes the frame. The body sits on
// the text column; rows past the preview fold into `┈ N more lines`.
func frameBody(top mdLine, body []mdLine, w int) []mdLine {
	edge := sty{tone: tEdge}
	inner := maxi(w-4, 1)
	if len(body) > bodyPreview+1 {
		more := len(body) - bodyPreview
		body = append(body[:bodyPreview:bodyPreview], mdLine{{"┈ ", sty{tone: tFaint}}, {fmt.Sprintf("%d more lines", more), sty{tone: tMuted}}})
	}
	out := []mdLine{top}
	for _, r := range body {
		row := mdLine{{"│ ", edge}}
		row = append(row, padTo(r, inner)...)
		out = append(out, append(row, seg{" │", edge}))
	}
	return append(out, mdLine{{"╰" + strings.Repeat("─", maxi(w-2, 0)) + "╯", edge}})
}

func (m *Model) renderTool(b block, w int, ts *tstyles) []mdLine {
	var icon seg
	switch {
	case !b.done:
		icon = seg{"›", sty{tone: tYellow}}
	case b.isError:
		icon = seg{"✗", sty{tone: tRed}}
	default:
		icon = seg{"✓", sty{tone: tGreen}}
	}
	v := classifyTool(b)
	var right mdLine
	if v.metric != "" {
		right = mdLine{{v.metric, sty{tone: v.metricTone}}}
	}
	if b.open && len(v.body) > 0 {
		top := frameTop(icon, seg{fitText(v.verb, verbColumn), sty{tone: tMuted}}, v.target, right, "▾", w)
		return frameBody(top, v.body, w)
	}
	chev := " "
	if len(v.body) > 0 {
		chev = "▸"
	}
	return []mdLine{toolRow(icon, v.verb, v.target, right, chev, w)}
}

// toolView is what a call's row and body show.
type toolView struct {
	verb, target string
	metric       string
	metricTone   tone
	body         []mdLine
}

// toolName is the call's tool; a block rebuilt from a stored session has
// only its headline.
func toolName(b block) (name, summary string) {
	if b.name != "" {
		return b.name, ""
	}
	name = b.title
	if i := strings.Index(name, "  "); i >= 0 {
		return name[:i], strings.TrimSpace(name[i:])
	}
	return name, ""
}

// toolVerb shortens the common tools to the verb a reader scans for.
func toolVerb(name string) string {
	switch name {
	case "read_file":
		return "read"
	case "write_file":
		return "write"
	case "edit_file":
		return "edit"
	case "list_files":
		return "list"
	case "terminal":
		return "shell"
	case "web_fetch":
		return "fetch"
	case "web_search":
		return "search"
	case "":
		return "tool"
	}
	return name
}

func toolArgs(raw string) map[string]any {
	var a map[string]any
	if strings.TrimSpace(raw) != "" {
		_ = json.Unmarshal([]byte(raw), &a)
	}
	return a
}

func argString(a map[string]any, key string) (string, bool) {
	v, ok := a[key]
	if !ok || v == nil {
		return "", false
	}
	if s, ok := v.(string); ok {
		return s, true
	}
	return fmt.Sprint(v), true
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

func counted(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return strconv.Itoa(n) + " " + many
}

// splitExit lifts the terminal tool's `Exit code N` preamble off its output.
func splitExit(text string) (int, string) {
	const pre = "Exit code "
	if !strings.HasPrefix(text, pre) {
		return 0, text
	}
	rest := text[len(pre):]
	end := strings.IndexAny(rest, "\n")
	if end < 0 {
		end = len(rest)
	}
	code, err := strconv.Atoi(strings.TrimSpace(rest[:end]))
	if err != nil {
		return 0, text
	}
	return code, strings.TrimLeft(rest[end:], "\n")
}

func classifyTool(b block) toolView {
	name, summary := toolName(b)
	args := toolArgs(b.args)
	v := toolView{verb: toolVerb(name), metricTone: tMuted}

	switch name {
	case "terminal":
		cmd, _ := argString(args, "command")
		v.target = oneLine(cmd)
	case "grep", "glob":
		v.target, _ = argString(args, "pattern")
		if p, _ := argString(args, "path"); p != "" && p != "." {
			v.target += "  " + p
		}
	case "skill":
		action, _ := argString(args, "action")
		n, _ := argString(args, "name")
		v.target = strings.TrimSpace(action + " " + n)
	default:
		v.target = summarizeArgs(b.args)
	}
	if v.target == "" {
		v.target = summary
	}
	v.target = oneLine(v.target)

	text := b.text
	var metric string
	switch {
	case name == "edit_file" && !b.isError:
		oldS, ok1 := argString(args, "old_string")
		newS, ok2 := argString(args, "new_string")
		if ok1 && ok2 {
			rows, add, del := diffRows(oldS, newS)
			v.body = rows
			metric = fmt.Sprintf("+%d -%d", add, del)
		}
	case name == "write_file" && !b.isError:
		if content, ok := argString(args, "content"); ok {
			n := len(strings.Split(strings.TrimRight(content, "\n"), "\n"))
			if content == "" {
				n = 0
			}
			metric = counted(n, "line", "lines")
			if strings.HasPrefix(text, "Created") {
				metric = "new · " + metric
			}
			v.body = bodyRows(content)
		}
	case name == "terminal":
		code, out := splitExit(text)
		text = out
		if code != 0 {
			v.body = bodyRows(out)
			metric = counted(len(v.body), "line", "lines") + fmt.Sprintf(" · exit %d", code)
		}
	}
	if v.body == nil {
		v.body = bodyRows(text)
		if metric == "" && len(v.body) > 0 {
			metric = counted(len(v.body), "line", "lines")
		}
	}
	if b.isError {
		v.metricTone = tRed
		if metric == "" {
			metric = "failed"
		}
	}
	if b.done && !b.started.IsZero() && !b.ended.IsZero() {
		if d := b.ended.Sub(b.started); d >= 2*time.Second {
			if metric != "" {
				metric += " · "
			}
			metric += fmtDur(d)
		}
	}
	v.metric = metric
	return v
}

// bodyRows is tool output as faint rows: escape sequences and control bytes
// dropped, tabs expanded, trailing blank rows trimmed. Rows are cut at the
// frame's wall, not wrapped.
func bodyRows(text string) []mdLine {
	text = strings.TrimRight(expandTabs(sanitizeText(ansi.Strip(text))), "\n\r\t ")
	text = strings.TrimLeft(text, "\n")
	if strings.TrimSpace(text) == "" {
		return nil
	}
	lines := strings.Split(text, "\n")
	out := make([]mdLine, len(lines))
	for i, l := range lines {
		out[i] = mdLine{{strings.TrimRight(l, "\r "), sty{tone: tFaint}}}
	}
	return out
}

// diffRows is an edit's change, line by line: removed rows red with `-`,
// added green with `+`, unchanged context faint.
func diffRows(oldS, newS string) ([]mdLine, int, int) {
	a := strings.Split(expandTabs(sanitizeText(oldS)), "\n")
	bb := strings.Split(expandTabs(sanitizeText(newS)), "\n")
	var out []mdLine
	add, del := 0, 0
	row := func(mark string, s string, t tone) {
		out = append(out, mdLine{{mark, sty{tone: t, bold: mark != "  "}}, {s, sty{tone: t}}})
	}
	if len(a)*len(bb) > 250000 {
		for _, l := range a {
			row("- ", l, tRed)
		}
		for _, l := range bb {
			row("+ ", l, tGreen)
		}
		return out, len(bb), len(a)
	}
	// Longest common subsequence, then walk it.
	n, mm := len(a), len(bb)
	lcs := make([][]int, n+1)
	for i := range lcs {
		lcs[i] = make([]int, mm+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := mm - 1; j >= 0; j-- {
			if a[i] == bb[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else {
				lcs[i][j] = maxi(lcs[i+1][j], lcs[i][j+1])
			}
		}
	}
	i, j := 0, 0
	for i < n || j < mm {
		switch {
		case i < n && j < mm && a[i] == bb[j]:
			row("  ", a[i], tFaint)
			i++
			j++
		case j < mm && (i == n || lcs[i][j+1] > lcs[i+1][j]):
			row("+ ", bb[j], tGreen)
			add++
			j++
		default:
			row("- ", a[i], tRed)
			del++
			i++
		}
	}
	return out, add, del
}

// ---- runs of read-only calls ------------------------------------------------------

const (
	roleAlone = iota
	roleHead
	roleMember
)

type groupRole struct {
	kind    int
	head    int // the run's first block, for a member
	calls   int
	summary string
}

// readOnlyCall reports whether a finished call only read or looked, so it
// can fold into a run.
func readOnlyCall(b block) bool {
	name, _ := toolName(b)
	switch name {
	case "read_file", "list_files", "glob", "grep", "web_fetch", "web_search",
		"rag_search", "session_search", "read_document", "view_image", "project_info", "list_roles":
		return true
	case "skill":
		action, _ := argString(toolArgs(b.args), "action")
		switch action {
		case "", "list", "search", "find", "read", "get", "chains":
			return true
		}
	case "terminal":
		code, _ := splitExit(b.text)
		return code == 0
	}
	return false
}

// groupRoles marks the runs of two or more finished read-only calls, with
// the finished thoughts between them, that draw as one row until opened. A
// failed call, a running one, an edit or a write, or anything said ends a
// run.
func groupRoles(blocks []block) []groupRole {
	roles := make([]groupRole, len(blocks))
	quiet := func(i int) bool {
		b := blocks[i]
		return b.kind == blockTool && b.done && !b.isError && !b.streaming && readOnlyCall(b)
	}
	thought := func(i int) bool {
		return blocks[i].kind == blockReasoning && !liveThought(blocks[i])
	}
	for at := 0; at < len(blocks); {
		if !quiet(at) {
			at++
			continue
		}
		start, end := at, at
		for next := at + 1; next < len(blocks) && (quiet(next) || thought(next)); next++ {
			if quiet(next) {
				end = next
			}
		}
		var verbs []string
		count := map[string]int{}
		calls := 0
		for i := start; i <= end; i++ {
			if !quiet(i) {
				continue
			}
			calls++
			name, _ := toolName(blocks[i])
			v := toolVerb(name)
			if count[v] == 0 {
				verbs = append(verbs, v)
			}
			count[v]++
		}
		if calls >= 2 {
			parts := make([]string, len(verbs))
			for k, v := range verbs {
				parts[k] = v
				if count[v] > 1 {
					parts[k] = fmt.Sprintf("%s ×%d", v, count[v])
				}
			}
			roles[start] = groupRole{kind: roleHead, head: start, calls: calls, summary: strings.Join(parts, " · ")}
			for i := start + 1; i <= end; i++ {
				roles[i] = groupRole{kind: roleMember, head: start}
			}
		}
		at = end + 1
	}
	return roles
}

// toggleOpen is Ctrl+O: open every tool body, run and thought when any is
// closed, otherwise close them all.
func (m *Model) toggleOpen() {
	open := false
	roles := groupRoles(m.blocks)
	for i, b := range m.blocks {
		hasBody := strings.TrimSpace(b.text) != "" || (b.kind == blockTool && b.args != "")
		switch {
		case roles[i].kind == roleHead && !b.groupOpen:
			open = true
		case (b.kind == blockTool || b.kind == blockReasoning) && hasBody && !b.open:
			open = true
		}
	}
	for i := range m.blocks {
		if m.blocks[i].kind == blockTool || m.blocks[i].kind == blockReasoning {
			m.blocks[i].open = open
			m.blocks[i].groupOpen = open
		}
	}
	if open {
		m.setStatus("tool output open")
	} else {
		m.setStatus("tool output closed")
	}
}

// fitText cuts s to w display columns, ending it with `…` when cut.
func fitText(s string, w int) string {
	if strWidth(s) <= w {
		return s
	}
	if w < 1 {
		return ""
	}
	return ansi.Truncate(s, w, "…")
}
