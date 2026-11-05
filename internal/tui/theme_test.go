package tui

import (
	"math"
	"testing"
)

func luminance(hex string) float64 {
	c := hexToRGB(hex)
	lin := func(v int) float64 {
		f := float64(v) / 255
		if f <= 0.03928 {
			return f / 12.92
		}
		return math.Pow((f+0.055)/1.055, 2.4)
	}
	return 0.2126*lin(c.r) + 0.7152*lin(c.g) + 0.0722*lin(c.b)
}

func contrast(a, b string) float64 {
	x, y := luminance(a)+0.05, luminance(b)+0.05
	if x < y {
		x, y = y, x
	}
	return x / y
}

// TestBorderContrast holds every theme's box outline to about 2.4:1 against
// the panel it frames, in light and dark, and below the muted text inside.
func TestBorderContrast(t *testing.T) {
	for name, th := range themes {
		for _, dark := range []bool{false, true} {
			panel, border, muted := hexFor(th.Panel, dark), hexFor(th.Border, dark), hexFor(th.Muted, dark)
			if r := contrast(border, panel); r < 2.2 || r > 2.7 {
				t.Errorf("%s (dark %v): border %s is %.2f:1 against panel %s, want about 2.4", name, dark, border, r, panel)
			}
			if contrast(muted, panel) <= contrast(border, panel) {
				t.Errorf("%s (dark %v): the border must stay below muted text", name, dark)
			}
			for field, col := range map[string]string{
				"Accent2": hexFor(th.Accent2, dark), "Canvas": hexFor(th.Canvas, dark),
				"Subtle": hexFor(th.Subtle, dark), "Band": hexFor(th.Band, dark),
			} {
				if len(col) != 7 {
					t.Errorf("%s (dark %v): %s is not set", name, dark, field)
				}
			}
		}
	}
	if themes[defaultTheme].Accent.Dark != "#FAB283" {
		t.Error("the default theme keeps its warm orange accent")
	}
}
