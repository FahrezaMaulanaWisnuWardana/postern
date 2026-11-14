package ui

import (
	"image"
	"image/color"
	"strings"

	"gioui.org/font"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"

	"yourpm/core/model"
	"yourpm/core/service"
)

// treeRow is one visible line in the collection tree.
type treeRow struct {
	e     *model.Entity
	depth int
	kids  bool
	open  bool
}

// treeRows flattens the collection hierarchy into the rows that should be
// drawn. When a filter is active, only matches and their ancestors appear and
// every branch is forced open.
func (a *App) treeRows() []treeRow {
	children := map[string][]*model.Entity{}
	for _, e := range a.entities {
		if e.Kind == model.KindEnvironment || e.Deleted {
			continue
		}
		children[e.ParentID] = append(children[e.ParentID], e)
	}
	for parent := range children {
		list := children[parent]
		service.SortEntities(list)
		children[parent] = list
	}

	keep := map[string]bool{}
	if a.filter != "" {
		byID := map[string]*model.Entity{}
		for _, e := range a.entities {
			byID[e.ID] = e
		}
		needle := strings.ToLower(a.filter)
		for _, e := range a.entities {
			if e.Kind == model.KindEnvironment {
				continue
			}
			if !strings.Contains(strings.ToLower(e.Name), needle) {
				continue
			}
			cur := e
			for cur != nil && !keep[cur.ID] {
				keep[cur.ID] = true
				cur = byID[cur.ParentID]
			}
		}
	}

	var out []treeRow
	var walk func(parent string, depth int)
	walk = func(parent string, depth int) {
		for _, e := range children[parent] {
			if a.filter != "" && !keep[e.ID] {
				continue
			}
			kids := len(children[e.ID]) > 0
			open := (a.expanded[e.ID] || a.filter != "") && kids
			out = append(out, treeRow{e: e, depth: depth, kids: kids, open: open})
			if open {
				walk(e.ID, depth+1)
			}
		}
	}
	walk("", 0)
	return out
}

func (a *App) layoutTree(gtx layout.Context) layout.Dimensions {
	rows := a.treeRows()
	if len(rows) == 0 {
		return layout.Inset{Top: unit.Dp(20), Left: unit.Dp(16), Right: unit.Dp(16)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			msg := "No requests yet."
			if a.filter != "" {
				msg = "Nothing matches “" + a.filter + "”."
			}
			return txt(gtx, a.th, msg, 12, colFaint, faceUI, font.Normal)
		})
	}
	return a.tree.Layout(gtx, len(rows), func(gtx layout.Context, i int) layout.Dimensions {
		if i >= len(rows) {
			return layout.Dimensions{}
		}
		return a.layoutTreeRow(gtx, rows[i])
	})
}

func (a *App) layoutTreeRow(gtx layout.Context, row treeRow) layout.Dimensions {
	e := row.e
	selected := a.selected == e.ID
	rowClick := a.click("tree:" + e.ID)
	moreClick := a.click("more:" + e.ID)

	clicked := rowClick.Clicked(gtx)
	if moreClick.Clicked(gtx) {
		a.openRowMenu(e)
	}
	hovered := rowClick.Hovered() || moreClick.Hovered()
	if clicked {
		a.selected = e.ID
		if row.kids {
			a.expanded[e.ID] = !row.open
		} else {
			a.openEntity(e)
		}
		hovered = true
	}

	// Keep both the row height and action slot stable across hover states.
	// The action also retains its hit area when hidden so moving from the
	// label to the menu button cannot make the button disappear.
	rowHeight := gtx.Dp(unit.Dp(32))
	gtx.Constraints.Min.Y = rowHeight
	gtx.Constraints.Max.Y = rowHeight
	macro := op.Record(gtx.Ops)
	dims := layout.Inset{Top: unit.Dp(1), Bottom: unit.Dp(1), Left: unit.Dp(6), Right: unit.Dp(4)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				return rowClick.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return layout.Inset{Top: unit.Dp(4), Bottom: unit.Dp(4), Left: unit.Dp(6), Right: unit.Dp(2)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
							layout.Rigid(func(gtx layout.Context) layout.Dimensions {
								return layout.Dimensions{Size: image.Pt(gtx.Dp(unit.Dp(row.depth*13)), 0)}
							}),
							layout.Rigid(func(gtx layout.Context) layout.Dimensions {
								if !row.kids {
									return layout.Dimensions{Size: image.Pt(gtx.Dp(unit.Dp(16)), gtx.Dp(unit.Dp(16)))}
								}
								ic := a.ic.Chevron
								if row.open {
									ic = a.ic.Expand
								}
								gtx.Constraints = layout.Exact(image.Pt(gtx.Dp(unit.Dp(16)), gtx.Dp(unit.Dp(16))))
								return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
									gtx.Constraints = layout.Exact(image.Pt(gtx.Dp(unit.Dp(14)), gtx.Dp(unit.Dp(14))))
									return ic.Layout(gtx, colMuted)
								})
							}),
							layout.Rigid(gap(5, 0).Layout),
							layout.Rigid(func(gtx layout.Context) layout.Dimensions {
								ic := a.ic.Request
								c := colMuted
								if e.Kind == model.KindCollection || e.Kind == model.KindFolder {
									ic = a.ic.Folder
								} else {
									c = methodColor(requestMethod(e))
								}
								return iconPx(gtx, ic, 15, c)
							}),
							layout.Rigid(gap(7, 0).Layout),
							layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
								return layout.W.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
									return txtClip(gtx, a.th, e.Name, 13, colText, faceUI, font.Normal)
								})
							}),
							layout.Rigid(func(gtx layout.Context) layout.Dimensions {
								if e.Kind != model.KindRequest {
									return layout.Dimensions{}
								}
								m := requestMethod(e)
								if m == "" {
									return layout.Dimensions{}
								}
								return layout.Inset{Left: unit.Dp(4)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
									return txt(gtx, a.th, strings.ToUpper(m), 9, methodColor(m), faceMono, font.Medium)
								})
							}),
						)
					})
				})
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				ic := a.ic.More
				if !hovered && !selected {
					ic = nil
				}
				return iconBtn(gtx, a.th, moreClick, ic, 14, colMuted, nil)
			}),
		)
	})
	call := macro.Stop()
	bg := color.NRGBA{}
	switch {
	case selected:
		bg = alpha(colAccent, 0x1E)
	case hovered:
		bg = colHover
	}
	if bg.A > 0 {
		fillRR(gtx, dims.Size, unit.Dp(6), bg)
	}
	call.Add(gtx.Ops)
	return dims
}

func requestMethod(e *model.Entity) string {
	if e.Kind != model.KindRequest {
		return ""
	}
	r, err := service.RequestOf(e)
	if err != nil {
		return ""
	}
	return r.Method
}
