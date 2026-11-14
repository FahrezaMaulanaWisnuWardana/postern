package ui

import (
	"image/color"
	"io"
	"os"
	"path/filepath"

	"gioui.org/font"
	"gioui.org/layout"
	"gioui.org/unit"
	"gioui.org/widget"

	"yourpm/core/model"
)

// kvEditor is an editable key/value table used for headers, query params,
// form fields and variables. Rows can be enabled or disabled, which maps to
// Postman's checkbox semantics: disabled rows stay in the document but are
// skipped when a request is built.
type kvEditor struct {
	rows []*kvRow
	list layout.List
	add  widget.Clickable

	keyHint string
	valHint string
	// secrets lets environment editors flag a row as a secret.
	secrets bool
	// files lets form-data rows switch between text and file fields.
	files bool
}

type kvRow struct {
	key      widget.Editor
	value    widget.Editor
	tog      toggle
	del      widget.Clickable
	secret   widget.Clickable
	fileBtn  widget.Clickable
	browse   widget.Clickable
	typ      string
	isSecret bool
}

func newKV(list []model.KV) *kvEditor {
	k := &kvEditor{keyHint: "Key", valHint: "Value", list: layout.List{Axis: layout.Vertical}}
	for _, kv := range list {
		r := k.newRow()
		r.key.SetText(kv.Key)
		r.typ = kv.Type
		r.isSecret = kv.Secret
		if kv.Type == "file" {
			r.value.SetText(kv.Path)
		} else {
			r.value.SetText(kv.Value)
		}
		r.tog.On = !kv.Disabled
		k.rows = append(k.rows, r)
	}
	k.ensureBlank()
	return k
}

// newVarEditor builds a variable table with secret toggles.
func newVarEditor(list []model.KV) *kvEditor {
	k := newKV(list)
	k.secrets = true
	k.keyHint = "Variable"
	k.valHint = "Initial value"
	return k
}

// newFormEditor builds a multipart form table.
func newFormEditor(list []model.KV) *kvEditor {
	k := newKV(list)
	k.files = true
	k.keyHint = "Field"
	k.valHint = "Value"
	return k
}

func (k *kvEditor) newRow() *kvRow {
	r := &kvRow{tog: toggle{On: true}, typ: "text"}
	r.key.SingleLine = true
	r.value.SingleLine = true
	return r
}

func (k *kvEditor) ensureBlank() {
	if len(k.rows) == 0 {
		k.rows = append(k.rows, k.newRow())
		return
	}
	last := k.rows[len(k.rows)-1]
	if last.key.Text() != "" || last.value.Text() != "" {
		k.rows = append(k.rows, k.newRow())
	}
}

// Model returns the current rows in model form. The trailing blank row that
// the editor maintains is not emitted.
func (k *kvEditor) Model() []model.KV {
	if k == nil {
		return nil
	}
	out := make([]model.KV, 0, len(k.rows))
	for _, r := range k.rows {
		key := r.key.Text()
		val := r.value.Text()
		if key == "" && val == "" {
			continue
		}
		kv := model.KV{
			Key:      key,
			Value:    val,
			Disabled: !r.tog.On,
			Type:     r.typ,
		}
		// File fields store their path separately; the engine reads KV.Path.
		if k.files && r.typ == "file" {
			kv.Path = val
			kv.Value = ""
		}
		if k.secrets {
			kv.Secret = r.isSecret
		}
		out = append(out, kv)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// kvOpts tunes the table for its context.
type kvOpts struct {
	compact bool
}

func (a *App) layoutKV(gtx layout.Context, k *kvEditor, opts kvOpts) layout.Dimensions {
	th := a.th
	if k == nil {
		return layout.Dimensions{}
	}
	k.ensureBlank()
	n := len(k.rows) + 1 // rows plus the add button
	return k.list.Layout(gtx, n, func(gtx layout.Context, i int) layout.Dimensions {
		if i == len(k.rows) {
			return layout.Inset{Top: unit.Dp(6), Bottom: unit.Dp(6)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return button(gtx, th, &k.add, "+ Add row", btnGhost, func() {
							k.rows = append(k.rows, k.newRow())
							a.win.Invalidate()
						})
					}),
				)
			})
		}
		r := k.rows[i]
		return layout.Inset{Top: unit.Dp(1), Bottom: unit.Dp(1)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return r.tog.Layout(gtx, 14, colAccent)
					})
				}),
				layout.Rigid(gap(4, 0).Layout),
				layout.Flexed(2, func(gtx layout.Context) layout.Dimensions {
					opt := defaultInput()
					opt.bg = colPanel2
					if !r.tog.On {
						opt.bg = colPanel
					}
					if opts.compact {
						opt.pad = layout.Inset{Top: unit.Dp(5), Bottom: unit.Dp(5), Left: unit.Dp(8), Right: unit.Dp(8)}
					}
					return input(gtx, th, &r.key, k.keyHint, opt)
				}),
				layout.Rigid(gap(6, 0).Layout),
				layout.Flexed(3, func(gtx layout.Context) layout.Dimensions {
					opt := defaultInput()
					opt.bg = colPanel2
					if !r.tog.On {
						opt.bg = colPanel
					}
					if opts.compact {
						opt.pad = layout.Inset{Top: unit.Dp(5), Bottom: unit.Dp(5), Left: unit.Dp(8), Right: unit.Dp(8)}
					}
					hint := k.valHint
					if k.files && r.typ == "file" {
						hint = "File path"
					}
					return input(gtx, th, &r.value, hint, opt)
				}),
				layout.Rigid(gap(4, 0).Layout),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					if k.secrets {
						return a.secretChip(gtx, r)
					}
					if k.files {
						return a.fileChip(gtx, r)
					}
					return layout.Dimensions{}
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					if !k.files || r.typ != "file" {
						return layout.Dimensions{}
					}
					return iconBtn(gtx, th, &r.browse, a.ic.FileImport, 14, colMuted, func() {
						a.pickUploadPath(r)
					})
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return iconBtn(gtx, th, &r.del, a.ic.Close, 14, colFaint, func() {
						if i < len(k.rows) {
							k.rows = append(k.rows[:i], k.rows[i+1:]...)
						}
					})
				}),
			)
		})
	})
}

