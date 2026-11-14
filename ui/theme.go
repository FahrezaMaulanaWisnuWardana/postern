package ui

import (
	"image"
	"image/color"
	"strconv"
	"strings"

	"gioui.org/font"
	"gioui.org/font/gofont"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/text"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"

	"yourpm/core/brand"
)

// Palette. YourPM uses a warm cream surface (#F3EEE3), near-black ink
// (#111111), and a burnt-orange accent (#D9541E).
var (
	colBg       = rgb(0xF3EEE3)
	colPanel    = rgb(0xEDE6D8)
	colPanel2   = rgb(0xE6DEC9)
	colPanel3   = rgb(0xDDD3BC)
	colHover    = rgb(0xE2D9C4)
	colBorder   = rgb(0xD3C9B3)
	colBorder2  = rgb(0xBFB39B)
	colText     = rgb(0x111111)
	colMuted    = rgb(0x524D45)
	colFaint    = rgb(0x827A6E)
	colAccent   = parseAccent(brand.Accent, rgb(0xD9541E))
	colGreen    = rgb(0x1F8A4C)
	colYellow   = rgb(0xA66E00)
	colRed      = rgb(0xC63B32)
	colBlue     = rgb(0x2563EB)
	colPurple   = rgb(0x7C3AED)
	colCyan     = rgb(0x0E7490)
	colOnAccent = rgb(0xFFF8F0)
)

// applyColorMode runs on the UI goroutine before recording a frame.
// Existing drawing helpers share these tokens across every native surface.
func applyColorMode(mode string) {
	colAccent = parseAccent(brand.Accent, rgb(0xD9541E))
	colOnAccent = rgb(0xFFF8F0)
	if mode == "dark" {
		// Lift the branded accent for small labels on dark surfaces, with
		// dark ink on solid accent buttons.
		colAccent = mix(colAccent, rgb(0xFFFFFF), 0.35)
		colOnAccent = rgb(0x191D24)
		colBg, colPanel = rgb(0x191D24), rgb(0x20252E)
		colPanel2, colPanel3, colHover = rgb(0x2A313C), rgb(0x343E4C), rgb(0x303A48)
		colBorder, colBorder2 = rgb(0x394454), rgb(0x526174)
		colText, colMuted, colFaint = rgb(0xF0F3F7), rgb(0xC1CAD6), rgb(0x9CAABE)
		colGreen, colYellow, colRed = rgb(0x76D6A0), rgb(0xEBC66E), rgb(0xF98D8D)
		colBlue, colPurple, colCyan = rgb(0x86B7FF), rgb(0xC5A3FF), rgb(0x72CEDD)
		return
	}
	colBg, colPanel = rgb(0xF3EEE3), rgb(0xEDE6D8)
	colPanel2, colPanel3, colHover = rgb(0xE6DEC9), rgb(0xDDD3BC), rgb(0xE2D9C4)
	colBorder, colBorder2 = rgb(0xD3C9B3), rgb(0xBFB39B)
	colText, colMuted, colFaint = rgb(0x111111), rgb(0x524D45), rgb(0x827A6E)
	colGreen, colYellow, colRed = rgb(0x1F8A4C), rgb(0xA66E00), rgb(0xC63B32)
	colBlue, colPurple, colCyan = rgb(0x2563EB), rgb(0x7C3AED), rgb(0x0E7490)
}

func updateThemePalette(th *material.Theme) {
	th.Palette = material.Palette{
		Bg: colBg, Fg: colText, ContrastBg: colAccent, ContrastFg: colOnAccent,
	}
}

func (a *App) setColorMode(mode string) {
	if mode != "dark" {
		mode = "light"
	}
	if err := a.svc.Store.SetSetting("ui.theme", mode); err != nil {
		a.notify("Could not save appearance: " + err.Error())
		return
	}
	a.colorMode = mode
	if a.win != nil {
		a.win.Invalidate()
	}
}

const (
	faceUI   font.Typeface = "Go"
	faceMono font.Typeface = "Go Mono"
)

func rgb(v uint32) color.NRGBA {
	return color.NRGBA{R: uint8(v >> 16), G: uint8(v >> 8), B: uint8(v), A: 0xFF}
}

// parseAccent accepts the #RRGGBB colour configured for this build.
func parseAccent(value string, fallback color.NRGBA) color.NRGBA {
	value = strings.TrimSpace(strings.TrimPrefix(value, "#"))
	if len(value) != 6 {
		return fallback
	}
	n, err := strconv.ParseUint(value, 16, 32)
	if err != nil {
		return fallback
	}
	return rgb(uint32(n))
}

