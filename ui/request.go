package ui

import (
	"strings"

	"gioui.org/font"
	"gioui.org/layout"
	"gioui.org/unit"
	"gioui.org/widget"

	"yourpm/core/model"
)

var requestTabs = []string{"Params", "Headers", "Body", "Auth", "Scripts", "Settings"}

var methods = []string{"GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS"}

var bodyModes = []struct {
	label string
	mode  string
}{
	{"none", model.BodyNone},
	{"JSON", model.BodyRaw},
	{"form-data", model.BodyFormData},
	{"x-www-form-urlencoded", model.BodyURLEncoded},
	{"binary", model.BodyBinary},
}

var bodyLangs = []string{"json", "text", "xml", "html", "javascript"}

func (a *App) layoutRequest(gtx layout.Context, t *Tab) layout.Dimensions {
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return a.requestURLBar(gtx, t)
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Top: unit.Dp(2), Bottom: unit.Dp(2), Left: unit.Dp(14), Right: unit.Dp(14)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return hline(gtx, colBorder)
			})
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Top: unit.Dp(6), Left: unit.Dp(14), Right: unit.Dp(14)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return a.requestTabBar(gtx, t)
			})
		}),
		layout.Rigid(gap(0, 8).Layout),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Left: unit.Dp(14), Right: unit.Dp(14), Bottom: unit.Dp(10)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				gtx.Constraints.Min.Y = gtx.Constraints.Max.Y
				return a.requestPane(gtx, t)
			})
		}),
	)
}

func (a *App) requestURLBar(gtx layout.Context, t *Tab) layout.Dimensions {
	th := a.th
	return layout.Inset{Top: unit.Dp(10), Bottom: unit.Dp(8), Left: unit.Dp(14), Right: unit.Dp(14)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				m := strings.ToUpper(t.Req.Method)
				if m == "" {
					m = "GET"
				}
				click := a.click("method:" + t.ID)
				if click.Clicked(gtx) {
					a.openMethodMenu(t)
				}
				return click.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return panel(gtx, colPanel2, unit.Dp(7), func(gtx layout.Context) layout.Dimensions {
						return layout.Inset{Top: unit.Dp(8), Bottom: unit.Dp(8), Left: unit.Dp(10), Right: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
							return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
								layout.Rigid(func(gtx layout.Context) layout.Dimensions {
									return widthOf(gtx, 58, func(gtx layout.Context) layout.Dimensions {
										return txt(gtx, th, m, 13, methodColor(m), faceMono, font.SemiBold)
									})
								}),
								layout.Rigid(func(gtx layout.Context) layout.Dimensions {
									return iconPx(gtx, a.ic.Dropdown, 16, colMuted)
								}),
							)
						})
					})
				})
			}),
			layout.Rigid(gap(8, 0).Layout),
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				opt := defaultInput()
				opt.mono = true
				opt.size = 13
				return input(gtx, th, &t.url, "https://api.example.com/v1/users  or  {{baseUrl}}/v1/users", opt)
			}),
			layout.Rigid(gap(8, 0).Layout),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				label := "Send"
				if t.Sending {
					label = "Sending…"
				}
				kind := btnPrimary
				if t.Sending {
					kind = btnSecondary
				}
				return button(gtx, th, a.click("send:"+t.ID), label, kind, func() {
					a.send(t)
				})
			}),
			layout.Rigid(gap(4, 0).Layout),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return button(gtx, th, a.click("save:"+t.ID), "Save", btnGhost, func() {
					a.saveTab(t)
					a.notify("Saved.")
				})
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return iconBtn(gtx, th, a.click("curl:"+t.ID), a.ic.Code, 16, colMuted, func() {
					a.curlForActive()
				})
			}),
		)
	})
}

func (a *App) openMethodMenu(t *Tab) {
	m := &menu{title: "HTTP method"}
	for _, method := range methods {
		method := method
		m.items = append(m.items, menuItem{
			label: method,
			onClick: func() {
				t.Req.Method = method
				a.pullEditors(t)
				t.Dirty = true
			},
		})
	}
	a.menu = m
}

