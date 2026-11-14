package ui

import (
	"encoding/base64"
	"fmt"
	"io"
	"strings"

	"gioui.org/font"
	"gioui.org/io/clipboard"
	"gioui.org/layout"
	"gioui.org/unit"

	"yourpm/core/model"
)

var responseTabs = []string{"Body", "Headers", "Cookies", "Tests", "Console", "Timing"}

func (a *App) layoutResponse(gtx layout.Context, t *Tab) layout.Dimensions {
	th := a.th
	resp := t.Resp
	if resp == nil {
		return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Vertical, Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return txt(gtx, th, "No response yet", 14, colMuted, faceUI, font.Medium)
				}),
				layout.Rigid(gap(0, 6).Layout),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return txt(gtx, th, "Send the request to see status, timing and the body.", 12, colFaint, faceUI, font.Normal)
				}),
			)
		})
	}

	status := fmt.Sprintf("%d %s", resp.Status, resp.StatusText)
	if resp.Error != "" && resp.Status == 0 {
		status = "Request failed"
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Top: unit.Dp(8), Bottom: unit.Dp(6), Left: unit.Dp(14), Right: unit.Dp(14)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return badge(gtx, th, status, statusColor(resp.Status))
					}),
					layout.Rigid(gap(12, 0).Layout),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return txt(gtx, th, fmt.Sprintf("%d ms", resp.TimeMs), 11, colMuted, faceMono, font.Normal)
					}),
					layout.Rigid(gap(12, 0).Layout),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return txt(gtx, th, humanBytes(resp.Size), 11, colMuted, faceMono, font.Normal)
					}),
					layout.Rigid(gap(12, 0).Layout),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						if !resp.Truncated {
							return layout.Dimensions{}
						}
						return badge(gtx, th, "truncated", colYellow)
					}),
					layout.Flexed(1, func(gtx layout.Context) layout.Dimensions { return layout.Dimensions{} }),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return button(gtx, th, a.click("resp-copy:"+t.ID), "Copy", btnGhost, func() {
							text := resp.Body
							if resp.Binary {
								text = resp.BodyBase64
							}
							gtx.Execute(clipboard.WriteCmd{Type: "application/text", Data: io.NopCloser(strings.NewReader(text))})
							a.notify("Response copied.")
						})
					}),
					layout.Rigid(gap(4, 0).Layout),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return button(gtx, th, a.click("resp-save:"+t.ID), "Save", btnGhost, func() {
							a.saveResponse(resp)
						})
					}),
					layout.Rigid(gap(8, 0).Layout),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return a.responseTabBar(gtx, t)
					}),
				)
			})
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Left: unit.Dp(14), Right: unit.Dp(14)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return hline(gtx, colBorder)
			})
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			if resp.Error == "" {
				return layout.Dimensions{}
			}
			return layout.Inset{Top: unit.Dp(8), Left: unit.Dp(14), Right: unit.Dp(14)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return panel(gtx, alpha(colRed, 0x18), unit.Dp(6), func(gtx layout.Context) layout.Dimensions {
					return layout.UniformInset(unit.Dp(8)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return txt(gtx, th, resp.Error, 12, colRed, faceMono, font.Normal)
					})
				})
			})
		}),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Min.Y = gtx.Constraints.Max.Y
			return layout.Inset{Top: unit.Dp(8), Left: unit.Dp(14), Right: unit.Dp(14), Bottom: unit.Dp(10)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				gtx.Constraints.Min.Y = gtx.Constraints.Max.Y
				switch t.respTab {
				case 0:
					return a.layoutJSONBody(gtx, &t.responseJSON, &t.respBody, false)
				case 1:
					return a.responseMetaList(gtx, t, resp.Headers, "No headers.")
				case 2:
					return a.responseMetaList(gtx, t, resp.Cookies, "No cookies.")
				case 3:
					return a.responseTests(gtx, t)
				case 4:
					return a.responseConsole(gtx, t)
				case 5:
					return a.responseTiming(gtx, t)
				}
				return layout.Dimensions{}
			})
		}),
	)
}

func (a *App) responseTabBar(gtx layout.Context, t *Tab) layout.Dimensions {
	th := a.th
	var kids []layout.FlexChild
	for i, name := range responseTabs {
		i, name := i, name
		kids = append(kids, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			active := t.respTab == i
			c := colFaint
			if active {
				c = colText
			}
			click := a.click("resptab:" + t.ID + ":" + itoa(i))
			if click.Clicked(gtx) {
				t.respTab = i
			}
			return click.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Top: unit.Dp(5), Bottom: unit.Dp(5), Left: unit.Dp(7), Right: unit.Dp(7)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return txt(gtx, th, name, 11, c, faceUI, font.Medium)
				})
			})
		}))
	}
	return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx, kids...)
}

