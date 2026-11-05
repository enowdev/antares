package tui

import (
	"strconv"
	"strings"
)

// showWelcome reports whether the transcript is completely empty. Any block
// at all (including a system reply from a command like /help) ends it, so
// command output is never hidden. With no session as well, the home screen
// shows instead of the grid (see isHome and home.go).
func (m *Model) showWelcome() bool {
	return len(m.blocks) == 0
}

// welcomeView is what an empty transcript holds: nothing. The home screen
// draws the wordmark and the composer itself, outside the chat box, and a
// session whose transcript is empty shows an empty chat box.
func (m *Model) welcomeView(w, h int) string {
	return ""
}

// ---- tiny colour helpers ----------------------------------------------------

type rgb struct{ r, g, b int }

func hexToRGB(h string) rgb {
	h = strings.TrimPrefix(h, "#")
	if len(h) != 6 {
		return rgb{200, 200, 200}
	}
	v, _ := strconv.ParseInt(h, 16, 64)
	return rgb{int(v>>16) & 0xff, int(v>>8) & 0xff, int(v) & 0xff}
}

func rgbHex(c rgb) string {
	clamp := func(x int) int {
		if x < 0 {
			return 0
		}
		if x > 255 {
			return 255
		}
		return x
	}
	const hexdig = "0123456789abcdef"
	b := []byte{'#', 0, 0, 0, 0, 0, 0}
	vals := []int{clamp(c.r), clamp(c.g), clamp(c.b)}
	for i, v := range vals {
		b[1+i*2] = hexdig[v>>4]
		b[2+i*2] = hexdig[v&0xf]
	}
	return string(b)
}

func lerpRGB(a, b rgb, f float64) rgb {
	if f < 0 {
		f = 0
	}
	if f > 1 {
		f = 1
	}
	return rgb{
		int(float64(a.r) + (float64(b.r)-float64(a.r))*f + 0.5),
		int(float64(a.g) + (float64(b.g)-float64(a.g))*f + 0.5),
		int(float64(a.b) + (float64(b.b)-float64(a.b))*f + 0.5),
	}
}
