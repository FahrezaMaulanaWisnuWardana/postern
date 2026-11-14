package ui

import (
	"image"
	"image/color"
	"strings"
	"time"

	"gioui.org/font"
	"gioui.org/io/event"
	"gioui.org/io/key"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"

	"yourpm/core/brand"
	"yourpm/core/model"
)

// Layout is the root of the native window. It is called once per frame.
func (a *App) Layout(gtx layout.Context) {
	applyColorMode(a.colorMode)
	updateThemePalette(a.th)
	a.drain()
	a.filter = a.search.Text()
	a.refreshDirty()
	a.checkIdle()

	live := gtx
	if a.dialog != nil || a.menu != nil {
		// The modal sheet owns input while it is open.
		gtx = gtx.Disabled()
	}

	if a.locked {
		a.layoutLock(gtx)
		a.layoutDialog(live)
		a.layoutToast(live)
		return
	}
	if !a.ready {
		a.layoutLoading(gtx)
		return
	}
	if !a.keysFocused {
		a.keysFocused = true
		gtx.Execute(key.FocusCmd{Tag: &a.keys})
	}
	// Register a full-window tag so pointer and key activity can defer the
	// vault auto-lock timer.
	event.Op(gtx.Ops, &a.activity)
	a.maybeAutoSync()

	fillRect(gtx, gtx.Constraints.Max, colBg)
	layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions { return a.layoutTopbar(gtx) }),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Min.Y = gtx.Constraints.Max.Y
			return layout.Flex{Axis: layout.Horizontal}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return widthOf(gtx, sidebarW, func(gtx layout.Context) layout.Dimensions {
						gtx.Constraints.Min.Y = gtx.Constraints.Max.Y
						return a.layoutSidebar(gtx)
					})
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return vline(gtx, colBorder)
				}),
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					gtx.Constraints.Min.Y = gtx.Constraints.Max.Y
					return a.layoutMain(gtx)
				}),
			)
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions { return a.layoutStatus(gtx) }),
	)

	a.layoutDialog(live)
	a.layoutMenu(live)
	a.layoutToast(live)
}

/* ------------------------------------------------------------------ */
/* Top bar                                                            */
/* ------------------------------------------------------------------ */

func (a *App) layoutTopbar(gtx layout.Context) layout.Dimensions {
	th := a.th
	return barRow(gtx, topbarH, colPanel, layout.Inset{Left: unit.Dp(14), Right: unit.Dp(12)}, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return a.brandMark(gtx, 22)
			}),
			layout.Rigid(gap(9, 0).Layout),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return txt(gtx, th, brand.Name, 15, colText, faceUI, font.SemiBold)
			}),
			layout.Rigid(gap(20, 0).Layout),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return a.workspaceChip(gtx)
			}),
			layout.Rigid(gap(6, 0).Layout),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return a.environmentChip(gtx)
			}),
			layout.Flexed(1, flexibleSpace),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return a.syncChip(gtx)
			}),
			layout.Rigid(gap(4, 0).Layout),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return iconBtn(gtx, th, a.click("top-history"), a.ic.History, 17, pick(a.historyOpen, colAccent, colMuted), func() {
					if a.historyOpen {
						a.historyOpen = false
					} else {
						a.openHistory()
					}
				})
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return iconBtn(gtx, th, a.click("top-settings"), a.ic.Settings, 17, colMuted, func() {
					a.openSettings(0)
				})
			}),
		)
	})
}

func (a *App) brandMark(gtx layout.Context, size unit.Dp) layout.Dimensions {
	px := gtx.Dp(size)
	if brandLogo() != nil {
		gtx.Constraints = layout.Exact(image.Pt(px, px))
		return a.drawLogo(gtx, px)
	}
	gtx.Constraints = layout.Exact(image.Pt(px, px))
	fillRR(gtx, image.Pt(px, px), unit.Dp(6), colAccent)
	initial := "Y"
	if r := []rune(brand.Name); len(r) > 0 {
		initial = strings.ToUpper(string(r[0]))
	}
	return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return txt(gtx, a.th, initial, unit.Sp(float32(size)*0.62), colOnAccent, faceUI, font.Bold)
	})
}

