package tui

// home.go is the home screen a new conversation opens on: the ANTARES
// wordmark and the composer centred on the window, the state, agent and model
// under the composer with the workspace (or what to set up) and the version,
// and the command list under that while a /command is typed. No side column
// and no chat box: until the first message there is nothing for them to show.
// The status bar keeps only its keys, on the columns they have in the grid.

import (
	"math"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/enowdev/antares/internal/version"
)

// letterGlyphs are the wordmark's letters, one character per pixel, drawn at
// two pixel columns per stroke and one pixel row per bar: the weight they
// have at this size, since a cell is two pixel rows tall.
var letterGlyphs = map[byte][8]string{
	'A': {".####.", "##..##", "##..##", "##..##", "######", "##..##", "##..##", "##..##"},
	'N': {"##..##", "###.##", "###.##", "##.###", "##.###", "##..##", "##..##", "##..##"},
	'T': {"######", "..##..", "..##..", "..##..", "..##..", "..##..", "..##..", "..##.."},
	'R': {"#####.", "##..##", "##..##", "#####.", "##.##.", "##..##", "##..##", "##..##"},
	'E': {"######", "##....", "##....", "#####.", "##....", "##....", "##....", "######"},
	'S': {".####.", "##..##", "##....", ".####.", "....##", "....##", "##..##", ".####."},
}

// star is Antares itself, rising above the letters as enowX's X does: 'b'
// its arms, at the letters' stroke weight, 's' the glints between them, 'o'
// its core.
var star = [10]string{
	"....bb....",
	"....bb....",
	"....bb....",
	"...sbbs...",
	"bbbboobbbb",
	"bbbboobbbb",
	"...sbbs...",
	"....bb....",
	"....bb....",
	"....bb....",
}

// wordmark is ANTARES and the star, one character per pixel, ten pixel rows.
var wordmark = buildWordmark()

func buildWordmark() [10]string {
	var rows [10][]byte
	for y := range rows {
		rows[y] = []byte(strings.Repeat(".", logoW))
	}
	for i, ch := range []byte("ANTARES") {
		g := letterGlyphs[ch]
		for y, ln := range g {
			copy(rows[y+2][i*7:], ln)
		}
	}
	for y, ln := range star {
		copy(rows[y][starX:], ln)
	}
	var out [10]string
	for y := range rows {
		out[y] = string(rows[y])
	}
	return out
}

const (
	// starX is the star's first pixel column: seven letters of six pixels
	// with one between them, then a gap of two.
	starX = 7*6 + 6 + 2
	logoW = starX + 10
	logoH = 5 // a cell holds two pixel rows
	// homeMinW and homeMaxW bound the composer: never narrower than the
	// wordmark with a margin, never so wide a message cannot be read back.
	homeMinW = logoW + 4
	homeMaxW = 120
	// homeFrame is how long one welcome tick lasts, in seconds.
	homeFrame = 0.09
)

// The opening, in seconds. The letters fade in a beat apart while the star's
// long arms draw out from the core, then the short ones; then the core
// lights. Afterwards only the core moves, one slow breath every 2.4 s.
const (
	letterFade    = 0.6
	letterStagger = 0.05
	armStep       = 0.06
	shortFrom     = 0.35
	coreOn        = 0.6
	coreLit       = 0.8
	breath        = 2.4
)

