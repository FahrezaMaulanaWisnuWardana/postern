package ui

import (
	"image/color"
	"math"
	"testing"

	"yourpm/core/service"
)

func TestDarkPaletteContrastAndLightRestore(t *testing.T) {
	t.Cleanup(func() { applyColorMode("light") })
	applyColorMode("light")
	lightBg, lightText := colBg, colText
	applyColorMode("dark")
	th := newTheme()
	if th.Bg != colBg || th.Fg != colText || th.ContrastFg != colOnAccent {
		t.Fatal("material theme does not match the dark palette")
	}
	for _, surface := range []color.NRGBA{colBg, colPanel, colPanel2, colPanel3, colHover} {
		for _, ink := range []color.NRGBA{colText, colMuted, colFaint, colGreen, colYellow, colRed, colBlue, colPurple, colCyan} {
			if ratio := contrastRatio(surface, ink); ratio < 4.5 {
				t.Errorf("insufficient dark-mode contrast: %v on %v = %.2f", ink, surface, ratio)
			}
		}
	}
	if contrastRatio(colAccent, colOnAccent) < 4.5 {
		t.Error("dark-mode primary buttons have insufficient contrast")
	}
	applyColorMode("light")
	if colBg != lightBg || colText != lightText {
		t.Fatal("switching back did not restore light colors")
	}
}

func TestColorModePreferenceIsSaved(t *testing.T) {
	svc, err := service.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer svc.Close()
	a := &App{svc: svc}
	for _, mode := range []string{"dark", "light"} {
		a.setColorMode(mode)
		if a.colorMode != mode || svc.Store.Setting("ui.theme") != mode {
			t.Fatalf("theme preference was not saved: %s", mode)
		}
	}
}

func contrastRatio(a, b color.NRGBA) float64 {
	luminance := func(c color.NRGBA) float64 {
		channel := func(v uint8) float64 {
			n := float64(v) / 255
			if n <= 0.04045 {
				return n / 12.92
			}
			return math.Pow((n+0.055)/1.055, 2.4)
		}
		return 0.2126*channel(c.R) + 0.7152*channel(c.G) + 0.0722*channel(c.B)
	}
	x, y := luminance(a), luminance(b)
	if x < y {
		x, y = y, x
	}
	return (x + 0.05) / (y + 0.05)
}