func (a *App) workspaceChip(gtx layout.Context) layout.Dimensions {
	th := a.th
	click := a.click("ws-chip")
	if click.Clicked(gtx) {
		a.workspaceMenu()
	}
	name := "Workspace"
	if w := a.workspace(); w != nil {
		name = w.Name
	}
	return click.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return panel(gtx, pick(click.Hovered(), colPanel3, colPanel2), unit.Dp(7), func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Top: unit.Dp(5), Bottom: unit.Dp(5), Left: unit.Dp(9), Right: unit.Dp(7)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return iconPx(gtx, a.ic.Home, 13, colMuted)
					}),
					layout.Rigid(gap(6, 0).Layout),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return txt(gtx, th, name, 12, colText, faceUI, font.Medium)
					}),
					layout.Rigid(gap(4, 0).Layout),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return iconPx(gtx, a.ic.Dropdown, 14, colMuted)
					}),
				)
			})
		})
	})
}

func (a *App) workspaceMenu() {
	m := &menu{title: "Workspaces"}
	for _, ws := range a.workspaces {
		ws := ws
		label := ws.Name
		if ws.ID == a.wsID {
			label += "  ✓"
		}
		if ws.Linked() {
			label += "  ·  synced"
		}
		m.items = append(m.items, menuItem{label: label, icon: a.ic.Home, onClick: func() {
			if ws.ID != a.wsID {
				a.setWorkspace(ws.ID)
			}
		}})
	}
	m.items = append(m.items, menuItem{label: "New workspace…", icon: a.ic.Add, onClick: func() {
		a.prompt("New workspace", "Name", "New workspace", "Create", func(name string) {
			ws, err := a.svc.CreateWorkspace(name)
			if err != nil {
				a.notify(err.Error())
				return
			}
			a.dialog = nil
			a.setWorkspace(ws.ID)
		})
	}})
	m.items = append(m.items, menuItem{label: "Trash…", icon: a.ic.Delete, onClick: func() {
		a.openTrash()
	}})
	m.items = append(m.items, menuItem{label: "Export workspace backup…", icon: a.ic.FileExport, onClick: func() {
		a.exportWorkspaceBackup()
	}})
	m.items = append(m.items, menuItem{label: "Restore workspace backup…", icon: a.ic.FileImport, onClick: func() {
		a.importWorkspaceBackup()
	}})
	a.menu = m
}

func (a *App) environmentChip(gtx layout.Context) layout.Dimensions {
	th := a.th
	click := a.click("env-chip")
	if click.Clicked(gtx) {
		a.environmentMenu()
	}
	label := "No environment"
	for _, e := range a.envs {
		if e.ID == a.activeEnv {
			label = e.Name
			break
		}
	}
	return click.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return panel(gtx, pick(click.Hovered(), colPanel3, colPanel2), unit.Dp(7), func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Top: unit.Dp(5), Bottom: unit.Dp(5), Left: unit.Dp(9), Right: unit.Dp(7)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return iconPx(gtx, a.ic.Code, 13, colMuted)
					}),
					layout.Rigid(gap(6, 0).Layout),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return txt(gtx, th, label, 12, colText, faceUI, font.Medium)
					}),
					layout.Rigid(gap(4, 0).Layout),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return iconPx(gtx, a.ic.Dropdown, 14, colMuted)
					}),
				)
			})
		})
	})
}

func (a *App) environmentMenu() {
	m := &menu{title: "Environments"}
	m.items = append(m.items, menuItem{label: "No environment", icon: a.ic.Cancel, onClick: func() {
		a.setEnvironment("")
	}})
	for _, env := range a.envs {
		env := env
		m.items = append(m.items, menuItem{label: env.Name, icon: a.ic.Code, onClick: func() {
			a.setEnvironment(env.ID)
		}})
	}
	if len(a.envs) > 0 {
		m.items = append(m.items, menuItem{label: "Edit environment…", icon: a.ic.Edit, onClick: func() {
			for _, env := range a.envs {
				if env.ID == a.activeEnv {
					a.openEntity(env)
					return
				}
			}
			if len(a.envs) > 0 {
				a.openEntity(a.envs[0])
			}
		}})
	}
	m.items = append(m.items, menuItem{label: "New environment…", icon: a.ic.Add, onClick: func() {
		a.newEntity(model.KindEnvironment, "")
	}})
	a.menu = m
}

