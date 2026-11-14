package ui

import (
	"image"
	"strings"
	"testing"
	"time"

	"gioui.org/f32"
	"gioui.org/font"
	gioinput "gioui.org/io/input"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"
	"gioui.org/widget"

	"yourpm/core/model"
)

func testContext(size image.Point, scale float32) layout.Context {
	return layout.Context{
		Ops:         new(op.Ops),
		Constraints: layout.Exact(size),
		Metric:      unit.Metric{PxPerDp: scale, PxPerSp: scale},
	}
}

func TestBarContentsKeepNaturalHeight(t *testing.T) {
	for _, scale := range []float32{1, 2} {
		gtx := testContext(image.Pt(int(980*scale), int(620*scale)), scale)
		var content layout.Dimensions
		dims := barRow(gtx, topbarH, colPanel, layout.Inset{Left: 14, Right: 12}, func(gtx layout.Context) layout.Dimensions {
			if gtx.Constraints.Min.Y != 0 {
				t.Fatal("bar forces its height onto its contents")
			}
			content = txt(gtx, newTheme(), "Personal", 12, colText, faceUI, font.Normal)
			return content
		})
		if dims.Size != image.Pt(gtx.Constraints.Max.X, gtx.Dp(topbarH)) {
			t.Fatalf("scale %v: unexpected bar size %v", scale, dims.Size)
		}
		if content.Size.Y >= dims.Size.Y {
			t.Fatalf("scale %v: text stretched to bar height", scale)
		}
	}
}

func TestFlexibleSpacePushesTrailingContentToEdge(t *testing.T) {
	gtx := testContext(image.Pt(980, 26), 1)
	gtx.Constraints.Min.Y = 0
	var space image.Point
	dims := layout.Flex{}.Layout(gtx,
		layout.Rigid(gap(100, 20).Layout),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			dims := flexibleSpace(gtx)
			space = dims.Size
			return dims
		}),
		layout.Rigid(gap(80, 20).Layout),
	)
	if space.X != 800 || dims.Size.X != 980 {
		t.Fatalf("spacer = %v, row = %v", space, dims.Size)
	}
}

func TestTreeRowsStayCompactAndFillSidebarWidth(t *testing.T) {
	a := &App{th: newTheme(), ic: loadIcons(), clicks: map[string]*widget.Clickable{}}
	for _, selected := range []bool{false, true} {
		e := &model.Entity{ID: "collection", Kind: model.KindCollection, Name: "Example collection"}
		if selected {
			a.selected = e.ID
		}
		gtx := testContext(image.Pt(278, 700), 1)
		gtx.Constraints.Min.Y = 0
		dims := a.layoutTreeRow(gtx, treeRow{e: e, kids: true, open: true})
		if dims.Size != image.Pt(278, 32) {
			t.Fatalf("selected %v: unexpected tree row size %v", selected, dims.Size)
		}
	}
}

func TestTabResponseListsAreVertical(t *testing.T) {
	a := &App{}
	tab := &Tab{}
	a.initTab(tab, &model.Entity{Kind: model.KindRequest})
	if tab.respScroll.Axis != layout.Vertical || tab.metaScroll.Axis != layout.Vertical {
		t.Fatal("response lists must scroll vertically")
	}
}

func TestToastMessageDoesNotStretchToWindow(t *testing.T) {
	for _, scale := range []float32{1, 2} {
		for _, message := range []string{"Deleted “My collection”.", strings.Repeat("Long notification message. ", 20)} {
			a := &App{th: newTheme(), toast: message, toastAt: time.Now()}
			gtx := testContext(image.Pt(int(980*scale), int(900*scale)), scale)
			dims := a.layoutToastMessage(gtx)
			if dims.Size.X > gtx.Dp(420) || dims.Size.Y >= gtx.Constraints.Max.Y/2 {
				t.Fatalf("scale %v: notification stretched to %v", scale, dims.Size)
			}
			if dims.Size.X <= 0 || dims.Size.Y <= gtx.Dp(18) {
				t.Fatalf("notification content is missing: %v", dims.Size)
			}
			gtx.Ops.Reset()
			if dims := a.layoutToast(gtx); dims.Size != gtx.Constraints.Max {
				t.Fatalf("overlay should preserve window bounds, got %v", dims.Size)
			}
		}
	}
}

func TestTreeMenuHoverRetainsHitArea(t *testing.T) {
	a := &App{th: newTheme(), ic: loadIcons(), clicks: map[string]*widget.Clickable{}}
	row := treeRow{e: &model.Entity{ID: "collection", Kind: model.KindCollection, Name: "Collection"}}
	var router gioinput.Router
	gtx := testContext(image.Pt(278, 700), 1)
	gtx.Source = router.Source()
	frame := func() {
		gtx.Ops.Reset()
		dims := a.layoutTreeRow(gtx, row)
		if dims.Size != image.Pt(278, 32) {
			t.Fatalf("hover changed row dimensions: %v", dims.Size)
		}
		router.Frame(gtx.Ops)
	}
	frame()
	for _, x := range []float32{100, 259, 100, 259} {
		router.Queue(pointer.Event{Kind: pointer.Move, Source: pointer.Mouse, Position: f32.Pt(x, 16)})
		frame()
		frame()
		if x == 259 && !a.click("more:collection").Hovered() {
			t.Fatal("menu button lost its hover hit area")
		}
	}
}
