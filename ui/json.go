package ui

import (
	"bytes"
	"encoding/json"
	"image"
	"strings"

	"gioui.org/font"
	"gioui.org/layout"
	"gioui.org/unit"
	"gioui.org/widget"
)

type jsonLine struct {
	text string
	end  int // matching closing line, or -1 for a non-foldable line
}

type jsonView struct {
	source      string
	initialized bool
	valid       bool
	tree        bool
	lines       []jsonLine
	collapsed   map[int]bool
	clicks      map[int]*widget.Clickable
	list        layout.List
	toggle      widget.Clickable
	expand      widget.Clickable
	collapse    widget.Clickable
}

func (v *jsonView) update(source string, editable bool) {
	if v.initialized && source == v.source {
		return
	}
	if !v.initialized {
		v.tree = !editable
	}
	v.initialized = true
	v.source = source
	v.lines = nil
	v.collapsed = map[int]bool{}
	v.clicks = map[int]*widget.Clickable{}
	v.list = layout.List{Axis: layout.Vertical}
	var pretty bytes.Buffer
	v.valid = json.Indent(&pretty, []byte(source), "", "  ") == nil
	if !v.valid {
		return
	}
	var stack []int
	for i, text := range strings.Split(pretty.String(), "\n") {
		v.lines = append(v.lines, jsonLine{text: text, end: -1})
		trimmed := strings.TrimSpace(text)
		if strings.HasSuffix(trimmed, "{") || strings.HasSuffix(trimmed, "[") {
			stack = append(stack, i)
		} else if len(stack) > 0 && (strings.HasPrefix(trimmed, "}") || strings.HasPrefix(trimmed, "]")) {
			start := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			v.lines[start].end = i
		}
	}
}

func (v *jsonView) visibleLines() []int {
	var out []int
	for i := 0; i < len(v.lines); i++ {
		out = append(out, i)
		if v.collapsed[i] && v.lines[i].end > i {
			i = v.lines[i].end
		}
	}
	return out
}

func (v *jsonView) lineText(i int) string {
	line := v.lines[i]
	if !v.collapsed[i] || line.end < 0 {
		return line.text
	}
	return line.text + " … " + strings.TrimSpace(v.lines[line.end].text)
}

// Folding is a presentation of the editor's text, never a mutation of it.
func (a *App) layoutJSONBody(gtx layout.Context, v *jsonView, ed *widget.Editor, editable bool) layout.Dimensions {
	v.update(ed.Text(), editable)
	raw := func(gtx layout.Context) layout.Dimensions {
		opt := defaultInput()
		opt.mono = true
		opt.bg = colPanel
		return input(gtx, a.th, ed, "", opt)
	}
	if !v.valid {
		return raw(gtx)
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Bottom: unit.Dp(6)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						label := "JSON tree"
						if v.tree {
							label = pick(editable, "Edit JSON", "Raw")
						}
						return button(gtx, a.th, &v.toggle, label, btnSecondary, func() { v.tree = !v.tree })
					}),
					layout.Rigid(gap(6, 0).Layout),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						if !v.tree {
							return layout.Dimensions{}
						}
						return button(gtx, a.th, &v.expand, "Expand all", btnGhost, func() {
							v.collapsed = map[int]bool{}
						})
					}),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						if !v.tree {
							return layout.Dimensions{}
						}
						return button(gtx, a.th, &v.collapse, "Collapse all", btnGhost, func() {
							for i, line := range v.lines {
								if line.end >= 0 {
									v.collapsed[i] = true
								}
							}
						})
					}),
				)
			})
		}),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			if !v.tree {
				return raw(gtx)
			}
			return bordered(gtx, colPanel, colBorder, 6, func(gtx layout.Context) layout.Dimensions {
				return layout.UniformInset(unit.Dp(6)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					visible := v.visibleLines()
					return v.list.Layout(gtx, len(visible), func(gtx layout.Context, index int) layout.Dimensions {
						i := visible[index]
						line := v.lines[i]
						return heightOf(gtx, 30, func(gtx layout.Context) layout.Dimensions {
							return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
								layout.Rigid(func(gtx layout.Context) layout.Dimensions {
									if line.end < 0 {
										return layout.Dimensions{Size: image.Pt(gtx.Dp(30), gtx.Dp(30))}
									}
									click := v.clicks[i]
									if click == nil {
										click = new(widget.Clickable)
										v.clicks[i] = click
									}
									ic := a.ic.Expand
									if v.collapsed[i] {
										ic = a.ic.Chevron
									}
									return iconBtn(gtx, a.th, click, ic, 14, colMuted, func() {
										v.collapsed[i] = !v.collapsed[i]
									})
								}),
								layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
									return layout.W.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
										return txtClip(gtx, a.th, v.lineText(i), 13, colText, faceMono, font.Normal)
									})
								}),
							)
						})
					})
				})
			})
		}),
	)
}