func (a *App) syncChip(gtx layout.Context) layout.Dimensions {
	th := a.th
	ws := a.workspace()
	click := a.click("sync-chip")
	if click.Clicked(gtx) {
		if ws != nil && ws.Linked() {
			a.syncNow()
		} else {
			a.openSyncSettings()
		}
	}
	label := "Local only"
	c := colFaint
	ic := a.ic.CloudOff
	switch {
	case a.syncing:
		label = "Syncing…"
		c = colAccent
		ic = a.ic.Sync
	case ws != nil && ws.Linked():
		label = "Synced"
		if ws.Sync.LastSyncAt > 0 {
			label = humanAgo(ws.Sync.LastSyncAt)
		}
		c = colGreen
		ic = a.ic.Cloud
	case a.syn.LoggedIn():
		label = "Set up sync"
		c = colMuted
	}
	if a.syncNote != "" && time.Since(a.syncWhen) < 8*time.Second {
		label = a.syncNote
	}
	return click.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Inset{Top: unit.Dp(5), Bottom: unit.Dp(5), Left: unit.Dp(8), Right: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						gtx.Constraints = layout.Exact(image.Pt(gtx.Dp(unit.Dp(14)), gtx.Dp(unit.Dp(14))))
						return ic.Layout(gtx, c)
					})
				}),
				layout.Rigid(gap(6, 0).Layout),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return txt(gtx, th, label, 11, c, faceUI, font.Medium)
				}),
			)
		})
	})
}

/* ------------------------------------------------------------------ */
/* Sidebar                                                            */
/* ------------------------------------------------------------------ */

func (a *App) layoutSidebar(gtx layout.Context) layout.Dimensions {
	th := a.th
	fillRect(gtx, image.Pt(gtx.Constraints.Max.X, gtx.Constraints.Max.Y), colPanel)
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Top: unit.Dp(10), Bottom: unit.Dp(8), Left: unit.Dp(10), Right: unit.Dp(10)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return panel(gtx, colPanel2, unit.Dp(7), func(gtx layout.Context) layout.Dimensions {
					return layout.Inset{Top: unit.Dp(2), Bottom: unit.Dp(2), Left: unit.Dp(8), Right: unit.Dp(6)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
							layout.Rigid(func(gtx layout.Context) layout.Dimensions {
								return iconPx(gtx, a.ic.Search, 14, colFaint)
							}),
							layout.Rigid(gap(6, 0).Layout),
							layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
								opt := defaultInput()
								opt.bg = color.NRGBA{}
								opt.border = color.NRGBA{}
								opt.pad = layout.Inset{Top: unit.Dp(6), Bottom: unit.Dp(6)}
								return input(gtx, th, &a.search, "Search requests", opt)
							}),
						)
					})
				})
			})
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Left: unit.Dp(14), Right: unit.Dp(14), Bottom: unit.Dp(2)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
					layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
						return sectionLabel(gtx, th, "COLLECTIONS")
					}),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return iconBtn(gtx, th, a.click("side-new-col"), a.ic.Add, 13, colMuted, func() {
							a.newEntity(model.KindCollection, "")
						})
					}),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return iconBtn(gtx, th, a.click("side-import"), a.ic.FileImport, 13, colMuted, func() {
							a.importPostman()
						})
					}),
				)
			})
		}),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Min.Y = gtx.Constraints.Max.Y
			return layout.Inset{Bottom: unit.Dp(6)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return a.layoutTree(gtx)
			})
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Left: unit.Dp(14), Right: unit.Dp(14)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return hline(gtx, colBorder)
			})
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Top: unit.Dp(8), Left: unit.Dp(14), Right: unit.Dp(14)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
					layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
						return sectionLabel(gtx, th, "ENVIRONMENTS")
					}),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return iconBtn(gtx, th, a.click("side-new-env"), a.ic.Add, 13, colMuted, func() {
							a.newEntity(model.KindEnvironment, "")
						})
					}),
				)
			})
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			if len(a.envs) == 0 {
				return layout.Inset{Top: unit.Dp(4), Bottom: unit.Dp(8), Left: unit.Dp(18)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return txt(gtx, th, "No environments", 11, colFaint, faceUI, font.Normal)
				})
			}
			var kids []layout.FlexChild
			for _, env := range a.envs {
				env := env
				kids = append(kids, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return a.envRow(gtx, env)
				}))
			}
			return layout.Inset{Top: unit.Dp(2), Bottom: unit.Dp(6), Left: unit.Dp(8), Right: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Vertical}.Layout(gtx, kids...)
			})
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Left: unit.Dp(10), Right: unit.Dp(10), Bottom: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return button(gtx, th, a.click("side-new-req"), "+ New request", btnSecondary, func() {
					a.newEntity(model.KindRequest, "")
				})
			})
		}),
	)
}