func (a *App) requestTabBar(gtx layout.Context, t *Tab) layout.Dimensions {
	th := a.th
	var kids []layout.FlexChild
	for i, name := range requestTabs {
		i, name := i, name
		label := name
		switch i {
		case 0:
			if n := activeCount(t.Req.Params); n > 0 {
				label += " " + itoa(n)
			}
		case 1:
			if n := activeCount(t.Req.Headers); n > 0 {
				label += " " + itoa(n)
			}
		}
		kids = append(kids, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			active := t.reqTab == i
			c := colMuted
			if active {
				c = colText
			}
			click := a.click("reqtab:" + t.ID + ":" + itoa(i))
			if click.Clicked(gtx) {
				t.reqTab = i
			}
			return click.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				macro := layout.Inset{Top: unit.Dp(6), Bottom: unit.Dp(6), Left: unit.Dp(10), Right: unit.Dp(10)}
				dims := macro.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return txt(gtx, th, label, 12, c, faceUI, font.Medium)
				})
				_ = dims
				return dims
			})
		}))
	}
	return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx, kids...)
}

func (a *App) requestPane(gtx layout.Context, t *Tab) layout.Dimensions {
	switch t.reqTab {
	case 0:
		return a.layoutKV(gtx, t.params, kvOpts{compact: true})
	case 1:
		return a.layoutKV(gtx, t.header, kvOpts{compact: true})
	case 2:
		return a.layoutBody(gtx, t)
	case 3:
		return a.layoutAuth(gtx, t)
	case 4:
		return a.layoutScripts(gtx, t)
	case 5:
		return a.layoutRequestSettings(gtx, t)
	}
	return layout.Dimensions{}
}

func (a *App) layoutBody(gtx layout.Context, t *Tab) layout.Dimensions {
	th := a.th
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			var kids []layout.FlexChild
			for i, m := range bodyModes {
				i, m := i, m
				kids = append(kids, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					active := t.bodyMode == i
					return layout.Inset{Right: unit.Dp(4)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return button(gtx, th, a.click("body:"+t.ID+":"+itoa(i)), m.label, pick(active, btnSecondary, btnGhost), func() {
							t.bodyMode = i
							t.Req.Body.Mode = m.mode
							t.Dirty = true
						})
					})
				}))
			}
			return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx, kids...)
		}),
		layout.Rigid(gap(0, 8).Layout),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Min.Y = gtx.Constraints.Max.Y
			switch bodyModes[t.bodyMode].mode {
			case model.BodyNone:
				return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return txt(gtx, th, "This request has no body.", 12, colFaint, faceUI, font.Normal)
				})
			case model.BodyRaw:
				return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						var kids []layout.FlexChild
						for i, lang := range bodyLangs {
							i, lang := i, lang
							kids = append(kids, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
								active := t.bodyLang == i
								return layout.Inset{Right: unit.Dp(4), Bottom: unit.Dp(6)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
									return button(gtx, th, a.click("lang:"+t.ID+":"+itoa(i)), lang, pick(active, btnSecondary, btnGhost), func() {
										t.bodyLang = i
										t.Req.Body.Lang = lang
										t.Dirty = true
									})
								})
							}))
						}
						return layout.Flex{Axis: layout.Horizontal}.Layout(gtx, kids...)
					}),
					layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
						gtx.Constraints.Min.Y = gtx.Constraints.Max.Y
						return a.layoutJSONBody(gtx, &t.requestJSON, &t.body, true)
					}),
				)
			case model.BodyFormData:
				return a.layoutKV(gtx, t.form, kvOpts{compact: true})
			case model.BodyURLEncoded:
				return a.layoutKV(gtx, t.urlenc, kvOpts{compact: true})
			case model.BodyBinary:
				return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return txt(gtx, th, "File path", 11, colFaint, faceUI, font.Medium)
					}),
					layout.Rigid(gap(0, 4).Layout),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						opt := defaultInput()
						opt.mono = true
						return input(gtx, th, &t.binaryPath, "/path/to/payload.bin", opt)
					}),
				)
			}
			return layout.Dimensions{}
		}),
	)
}