func alpha(c color.NRGBA, a uint8) color.NRGBA {
	c.A = a
	return c
}

func mix(a, b color.NRGBA, t float32) color.NRGBA {
	return color.NRGBA{
		R: uint8(float32(a.R) + (float32(b.R)-float32(a.R))*t),
		G: uint8(float32(a.G) + (float32(b.G)-float32(a.G))*t),
		B: uint8(float32(a.B) + (float32(b.B)-float32(a.B))*t),
		A: 0xFF,
	}
}

func newTheme() *material.Theme {
	th := material.NewTheme()
	th.Shaper = text.NewShaper(text.WithCollection(gofont.Collection()))
	updateThemePalette(th)
	th.TextSize = 14
	th.Face = faceUI
	return th
}

func methodColor(m string) color.NRGBA {
	switch m {
	case "GET":
		return colGreen
	case "POST":
		return colYellow
	case "PUT":
		return colBlue
	case "PATCH":
		return colPurple
	case "DELETE":
		return colRed
	case "HEAD", "OPTIONS":
		return colCyan
	}
	return colMuted
}

func statusColor(code int) color.NRGBA {
	switch {
	case code == 0:
		return colRed
	case code < 300:
		return colGreen
	case code < 400:
		return colCyan
	case code < 500:
		return colYellow
	}
	return colRed
}

/* ------------------------------------------------------------------ */
/* Painting helpers                                                   */
/* ------------------------------------------------------------------ */

func fillRR(gtx layout.Context, size image.Point, radius unit.Dp, c color.NRGBA) {
	if size.X <= 0 || size.Y <= 0 {
		return
	}
	defer clip.UniformRRect(image.Rectangle{Max: size}, gtx.Dp(radius)).Push(gtx.Ops).Pop()
	paint.Fill(gtx.Ops, c)
}

func strokeRR(gtx layout.Context, size image.Point, radius unit.Dp, width unit.Dp, c color.NRGBA) {
	if size.X <= 0 || size.Y <= 0 {
		return
	}
	rr := clip.UniformRRect(image.Rectangle{Max: size}, gtx.Dp(radius))
	defer clip.Stroke{Path: rr.Path(gtx.Ops), Width: float32(gtx.Dp(width))}.Op().Push(gtx.Ops).Pop()
	paint.Fill(gtx.Ops, c)
}

func fillRect(gtx layout.Context, size image.Point, c color.NRGBA) {
	if size.X <= 0 || size.Y <= 0 {
		return
	}
	defer clip.Rect{Max: size}.Push(gtx.Ops).Pop()
	paint.Fill(gtx.Ops, c)
}

// panel records the content so the background can be painted behind it at
// exactly the content size.
func panel(gtx layout.Context, bg color.NRGBA, radius unit.Dp, content layout.Widget) layout.Dimensions {
	macro := op.Record(gtx.Ops)
	dims := content(gtx)
	call := macro.Stop()
	fillRR(gtx, dims.Size, radius, bg)
	call.Add(gtx.Ops)
	return dims
}

func bordered(gtx layout.Context, bg, border color.NRGBA, radius unit.Dp, content layout.Widget) layout.Dimensions {
	macro := op.Record(gtx.Ops)
	dims := content(gtx)
	call := macro.Stop()
	fillRR(gtx, dims.Size, radius, bg)
	strokeRR(gtx, dims.Size, radius, 1, border)
	call.Add(gtx.Ops)
	return dims
}

func hline(gtx layout.Context, c color.NRGBA) layout.Dimensions {
	size := image.Pt(gtx.Constraints.Max.X, gtx.Dp(1))
	fillRect(gtx, size, c)
	return layout.Dimensions{Size: size}
}

func vline(gtx layout.Context, c color.NRGBA) layout.Dimensions {
	h := gtx.Constraints.Max.Y
	if h > gtx.Constraints.Min.Y && gtx.Constraints.Min.Y > 0 {
		h = gtx.Constraints.Min.Y
	}
	size := image.Pt(gtx.Dp(1), h)
	fillRect(gtx, size, c)
	return layout.Dimensions{Size: size}
}

// spacer is a fixed-size blank area. It has a Layout method so callers can
// write layout.Rigid(gap(8, 0).Layout).
type spacer struct{ w, h unit.Dp }

func gap(w, h unit.Dp) spacer { return spacer{w: w, h: h} }

func (s spacer) Layout(gtx layout.Context) layout.Dimensions {
	return layout.Dimensions{Size: image.Pt(gtx.Dp(s.w), gtx.Dp(s.h))}
}