func (a *App) envRow(gtx layout.Context, env *model.Entity) layout.Dimensions {
	th := a.th
	active := env.ID == a.activeEnv
	click := a.click("env:" + env.ID)
	if click.Clicked(gtx) {
		a.setEnvironment(env.ID)
		a.openEntity(env)
	}
	return click.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Inset{Top: unit.Dp(1), Bottom: unit.Dp(1)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return panel(gtx, pick(active, alpha(colAccent, 0x1E), pick(click.Hovered(), colHover, colorNRGBA())), unit.Dp(6), func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Top: unit.Dp(5), Bottom: unit.Dp(5), Left: unit.Dp(8), Right: unit.Dp(6)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							c := colFaint
							if active {
								c = colAccent
							}
							return iconPx(gtx, a.ic.Code, 13, c)
						}),
						layout.Rigid(gap(7, 0).Layout),
						layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
							return txtClip(gtx, th, env.Name, 12, colText, faceUI, font.Normal)
						}),
					)
				})
			})
		})
	})
}

/* ------------------------------------------------------------------ */
/* Main area                                                          */
/* ------------------------------------------------------------------ */

func (a *App) layoutMain(gtx layout.Context) layout.Dimensions {
	fillRect(gtx, gtx.Constraints.Max, colBg)
	if a.historyOpen {
		return a.layoutHistory(gtx)
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions { return a.layoutTabbar(gtx) }),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return hline(gtx, colBorder)
		}),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Min.Y = gtx.Constraints.Max.Y
			t := a.activeTab()
			if t == nil {
				return a.layoutEmpty(gtx)
			}
			switch t.Kind {
			case model.KindRequest:
				return a.layoutRequestTab(gtx, t)
			case model.KindEnvironment:
				return a.layoutEnvironmentTab(gtx, t)
			case model.KindCollection:
				return a.layoutCollectionTab(gtx, t)
			}
			return a.layoutEmpty(gtx)
		}),
	)
}

func (a *App) layoutTabbar(gtx layout.Context) layout.Dimensions {
	th := a.th
	return barRow(gtx, tabbarH, colPanel, layout.Inset{Left: unit.Dp(6), Right: unit.Dp(6)}, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				return a.tabList.Layout(gtx, len(a.tabs)+1, func(gtx layout.Context, i int) layout.Dimensions {
					if i == len(a.tabs) {
						return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
							return iconBtn(gtx, th, a.click("tab-add"), a.ic.Add, 14, colMuted, func() {
								a.newEntity(model.KindRequest, "")
							})
						})
					}
					return a.layoutTab(gtx, i)
				})
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				if t := a.activeTab(); t != nil && t.Kind == model.KindRequest {
					return button(gtx, th, a.click("tab-curl"), "cURL", btnGhost, func() {
						a.curlForActive()
					})
				}
				return layout.Dimensions{}
			}),
		)
	})
}

