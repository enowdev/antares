package tui

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
	gast "github.com/yuin/goldmark/ast"
	east "github.com/yuin/goldmark/extension/ast"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/text"
)

// The transcript's Markdown renderer. Glamour could not draw the look the
// transcript wants — a code block's language on the same bar as its lines,
// no caps; rounded tables whose cells wrap; blocks with exactly one blank
// row between them — so this walks goldmark's tree (the parser Glamour
// already brings in) and lays the rows out itself. Every row it returns fits
// the width it was given.

var mdParser = goldmark.New(goldmark.WithExtensions(
	extension.Table,
	extension.Strikethrough,
	extension.TaskList,
))

// renderMarkdown lays text out as styled rows no wider than w. dim draws
// every run in the muted tone (quotes, an opened thought).
func renderMarkdown(src string, w int, ts *tstyles, dim bool) []mdLine {
	if w < 1 {
		w = 1
	}
	src = sanitizeText(src)
	if strings.TrimSpace(src) == "" {
		return nil
	}
	source := []byte(src)
	doc := mdParser.Parser().Parse(text.NewReader(source))
	r := &mdr{src: source, ts: ts, dim: dim}
	return trimBlank(r.children(doc, w, true))
}

type mdr struct {
	src   []byte
	ts    *tstyles
	dim   bool
	depth int // list nesting, for the bullet shape
}

// children renders n's block children, one blank row between them when
// spaced (and never a leading or trailing one).
func (r *mdr) children(n gast.Node, w int, spaced bool) []mdLine {
	var out []mdLine
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		rows := trimBlank(r.block(c, w))
		if len(rows) == 0 {
			continue
		}
		if len(out) > 0 && spaced {
			out = append(out, nil)
		}
		out = append(out, rows...)
	}
	return out
}

func (r *mdr) tone(t tone) tone {
	if r.dim && t != tEdge && t != tFaint {
		return tMuted
	}
	return t
}

func (r *mdr) block(n gast.Node, w int) []mdLine {
	switch n := n.(type) {
	case *gast.Paragraph, *gast.TextBlock:
		var segs []seg
		r.inlines(n, sty{tone: r.tone(tText)}, &segs)
		return wrapSegs(segs, w)

	case *gast.Heading:
		base := sty{tone: r.tone(tAccent), bold: true}
		switch n.Level {
		case 1:
			base.under = true
		case 2:
		default:
			base.tone = r.tone(tText)
		}
		var segs []seg
		r.inlines(n, base, &segs)
		return wrapSegs(segs, w)

	case *gast.ThematicBreak:
		return []mdLine{{{strings.Repeat("─", w), sty{tone: tEdge}}}}

	case *gast.FencedCodeBlock:
		return r.code(string(n.Language(r.src)), n.Lines(), w)

	case *gast.CodeBlock:
		return r.code("", n.Lines(), w)

	case *gast.Blockquote:
		inner := &mdr{src: r.src, ts: r.ts, dim: true, depth: r.depth}
		rows := inner.children(n, w-2, true)
		bar := seg{"│ ", sty{tone: tEdge}}
		out := make([]mdLine, 0, len(rows))
		for _, row := range rows {
			out = append(out, append(mdLine{bar}, row...))
		}
		return out

	case *gast.List:
		return r.list(n, w)

	case *east.Table:
		return r.table(n, w)

	case *gast.HTMLBlock:
		var b strings.Builder
		lines := n.Lines()
		for i := 0; i < lines.Len(); i++ {
			seg := lines.At(i)
			b.Write(seg.Value(r.src))
		}
		if n.HasClosure() {
			seg := n.ClosureLine
			b.Write(seg.Value(r.src))
		}
		plain := strings.TrimSpace(stripHTML(b.String()))
		if plain == "" {
			return nil
		}
		return wrapSegs([]seg{{plain, sty{tone: r.tone(tText)}}}, w)
	}
	// Anything else: its blocks if it has them, its text otherwise.
	if n.FirstChild() != nil && n.FirstChild().Type() == gast.TypeBlock {
		return r.children(n, w, true)
	}
	var segs []seg
	r.inlines(n, sty{tone: r.tone(tText)}, &segs)
	return wrapSegs(segs, w)
}

var (
	htmlComment = regexp.MustCompile(`(?s)<!--.*?-->`)
	htmlBreak   = regexp.MustCompile(`(?i)<br\s*/?>`)
	htmlTag     = regexp.MustCompile(`</?[A-Za-z][A-Za-z0-9-]*(\s[^<>]*)?/?>`)
)

func stripHTML(s string) string {
	s = htmlComment.ReplaceAllString(s, "")
	s = htmlBreak.ReplaceAllString(s, "\n")
	return htmlTag.ReplaceAllString(s, "")
}