func (a *App) responseMetaList(gtx layout.Context, t *Tab, kvs []model.KV, empty string) layout.Dimensions {
	th := a.th
	if len(kvs) == 0 {
		return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return txt(gtx, th, empty, 12, colFaint, faceUI, font.Normal)
		})
	}
	return t.metaScroll.Layout(gtx, len(kvs), func(gtx layout.Context, i int) layout.Dimensions {
		if i >= len(kvs) {
			return layout.Dimensions{}
		}
		kv := kvs[i]
		return layout.Inset{Top: unit.Dp(2), Bottom: unit.Dp(2)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Horizontal}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return widthOf(gtx, 210, func(gtx layout.Context) layout.Dimensions {
						return txtClip(gtx, th, kv.Key, 12, colMuted, faceMono, font.Normal)
					})
				}),
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					return txt(gtx, th, kv.Value, 12, colText, faceMono, font.Normal)
				}),
			)
		})
	})
}

func (a *App) responseTests(gtx layout.Context, t *Tab) layout.Dimensions {
	th := a.th
	resp := t.Resp
	if len(resp.Tests) == 0 {
		return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return txt(gtx, th, "No tests ran. Add a test script to this request.", 12, colFaint, faceUI, font.Normal)
		})
	}
	return t.metaScroll.Layout(gtx, len(resp.Tests), func(gtx layout.Context, i int) layout.Dimensions {
		if i >= len(resp.Tests) {
			return layout.Dimensions{}
		}
		result := resp.Tests[i]
		return layout.Inset{Top: unit.Dp(3), Bottom: unit.Dp(3)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					c := colGreen
					ic := a.ic.Check
					if !result.Passed {
						c = colRed
						ic = a.ic.Error
					}
					return iconPx(gtx, ic, 14, c)
				}),
				layout.Rigid(gap(8, 0).Layout),
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					text := result.Name
					if result.Error != "" {
						text += " — " + result.Error
					}
					return txt(gtx, th, text, 12, colText, faceUI, font.Normal)
				}),
			)
		})
	})
}

func (a *App) responseConsole(gtx layout.Context, t *Tab) layout.Dimensions {
	th := a.th
	resp := t.Resp
	if len(resp.Console) == 0 {
		return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return txt(gtx, th, "The script console is empty.", 12, colFaint, faceUI, font.Normal)
		})
	}
	return t.metaScroll.Layout(gtx, len(resp.Console), func(gtx layout.Context, i int) layout.Dimensions {
		if i >= len(resp.Console) {
			return layout.Dimensions{}
		}
		return layout.Inset{Top: unit.Dp(1), Bottom: unit.Dp(1)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return txt(gtx, th, resp.Console[i], 12, colMuted, faceMono, font.Normal)
		})
	})
}

func (a *App) responseTiming(gtx layout.Context, t *Tab) layout.Dimensions {
	th := a.th
	resp := t.Resp
	rows := []struct {
		label string
		value int64
	}{
		{"DNS", resp.Timings.DNS},
		{"Connect", resp.Timings.Connect},
		{"TLS", resp.Timings.TLS},
		{"TTFB", resp.Timings.TTFB},
		{"Redirects", resp.Timings.Redirects},
		{"Total", resp.Timings.Total},
	}
	var kids []layout.FlexChild
	for _, row := range rows {
		row := row
		kids = append(kids, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Top: unit.Dp(3), Bottom: unit.Dp(3)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Horizontal}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return widthOf(gtx, 120, func(gtx layout.Context) layout.Dimensions {
							return txt(gtx, th, row.label, 12, colFaint, faceUI, font.Normal)
						})
					}),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return txt(gtx, th, fmt.Sprintf("%d ms", row.value), 12, colText, faceMono, font.Normal)
					}),
				)
			})
		}))
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx, kids...)
}

// responseBytes returns the full body, decoding binary payloads that are kept
// base64-encoded in the model.
func responseBytes(resp *model.Response) []byte {
	if resp == nil {
		return nil
	}
	if resp.Binary && resp.BodyBase64 != "" {
		if raw, err := base64.StdEncoding.DecodeString(resp.BodyBase64); err == nil {
			return raw
		}
	}
	return []byte(resp.Body)
}

func responseFileName(resp *model.Response) string {
	ct := strings.ToLower(headerValue(resp.Headers, "content-type"))
	switch {
	case strings.Contains(ct, "json"):
		return "response.json"
	case strings.Contains(ct, "html"):
		return "response.html"
	case strings.Contains(ct, "xml"):
		return "response.xml"
	case strings.Contains(ct, "javascript"):
		return "response.js"
	case strings.Contains(ct, "css"):
		return "response.css"
	case strings.Contains(ct, "image/png"):
		return "response.png"
	case strings.Contains(ct, "image/jpeg"):
		return "response.jpg"
	case strings.Contains(ct, "pdf"):
		return "response.pdf"
	case resp.Binary:
		return "response.bin"
	}
	return "response.txt"
}

func (a *App) saveResponse(resp *model.Response) {
	if a.ex == nil || resp == nil {
		return
	}
	raw := responseBytes(resp)
	name := responseFileName(resp)
	go func() {
		w, err := a.ex.CreateFile(name)
		if err != nil {
			a.Post(func() { a.notify("Save cancelled.") })
			return
		}
		_, werr := w.Write(raw)
		cerr := w.Close()
		a.Post(func() {
			if werr != nil || cerr != nil {
				a.notify("Could not write the response file.")
				return
			}
			a.notify("Response saved as " + name + ".")
		})
	}()
}