// homeLines draws the home screen, exactly m.height lines.
func (m *Model) homeLines() []string {
	t := themeByName(m.themeName)
	m.syncComposer()
	m.chrome.sideRect = rect{}
	w, h := m.width, m.height
	bodyH := h - 1
	canvas := make([]string, bodyH)
	blank := paint("", w, t.Canvas)
	for i := range canvas {
		canvas[i] = blank
	}

	const frame = 2
	showLogo := w >= logoW+2 && bodyH >= logoH+frame+8
	logoRows := 0
	if showLogo {
		logoRows = logoH + 1
	}
	block := logoRows + frame + 1 + 1
	boxW := homeWidth(w, bodyH, block)
	info := m.homeInfo(boxW - 2*(1+padX))
	if len(info) > 1 {
		block++
		boxW = homeWidth(w, bodyH, block)
		info = m.homeInfo(boxW - 2*(1+padX))
	}

	fieldW := maxi(boxW-composerLeft-1-padX, 1)
	m.ta.SetWidth(fieldW)
	rows := m.composerRows(fieldW)
	m.ta.SetHeight(rows)
	ih := lesser(rows+frame, maxi(bodyH/2, frame+1))
	// Centred for a one-line composer. One that grows grows down, so the
	// wordmark stays put, and moves up only when it would pass the bottom.
	top := (bodyH - block) / 2
	top = lesser(top, bodyH-(logoRows+ih+len(info)))
	top = maxi(top, 0)

	if showLogo {
		x := (w - logoW) / 2
		for i, ln := range m.wordmarkLines(t) {
			if top+i < bodyH {
				canvas[top+i] = paint(strings.Repeat(" ", x)+ln, w, t.Canvas)
			}
		}
	}
	bx := (w - boxW) / 2
	by := top + logoRows
	composer := m.composerLines(boxW, lesser(ih, bodyH-by), t.Canvas)
	canvas = splice(canvas, composer, bx, by)

	below := by + len(composer)
	if len(m.palette) > 0 {
		// Under the composer, and over the wordmark only when the window is
		// too short for that.
		wanted := lesser(len(m.palette), 10) + frame
		switch {
		case bodyH-below > frame:
			canvas = splice(canvas, m.paletteLines(boxW, lesser(wanted, bodyH-below)), bx, below)
		case by > frame:
			ph := lesser(wanted, by)
			canvas = splice(canvas, m.paletteLines(boxW, ph), bx, by-ph)
		}
	} else {
		// On the composer's own text columns: the badge starts under the ❯.
		inset := 1 + padX
		for i, ln := range info {
			if below+i >= bodyH {
				break
			}
			canvas[below+i] = paint(strings.Repeat(" ", bx+inset)+ln, w, t.Canvas)
		}
	}
	return append(canvas, m.statusLine(w, false, true))
}

// homeWidth is the composer's width for a block blockH rows tall, centred
// with the same gap on every side as it looks on screen: a cell is about
// twice as tall as it is wide, so the gap at the sides is twice as many
// columns as the gap above and below is rows.
func homeWidth(w, h, blockH int) int {
	gapRows := maxi(h-blockH, 0) / 2
	widest := lesser(maxi(w-4, lesser(w, 12)), homeMaxW)
	narrowest := lesser(homeMinW, widest)
	width := clampi(w-4*gapRows, narrowest, widest)
	if (w-width)%2 == 1 && width > narrowest {
		width--
	}
	return width
}

// homeInfo is the line under the composer: the state, agent and model on the
// left; the workspace, read from its end, or what to set up, then the version
// on the right. Two lines when one cannot carry both.
func (m *Model) homeInfo(w int) []string {
	t := themeByName(m.themeName)
	status := m.statusLeft()
	ver := fg(t.Faint).Render(version.Version)
	var setup string
	switch {
	case m.cfg == nil || m.demo:
	case len(m.cfg.Providers) == 0 || m.cfg.Model.Provider == "":
		setup = fg(t.Accent).Bold(true).Render("Connect a provider") + fg(t.Muted).Render(" · /provider")
	case strings.TrimSpace(m.cfg.Model.Default) == "":
		setup = fg(t.Accent).Bold(true).Render("Choose a model") + fg(t.Muted).Render(" · /model")
	}
	place := func(room int) string {
		if setup != "" {
			return setup
		}
		return fg(t.Muted).Render(tail(homeRelative(m.workspace()), room))
	}
	const gap, space = 3, 2
	room := w - ansi.StringWidth(status) - gap - ansi.StringWidth(ver) - space
	full := ansi.StringWidth(place(math.MaxInt32))
	if full <= room || (setup == "" && room >= 16) {
		return []string{splitLine(status, place(room)+strings.Repeat(" ", space)+ver, w)}
	}
	room = w - ansi.StringWidth(ver) - space
	return []string{splitLine(status, "", w), splitLine(place(room), ver, w)}
}