// inlines flattens n's inline children into styled runs. Soft breaks stay
// breaks, as chat interfaces keep them.
func (r *mdr) inlines(n gast.Node, st sty, out *[]seg) {
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		switch c := c.(type) {
		case *gast.Text:
			*out = append(*out, seg{string(c.Segment.Value(r.src)), st})
			if c.SoftLineBreak() || c.HardLineBreak() {
				*out = append(*out, seg{"\n", st})
			}
		case *gast.String:
			*out = append(*out, seg{string(c.Value), st})
		case *gast.CodeSpan:
			var b strings.Builder
			for t := c.FirstChild(); t != nil; t = t.NextSibling() {
				switch t := t.(type) {
				case *gast.Text:
					b.Write(t.Segment.Value(r.src))
				case *gast.String:
					b.Write(t.Value)
				}
			}
			code := st
			code.tone = r.tone(tAccent)
			*out = append(*out, seg{b.String(), code})
		case *gast.Emphasis:
			em := st
			if c.Level >= 2 {
				em.bold = true
			} else {
				em.italic = true
			}
			r.inlines(c, em, out)
		case *east.Strikethrough:
			s := st
			s.strike = true
			s.tone = r.tone(tMuted)
			r.inlines(c, s, out)
		case *gast.Link:
			l := st
			l.tone = r.tone(tAccent)
			r.inlines(c, l, out)
		case *gast.AutoLink:
			l := st
			l.tone = r.tone(tAccent)
			*out = append(*out, seg{string(c.Label(r.src)), l})
		case *gast.Image:
			alt := st
			alt.tone = r.tone(tMuted)
			alt.italic = true
			r.inlines(c, alt, out)
		case *gast.RawHTML:
			var b strings.Builder
			for i := 0; i < c.Segments.Len(); i++ {
				s := c.Segments.At(i)
				b.Write(s.Value(r.src))
			}
			if htmlBreak.MatchString(b.String()) {
				*out = append(*out, seg{"\n", st})
			} else if !htmlTag.MatchString(b.String()) && !htmlComment.MatchString(b.String()) {
				*out = append(*out, seg{b.String(), st}) // `Vec<String>` stays text
			}
		case *east.TaskCheckBox:
			// Drawn by the list as the item's marker.
		default:
			r.inlines(c, st, out)
		}
	}
}

// ---- lists ------------------------------------------------------------------

func (r *mdr) list(n *gast.List, w int) []mdLine {
	var items []gast.Node
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		items = append(items, c)
	}
	numW := 0
	if n.IsOrdered() {
		numW = len(strconv.Itoa(n.Start + len(items) - 1))
	}
	r.depth++
	defer func() { r.depth-- }()
	var out []mdLine
	for i, item := range items {
		var marker seg
		switch {
		case taskState(item) != 0:
			if taskState(item) > 0 {
				marker = seg{"☑ ", sty{tone: r.tone(tGreen)}}
			} else {
				marker = seg{"☐ ", sty{tone: r.tone(tMuted)}}
			}
		case n.IsOrdered():
			num := strconv.Itoa(n.Start + i)
			marker = seg{strings.Repeat(" ", numW-len(num)) + num + ". ", sty{tone: r.tone(tMuted)}}
		default:
			bullet := "• "
			if r.depth%2 == 0 {
				bullet = "◦ "
			}
			marker = seg{bullet, sty{tone: r.tone(tMuted)}}
		}
		mw := strWidth(marker.s)
		body := r.children(item, maxi(w-mw, 1), !n.IsTight)
		if len(body) == 0 {
			body = []mdLine{nil}
		}
		if i > 0 && !n.IsTight {
			out = append(out, nil)
		}
		indent := seg{strings.Repeat(" ", mw), sty{}}
		for j, row := range body {
			if j == 0 {
				out = append(out, append(mdLine{marker}, row...))
			} else if len(row) == 0 {
				out = append(out, nil)
			} else {
				out = append(out, append(mdLine{indent}, row...))
			}
		}
	}
	return out
}

// taskState is 1 for a checked task item, -1 for an open one, 0 otherwise.
func taskState(item gast.Node) int {
	first := item.FirstChild()
	if first == nil {
		return 0
	}
	if box, ok := first.FirstChild().(*east.TaskCheckBox); ok {
		if box.IsChecked {
			return 1
		}
		return -1
	}
	return 0
}

// ---- code -------------------------------------------------------------------