func widthOf(gtx layout.Context, w unit.Dp, child layout.Widget) layout.Dimensions {
	px := gtx.Dp(w)
	gtx.Constraints.Min.X = px
	gtx.Constraints.Max.X = px
	return child(gtx)
}

func heightOf(gtx layout.Context, h unit.Dp, child layout.Widget) layout.Dimensions {
	px := gtx.Dp(h)
	gtx.Constraints.Min.Y = px
	gtx.Constraints.Max.Y = px
	return child(gtx)
}

func barRow(gtx layout.Context, h unit.Dp, bg color.NRGBA, inset layout.Inset, child layout.Widget) layout.Dimensions {
	return heightOf(gtx, h, func(gtx layout.Context) layout.Dimensions {
		fillRect(gtx, image.Pt(gtx.Constraints.Max.X, gtx.Constraints.Max.Y), bg)
		return layout.W.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Min.X = gtx.Constraints.Max.X
			// The bar owns its height; its contents keep their natural height
			// and are vertically centered by W instead of stretching labels.
			gtx.Constraints.Min.Y = 0
			return inset.Layout(gtx, child)
		})
	})
}

func expandX(gtx layout.Context, w layout.Widget) layout.Dimensions {
	gtx.Constraints.Min.X = gtx.Constraints.Max.X
	return w(gtx)
}

// flexibleSpace consumes the allocation from Flexed, even without content.
func flexibleSpace(gtx layout.Context) layout.Dimensions {
	return layout.Dimensions{Size: gtx.Constraints.Min}
}

/* ------------------------------------------------------------------ */
/* Text                                                               */
/* ------------------------------------------------------------------ */

func txt(gtx layout.Context, th *material.Theme, s string, size unit.Sp, c color.NRGBA, face font.Typeface, weight font.Weight) layout.Dimensions {
	l := material.Label(th, size, s)
	l.Color = c
	if face == "" {
		face = faceUI
	}
	l.Font = font.Font{Typeface: face, Weight: weight}
	return l.Layout(gtx)
}

func txtClip(gtx layout.Context, th *material.Theme, s string, size unit.Sp, c color.NRGBA, face font.Typeface, weight font.Weight) layout.Dimensions {
	l := material.Label(th, size, s)
	l.Color = c
	l.MaxLines = 1
	l.Truncator = "…"
	if face == "" {
		face = faceUI
	}
	l.Font = font.Font{Typeface: face, Weight: weight}
	return l.Layout(gtx)
}

func muted(gtx layout.Context, th *material.Theme, s string) layout.Dimensions {
	return txt(gtx, th, s, 12, colMuted, faceUI, font.Normal)
}

/* ------------------------------------------------------------------ */
/* Buttons                                                            */
/* ------------------------------------------------------------------ */

type btnKind int

const (
	btnPrimary btnKind = iota
	btnSecondary
	btnGhost
	btnDanger
)

func button(gtx layout.Context, th *material.Theme, click *widget.Clickable, s string, kind btnKind, onClick func()) layout.Dimensions {
	if click.Clicked(gtx) && onClick != nil {
		onClick()
	}
	bg, fg, border := colPanel3, colText, color.NRGBA{}
	switch kind {
	case btnPrimary:
		bg, fg = colAccent, colOnAccent
	case btnSecondary:
		bg, fg, border = colPanel2, colText, colBorder2
	case btnGhost:
		bg, fg = color.NRGBA{}, colMuted
	case btnDanger:
		bg, fg = rgb(0xC63B32), rgb(0xFFF8F0)
	}
	if click.Hovered() && kind == btnGhost {
		bg = colHover
		fg = colText
	} else if click.Hovered() {
		bg = mix(bg, colText, 0.08)
	}

	pad := layout.Inset{Top: unit.Dp(7), Bottom: unit.Dp(7), Left: unit.Dp(12), Right: unit.Dp(12)}
	if s == "" {
		pad = layout.Inset{Top: unit.Dp(6), Bottom: unit.Dp(6), Left: unit.Dp(8), Right: unit.Dp(8)}
	}
	return click.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		macro := op.Record(gtx.Ops)
		dims := pad.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return txt(gtx, th, s, 13, fg, faceUI, font.Medium)
		})
		call := macro.Stop()
		if bg.A > 0 {
			fillRR(gtx, dims.Size, unit.Dp(7), bg)
		}
		if border.A > 0 {
			strokeRR(gtx, dims.Size, unit.Dp(7), 1, border)
		}
		call.Add(gtx.Ops)
		return dims
	})
}