func (a *App) layoutTab(gtx layout.Context, i int) layout.Dimensions {
	th := a.th
	t := a.tabs[i]
	e := a.entityByID(t.ID)
	name := "Untitled"
	if e != nil {
		name = e.Name
	}
	active := a.active == i
	closeClick := a.click("tabclose:" + t.ID)
	if closeClick.Clicked(gtx) {
		a.closeTab(i)
		return layout.Dimensions{}
	}
	click := a.click("tab:" + t.ID)
	if click.Clicked(gtx) {
		a.active = i
		if e != nil {
			a.selected = e.ID
		}
	}
	return layout.Inset{Top: unit.Dp(5), Bottom: unit.Dp(5), Right: unit.Dp(2)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return click.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			macro := op.Record(gtx.Ops)
			dims := layout.Inset{Top: unit.Dp(4), Bottom: unit.Dp(4), Left: unit.Dp(10), Right: unit.Dp(6)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						ic := a.ic.Request
						c := colMuted
						switch t.Kind {
						case model.KindEnvironment:
							ic = a.ic.Code
						case model.KindCollection:
							ic = a.ic.Folder
						default:
							c = methodColor(t.Req.Method)
						}
						return iconPx(gtx, ic, 12, c)
					}),
					layout.Rigid(gap(6, 0).Layout),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return widthOf(gtx, 160, func(gtx layout.Context) layout.Dimensions {
							return txtClip(gtx, th, name, 12, pick(active, colText, colMuted), faceUI, font.Medium)
						})
					}),
					layout.Rigid(gap(4, 0).Layout),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						if !t.Dirty {
							return layout.Dimensions{}
						}
						return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
							px := gtx.Dp(unit.Dp(6))
							gtx.Constraints = layout.Exact(image.Pt(px, px))
							fillRR(gtx, image.Pt(px, px), unit.Dp(3), colAccent)
							return layout.Dimensions{Size: image.Pt(px, px)}
						})
					}),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return closeClick.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
							gtx.Constraints = layout.Exact(image.Pt(gtx.Dp(unit.Dp(18)), gtx.Dp(unit.Dp(18))))
							return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
								gtx.Constraints = layout.Exact(image.Pt(gtx.Dp(unit.Dp(12)), gtx.Dp(unit.Dp(12))))
								return a.ic.Close.Layout(gtx, colFaint)
							})
						})
					}),
				)
			})
			call := macro.Stop()
			bg := colorNRGBA()
			if active {
				bg = colPanel2
			} else if click.Hovered() {
				bg = colHover
			}
			if bg.A > 0 {
				fillRR(gtx, dims.Size, unit.Dp(6), bg)
			}
			call.Add(gtx.Ops)
			return dims
		})
	})
}

func (a *App) layoutRequestTab(gtx layout.Context, t *Tab) layout.Dimensions {
	top := int(float32(gtx.Constraints.Max.Y) * a.split)
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Min.Y = top
			gtx.Constraints.Max.Y = top
			return a.layoutRequest(gtx, t)
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return hline(gtx, colBorder2)
		}),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Min.Y = gtx.Constraints.Max.Y
			return a.layoutResponse(gtx, t)
		}),
	)
}

func (a *App) layoutEnvironmentTab(gtx layout.Context, t *Tab) layout.Dimensions {
	th := a.th
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Top: unit.Dp(16), Bottom: unit.Dp(10), Left: unit.Dp(18), Right: unit.Dp(18)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
					layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
						e := a.entityByID(t.ID)
						name := "Environment"
						if e != nil {
							name = e.Name
						}
						return txt(gtx, th, name, 20, colText, faceUI, font.SemiBold)
					}),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return button(gtx, th, a.click("env-save"), "Save", btnPrimary, func() {
							a.saveTab(t)
							a.notify("Environment saved.")
						})
					}),
					layout.Rigid(gap(6, 0).Layout),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return button(gtx, th, a.click("env-export"), "Export", btnGhost, func() {
							if e := a.entityByID(t.ID); e != nil {
								a.exportEnvironment(e)
							}
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
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Top: unit.Dp(12), Left: unit.Dp(18), Right: unit.Dp(18)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return txt(gtx, th, "Variables are available as {{name}} in URLs, headers, bodies, auth and scripts. Secret values stay local to this device.", 12, colMuted, faceUI, font.Normal)
			})
		}),
		layout.Rigid(gap(0, 10).Layout),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Min.Y = gtx.Constraints.Max.Y
			return layout.Inset{Left: unit.Dp(14), Right: unit.Dp(14), Bottom: unit.Dp(12)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return a.layoutKV(gtx, t.envVars, kvOpts{})
			})
		}),
	)
}