// code draws a block on a `│` bar: the language on the first row, then the
// lines, highlighted when the language is known; a line too long for the row
// continues on `│↳`. No caps above or below.
func (r *mdr) code(lang string, lines *text.Segments, w int) []mdLine {
	var b strings.Builder
	for i := 0; i < lines.Len(); i++ {
		s := lines.At(i)
		b.Write(s.Value(r.src))
	}
	body := strings.TrimRight(expandTabs(b.String()), "\n ")
	bar := seg{"│ ", sty{tone: r.tone(tMuted)}}
	cont := seg{"│↳", sty{tone: r.tone(tMuted)}}
	var out []mdLine
	lang = strings.TrimSpace(lang)
	if lang != "" {
		out = append(out, mdLine{bar, {fitText(lang, maxi(w-2, 1)), sty{tone: r.tone(tMuted), italic: true}}})
	}
	room := maxi(w-2, 1)
	for _, line := range r.highlight(lang, body) {
		for k, piece := range hardWrap(line, room) {
			lead := bar
			if k > 0 {
				lead = cont
			}
			out = append(out, append(mdLine{lead}, piece...))
		}
	}
	return out
}

// highlight splits code into rows of styled runs, coloured by chroma's lexer
// for lang when there is one.
func (r *mdr) highlight(lang, code string) []mdLine {
	plain := sty{tone: r.tone(tText)}
	var lexer chroma.Lexer
	if lang != "" && !r.dim {
		lexer = lexers.Get(lang)
	}
	var rows []mdLine
	if lexer == nil || len(code) > 64<<10 {
		for _, l := range strings.Split(code, "\n") {
			rows = append(rows, mdLine{{l, plain}})
		}
		return rows
	}
	it, err := chroma.Coalesce(lexer).Tokenise(nil, code)
	if err != nil {
		for _, l := range strings.Split(code, "\n") {
			rows = append(rows, mdLine{{l, plain}})
		}
		return rows
	}
	cur := mdLine{}
	for tok := it(); tok != chroma.EOF; tok = it() {
		st := codeStyle(tok.Type)
		parts := strings.Split(tok.Value, "\n")
		for i, p := range parts {
			if i > 0 {
				rows = append(rows, cur)
				cur = mdLine{}
			}
			if p != "" {
				cur = append(cur, seg{p, st})
			}
		}
	}
	rows = append(rows, cur)
	for len(rows) > 0 && lineWidth(rows[len(rows)-1]) == 0 {
		rows = rows[:len(rows)-1]
	}
	return rows
}

func codeStyle(t chroma.TokenType) sty {
	switch {
	case t.InCategory(chroma.Comment):
		return sty{tone: tMuted, italic: true}
	case t.InCategory(chroma.Keyword):
		return sty{tone: tAccent}
	case t.InCategory(chroma.LiteralString):
		return sty{tone: tGreen}
	case t.InCategory(chroma.LiteralNumber):
		return sty{tone: tYellow}
	case t == chroma.NameFunction || t == chroma.NameClass || t == chroma.NameBuiltin:
		return sty{tone: tText, bold: true}
	}
	return sty{tone: tText}
}

// ---- tables -----------------------------------------------------------------

type mdCell struct {
	segs  []seg
	align east.Alignment
}