// workspace is where the agent will work: the bound project, else the
// configured workspace.
func (m *Model) workspace() string {
	if m.projectDir != "" {
		return m.projectDir
	}
	if m.cfg != nil {
		return m.cfg.Agent.Workspace
	}
	return ""
}

// homeRelative shows a path with the home directory as ~.
func homeRelative(p string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" || p == "" {
		return p
	}
	if p == home {
		return "~"
	}
	if rel, err := filepath.Rel(home, p); err == nil && !strings.HasPrefix(rel, "..") {
		return "~/" + rel
	}
	return p
}

// tail is the end of s in w cells: a path is read from its end, where the
// project's own directory is.
func tail(s string, w int) string {
	r := []rune(s)
	if len(r) <= w {
		return s
	}
	if w <= 0 {
		return ""
	}
	return "…" + string(r[len(r)-(w-1):])
}

// wordmarkLines draws the wordmark in half blocks at the current point of the
// opening.
func (m *Model) wordmarkLines(t Theme) []string {
	dark := lipgloss.HasDarkBackground()
	elapsed := float64(m.welcomeFrame) * homeFrame
	canvas := hexToRGB(hexFor(t.Canvas, dark))
	out := make([]string, logoH)
	for row := 0; row < logoH; row++ {
		var b strings.Builder
		for col := 0; col < logoW; col++ {
			top, okT := wordmarkPixel(t, dark, col, row*2, elapsed)
			bot, okB := wordmarkPixel(t, dark, col, row*2+1, elapsed)
			switch {
			case !okT && !okB:
				b.WriteByte(' ')
			case okT && !okB:
				b.WriteString(pixelCell("▀", top, canvas))
			case !okT && okB:
				b.WriteString(pixelCell("▄", bot, canvas))
			case top == bot:
				b.WriteString(pixelCell("█", top, canvas))
			default:
				b.WriteString(pixelCell("▀", top, bot))
			}
		}
		out[row] = b.String()
	}
	return out
}

func pixelCell(glyph string, fgc, bgc rgb) string {
	return lipgloss.NewStyle().
		Foreground(lipgloss.Color(rgbHex(fgc))).
		Background(lipgloss.Color(rgbHex(bgc))).
		Render(glyph)
}

// wordmarkPixel is one pixel's colour elapsed seconds into the opening, or
// false while it has not appeared.
func wordmarkPixel(t Theme, dark bool, x, y int, elapsed float64) (rgb, bool) {
	canvas := hexToRGB(hexFor(t.Canvas, dark))
	text := hexToRGB(hexFor(t.Text, dark))
	accent := hexToRGB(hexFor(t.Accent, dark))
	accent2 := hexToRGB(hexFor(t.Accent2, dark))
	switch wordmark[y][x] {
	case '#':
		letter := float64(x / 7)
		shown := easeOut((elapsed - letter*letterStagger) / letterFade)
		if shown <= 0 {
			return rgb{}, false
		}
		return lerpRGB(canvas, text, shown), true
	case 'b':
		// Out from the core, one pixel per step; towards the second accent
		// at the tips, the way a star's light reddens at its edge.
		// Pixels from the 2×2 core: its centre is between pixels 4 and 5.
		d := absi(2*(x-starX)-9)/2 + absi(2*y-9)/2
		if elapsed < float64(d)*armStep {
			return rgb{}, false
		}
		return lerpRGB(accent, accent2, 0.12*float64(d)), true
	case 's':
		if elapsed < shortFrom {
			return rgb{}, false
		}
		return lerpRGB(accent2, accent, 0.35), true
	case 'o':
		if elapsed < coreOn {
			return accent, elapsed >= 0
		}
		if elapsed < coreLit {
			return lerpRGB(accent, text, (elapsed-coreOn)/(coreLit-coreOn)), true
		}
		b := 0.5 + 0.5*math.Cos(2*math.Pi*(elapsed-coreLit)/breath)
		return lerpRGB(accent, text, 0.35+0.65*b), true
	}
	return rgb{}, false
}

func easeOut(p float64) float64 {
	p = math.Max(0, math.Min(1, p))
	return 1 - math.Pow(1-p, 3)
}

func absi(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