// iconBtn draws a square icon button with a hover surface.
func iconBtn(gtx layout.Context, th *material.Theme, click *widget.Clickable, ic *widget.Icon, size unit.Dp, c color.NRGBA, onClick func()) layout.Dimensions {
	if click.Clicked(gtx) && onClick != nil {
		onClick()
	}
	box := gtx.Dp(unit.Dp(30))
	if size > 18 {
		box = gtx.Dp(size) + gtx.Dp(12)
	}
	return click.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints = layout.Exact(image.Pt(box, box))
		if click.Hovered() {
			fillRR(gtx, image.Pt(box, box), unit.Dp(6), colHover)
		}
		if ic == nil {
			return layout.Dimensions{Size: image.Pt(box, box)}
		}
		return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints = layout.Exact(image.Pt(gtx.Dp(size), gtx.Dp(size)))
			return ic.Layout(gtx, c)
		})
	})
}

func iconPx(gtx layout.Context, ic *widget.Icon, size unit.Dp, c color.NRGBA) layout.Dimensions {
	if ic == nil {
		return layout.Dimensions{}
	}
	gtx.Constraints = layout.Exact(image.Pt(gtx.Dp(size), gtx.Dp(size)))
	return ic.Layout(gtx, c)
}

/* ------------------------------------------------------------------ */
/* Inputs                                                             */
/* ------------------------------------------------------------------ */

type inputOpts struct {
	size    unit.Sp
	mono    bool
	bg      color.NRGBA
	border  color.NRGBA
	pad     layout.Inset
	radius  unit.Dp
	hintCol color.NRGBA
}

func defaultInput() inputOpts {
	return inputOpts{
		size:    13,
		bg:      colPanel2,
		border:  colBorder,
		pad:     layout.Inset{Top: unit.Dp(7), Bottom: unit.Dp(7), Left: unit.Dp(10), Right: unit.Dp(10)},
		radius:  6,
		hintCol: colFaint,
	}
}

func input(gtx layout.Context, th *material.Theme, ed *widget.Editor, hint string, opt inputOpts) layout.Dimensions {
	macro := op.Record(gtx.Ops)
	dims := opt.pad.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		e := material.Editor(th, ed, hint)
		e.TextSize = opt.size
		e.Color = colText
		e.HintColor = opt.hintCol
		e.SelectionColor = alpha(colAccent, 0x50)
		face := faceUI
		if opt.mono {
			face = faceMono
		}
		e.Font = font.Font{Typeface: face}
		return e.Layout(gtx)
	})
	call := macro.Stop()
	if opt.bg.A > 0 {
		fillRR(gtx, dims.Size, opt.radius, opt.bg)
	}
	if opt.border.A > 0 {
		strokeRR(gtx, dims.Size, opt.radius, 1, opt.border)
	}
	call.Add(gtx.Ops)
	return dims
}

// toggle is a compact square checkbox that avoids Material's large hit box.
type toggle struct {
	Click widget.Clickable
	On    bool
}

func (t *toggle) Layout(gtx layout.Context, size unit.Dp, onColor color.NRGBA) layout.Dimensions {
	if t.Click.Clicked(gtx) {
		t.On = !t.On
	}
	px := gtx.Dp(size)
	box := gtx.Dp(unit.Dp(26))
	return t.Click.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints = layout.Exact(image.Pt(box, box))
		return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints = layout.Exact(image.Pt(px, px))
			off := colText
			if !t.On {
				off = colFaint
			}
			fillRR(gtx, image.Pt(px, px), unit.Dp(4), off)
			if t.On {
				defer clip.UniformRRect(image.Rectangle{Max: image.Pt(px, px)}, gtx.Dp(4)).Push(gtx.Ops).Pop()
				paint.Fill(gtx.Ops, onColor)
			}
			return layout.Dimensions{Size: image.Pt(px, px)}
		})
	})
}

/* ------------------------------------------------------------------ */
/* Small pieces                                                       */
/* ------------------------------------------------------------------ */

func badge(gtx layout.Context, th *material.Theme, text string, c color.NRGBA) layout.Dimensions {
	return panel(gtx, alpha(c, 0x22), unit.Dp(4), func(gtx layout.Context) layout.Dimensions {
		return layout.Inset{Top: unit.Dp(2), Bottom: unit.Dp(2), Left: unit.Dp(7), Right: unit.Dp(7)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return txt(gtx, th, text, 11, c, faceMono, font.Medium)
		})
	})
}

func sectionLabel(gtx layout.Context, th *material.Theme, text string) layout.Dimensions {
	return txt(gtx, th, text, 11, colFaint, faceUI, font.Medium)
}