func (a *App) layoutCollectionTab(gtx layout.Context, t *Tab) layout.Dimensions {
	th := a.th
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Top: unit.Dp(16), Bottom: unit.Dp(10), Left: unit.Dp(18), Right: unit.Dp(18)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
					layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
						e := a.entityByID(t.ID)
						name := "Collection"
						if e != nil {
							name = e.Name
						}
						return txt(gtx, th, name, 20, colText, faceUI, font.SemiBold)
					}),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return button(gtx, th, a.click("col-run"), "Run", btnSecondary, func() {
							a.runCollection(t.ID)
						})
					}),
					layout.Rigid(gap(6, 0).Layout),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return button(gtx, th, a.click("col-save"), "Save", btnPrimary, func() {
							a.saveTab(t)
							a.notify("Collection saved.")
						})
					}),
					layout.Rigid(gap(6, 0).Layout),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return button(gtx, th, a.click("col-export"), "Export", btnGhost, func() {
							if e := a.entityByID(t.ID); e != nil {
								a.exportCollection(e)
							}
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
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Top: unit.Dp(12), Left: unit.Dp(18), Right: unit.Dp(18)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return sectionLabel(gtx, th, "COLLECTION VARIABLES")
			})
		}),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Min.Y = gtx.Constraints.Max.Y
			return layout.Inset{Top: unit.Dp(6), Left: unit.Dp(14), Right: unit.Dp(14)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return a.layoutKV(gtx, t.colVars, kvOpts{})
			})
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Top: unit.Dp(8), Left: unit.Dp(18), Right: unit.Dp(18)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return sectionLabel(gtx, th, "PRE-REQUEST SCRIPT")
			})
		}),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Min.Y = gtx.Constraints.Max.Y
			opt := defaultInput()
			opt.mono = true
			opt.bg = colPanel
			return layout.Inset{Top: unit.Dp(4), Left: unit.Dp(18), Right: unit.Dp(18)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return input(gtx, th, &t.colPre, "// runs before every request in this collection", opt)
			})
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Top: unit.Dp(10), Left: unit.Dp(18), Right: unit.Dp(18)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return sectionLabel(gtx, th, "TEST SCRIPT")
			})
		}),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Min.Y = gtx.Constraints.Max.Y
			opt := defaultInput()
			opt.mono = true
			opt.bg = colPanel
			return layout.Inset{Top: unit.Dp(4), Left: unit.Dp(18), Right: unit.Dp(18), Bottom: unit.Dp(14)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return input(gtx, th, &t.colTest, "// runs after every request in this collection", opt)
			})
		}),
	)
}

func (a *App) layoutEmpty(gtx layout.Context) layout.Dimensions {
	th := a.th
	return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical, Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return a.brandMark(gtx, 44)
			}),
			layout.Rigid(gap(0, 16).Layout),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return txt(gtx, th, "Ready when you are", 22, colText, faceUI, font.SemiBold)
			}),
			layout.Rigid(gap(0, 6).Layout),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return txt(gtx, th, brand.Tagline+". Open a request from the sidebar, or start a new one.", 13, colMuted, faceUI, font.Normal)
			}),
			layout.Rigid(gap(0, 20).Layout),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Horizontal}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return button(gtx, th, a.click("empty-new"), "+ New request", btnPrimary, func() {
							a.newEntity(model.KindRequest, "")
						})
					}),
					layout.Rigid(gap(8, 0).Layout),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return button(gtx, th, a.click("empty-import"), "Import Postman", btnSecondary, func() {
							a.importPostman()
						})
					}),
					layout.Rigid(gap(8, 0).Layout),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return button(gtx, th, a.click("empty-history"), "History", btnGhost, func() {
							a.openHistory()
						})
					}),
				)
			}),
		)
	})
}

/* ------------------------------------------------------------------ */
/* Status bar                                                         */
/* ------------------------------------------------------------------ */

func (a *App) layoutStatus(gtx layout.Context) layout.Dimensions {
	th := a.th
	return barRow(gtx, statusH, colPanel, layout.Inset{Left: unit.Dp(12), Right: unit.Dp(12)}, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return txt(gtx, th, a.wsName(), 10, colFaint, faceUI, font.Normal)
			}),
			layout.Rigid(gap(10, 0).Layout),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return txt(gtx, th, brand.Name+" "+brand.Version, 10, colFaint, faceMono, font.Normal)
			}),
			layout.Flexed(1, flexibleSpace),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				if !a.vaultOn {
					return txt(gtx, th, "no vault", 10, colFaint, faceUI, font.Normal)
				}
				if a.locked {
					return txt(gtx, th, "vault locked", 10, colYellow, faceUI, font.Normal)
				}
				return txt(gtx, th, "vault on", 10, colGreen, faceUI, font.Normal)
			}),
			layout.Rigid(gap(12, 0).Layout),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return txt(gtx, th, itoa(len(a.entities))+" items", 10, colFaint, faceUI, font.Normal)
			}),
		)
	})
}