var authTypes = []struct {
	label string
	kind  string
}{
	{"Inherit", model.AuthInherit},
	{"None", model.AuthNone},
	{"Bearer", model.AuthBearer},
	{"Basic", model.AuthBasic},
	{"API key", model.AuthAPIKey},
	{"OAuth 2.0", model.AuthOAuth2},
}

func (a *App) layoutAuth(gtx layout.Context, t *Tab) layout.Dimensions {
	th := a.th
	chips := func(gtx layout.Context) layout.Dimensions {
		var kids []layout.FlexChild
		for i, at := range authTypes {
			i, at := i, at
			kids = append(kids, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				active := t.authType == i
				return layout.Inset{Right: unit.Dp(4)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return button(gtx, th, a.click("auth:"+t.ID+":"+itoa(i)), at.label, pick(active, btnSecondary, btnGhost), func() {
						t.authType = i
						t.Req.Auth.Type = at.kind
						if at.kind == model.AuthOAuth2 && t.Req.Auth.OAuth2 == nil {
							t.Req.Auth.OAuth2 = &model.OAuth2{GrantType: model.GrantClientCredentials}
						}
						t.Dirty = true
					})
				})
			}))
		}
		return layout.Flex{Axis: layout.Horizontal}.Layout(gtx, kids...)
	}

	field := func(gtx layout.Context, label, hint string, ed *widget.Editor) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return txt(gtx, th, label, 11, colFaint, faceUI, font.Medium)
			}),
			layout.Rigid(gap(0, 3).Layout),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return input(gtx, th, ed, hint, defaultInput())
			}),
			layout.Rigid(gap(0, 8).Layout),
		)
	}

	var fields []layout.Widget
	switch authTypes[t.authType].kind {
	case model.AuthBearer:
		fields = append(fields, func(gtx layout.Context) layout.Dimensions {
			return field(gtx, "Token", "{{token}}", &t.authTok)
		})
	case model.AuthBasic:
		fields = append(fields,
			func(gtx layout.Context) layout.Dimensions { return field(gtx, "Username", "username", &t.authUser) },
			func(gtx layout.Context) layout.Dimensions { return field(gtx, "Password", "password", &t.authPass) },
		)
	case model.AuthAPIKey:
		fields = append(fields,
			func(gtx layout.Context) layout.Dimensions { return field(gtx, "Key", "X-API-Key", &t.authKey) },
			func(gtx layout.Context) layout.Dimensions { return field(gtx, "Value", "{{apiKey}}", &t.authVal) },
			func(gtx layout.Context) layout.Dimensions {
				in := t.Req.Auth.In
				if in == "" {
					in = "header"
				}
				return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return txt(gtx, th, "Add to", 11, colFaint, faceUI, font.Medium)
					}),
					layout.Rigid(gap(8, 0).Layout),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return button(gtx, th, a.click("auth-in-header:"+t.ID), "Header", pick(in == "header", btnSecondary, btnGhost), func() {
							t.Req.Auth.In = "header"
							t.Dirty = true
						})
					}),
					layout.Rigid(gap(4, 0).Layout),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return button(gtx, th, a.click("auth-in-query:"+t.ID), "Query", pick(in == "query", btnSecondary, btnGhost), func() {
							t.Req.Auth.In = "query"
							t.Dirty = true
						})
					}),
				)
			},
		)
	case model.AuthOAuth2:
		if t.Req.Auth.OAuth2 == nil {
			t.Req.Auth.OAuth2 = &model.OAuth2{GrantType: model.GrantClientCredentials}
		}
		o := t.Req.Auth.OAuth2
		grant := o.GrantType
		if grant == "" {
			grant = model.GrantClientCredentials
		}
		grantChip := func(gtx layout.Context, label, kind string) layout.Dimensions {
			return button(gtx, th, a.click("oauth-grant:"+t.ID+":"+kind), label,
				pick(grant == kind, btnSecondary, btnGhost), func() {
					o.GrantType = kind
					t.oauthNote = ""
					t.oauthErr = false
					t.Dirty = true
				})
		}
		fields = append(fields,
			func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return txt(gtx, th, "Grant", 11, colFaint, faceUI, font.Medium)
					}),
					layout.Rigid(gap(8, 0).Layout),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return grantChip(gtx, "Client credentials", model.GrantClientCredentials)
					}),
					layout.Rigid(gap(4, 0).Layout),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return grantChip(gtx, "Password", model.GrantPassword)
					}),
					layout.Rigid(gap(4, 0).Layout),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return grantChip(gtx, "Authorization code", model.GrantAuthorizationCode)
					}),
				)
			},
		)
		if grant == model.GrantAuthorizationCode {
			fields = append(fields, func(gtx layout.Context) layout.Dimensions {
				return field(gtx, "Authorization URL", "https://auth.example.com/oauth/authorize", &t.oauthAuth)
			})
		}
		fields = append(fields,
			func(gtx layout.Context) layout.Dimensions {
				return field(gtx, "Access token URL", "https://auth.example.com/oauth/token", &t.oauthURL)
			},
			func(gtx layout.Context) layout.Dimensions { return field(gtx, "Client ID", "client id", &t.oauthID) },
			func(gtx layout.Context) layout.Dimensions {
				return field(gtx, "Client secret", "client secret", &t.oauthSec)
			},
			func(gtx layout.Context) layout.Dimensions {
				in := o.ClientAuth
				if in == "" {
					in = "header"
				}
				return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return txt(gtx, th, "Client credentials in", 11, colFaint, faceUI, font.Medium)
					}),
					layout.Rigid(gap(8, 0).Layout),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return button(gtx, th, a.click("oauth-client-auth:"+t.ID+":header"), "Header", pick(in == "header", btnSecondary, btnGhost), func() {
							o.ClientAuth = "header"
							t.Dirty = true
						})
					}),
					layout.Rigid(gap(4, 0).Layout),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return button(gtx, th, a.click("oauth-client-auth:"+t.ID+":body"), "Body", pick(in == "body", btnSecondary, btnGhost), func() {
							o.ClientAuth = "body"
							t.Dirty = true
						})
					}),
				)
			},
			func(gtx layout.Context) layout.Dimensions { return field(gtx, "Scope", "read write", &t.oauthSc) },
		)
		if grant == model.GrantPassword {
			fields = append(fields,
				func(gtx layout.Context) layout.Dimensions {
					return field(gtx, "Username", "resource owner username", &t.oauthUser)
				},
				func(gtx layout.Context) layout.Dimensions {
					return field(gtx, "Password", "resource owner password", &t.oauthPass)
				},
			)
		}
		if grant == model.GrantAuthorizationCode {
			fields = append(fields,
				func(gtx layout.Context) layout.Dimensions {
					return field(gtx, "Redirect URL (optional)", "http://127.0.0.1:8787/callback — empty picks a free port", &t.oauthRedirect)
				},
				func(gtx layout.Context) layout.Dimensions {
					label := "Authorize"
					if t.oauthBusy {
						label = "Waiting for browser…"
					}
					var onClick func()
					if !t.oauthBusy {
						onClick = func() { a.authorizeOAuth(t) }
					}
					note := "Opens your browser; the token is stored with the vault."
					c := colMuted
					if t.oauthNote != "" {
						note = t.oauthNote
					}
					if t.oauthErr {
						c = colRed
					}
					return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return button(gtx, th, a.click("oauth-authorize:"+t.ID), label, btnPrimary, onClick)
						}),
						layout.Rigid(gap(0, 6).Layout),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return txt(gtx, th, note, 12, c, faceUI, font.Normal)
						}),
					)
				},
			)
		}
	case model.AuthInherit:
		fields = append(fields, func(gtx layout.Context) layout.Dimensions {
			return txt(gtx, th, "Auth is inherited from the enclosing collection.", 12, colMuted, faceUI, font.Normal)
		})
	case model.AuthNone:
		fields = append(fields, func(gtx layout.Context) layout.Dimensions {
			return txt(gtx, th, "No credentials are attached.", 12, colMuted, faceUI, font.Normal)
		})
	}

	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(chips),
		layout.Rigid(gap(0, 14).Layout),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return a.dialogFieldStack(gtx, &dialog{}, fields)
		}),
	)
}