// secretChip toggles the secret flag on a variable row.
func (a *App) secretChip(gtx layout.Context, r *kvRow) layout.Dimensions {
	label := "secret"
	c := color.NRGBA(colFaint)
	if r.isSecret {
		c = colAccent
	}
	return layout.Inset{Left: unit.Dp(2), Right: unit.Dp(2)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return a.smallTag(gtx, &r.secret, label, c, func() {
			r.isSecret = !r.isSecret
		})
	})
}

func (a *App) fileChip(gtx layout.Context, r *kvRow) layout.Dimensions {
	label := "text"
	c := color.NRGBA(colFaint)
	if r.typ == "file" {
		label = "file"
		c = colAccent
	}
	return layout.Inset{Left: unit.Dp(2), Right: unit.Dp(2)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return a.smallTag(gtx, &r.fileBtn, label, c, func() {
			if r.typ == "file" {
				r.typ = "text"
			} else {
				r.typ = "file"
			}
		})
	})
}

// pickUploadPath opens a native file picker and records the selected path on
// the form-data row. Desktop pickers expose a real path; platforms that only
// return a stream copy the bytes into the data directory so the engine can
// still upload them.
func (a *App) pickUploadPath(r *kvRow) {
	if a.ex == nil {
		a.notify("File picker is not available.")
		return
	}
	go func() {
		f, err := a.ex.ChooseFile()
		if err != nil {
			return
		}
		defer f.Close()
		path := ""
		if u, ok := f.(interface{ URI() string }); ok {
			path = u.URI()
		}
		if path == "" {
			if n, ok := f.(interface{ Name() string }); ok {
				if st, err := os.Stat(n.Name()); err == nil && !st.IsDir() {
					path = n.Name()
				}
			}
		}
		if path == "" {
			dir := filepath.Join(a.svc.DataDir, "uploads")
			if err := os.MkdirAll(dir, 0o700); err == nil {
				if tmp, err := os.CreateTemp(dir, "upload-*"); err == nil {
					_, cerr := io.Copy(tmp, f)
					if err := tmp.Close(); cerr == nil {
						cerr = err
					}
					if cerr == nil {
						path = tmp.Name()
					} else {
						_ = os.Remove(tmp.Name())
					}
				}
			}
		}
		a.Post(func() {
			if path == "" {
				a.notify("Could not read the selected file.")
				return
			}
			r.value.SetText(path)
			if a.win != nil {
				a.win.Invalidate()
			}
		})
	}()
}

// smallTag is a tiny clickable label. It is intentionally quiet so tables do
// not turn into a wall of buttons.
func (a *App) smallTag(gtx layout.Context, click *widget.Clickable, label string, c color.NRGBA, onClick func()) layout.Dimensions {
	return click.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Inset{Top: unit.Dp(3), Bottom: unit.Dp(3), Left: unit.Dp(5), Right: unit.Dp(5)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return txt(gtx, a.th, label, 10, c, faceMono, font.Medium)
		})
	})
}
