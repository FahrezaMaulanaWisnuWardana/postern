package ui

import (
	"encoding/json"
	"strings"
	"time"

	"gioui.org/font"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"

	"yourpm/core/model"
	"yourpm/core/service"
)

func (a *App) openHistory() {
	a.historyOpen = true
	entries, err := a.svc.History(a.wsID, 500)
	if err != nil {
		a.notify(err.Error())
		return
	}
	a.history = entries
	a.histList.Position.First = 0
	a.histList.Position.Offset = 0
	a.win.Invalidate()
}

func (a *App) layoutHistory(gtx layout.Context) layout.Dimensions {
	th := a.th
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Top: unit.Dp(14), Bottom: unit.Dp(10), Left: unit.Dp(18), Right: unit.Dp(18)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return iconBtn(gtx, th, a.click("hist-back"), a.ic.ArrowBack, 16, colMuted, func() {
							a.historyOpen = false
						})
					}),
					layout.Rigid(gap(8, 0).Layout),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return txt(gtx, th, "History", 20, colText, faceUI, font.SemiBold)
					}),
					layout.Rigid(gap(16, 0).Layout),
					layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
						opt := defaultInput()
						opt.pad = layout.Inset{Top: unit.Dp(6), Bottom: unit.Dp(6), Left: unit.Dp(10), Right: unit.Dp(10)}
						return input(gtx, th, &a.histQuery, "Filter by URL or method", opt)
					}),
					layout.Rigid(gap(8, 0).Layout),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return button(gtx, th, a.click("hist-refresh"), "Refresh", btnGhost, func() {
							a.openHistory()
						})
					}),
					layout.Rigid(gap(4, 0).Layout),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return button(gtx, th, a.click("hist-clear"), "Clear", btnGhost, func() {
							a.confirm("Clear history?", "This removes the local request and response log for this workspace.", "Clear", func() {
								if err := a.svc.ClearHistory(a.wsID); err != nil {
									a.notify(err.Error())
									return
								}
								a.history = nil
								a.dialog = nil
								a.notify("History cleared.")
							})
						})
					}),
				)
			})
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Left: unit.Dp(18), Right: unit.Dp(18)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return hline(gtx, colBorder)
			})
		}),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Min.Y = gtx.Constraints.Max.Y
			query := strings.ToLower(strings.TrimSpace(a.histQuery.Text()))
			var rows []*model.HistoryEntry
			for _, h := range a.history {
				if query != "" {
					if !strings.Contains(strings.ToLower(h.URL), query) && !strings.Contains(strings.ToLower(h.Method), query) {
						continue
					}
				}
				rows = append(rows, h)
			}
			if len(rows) == 0 {
				return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Axis: layout.Vertical, Alignment: layout.Middle}.Layout(gtx,
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return txt(gtx, th, "No history", 14, colMuted, faceUI, font.Medium)
						}),
						layout.Rigid(gap(0, 6).Layout),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return txt(gtx, th, "Requests you send will appear here.", 12, colFaint, faceUI, font.Normal)
						}),
					)
				})
			}
			return layout.Inset{Top: unit.Dp(6), Left: unit.Dp(10), Right: unit.Dp(10)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				gtx.Constraints.Min.Y = gtx.Constraints.Max.Y
				return a.histList.Layout(gtx, len(rows), func(gtx layout.Context, i int) layout.Dimensions {
					if i >= len(rows) {
						return layout.Dimensions{}
					}
					return a.layoutHistoryRow(gtx, rows[i])
				})
			})
		}),
	)
}

func (a *App) layoutHistoryRow(gtx layout.Context, h *model.HistoryEntry) layout.Dimensions {
	th := a.th
	click := a.click("hist:" + h.ID)
	if click.Clicked(gtx) {
		a.restoreHistory(h)
	}
	return click.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		macro := op.Record(gtx.Ops)
		dims := layout.Inset{Top: unit.Dp(8), Bottom: unit.Dp(8), Left: unit.Dp(10), Right: unit.Dp(10)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return widthOf(gtx, 60, func(gtx layout.Context) layout.Dimensions {
						return txt(gtx, th, strings.ToUpper(h.Method), 11, methodColor(strings.ToUpper(h.Method)), faceMono, font.Medium)
					})
				}),
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					return txtClip(gtx, th, h.URL, 12, colText, faceMono, font.Normal)
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					if h.Status == 0 {
						return layout.Dimensions{}
					}
					return layout.Inset{Left: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return txt(gtx, th, itoa(h.Status), 11, statusColor(h.Status), faceMono, font.Medium)
					})
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.Inset{Left: unit.Dp(12)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return widthOf(gtx, 64, func(gtx layout.Context) layout.Dimensions {
							return txt(gtx, th, humanAgo(h.At), 11, colFaint, faceUI, font.Normal)
						})
					})
				}),
			)
		})
		call := macro.Stop()
		if click.Hovered() {
			fillRR(gtx, dims.Size, unit.Dp(6), colHover)
		}
		call.Add(gtx.Ops)
		return dims
	})
}

func (a *App) restoreHistory(h *model.HistoryEntry) {
	if h.RequestID != "" {
		if e := a.entityByID(h.RequestID); e != nil {
			a.openEntity(e)
			a.historyOpen = false
			return
		}
	}
	if len(h.Request) == 0 {
		a.notify("That request no longer exists.")
		return
	}
	var req model.RequestData
	if err := json.Unmarshal(h.Request, &req); err != nil {
		a.notify("Could not read the stored request.")
		return
	}
	name := "Restored " + strings.ToUpper(h.Method)
	if path := urlPathName(h.URL); path != "" {
		name += " " + path
	}
	parent := ""
	for _, e := range a.entities {
		if e.Kind == model.KindCollection && e.ParentID == "" {
			parent = e.ID
			break
		}
	}
	e, err := a.svc.NewEntity(a.wsID, model.KindRequest, parent, name)
	if err != nil {
		a.notify(err.Error())
		return
	}
	_ = setRequestData(e, req)
	if err := a.svc.SaveEntity(e); err != nil {
		a.notify(err.Error())
		return
	}
	a.reloadEntities()
	a.openEntity(e)
	a.historyOpen = false
	a.notify("Restored into a new request.")
}

func setRequestData(e *model.Entity, req model.RequestData) error {
	return service.SetRequest(e, req)
}

func urlPathName(raw string) string {
	s := raw
	if i := strings.Index(s, "?"); i >= 0 {
		s = s[:i]
	}
	s = strings.TrimSuffix(s, "/")
	if i := strings.LastIndex(s, "/"); i >= 0 && i+1 < len(s) {
		return s[i+1:]
	}
	return ""
}

var _ = time.Now