// table draws rounded borders with the alignment honoured and cells wrapped;
// once any row wraps, a rule separates every row. Too narrow to keep
// ordinary words whole, it stacks as `header: value` rows instead.
func (r *mdr) table(n *east.Table, w int) []mdLine {
	var rows [][]mdCell
	header := 0
	for row := n.FirstChild(); row != nil; row = row.NextSibling() {
		var cells []mdCell
		for c := row.FirstChild(); c != nil; c = c.NextSibling() {
			cell, ok := c.(*east.TableCell)
			if !ok {
				continue
			}
			st := sty{tone: r.tone(tText)}
			if _, isHead := row.(*east.TableHeader); isHead {
				st.bold = true
			}
			var segs []seg
			r.inlines(cell, st, &segs)
			cells = append(cells, mdCell{segs: segs, align: cell.Alignment})
		}
		if _, isHead := row.(*east.TableHeader); isHead {
			header = 1
		}
		rows = append(rows, cells)
	}
	cols := 0
	for _, row := range rows {
		cols = maxi(cols, len(row))
	}
	if cols == 0 {
		return nil
	}
	natural := make([]int, cols)
	minimum := make([]int, cols)
	for _, row := range rows {
		for i, cell := range row {
			for _, l := range wrapSegs(cell.segs, 1<<20) {
				natural[i] = maxi(natural[i], lineWidth(l))
			}
			for _, word := range strings.Fields(segText(cell.segs)) {
				minimum[i] = maxi(minimum[i], strWidth(word))
			}
		}
	}
	avail := w - (cols + 1) - 2*cols
	widths := append([]int(nil), natural...)
	for i := range widths {
		widths[i] = maxi(widths[i], 1)
		minimum[i] = mini(maxi(minimum[i], 1), widths[i])
	}
	total := func() int {
		s := 0
		for _, x := range widths {
			s += x
		}
		return s
	}
	for total() > avail {
		// Take a column off the widest column that can still give one.
		at := -1
		for i := range widths {
			if widths[i] > minimum[i] && (at < 0 || widths[i] > widths[at]) {
				at = i
			}
		}
		if at < 0 {
			return r.stacked(rows, header, w)
		}
		widths[at]--
	}

	edge := sty{tone: tEdge}
	rule := func(l, mid, rr string) mdLine {
		var b strings.Builder
		b.WriteString(l)
		for i, cw := range widths {
			if i > 0 {
				b.WriteString(mid)
			}
			b.WriteString(strings.Repeat("─", cw+2))
		}
		b.WriteString(rr)
		return mdLine{{b.String(), edge}}
	}
	var drawn [][]mdLine
	wraps := false
	for _, row := range rows {
		cellRows := make([][]mdLine, cols)
		height := 1
		for i := 0; i < cols; i++ {
			var c mdCell
			if i < len(row) {
				c = row[i]
			}
			cellRows[i] = wrapSegs(c.segs, widths[i])
			height = maxi(height, len(cellRows[i]))
		}
		if height > 1 {
			wraps = true
		}
		var lines []mdLine
		for k := 0; k < height; k++ {
			line := mdLine{{"│", edge}}
			for i := 0; i < cols; i++ {
				var content mdLine
				if k < len(cellRows[i]) {
					content = cellRows[i][k]
				}
				align := east.AlignNone
				if i < len(row) {
					align = row[i].align
				}
				gap := widths[i] - lineWidth(content)
				left := 0
				switch align {
				case east.AlignRight:
					left = gap
				case east.AlignCenter:
					left = gap / 2
				}
				line = append(line, seg{" " + strings.Repeat(" ", left), sty{}})
				line = append(line, content...)
				line = append(line, seg{strings.Repeat(" ", gap-left) + " ", sty{}}, seg{"│", edge})
			}
			lines = append(lines, line)
		}
		drawn = append(drawn, lines)
	}
	out := []mdLine{rule("╭", "┬", "╮")}
	for i, lines := range drawn {
		if i > 0 && (i == header || wraps) {
			out = append(out, rule("├", "┼", "┤"))
		}
		out = append(out, lines...)
	}
	return append(out, rule("╰", "┴", "╯"))
}

// stacked is a table too narrow for its words: each row becomes
// `header: value` lines, a blank row between records.
func (r *mdr) stacked(rows [][]mdCell, header, w int) []mdLine {
	var heads []string
	if header == 1 && len(rows) > 0 {
		for _, c := range rows[0] {
			heads = append(heads, strings.TrimSpace(segText(c.segs)))
		}
	}
	var out []mdLine
	for _, row := range rows[header:] {
		if len(out) > 0 {
			out = append(out, nil)
		}
		for i, c := range row {
			segs := []seg{}
			if i < len(heads) && heads[i] != "" {
				segs = append(segs, seg{heads[i] + ": ", sty{tone: r.tone(tMuted), bold: true}})
			}
			segs = append(segs, c.segs...)
			out = append(out, wrapSegs(segs, w)...)
		}
	}
	return out
}

func segText(segs []seg) string {
	var b strings.Builder
	for _, s := range segs {
		b.WriteString(s.s)
	}
	return b.String()
}

// ---- text utilities ------------------------------------------------------------

// sanitizeText drops control bytes (escape sequences included) so model text
// cannot drive the terminal, keeping newlines and tabs.
func sanitizeText(s string) string {
	clean := true
	for _, r := range s {
		if (r < 0x20 && r != '\n' && r != '\t') || r == 0x7f || (r >= 0x80 && r < 0xa0) {
			clean = false
			break
		}
	}
	if clean {
		return s
	}
	var b strings.Builder
	for _, r := range s {
		if (r < 0x20 && r != '\n' && r != '\t') || r == 0x7f || (r >= 0x80 && r < 0xa0) {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// expandTabs replaces tabs with spaces to the next four-column stop.
func expandTabs(s string) string {
	if !strings.Contains(s, "\t") {
		return s
	}
	var b strings.Builder
	col := 0
	for _, r := range s {
		switch r {
		case '\t':
			n := 4 - col%4
			b.WriteString(strings.Repeat(" ", n))
			col += n
		case '\n':
			b.WriteRune(r)
			col = 0
		default:
			b.WriteRune(r)
			col += strWidth(string(r))
		}
	}
	return b.String()
}

func trimBlank(rows []mdLine) []mdLine {
	for len(rows) > 0 && lineWidth(rows[0]) == 0 {
		rows = rows[1:]
	}
	for len(rows) > 0 && lineWidth(rows[len(rows)-1]) == 0 {
		rows = rows[:len(rows)-1]
	}
	return rows
}

func mini(a, b int) int {
	if a < b {
		return a
	}
	return b
}
