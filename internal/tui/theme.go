package tui

import (
	"sort"

	"github.com/charmbracelet/lipgloss"
)

// Theme is a named colour scheme. Everything else in the UI is greyscale; a
// theme only picks the accents and the neutral ramp, so switching stays calm.
//
// The surfaces are what the grid of boxes is painted with: Canvas behind
// everything, Panel inside every box, Subtle inside the composer, Band under
// a selected row. Border is mixed from the theme's own Panel and Muted to
// about 2.4:1 against Panel, so a box's outline can be seen without
// competing with the muted text it frames; theme_test.go holds every theme
// to that.
type Theme struct {
	Name    string
	Accent  lipgloss.AdaptiveColor // the highlight (prompt, focus, "you", the active tab)
	Accent2 lipgloss.AdaptiveColor // the second accent (a command being typed, a question)
	Text    lipgloss.AdaptiveColor
	Muted   lipgloss.AdaptiveColor
	Faint   lipgloss.AdaptiveColor
	Border  lipgloss.AdaptiveColor
	Green   lipgloss.AdaptiveColor
	Red     lipgloss.AdaptiveColor
	Yellow  lipgloss.AdaptiveColor

	Canvas lipgloss.AdaptiveColor // behind the boxes and the status bar
	Panel  lipgloss.AdaptiveColor // inside every box
	Subtle lipgloss.AdaptiveColor // inside the composer
	Band   lipgloss.AdaptiveColor // under the selected row of a list
}

func c(light, dark string) lipgloss.AdaptiveColor {
	return lipgloss.AdaptiveColor{Light: light, Dark: dark}
}

// themes: the default "antares" is a grey/black scheme with a restrained
// warm-orange accent (after the red supergiant star), its second accent the
// star's red. The rest change only the accents + neutral temperature.
var themes = map[string]Theme{
	"antares": {
		Name:   "antares",
		Accent: c("#C2410C", "#FAB283"), Accent2: c("#B42318", "#EF6F6C"),
		Text: c("#24283B", "#CBD2E0"), Muted: c("#5C6370", "#8B93A7"), Faint: c("#9CA3AF", "#5A6172"),
		Border: c("#A3A7AE", "#505562"), Green: c("#3F7A3F", "#9ECE6A"), Red: c("#B4413A", "#F7768E"), Yellow: c("#B45309", "#E0AF68"),
		Canvas: c("#F3F3F4", "#101217"), Panel: c("#FFFFFF", "#15171D"), Subtle: c("#F7F7F8", "#1B1E25"), Band: c("#ECECEF", "#272B35"),
	},
	"mono": {
		Name:   "mono",
		Accent: c("#111111", "#E6E6E6"), Accent2: c("#6B7280", "#9CA3AF"),
		Text: c("#1F1F1F", "#D0D0D0"), Muted: c("#6B7280", "#8A8A8A"), Faint: c("#A1A1AA", "#5A5A5A"),
		Border: c("#A2A7B0", "#545454"), Green: c("#4B5563", "#B8B8B8"), Red: c("#B4413A", "#F0A0A0"), Yellow: c("#6B7280", "#C8C8C8"),
		Canvas: c("#F3F3F3", "#0E0E0E"), Panel: c("#FFFFFF", "#141414"), Subtle: c("#F7F7F7", "#1B1B1B"), Band: c("#EAEAEA", "#2A2A2A"),
	},
	"tokyonight": {
		Name:   "tokyonight",
		Accent: c("#2E7DE9", "#7AA2F7"), Accent2: c("#7847BD", "#BB9AF7"),
		Text: c("#343B58", "#C0CAF5"), Muted: c("#6172B0", "#9AA5CE"), Faint: c("#9BA3C9", "#565F89"),
		Border: c("#95A0C9", "#52576F"), Green: c("#587539", "#9ECE6A"), Red: c("#8C4351", "#F7768E"), Yellow: c("#8F5E15", "#E0AF68"),
		Canvas: c("#E9E9ED", "#16161E"), Panel: c("#F7F7F9", "#1A1B26"), Subtle: c("#EEEFF3", "#1F2335"), Band: c("#DCDEE7", "#292E42"),
	},
	"dracula": {
		Name:   "dracula",
		Accent: c("#7C3AED", "#BD93F9"), Accent2: c("#C2185B", "#FF79C6"),
		Text: c("#282A36", "#F8F8F2"), Muted: c("#6272A4", "#A9B1D6"), Faint: c("#9AA0C0", "#6272A4"),
		Border: c("#9CA6C6", "#5F637A"), Green: c("#2E7D32", "#50FA7B"), Red: c("#C62828", "#FF5555"), Yellow: c("#B45309", "#F1FA8C"),
		Canvas: c("#F1F1F4", "#21222C"), Panel: c("#FFFFFF", "#282A36"), Subtle: c("#F6F6F9", "#2F3241"), Band: c("#E8E8EF", "#3B3E51"),
	},
	"gruvbox": {
		Name:   "gruvbox",
		Accent: c("#AF3A03", "#FE8019"), Accent2: c("#427B58", "#8EC07C"),
		Text: c("#3C3836", "#EBDBB2"), Muted: c("#7C6F64", "#A89984"), Faint: c("#A89984", "#665C54"),
		Border: c("#A79B86", "#686156"), Green: c("#79740E", "#B8BB26"), Red: c("#9D0006", "#FB4934"), Yellow: c("#B57614", "#FABD2F"),
		Canvas: c("#F2E5BC", "#1D2021"), Panel: c("#FBF1C7", "#282828"), Subtle: c("#F6EAC0", "#32302F"), Band: c("#EBDBB2", "#3C3836"),
	},
	"forest": {
		Name:   "forest",
		Accent: c("#0F766E", "#5EEAD4"), Accent2: c("#4D7C0F", "#A3E635"),
		Text: c("#1F2937", "#D1FAE5"), Muted: c("#4B7C6F", "#7FB3A3"), Faint: c("#94A3A0", "#4B6358"),
		Border: c("#8CABA3", "#405C53"), Green: c("#3F7A3F", "#6EE7B7"), Red: c("#B4413A", "#FCA5A5"), Yellow: c("#B45309", "#FCD34D"),
		Canvas: c("#EEF3F1", "#0C1412"), Panel: c("#FAFCFB", "#111B18"), Subtle: c("#F2F6F4", "#16231F"), Band: c("#E0EAE6", "#1F302B"),
	},
}

const defaultTheme = "antares"

// ThemeNames lists the available theme names (exported for the CLI).
func ThemeNames() []string { return themeNames() }

// ThemeExists reports whether a theme name is known (exported for the CLI).
func ThemeExists(name string) bool { _, ok := themes[name]; return ok }

func themeByName(name string) Theme {
	if t, ok := themes[name]; ok {
		return t
	}
	return themes[defaultTheme]
}

func themeNames() []string {
	out := make([]string, 0, len(themes))
	for n := range themes {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}