/* ------------------------------------------------------------------ */
/* Overlays                                                           */
/* ------------------------------------------------------------------ */

func (a *App) layoutLoading(gtx layout.Context) layout.Dimensions {
	fillRect(gtx, gtx.Constraints.Max, colBg)
	return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical, Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return a.brandMark(gtx, 40) }),
			layout.Rigid(gap(0, 14).Layout),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return txt(gtx, a.th, "Loading "+brand.Name+"…", 14, colMuted, faceUI, font.Normal)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				if a.bootErr == "" {
					return layout.Dimensions{}
				}
				return layout.Inset{Top: unit.Dp(12)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return txt(gtx, a.th, a.bootErr, 12, colRed, faceUI, font.Normal)
				})
			}),
		)
	})
}

func (a *App) layoutLock(gtx layout.Context) layout.Dimensions {
	th := a.th
	fillRect(gtx, gtx.Constraints.Max, colBg)
	a.lockPass.SingleLine = true
	a.lockPass.Mask = '•'
	if !a.lockFocused {
		a.lockFocused = true
		gtx.Execute(key.FocusCmd{Tag: &a.lockPass})
	}
	return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints.Max.X = gtx.Dp(unit.Dp(420))
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		return panel(gtx, colPanel, unit.Dp(16), func(gtx layout.Context) layout.Dimensions {
			return layout.UniformInset(unit.Dp(26)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Vertical, Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions { return a.brandMark(gtx, 40) }),
					layout.Rigid(gap(0, 14).Layout),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return txt(gtx, th, "Unlock "+brand.Name, 20, colText, faceUI, font.SemiBold)
					}),
					layout.Rigid(gap(0, 4).Layout),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return txt(gtx, th, "The vault is locked. Your data stays encrypted until you enter the passphrase.", 12, colMuted, faceUI, font.Normal)
					}),
					layout.Rigid(gap(0, 16).Layout),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return input(gtx, th, &a.lockPass, "Passphrase", defaultInput())
					}),
					layout.Rigid(gap(0, 10).Layout),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						if a.lockErr == "" {
							return layout.Dimensions{}
						}
						return txt(gtx, th, a.lockErr, 12, colRed, faceUI, font.Normal)
					}),
					layout.Rigid(gap(0, 14).Layout),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return button(gtx, th, a.click("unlock"), "Unlock", btnPrimary, func() {
							if err := a.svc.VaultUnlock(a.lockPass.Text()); err != nil {
								a.lockErr = "Incorrect passphrase."
								return
							}
							a.lockErr = ""
							a.locked = false
							a.lockPass.SetText("")
							a.load()
						})
					}),
				)
			})
		})
	})
}

func (a *App) layoutToast(gtx layout.Context) layout.Dimensions {
	if a.toast == "" {
		return layout.Dimensions{}
	}
	if time.Since(a.toastAt) > 5*time.Second {
		a.toast = ""
		return layout.Dimensions{}
	}
	return layout.Inset{Bottom: unit.Dp(38), Right: unit.Dp(18)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.SE.Layout(gtx, a.layoutToastMessage)
	})
}

func (a *App) layoutToastMessage(gtx layout.Context) layout.Dimensions {
	// Notifications size to their message, never to the window's minimum
	// constraints. Long messages wrap without covering the workspace.
	gtx.Constraints.Min = image.Point{}
	if maxWidth := gtx.Dp(unit.Dp(420)); gtx.Constraints.Max.X > maxWidth {
		gtx.Constraints.Max.X = maxWidth
	}
	return panel(gtx, colPanel3, unit.Dp(8), func(gtx layout.Context) layout.Dimensions {
		return layout.Inset{Top: unit.Dp(9), Bottom: unit.Dp(9), Left: unit.Dp(13), Right: unit.Dp(13)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return txt(gtx, a.th, a.toast, 12, colText, faceUI, font.Normal)
		})
	})
}