func (a *App) layoutScripts(gtx layout.Context, t *Tab) layout.Dimensions {
	th := a.th
	editor := func(gtx layout.Context, label, hint string, ed *widget.Editor) layout.Dimensions {
		opt := defaultInput()
		opt.mono = true
		opt.bg = colPanel
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return txt(gtx, th, label, 11, colFaint, faceUI, font.Medium)
			}),
			layout.Rigid(gap(0, 4).Layout),
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				gtx.Constraints.Min.Y = gtx.Constraints.Max.Y
				return input(gtx, th, ed, hint, opt)
			}),
		)
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Min.Y = gtx.Constraints.Max.Y / 2
			return editor(gtx, "Pre-request script", "// runs before the request", &t.pre)
		}),
		layout.Rigid(gap(0, 10).Layout),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Min.Y = gtx.Constraints.Max.Y / 2
			return editor(gtx, "Tests", "// pm.test(\"ok\", () => pm.response.to.have.status(200))", &t.tests)
		}),
	)
}

// layoutRequestSettings exposes the transport controls that the HTTP engine
// already supports, so the desktop app matches the CLI and the stored model.
func (a *App) layoutRequestSettings(gtx layout.Context, t *Tab) layout.Dimensions {
	th := a.th
	field := func(label, hint string, ed *widget.Editor) layout.Widget {
		return func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return txt(gtx, th, label, 11, colFaint, faceUI, font.Medium)
				}),
				layout.Rigid(gap(0, 3).Layout),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return input(gtx, th, ed, hint, defaultInput())
				}),
			)
		}
	}
	toggle := func(label, key string, on *bool) layout.Widget {
		return func(gtx layout.Context) layout.Dimensions {
			return button(gtx, th, a.click(key), label, pick(*on, btnSecondary, btnGhost), func() {
				*on = !*on
				t.Dirty = true
			})
		}
	}
	s := &t.Req.Settings
	widgets := []layout.Widget{
		func(gtx layout.Context) layout.Dimensions {
			return txt(gtx, th, "These options apply only to this request. They override the defaults for this send.", 12, colMuted, faceUI, font.Normal)
		},
		field("Timeout (ms)", "30000 — 0 uses the engine default", &t.timeout),
		field("Maximum redirects", "10 — 0 uses the engine default", &t.maxRedir),
		field("Proxy URL", "http://127.0.0.1:8080 — empty uses the system proxy", &t.proxy),
		func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(toggle("Skip TLS verification", "req-skip-tls:"+t.ID, &s.SkipTLSVerify)),
				layout.Rigid(gap(6, 0).Layout),
				layout.Rigid(toggle("Disable redirects", "req-no-redirect:"+t.ID, &s.DisableRedirects)),
				layout.Rigid(gap(6, 0).Layout),
				layout.Rigid(toggle("Disable cookies", "req-no-cookies:"+t.ID, &s.DisableCookies)),
			)
		},
		func(gtx layout.Context) layout.Dimensions {
			if !s.SkipTLSVerify {
				return layout.Dimensions{}
			}
			return panel(gtx, alpha(colYellow, 0x18), unit.Dp(6), func(gtx layout.Context) layout.Dimensions {
				return layout.UniformInset(unit.Dp(8)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return txt(gtx, th, "TLS verification is off for this request. Only use this for hosts you trust.", 12, colYellow, faceUI, font.Normal)
				})
			})
		},
	}
	return layout.Inset{Top: unit.Dp(4)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return a.dialogFieldStack(gtx, &dialog{}, widgets)
	})
}

func activeCount(kvs []model.KV) int {
	n := 0
	for _, kv := range kvs {
		if kv.Key != "" && !kv.Disabled {
			n++
		}
	}
	return n
}
