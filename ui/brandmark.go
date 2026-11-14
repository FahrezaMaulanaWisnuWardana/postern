package ui

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/png"
	"sync"

	"gioui.org/f32"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"

	"yourpm/core/brand"
)

var (
	logoOnce sync.Once
	logoImg  image.Image
)

// brandLogo decodes the optional base64 PNG injected by the white-label build.
// A malformed value is ignored so the letter mark remains available.
func brandLogo() image.Image {
	logoOnce.Do(func() {
		if brand.Logo == "" {
			return
		}
		raw, err := base64.StdEncoding.DecodeString(brand.Logo)
		if err != nil {
			return
		}
		img, err := png.Decode(bytes.NewReader(raw))
		if err != nil {
			return
		}
		logoImg = img
	})
	return logoImg
}

// drawLogo paints the brand image inside a square of px pixels, preserving its
// aspect ratio.
func (a *App) drawLogo(gtx layout.Context, px int) layout.Dimensions {
	img := brandLogo()
	if img == nil {
		return layout.Dimensions{}
	}
	b := img.Bounds()
	w, h := float32(b.Dx()), float32(b.Dy())
	if w <= 0 || h <= 0 {
		return layout.Dimensions{}
	}
	scale := float32(px) / w
	if s := float32(px) / h; s < scale {
		scale = s
	}
	dw, dh := w*scale, h*scale
	x, y := (float32(px)-dw)/2, (float32(px)-dh)/2

	defer clip.Rect(image.Rect(0, 0, px, px)).Push(gtx.Ops).Pop()
	defer op.Offset(image.Pt(int(x), int(y))).Push(gtx.Ops).Pop()
	defer op.Affine(f32.Affine2D{}.Scale(f32.Point{}, f32.Pt(scale, scale))).Push(gtx.Ops).Pop()
	imgOp := paint.NewImageOp(img)
	imgOp.Filter = paint.FilterLinear
	imgOp.Add(gtx.Ops)
	paint.PaintOp{}.Add(gtx.Ops)
	return layout.Dimensions{Size: image.Pt(px, px)}
}