/* ------------------------------------------------------------------ */
/* Keyboard                                                           */
/* ------------------------------------------------------------------ */

func (a *App) handleKeys(gtx layout.Context) {
	if a.locked {
		for {
			e, ok := gtx.Event(key.Filter{Name: key.NameEnter, Focus: &a.lockPass})
			if !ok {
				break
			}
			if _, ok := e.(key.Event); ok {
				if err := a.svc.VaultUnlock(a.lockPass.Text()); err != nil {
					a.lockErr = "Incorrect passphrase."
				} else {
					a.locked = false
					a.lockErr = ""
					a.lockPass.SetText("")
					a.load()
				}
			}
		}
		return
	}
	for {
		ev, ok := gtx.Event(pointer.Filter{Target: &a.activity, Kinds: pointer.Press | pointer.Move | pointer.Scroll | pointer.Drag})
		if !ok {
			break
		}
		_ = ev
		a.lastActive = time.Now()
	}
	filters := []eventFilter{
		{key.Filter{Name: "S", Required: key.ModShortcut}, func() { a.saveActive() }},
		{key.Filter{Name: "N", Required: key.ModShortcut}, func() { a.newEntity(model.KindRequest, "") }},
		{key.Filter{Name: "K", Required: key.ModShortcut}, func() {
			gtx.Execute(key.FocusCmd{Tag: &a.search})
		}},
		{key.Filter{Name: key.NameEnter, Required: key.ModShortcut}, func() { a.sendActive() }},
		{key.Filter{Name: "W", Required: key.ModShortcut}, func() { a.closeTab(a.active) }},
		{key.Filter{Name: key.NameEscape}, func() {
			if a.menu != nil {
				a.menu = nil
			} else if a.dialog != nil {
				a.dialog = nil
			} else if a.historyOpen {
				a.historyOpen = false
			}
		}},
		{key.Filter{Name: key.NameF5}, func() { a.syncNow() }},
	}
	for {
		var fs []event.Filter
		for _, f := range filters {
			fs = append(fs, f.filter)
		}
		e, ok := gtx.Event(fs...)
		if !ok {
			break
		}
		ke, ok := e.(key.Event)
		if !ok || ke.State != key.Press {
			continue
		}
		a.lastActive = time.Now()
		for _, f := range filters {
			if keyMatches(f.filter, ke) && f.action != nil {
				f.action()
				break
			}
		}
	}
}

type eventFilter struct {
	filter key.Filter
	action func()
}

func keyMatches(f key.Filter, e key.Event) bool {
	if f.Name != "" && e.Name != f.Name {
		return false
	}
	if f.Required != 0 && !e.Modifiers.Contain(f.Required) {
		return false
	}
	return true
}

func (a *App) saveActive() {
	t := a.activeTab()
	if t == nil {
		return
	}
	a.saveTab(t)
	a.notify("Saved.")
}

func (a *App) sendActive() {
	if t := a.activeTab(); t != nil && t.Kind == model.KindRequest {
		a.send(t)
	}
}

// checkIdle locks the vault after the configured number of inactive minutes.
func (a *App) checkIdle() {
	if !a.ready || a.locked || !a.vaultOn {
		return
	}
	mins := atoiSafe(a.svc.Store.Setting("vault.autolock"))
	if mins <= 0 {
		return
	}
	if time.Since(a.lastActive) >= time.Duration(mins)*time.Minute {
		a.lockNow()
	}
}

// maybeAutoSync runs a background sync on startup and then on the configured
// interval. It is a no-op when the feature is off or the workspace is local.
func (a *App) maybeAutoSync() {
	if !a.ready || a.locked || a.syncing {
		return
	}
	ws := a.workspace()
	if ws == nil || !ws.Linked() {
		return
	}
	if a.svc.Store.Setting("sync.auto") != "1" {
		return
	}
	mins := atoiSafe(a.svc.Store.Setting("sync.interval"))
	if mins <= 0 {
		mins = 15
	}
	if !a.lastAuto.IsZero() && time.Since(a.lastAuto) < time.Duration(mins)*time.Minute {
		return
	}
	a.lastAuto = time.Now()
	a.syncNow()
}

// colorNRGBA is a tiny helper so pick() can stay generic with colours.
func colorNRGBA() color.NRGBA { return color.NRGBA{} }
