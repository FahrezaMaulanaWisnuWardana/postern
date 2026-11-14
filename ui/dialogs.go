package ui

import (
	"context"
	"errors"
	"image"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gioui.org/font"
	"gioui.org/io/key"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"
	"gioui.org/widget"

	"yourpm/core/brand"
	"yourpm/core/model"
	"yourpm/core/service"
)

// dialog is a modal sheet. Only one exists at a time. The kind selects the
// body layout; the onOK callback validates and may keep the dialog open by
// writing d.err.
type dialog struct {
	kind    string
	title   string
	message string
	hint    string
	okLabel string
	value   string

	input  widget.Editor
	input2 widget.Editor
	input3 widget.Editor
	code   widget.Editor

	err     string
	busy    bool
	focused bool

	onOK     func(a *App, d *dialog)
	onAlt    func(a *App, d *dialog)
	onCancel func(a *App)

	altLabel string
}

// menu is a small popup action list.
type menu struct {
	title string
	items []menuItem
}

type menuItem struct {
	label   string
	icon    *widget.Icon
	danger  bool
	onClick func()
}

/* ------------------------------------------------------------------ */
/* Constructors                                                       */
/* ------------------------------------------------------------------ */

func (a *App) prompt(title, hint, value, okLabel string, onOK func(string)) {
	d := &dialog{kind: "prompt", title: title, hint: hint, value: value, okLabel: okLabel}
	d.input.SingleLine = true
	d.input.SetText(value)
	d.onOK = func(a *App, d *dialog) {
		v := strings.TrimSpace(d.input.Text())
		if v == "" {
			d.err = "A value is required."
			return
		}
		onOK(v)
	}
	a.dialog = d
	a.win.Invalidate()
}

func (a *App) confirm(title, message, okLabel string, onOK func()) {
	d := &dialog{kind: "confirm", title: title, message: message, okLabel: okLabel}
	d.onOK = func(a *App, d *dialog) { onOK() }
	a.dialog = d
	a.win.Invalidate()
}

func (a *App) showCode(title, code string) {
	d := &dialog{kind: "code", title: title, okLabel: "Close"}
	d.code.ReadOnly = true
	d.code.SingleLine = false
	d.code.SetText(code)
	d.onOK = func(a *App, d *dialog) { a.dialog = nil }
	a.dialog = d
	a.win.Invalidate()
}

func (a *App) openSettings(tab int) {
	if a.dialog == nil || a.dialog.kind != "settings" {
		a.dialog = &dialog{kind: "settings", title: "Settings", okLabel: "Done"}
	}
	d := a.dialog
	a.settings.tab = tab
	a.settings.scroll = layout.List{Axis: layout.Vertical}
	a.settings.note = ""
	d.busy = false
	a.win.Invalidate()
	if tab == 3 {
		a.loadTeam()
	}
}

func (a *App) openSyncSettings() {
	a.openSettings(1)
	a.loadRemoteWorkspaces()
}

func (a *App) openVault(kind string) {
	d := &dialog{kind: kind, title: "Vault", okLabel: "Continue"}
	switch kind {
	case "vault-setup":
		d.title = "Enable the vault"
		d.message = "Your data is encrypted at rest with a passphrase you choose. There is no recovery key: if you lose the passphrase, the local data cannot be read."
		d.hint = "Passphrase (8+ characters)"
		d.input.Mask = '•'
		d.input.SingleLine = true
		d.input2.Mask = '•'
		d.input2.SingleLine = true
		d.onOK = func(a *App, d *dialog) {
			pass := d.input.Text()
			if len(strings.TrimSpace(pass)) < 8 {
				d.err = "Use at least 8 characters."
				return
			}
			if pass != d.input2.Text() {
				d.err = "The passphrases do not match."
				return
			}
			if err := a.svc.VaultSetup(pass); err != nil {
				d.err = err.Error()
				return
			}
			a.vaultOn = true
			a.locked = false
			a.dialog = nil
			a.notify("Vault enabled. Keep your passphrase safe.")
			a.load()
		}
	case "vault-unlock":
		d.title = "Unlock the vault"
		d.message = "Enter the passphrase for this device."
		d.hint = "Passphrase"
		d.input.Mask = '•'
		d.input.SingleLine = true
		d.onOK = func(a *App, d *dialog) {
			if err := a.svc.VaultUnlock(d.input.Text()); err != nil {
				d.err = err.Error()
				return
			}
			a.locked = false
			a.dialog = nil
			a.load()
		}
	case "vault-change":
		d.title = "Change passphrase"
		d.message = "Re-wraps the local data key. Existing data is not re-encrypted."
		d.hint = "Current passphrase"
		d.input.Mask = '•'
		d.input.SingleLine = true
		d.input2.Mask = '•'
		d.input2.SingleLine = true
		d.input3.Mask = '•'
		d.input3.SingleLine = true
		d.onOK = func(a *App, d *dialog) {
			next := d.input2.Text()
			if len(strings.TrimSpace(next)) < 8 {
				d.err = "Use at least 8 characters for the new passphrase."
				return
			}
			if next != d.input3.Text() {
				d.err = "The new passphrases do not match."
				return
			}
			if err := a.svc.VaultChangePassphrase(d.input.Text(), next); err != nil {
				d.err = err.Error()
				return
			}
			a.dialog = nil
			a.notify("Passphrase changed.")
		}
	}
	a.dialog = d
	a.win.Invalidate()
}

/* ------------------------------------------------------------------ */
/* Row actions                                                        */
/* ------------------------------------------------------------------ */

func (a *App) openRowMenu(e *model.Entity) {
	m := &menu{title: e.Name}
	m.items = append(m.items, menuItem{
		label: "Open", icon: a.ic.Request,
		onClick: func() { a.openEntity(e) },
	})
	m.items = append(m.items, menuItem{
		label: "Rename…", icon: a.ic.Edit,
		onClick: func() {
			a.prompt("Rename", "Name", e.Name, "Rename", func(v string) {
				e.Name = v
				if err := a.svc.SaveEntity(e); err != nil {
					a.notify(err.Error())
					return
				}
				a.reloadEntities()
				a.refreshTabs()
				a.dialog = nil
			})
		},
	})
	m.items = append(m.items, menuItem{
		label: "Duplicate", icon: a.ic.Copy,
		onClick: func() {
			a.duplicateEntity(e)
		},
	})
	if e.Kind == model.KindCollection || e.Kind == model.KindFolder {
		m.items = append(m.items, menuItem{
			label: "Run", icon: a.ic.Play,
			onClick: func() {
				a.runCollection(e.ID)
			},
		})
		m.items = append(m.items, menuItem{
			label: "New request here", icon: a.ic.Add,
			onClick: func() {
				a.newEntity(model.KindRequest, e.ID)
			},
		})
		m.items = append(m.items, menuItem{
			label: "New folder here", icon: a.ic.Folder,
			onClick: func() {
				a.newEntity(model.KindFolder, e.ID)
			},
		})
	}
	if e.Kind == model.KindCollection {
		m.items = append(m.items, menuItem{
			label: "Export as Postman v2.1", icon: a.ic.FileExport,
			onClick: func() {
				a.exportCollection(e)
			},
		})
	}
	if e.Kind == model.KindEnvironment {
		m.items = append(m.items, menuItem{
			label: "Export environment", icon: a.ic.FileExport,
			onClick: func() {
				a.exportEnvironment(e)
			},
		})
	}
	m.items = append(m.items, menuItem{
		label: "Delete", icon: a.ic.Delete, danger: true,
		onClick: func() {
			a.confirm("Delete “"+e.Name+"”?", "This removes the item and everything inside it from this device. Synced copies are deleted on the next sync.", "Delete", func() {
				if err := a.svc.DeleteEntity(e); err != nil {
					a.notify(err.Error())
					return
				}
				toRemove := map[string]bool{e.ID: true}
				changed := true
				for changed {
					changed = false
					for _, ent := range a.entities {
						if toRemove[ent.ParentID] && !toRemove[ent.ID] {
							toRemove[ent.ID] = true
							changed = true
						}
					}
				}
				for i := len(a.tabs) - 1; i >= 0; i-- {
					if toRemove[a.tabs[i].ID] {
						a.removeTab(i)
					}
				}
				a.reloadEntities()
				a.dialog = nil
				a.notify("Deleted “" + e.Name + "”.")
			})
		},
	})
	a.menu = m
	a.win.Invalidate()
}

func (a *App) refreshTabs() {
	for _, t := range a.tabs {
		e := a.entityByID(t.ID)
		if e == nil {
			continue
		}
		// Only names can change outside a tab; the payload lives in the tab.
		_ = e
	}
}

func (a *App) duplicateEntity(e *model.Entity) {
	root, err := a.cloneEntity(e, e.ParentID)
	if err != nil {
		a.notify(err.Error())
		return
	}
	a.reloadEntities()
	a.notify("Duplicated as “" + root.Name + "”.")
	a.menu = nil
}

func (a *App) cloneEntity(src *model.Entity, parent string) (*model.Entity, error) {
	dst, err := a.svc.NewEntity(a.wsID, src.Kind, parent, src.Name+" copy")
	if err != nil {
		return nil, err
	}
	dst.Data = src.Data
	dst.Position = src.Position + 0.5
	if err := a.svc.SaveEntity(dst); err != nil {
		return nil, err
	}
	for _, child := range a.entities {
		if child.ParentID == src.ID {
			if _, err := a.cloneEntity(child, dst.ID); err != nil {
				return nil, err
			}
		}
	}
	return dst, nil
}

/* ------------------------------------------------------------------ */
/* Creation                                                           */
/* ------------------------------------------------------------------ */

func (a *App) newEntity(kind, parent string) {
	title := "New " + kind
	placeholder := "Name"
	def := ""
	switch kind {
	case model.KindCollection:
		def = "New collection"
	case model.KindFolder:
		def = "New folder"
	case model.KindRequest:
		def = "New request"
	case model.KindEnvironment:
		def = "New environment"
	}
	isEnv := kind == model.KindEnvironment
	a.prompt(title, placeholder, def, "Create", func(name string) {
		e, err := a.svc.NewEntity(a.wsID, kind, parent, name)
		if err != nil {
			a.notify(err.Error())
			return
		}
		a.dialog = nil
		a.reloadEntities()
		a.ensureExpanded()
		if parent != "" {
			a.expanded[parent] = true
		}
		if isEnv {
			a.openEntity(e)
		} else if kind == model.KindRequest || kind == model.KindCollection {
			a.openEntity(e)
		}
	})
}

/* ------------------------------------------------------------------ */
/* Vault / lock                                                       */
/* ------------------------------------------------------------------ */

func (a *App) lockNow() {
	if !a.vaultOn {
		a.notify("Enable the vault first.")
		return
	}
	a.saveDirtyTabs()
	a.svc.VaultLock()
	a.locked = true
	a.entities = nil
	a.envs = nil
	a.tabs = nil
	a.settings.inviteCode.SetText("")
	a.settings.joinCode.SetText("")
	a.settings.joinPass.SetText("")
	a.settings.passphrase.SetText("")
	a.active = 0
	a.dialog = nil
}

/* ------------------------------------------------------------------ */
/* Sync                                                               */
/* ------------------------------------------------------------------ */

func (a *App) syncLogin(register bool) {
	a.settings.busy = true
	a.settings.note = ""
	server := strings.TrimSpace(a.settings.server.Text())
	email := strings.TrimSpace(a.settings.email.Text())
	pass := a.settings.password.Text()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
		defer cancel()
		acct, err := a.syn.Login(ctx, server, email, pass, register)
		a.Post(func() {
			a.settings.busy = false
			if err != nil {
				a.settings.note = err.Error()
				return
			}
			a.settings.note = "Signed in as " + acct.Email
			a.settings.password.SetText("")
			a.loadRemoteWorkspaces()
		})
	}()
}

func (a *App) loadRemoteWorkspaces() {
	if !a.syn.LoggedIn() {
		return
	}
	a.settings.busy = true
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		list, err := a.syn.RemoteWorkspaces(ctx)
		a.Post(func() {
			a.settings.busy = false
			if err != nil {
				a.settings.note = err.Error()
				return
			}
			a.settings.remote = list
			if len(list) == 0 {
				a.settings.note = "No remote workspaces yet."
			}
		})
	}()
}

func (a *App) loadDevices() {
	if !a.syn.LoggedIn() || a.settings.devicesBusy {
		return
	}
	a.settings.devicesBusy = true
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		list, err := a.syn.Devices(ctx)
		a.Post(func() {
			a.settings.devicesBusy = false
			if err != nil {
				a.settings.note = err.Error()
				return
			}
			a.settings.devices = list
		})
	}()
}

func (a *App) linkRemote(remote model.RemoteWorkspace) {
	local := a.workspace()
	if local == nil {
		return
	}
	if !local.Linked() {
		entities, err := a.svc.Store.ListEntities(local.ID, false)
		if err != nil {
			a.settings.note = err.Error()
			return
		}
		if len(entities) > 0 {
			a.confirm("Share this local workspace?", "Linking uploads this workspace's collections, environments and stored request credentials to “"+remote.Name+"”. Its members can read them. To keep your personal data separate, cancel and create an empty local workspace first.", "Link and share", func() {
				a.dialog = nil
				a.performLinkRemote(remote)
			})
			return
		}
	}
	a.performLinkRemote(remote)
}

func (a *App) performLinkRemote(remote model.RemoteWorkspace) {
	local := a.workspace()
	if local == nil {
		return
	}
	pass := a.settings.passphrase.Text()
	encrypted := remote.Encrypted
	if encrypted && pass == "" {
		a.settings.note = "Enter the workspace passphrase to link an encrypted workspace."
		return
	}
	a.settings.busy = true
	// Work on a copy: the sync goroutine must not mutate a workspace the UI
	// is reading while it renders.
	localCopy := *local
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		res, err := a.syn.Link(ctx, &localCopy, remote, pass)
		a.Post(func() {
			a.settings.busy = false
			if err != nil {
				a.settings.note = err.Error()
				return
			}
			a.settings.passphrase.SetText("")
			a.settings.note = "Linked “" + res.Remote.Name + "”."
			if res.ReSealed {
				a.settings.note += " Local data was re-encrypted with the workspace key."
			}
			a.vaultOn = a.svc.Store.VaultEnabled()
			a.workspaces = mustWorkspaces(a.svc.Store.ListWorkspaces())
			a.syncNow()
		})
	}()
}

func (a *App) createRemote() {
	local := a.workspace()
	if local == nil {
		return
	}
	if local.Linked() {
		a.settings.note = "Unlink the current remote before creating a new one. Your local data is kept."
		return
	}
	encrypted := a.svc.Store.VaultEnabled() && a.svc.Store.Unlocked()
	if encrypted && len(a.settings.passphrase.Text()) < 10 {
		a.settings.note = "Enter a new workspace passphrase (at least 10 characters). It is separate from your private vault passphrase."
		return
	}
	if !encrypted {
		a.confirm("Create an unencrypted remote workspace?",
			"The vault is off, so the sync server stores this workspace as plaintext. The server operator, backups and anyone with database access can read every request, header, token and body. Enable the vault first if that is not acceptable.",
			"Continue", func() {
				a.createRemotePrompt()
			})
		return
	}
	a.createRemotePrompt()
}

func (a *App) createRemotePrompt() {
	local := a.workspace()
	if local == nil {
		return
	}
	a.prompt("Create remote workspace", "Workspace name", local.Name, "Create", func(name string) {
		a.dialog = nil
		a.settings.busy = true
		encrypted := a.svc.Store.VaultEnabled() && a.svc.Store.Unlocked()
		pass := a.settings.passphrase.Text()
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
			defer cancel()
			var remote *model.RemoteWorkspace
			var err error
			if encrypted {
				remote, err = a.syn.CreateEncryptedRemote(ctx, name, pass)
			} else {
				remote, err = a.syn.CreateRemote(ctx, name, false, "", "")
			}
			a.Post(func() {
				a.settings.busy = false
				if err != nil {
					a.settings.note = err.Error()
					return
				}
				a.settings.note = "Created “" + remote.Name + "”. Linking now…"
				a.linkRemote(*remote)
			})
		}()
	})
}

func (a *App) syncNow() {
	local := a.workspace()
	if local == nil {
		return
	}
	if !local.Linked() {
		a.notify("This workspace is not linked to a server.")
		return
	}
	if a.syncing {
		return
	}
	a.syncing = true
	a.syncNote = "Syncing…"
	localCopy := *local
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		res, err := a.syn.Run(ctx, &localCopy)
		a.Post(func() {
			a.syncing = false
			a.syncWhen = time.Now()
			if err != nil {
				a.syncNote = "Sync failed: " + err.Error()
				return
			}
			a.syncNote = "Synced"
			if len(res.Errors) > 0 {
				a.syncNote = "Sync incomplete: " + res.Errors[0]
			}
			if res.Pushed > 0 || res.Pulled > 0 {
				a.syncNote += " · ↑" + itoa(res.Pushed) + " ↓" + itoa(res.Pulled)
			}
			if res.Conflicts > 0 {
				a.syncNote += " · " + itoa(res.Conflicts) + " conflicts"
			}
			a.reloadEntities()
			a.workspaces = mustWorkspaces(a.svc.Store.ListWorkspaces())
		})
	}()
}

func (a *App) unlinkWorkspace() {
	local := a.workspace()
	if local == nil {
		return
	}
	if !local.Linked() {
		a.settings.note = "This workspace is not linked."
		return
	}
	a.confirm("Unlink this workspace?", "Local data stays on this device. The server copy is untouched.", "Unlink", func() {
		if err := a.syn.Unlink(local); err != nil {
			a.notify(err.Error())
			return
		}
		a.dialog = nil
		a.workspaces = mustWorkspaces(a.svc.Store.ListWorkspaces())
		a.settings.note = "Workspace unlinked."
	})
}

/* ------------------------------------------------------------------ */
/* Import / export                                                    */
/* ------------------------------------------------------------------ */

func (a *App) importPostman() {
	if a.ex == nil {
		return
	}
	go func() {
		r, err := a.ex.ChooseFile(".json")
		if err != nil {
			a.Post(func() { a.notify("Import cancelled.") })
			return
		}
		defer r.Close()
		raw, err := readLimited(r, 64<<20)
		if err != nil {
			a.Post(func() { a.notify("Could not read file: " + err.Error()) })
			return
		}
		ent, err := a.svc.ImportPostman(a.wsID, raw)
		a.Post(func() {
			if err != nil {
				a.notify("Import failed: " + err.Error())
				return
			}
			a.reloadEntities()
			a.notify("Imported “" + ent.Name + "”.")
			a.openEntity(ent)
		})
	}()
}

func (a *App) exportCollection(e *model.Entity) {
	if a.ex == nil {
		return
	}
	go func() {
		raw, err := a.svc.ExportCollection(e)
		if err != nil {
			a.Post(func() { a.notify("Export failed: " + err.Error()) })
			return
		}
		w, err := a.ex.CreateFile(safeFileName(e.Name) + ".postman_collection.json")
		if err != nil {
			a.Post(func() { a.notify("Export cancelled.") })
			return
		}
		_, werr := w.Write(raw)
		cerr := w.Close()
		a.Post(func() {
			if werr != nil || cerr != nil {
				a.notify("Export failed while writing the file.")
				return
			}
			a.notify("Exported “" + e.Name + "”.")
		})
	}()
}

func (a *App) exportEnvironment(e *model.Entity) {
	if a.ex == nil {
		return
	}
	go func() {
		raw, err := a.svc.ExportEnvironment(e)
		if err != nil {
			a.Post(func() { a.notify("Export failed: " + err.Error()) })
			return
		}
		w, err := a.ex.CreateFile(safeFileName(e.Name) + ".postman_environment.json")
		if err != nil {
			a.Post(func() { a.notify("Export cancelled.") })
			return
		}
		_, werr := w.Write(raw)
		cerr := w.Close()
		a.Post(func() {
			if werr != nil || cerr != nil {
				a.notify("Export failed while writing the file.")
				return
			}
			a.notify("Exported “" + e.Name + "”.")
		})
	}()
}

func (a *App) curlForActive() {
	t := a.activeTab()
	if t == nil || t.Kind != model.KindRequest {
		a.notify("Open a request first.")
		return
	}
	a.pullEditors(t)
	col := a.svc.CollectionIDFor(a.wsID, t.ID)
	code := a.svc.ExportCurl(a.wsID, t.Req, a.activeEnv, col)
	a.showCode("cURL", code)
}

/* ------------------------------------------------------------------ */
/* Trash, backups and collection runs                                 */
/* ------------------------------------------------------------------ */

func (a *App) openTrash() {
	list, err := a.svc.Trash(a.wsID)
	if err != nil {
		a.notify("Could not load the trash: " + err.Error())
		return
	}
	a.trash = list
	a.trashList = layout.List{Axis: layout.Vertical}
	d := &dialog{kind: "trash", title: "Trash", okLabel: "Close"}
	d.onOK = func(a *App, d *dialog) { a.dialog = nil }
	a.dialog = d
	a.win.Invalidate()
}

func (a *App) trashBody(gtx layout.Context) layout.Dimensions {
	th := a.th
	if len(a.trash) == 0 {
		return layout.Inset{Top: unit.Dp(12)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return txt(gtx, th, "The trash is empty. Deleted items stay here until you restore them; sync propagates deletions as tombstones.", 12, colMuted, faceUI, font.Normal)
		})
	}
	height := gtx.Dp(unit.Dp(320))
	if height > gtx.Constraints.Max.Y {
		height = gtx.Constraints.Max.Y
	}
	gtx.Constraints.Min.Y = height
	gtx.Constraints.Max.Y = height
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return txt(gtx, th, "Restoring a collection or folder also restores the items inside it.", 11, colMuted, faceUI, font.Normal)
		}),
		layout.Rigid(gap(0, 8).Layout),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			return a.trashList.Layout(gtx, len(a.trash), func(gtx layout.Context, i int) layout.Dimensions {
				if i >= len(a.trash) {
					return layout.Dimensions{}
				}
				e := a.trash[i]
				name := e.Name
				if name == "" {
					name = "(name unavailable)"
				}
				return layout.Inset{Top: unit.Dp(2), Bottom: unit.Dp(2)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return badge(gtx, th, e.Kind, colMuted)
						}),
						layout.Rigid(gap(10, 0).Layout),
						layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
							return txtClip(gtx, th, name, 13, colText, faceUI, font.Normal)
						}),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return button(gtx, th, a.click("trash-restore:"+e.ID), "Restore", btnGhost, func() {
								if err := a.svc.RestoreEntity(e); err != nil {
									a.notify("Could not restore: " + err.Error())
									return
								}
								a.reloadEntities()
								a.notify("Restored “" + name + "”.")
								a.openTrash()
							})
						}),
					)
				})
			})
		}),
	)
}

func (a *App) exportWorkspaceBackup() {
	if a.ex == nil {
		return
	}
	ws := a.workspace()
	if ws == nil {
		return
	}
	d := &dialog{kind: "prompt", title: "Export workspace backup", okLabel: "Export"}
	d.message = "Optionally encrypt the backup with a passphrase. Leave empty for plaintext JSON, which anyone who can read the file can open."
	d.hint = "Passphrase (optional, 8+ characters)"
	d.input.Mask = '•'
	d.input.SingleLine = true
	d.onOK = func(a *App, d *dialog) {
		pass := d.input.Text()
		if pass != "" && len(strings.TrimSpace(pass)) < 8 {
			d.err = "Use at least 8 characters, or leave empty for plaintext."
			return
		}
		a.dialog = nil
		a.writeWorkspaceBackup(ws.ID, ws.Name, pass)
	}
	a.dialog = d
	a.win.Invalidate()
}

func (a *App) writeWorkspaceBackup(wsID, wsName, passphrase string) {
	go func() {
		raw, err := a.svc.ExportWorkspaceEncrypted(wsID, passphrase)
		if err != nil {
			a.Post(func() { a.notify("Backup failed: " + err.Error()) })
			return
		}
		w, err := a.ex.CreateFile(safeFileName(wsName) + ".yourpm.json")
		if err != nil {
			a.Post(func() { a.notify("Backup cancelled.") })
			return
		}
		_, werr := w.Write(raw)
		cerr := w.Close()
		a.Post(func() {
			if werr != nil || cerr != nil {
				a.notify("Backup failed while writing the file.")
				return
			}
			if passphrase != "" {
				a.notify("Encrypted workspace backup saved.")
				return
			}
			a.notify("Workspace backup saved. It is plaintext — keep it safe.")
		})
	}()
}

func (a *App) importWorkspaceBackup() {
	if a.ex == nil {
		return
	}
	go func() {
		r, err := a.ex.ChooseFile(".json")
		if err != nil {
			a.Post(func() { a.notify("Restore cancelled.") })
			return
		}
		defer r.Close()
		raw, err := readLimited(r, 128<<20)
		if err != nil {
			a.Post(func() { a.notify("Could not read the backup: " + err.Error()) })
			return
		}
		ws, err := a.svc.ImportWorkspace(raw, "")
		if errors.Is(err, service.ErrEncryptedBackup) {
			a.Post(func() { a.askBackupPassphrase(raw) })
			return
		}
		a.Post(func() { a.finishImport(ws, err) })
	}()
}

// askBackupPassphrase continues an import that turned out to be encrypted. The
// archive stays in memory while the user types; nothing is written to disk.
func (a *App) askBackupPassphrase(raw []byte) {
	d := &dialog{kind: "prompt", title: "Encrypted backup", okLabel: "Restore"}
	d.message = "This backup was exported with a passphrase. Enter it to restore the workspace."
	d.hint = "Passphrase"
	d.input.Mask = '•'
	d.input.SingleLine = true
	d.onOK = func(a *App, d *dialog) {
		pass := d.input.Text()
		if pass == "" {
			d.err = "Enter the passphrase."
			return
		}
		a.dialog = nil
		go func() {
			ws, err := a.svc.ImportWorkspaceEncrypted(raw, "", pass)
			a.Post(func() { a.finishImport(ws, err) })
		}()
	}
	a.dialog = d
	a.win.Invalidate()
}

func (a *App) finishImport(ws *model.Workspace, err error) {
	if err != nil {
		a.notify("Restore failed: " + err.Error())
		return
	}
	a.notify("Restored “" + ws.Name + "”.")
	a.setWorkspace(ws.ID)
}

// runCollection executes a collection or folder in the background and shows a
// pass/fail summary in a dialog.
func (a *App) runCollection(rootID string) {
	if a.runBusy {
		return
	}
	a.runBusy = true
	a.run = nil
	a.runList = layout.List{Axis: layout.Vertical}
	d := &dialog{kind: "run", title: "Running…", okLabel: "Close"}
	d.onOK = func(a *App, d *dialog) { a.dialog = nil }
	a.dialog = d
	ws, env := a.wsID, a.activeEnv
	go func() {
		sum, err := a.svc.RunCollection(context.Background(), ws, rootID, env, nil)
		a.Post(func() {
			a.runBusy = false
			d.title = "Run results"
			if err != nil {
				d.err = err.Error()
			} else {
				a.run = sum
			}
			if a.win != nil {
				a.win.Invalidate()
			}
		})
	}()
}

func (a *App) runBody(gtx layout.Context) layout.Dimensions {
	th := a.th
	if a.runBusy {
		return layout.Inset{Top: unit.Dp(12)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return txt(gtx, th, "Running every request in tree order…", 12, colMuted, faceUI, font.Normal)
		})
	}
	sum := a.run
	if sum == nil {
		return layout.Inset{Top: unit.Dp(12)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return txt(gtx, th, "No results.", 12, colMuted, faceUI, font.Normal)
		})
	}
	height := gtx.Dp(unit.Dp(320))
	if height > gtx.Constraints.Max.Y {
		height = gtx.Constraints.Max.Y
	}
	gtx.Constraints.Min.Y = height
	gtx.Constraints.Max.Y = height
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return badge(gtx, th, itoa(sum.Total)+" requests", colMuted)
				}),
				layout.Rigid(gap(8, 0).Layout),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return badge(gtx, th, itoa(sum.Passed)+" passed", colGreen)
				}),
				layout.Rigid(gap(8, 0).Layout),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					if sum.Failed == 0 {
						return layout.Dimensions{}
					}
					return badge(gtx, th, itoa(sum.Failed)+" failed", colRed)
				}),
				layout.Rigid(gap(8, 0).Layout),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					if sum.Errors == 0 {
						return layout.Dimensions{}
					}
					return badge(gtx, th, itoa(sum.Errors)+" errors", colYellow)
				}),
			)
		}),
		layout.Rigid(gap(0, 10).Layout),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Min.Y = gtx.Constraints.Max.Y
			return a.runList.Layout(gtx, len(sum.Items), func(gtx layout.Context, i int) layout.Dimensions {
				if i >= len(sum.Items) {
					return layout.Dimensions{}
				}
				item := sum.Items[i]
				return layout.Inset{Top: unit.Dp(2), Bottom: unit.Dp(2)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return badge(gtx, th, itoa(item.Status), statusColor(item.Status))
						}),
						layout.Rigid(gap(8, 0).Layout),
						layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
							text := item.Name + "  ·  " + strings.ToUpper(item.Method) + " " + item.URL
							return txtClip(gtx, th, text, 12, colText, faceUI, font.Normal)
						}),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							if item.Error != "" {
								return txtClip(gtx, th, item.Error, 11, colRed, faceMono, font.Normal)
							}
							if item.TestFail {
								return badge(gtx, th, "tests failed", colRed)
							}
							if item.HTTPFail {
								return badge(gtx, th, "request failed", colRed)
							}
							return layout.Dimensions{}
						}),
					)
				})
			})
		}),
	)
}

/* ------------------------------------------------------------------ */
/* Layout                                                             */
/* ------------------------------------------------------------------ */

func (a *App) layoutDialog(gtx layout.Context) {
	if a.dialog == nil {
		return
	}
	d := a.dialog
	layout.Stack{Alignment: layout.Center}.Layout(gtx,
		layout.Expanded(func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Min = gtx.Constraints.Max
			fillRect(gtx, gtx.Constraints.Max, alpha(colBg, 0xE8))
			return layout.Dimensions{Size: gtx.Constraints.Max}
		}),
		layout.Stacked(func(gtx layout.Context) layout.Dimensions {
			maxW := 620
			if d.kind == "settings" {
				maxW = 780
			}
			w := gtx.Dp(unit.Dp(maxW))
			if gtx.Constraints.Max.X-48 < w {
				w = gtx.Constraints.Max.X - 48
			}
			gtx.Constraints.Min.X = w
			gtx.Constraints.Max.X = w
			if d.kind == "settings" {
				gtx.Constraints.Max.Y = gtx.Constraints.Max.Y - gtx.Dp(unit.Dp(56))
			}
			return panel(gtx, colPanel, unit.Dp(14), func(gtx layout.Context) layout.Dimensions {
				return layout.UniformInset(unit.Dp(22)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return a.dialogBody(gtx, d)
				})
			})
		}),
	)
	if !d.focused {
		d.focused = true
		if !d.input.ReadOnly && d.kind != "settings" && d.kind != "code" {
			gtx.Execute(key.FocusCmd{Tag: &d.input})
		}
	}
}

func (a *App) dialogBody(gtx layout.Context, d *dialog) layout.Dimensions {
	th := a.th
	header := func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				return txt(gtx, th, d.title, 20, colText, faceUI, font.SemiBold)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return iconBtn(gtx, th, a.click("dlg-close"), a.ic.Close, 16, colMuted, func() {
					a.dialog = nil
				})
			}),
		)
	}

	var body layout.Widget
	switch d.kind {
	case "prompt":
		body = func(gtx layout.Context) layout.Dimensions {
			return a.dialogFieldStack(gtx, d, []layout.Widget{
				func(gtx layout.Context) layout.Dimensions {
					if d.message == "" {
						return layout.Dimensions{}
					}
					return txt(gtx, th, d.message, 12, colMuted, faceUI, font.Normal)
				},
				func(gtx layout.Context) layout.Dimensions {
					return input(gtx, th, &d.input, d.hint, defaultInput())
				},
			})
		}
	case "confirm":
		body = func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Top: unit.Dp(12)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return txt(gtx, th, d.message, 13, colMuted, faceUI, font.Normal)
			})
		}
	case "trash":
		body = func(gtx layout.Context) layout.Dimensions {
			return a.trashBody(gtx)
		}
	case "run":
		body = func(gtx layout.Context) layout.Dimensions {
			return a.runBody(gtx)
		}
	case "code":
		body = func(gtx layout.Context) layout.Dimensions {
			opt := defaultInput()
			opt.mono = true
			opt.bg = colBg
			gtx.Constraints.Min.Y = gtx.Dp(unit.Dp(260))
			gtx.Constraints.Max.Y = gtx.Dp(unit.Dp(360))
			return input(gtx, th, &d.code, "", opt)
		}
	case "vault-setup", "vault-unlock", "vault-change":
		body = func(gtx layout.Context) layout.Dimensions {
			widgets := []layout.Widget{
				func(gtx layout.Context) layout.Dimensions {
					return txt(gtx, th, d.message, 12, colMuted, faceUI, font.Normal)
				},
			}
			if d.kind != "vault-unlock" {
				widgets = append(widgets, func(gtx layout.Context) layout.Dimensions {
					return txt(gtx, th, "Current passphrase", 11, colFaint, faceUI, font.Medium)
				})
			}
			widgets = append(widgets, func(gtx layout.Context) layout.Dimensions {
				return input(gtx, th, &d.input, d.hint, defaultInput())
			})
			if d.kind == "vault-setup" || d.kind == "vault-change" {
				label := "Confirm passphrase"
				if d.kind == "vault-change" {
					label = "New passphrase"
				}
				widgets = append(widgets,
					func(gtx layout.Context) layout.Dimensions {
						return txt(gtx, th, label, 11, colFaint, faceUI, font.Medium)
					},
					func(gtx layout.Context) layout.Dimensions {
						return input(gtx, th, &d.input2, label, defaultInput())
					})
			}
			if d.kind == "vault-change" {
				widgets = append(widgets,
					func(gtx layout.Context) layout.Dimensions {
						return txt(gtx, th, "Confirm new passphrase", 11, colFaint, faceUI, font.Medium)
					},
					func(gtx layout.Context) layout.Dimensions {
						return input(gtx, th, &d.input3, "Confirm new passphrase", defaultInput())
					})
			}
			return a.dialogFieldStack(gtx, d, widgets)
		}
	case "settings":
		body = func(gtx layout.Context) layout.Dimensions {
			return a.settingsBody(gtx, d)
		}
	}

	bodyChild := layout.Rigid(body)
	if d.kind == "settings" {
		bodyChild = layout.Flexed(1, body)
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(header),
		layout.Rigid(gap(0, 14).Layout),
		bodyChild,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			if d.err == "" {
				return layout.Dimensions{}
			}
			return layout.Inset{Top: unit.Dp(12)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return panel(gtx, alpha(colRed, 0x18), unit.Dp(6), func(gtx layout.Context) layout.Dimensions {
					return layout.UniformInset(unit.Dp(8)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return txt(gtx, th, d.err, 12, colRed, faceUI, font.Normal)
					})
				})
			})
		}),
		layout.Rigid(gap(0, 16).Layout),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle, Spacing: layout.SpaceBetween}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					if d.kind == "code" || d.kind == "settings" {
						return layout.Dimensions{}
					}
					return button(gtx, th, a.click("dlg-cancel"), "Cancel", btnGhost, func() {
						if d.onCancel != nil {
							d.onCancel(a)
						}
						a.dialog = nil
					})
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					label := d.okLabel
					if label == "" {
						label = "OK"
					}
					if d.busy {
						label = "Working…"
					}
					if d.kind == "settings" {
						return button(gtx, th, a.click("dlg-close2"), "Done", btnPrimary, func() {
							a.dialog = nil
						})
					}
					return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							if d.altLabel == "" {
								return layout.Dimensions{}
							}
							return layout.Inset{Right: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
								return button(gtx, th, a.click("dlg-alt"), d.altLabel, btnGhost, func() {
									if d.onAlt != nil {
										d.onAlt(a, d)
									}
									a.dialog = nil
								})
							})
						}),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return button(gtx, th, a.click("dlg-ok"), label, btnPrimary, func() {
								if d.busy || d.onOK == nil {
									return
								}
								d.onOK(a, d)
								a.win.Invalidate()
							})
						}),
					)
				}),
			)
		}),
	)
}

func (a *App) dialogFieldStack(gtx layout.Context, d *dialog, widgets []layout.Widget) layout.Dimensions {
	var children []layout.FlexChild
	for i, w := range widgets {
		if i > 0 {
			children = append(children, layout.Rigid(gap(0, 10).Layout))
		}
		w := w
		children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return w(gtx)
		}))
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx, children...)
}

/* ------------------------------------------------------------------ */
/* Settings body                                                      */
/* ------------------------------------------------------------------ */

func (a *App) settingsBody(gtx layout.Context, d *dialog) layout.Dimensions {
	th := a.th
	tabs := []string{"General", "Sync", "Vault", "Team"}
	var tabBar layout.FlexChild
	tabBar = layout.Rigid(func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Horizontal}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				var kids []layout.FlexChild
				for i, name := range tabs {
					i, name := i, name
					kids = append(kids, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						active := a.settings.tab == i
						return layout.Inset{Right: unit.Dp(6)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
							return button(gtx, th, a.click("set-tab-"+itoa(i)), name, pick(active, btnSecondary, btnGhost), func() {
								a.settings.tab = i
								a.settings.note = ""
								a.settings.scroll = layout.List{Axis: layout.Vertical}
								if i == 1 {
									a.loadRemoteWorkspaces()
									a.loadDevices()
								}
								if i == 3 {
									a.loadTeam()
								}
							})
						})
					}))
				}
				return layout.Flex{Axis: layout.Horizontal}.Layout(gtx, kids...)
			}),
		)
	})

	var content layout.Widget
	switch a.settings.tab {
	case 1:
		content = a.settingsSync
	case 2:
		content = a.settingsVault
	case 3:
		content = a.settingsTeam
	default:
		content = a.settingsGeneral
	}

	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		tabBar,
		layout.Rigid(gap(0, 14).Layout),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			a.settings.scroll.Axis = layout.Vertical
			return a.settings.scroll.Layout(gtx, 1, func(gtx layout.Context, _ int) layout.Dimensions {
				return content(gtx)
			})
		}),
	)
}

func (a *App) settingsGeneral(gtx layout.Context) layout.Dimensions {
	th := a.th
	rows := []layout.Widget{
		func(gtx layout.Context) layout.Dimensions {
			return a.settingsHeading(gtx, "Appearance")
		},
		func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Horizontal}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return button(gtx, th, a.click("theme-light"), "Light", pick(a.colorMode != "dark", btnSecondary, btnGhost), func() {
						a.setColorMode("light")
					})
				}),
				layout.Rigid(gap(8, 0).Layout),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return button(gtx, th, a.click("theme-dark"), "Dark", pick(a.colorMode == "dark", btnSecondary, btnGhost), func() {
						a.setColorMode("dark")
					})
				}),
			)
		},
		func(gtx layout.Context) layout.Dimensions {
			return a.settingsHeading(gtx, "Application")
		},
		func(gtx layout.Context) layout.Dimensions {
			return a.kvLine(gtx, "Product", brand.Name+" "+brand.Version)
		},
		func(gtx layout.Context) layout.Dimensions {
			return a.kvLine(gtx, "Data directory", a.svc.DataDir)
		},
		func(gtx layout.Context) layout.Dimensions {
			return a.kvLine(gtx, "Workspace", a.wsName())
		},
		func(gtx layout.Context) layout.Dimensions {
			return a.kvLine(gtx, "Sync", linkedLabel(a.workspace()))
		},
		func(gtx layout.Context) layout.Dimensions {
			return a.settingsHeading(gtx, "Privacy")
		},
		func(gtx layout.Context) layout.Dimensions {
			return txt(gtx, th, "No telemetry, analytics, crash reporting or AI features. Requests go only to the URLs you enter.", 12, colMuted, faceUI, font.Normal)
		},
		func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Top: unit.Dp(10)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Horizontal}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return button(gtx, th, a.click("set-import"), "Import Postman", btnSecondary, func() {
							a.importPostman()
						})
					}),
					layout.Rigid(gap(8, 0).Layout),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return button(gtx, th, a.click("set-curl"), "Copy as cURL", btnSecondary, func() {
							a.curlForActive()
						})
					}),
				)
			})
		},
	}
	return a.settingsStack(gtx, rows)
}

func (a *App) settingsSync(gtx layout.Context) layout.Dimensions {
	th := a.th
	acct := a.syn.Account()
	rows := []layout.Widget{
		func(gtx layout.Context) layout.Dimensions {
			return a.settingsHeading(gtx, "Server")
		},
		func(gtx layout.Context) layout.Dimensions {
			return input(gtx, th, &a.settings.server, "https://sync.example.com", defaultInput())
		},
		func(gtx layout.Context) layout.Dimensions {
			return input(gtx, th, &a.settings.email, "you@example.com", defaultInput())
		},
		func(gtx layout.Context) layout.Dimensions {
			e := &a.settings.password
			e.SingleLine = true
			e.Mask = '•'
			return input(gtx, th, e, "Password", defaultInput())
		},
		func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					label := "Sign in"
					if a.settings.busy {
						label = "Signing in…"
					}
					return button(gtx, th, a.click("set-login"), label, btnPrimary, func() {
						if !a.settings.busy {
							a.syncLogin(false)
						}
					})
				}),
				layout.Rigid(gap(8, 0).Layout),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return button(gtx, th, a.click("set-register"), "Create account", btnSecondary, func() {
						if !a.settings.busy {
							a.syncLogin(true)
						}
					})
				}),
				layout.Rigid(gap(10, 0).Layout),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					if !a.syn.LoggedIn() {
						return layout.Dimensions{}
					}
					return button(gtx, th, a.click("set-logout"), "Sign out", btnGhost, func() {
						a.syn.Logout(context.Background())
						a.settings.note = "Signed out."
					})
				}),
			)
		},
		func(gtx layout.Context) layout.Dimensions {
			if acct.Email == "" {
				return layout.Dimensions{}
			}
			return a.kvLine(gtx, "Account", acct.Email)
		},
		func(gtx layout.Context) layout.Dimensions {
			return a.settingsHeading(gtx, "Current workspace")
		},
	}

	if ws := a.workspace(); ws != nil {
		rows = append(rows,
			func(gtx layout.Context) layout.Dimensions {
				return a.kvLine(gtx, ws.Name, linkedLabel(ws))
			},
			func(gtx layout.Context) layout.Dimensions {
				if !ws.Linked() {
					return layout.Dimensions{}
				}
				return layout.Inset{Top: unit.Dp(6)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Axis: layout.Horizontal}.Layout(gtx,
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							label := "Sync now"
							if a.syncing {
								label = "Syncing…"
							}
							return button(gtx, th, a.click("set-sync"), label, btnSecondary, func() {
								a.syncNow()
							})
						}),
						layout.Rigid(gap(8, 0).Layout),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return button(gtx, th, a.click("set-unlink"), "Unlink", btnGhost, func() {
								a.unlinkWorkspace()
							})
						}),
					)
				})
			})
	}

	rows = append(rows,
		func(gtx layout.Context) layout.Dimensions {
			return a.settingsAutomation(gtx)
		},
		func(gtx layout.Context) layout.Dimensions {
			return a.settingsDevices(gtx)
		},
		func(gtx layout.Context) layout.Dimensions {
			return a.settingsHeading(gtx, "Remote workspaces")
		},
		func(gtx layout.Context) layout.Dimensions {
			if !a.syn.LoggedIn() {
				return txt(gtx, th, "Sign in to list workspaces on the server.", 12, colFaint, faceUI, font.Normal)
			}
			if len(a.settings.remote) == 0 {
				return txt(gtx, th, "No remote workspaces yet.", 12, colFaint, faceUI, font.Normal)
			}
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx, a.remoteRows()...)
		},
		func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Top: unit.Dp(6)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
					layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
						e := &a.settings.passphrase
						e.SingleLine = true
						e.Mask = '•'
						return input(gtx, th, e, "Workspace passphrase (encrypted link)", defaultInput())
					}),
					layout.Rigid(gap(8, 0).Layout),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return button(gtx, th, a.click("set-create-remote"), "New remote", btnSecondary, func() {
							if a.syn.LoggedIn() {
								a.createRemote()
							} else {
								a.settings.note = "Sign in first."
							}
						})
					}),
				)
			})
		},
		func(gtx layout.Context) layout.Dimensions {
			if a.settings.note == "" {
				return layout.Dimensions{}
			}
			return layout.Inset{Top: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return txt(gtx, th, a.settings.note, 12, colMuted, faceUI, font.Normal)
			})
		},
	)
	return a.settingsStack(gtx, rows)
}

func (a *App) remoteRows() []layout.FlexChild {
	var out []layout.FlexChild
	for i := range a.settings.remote {
		remote := a.settings.remote[i]
		out = append(out, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Top: unit.Dp(3), Bottom: unit.Dp(3)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
					layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
						return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
							layout.Rigid(func(gtx layout.Context) layout.Dimensions {
								return txtClip(gtx, a.th, remote.Name, 13, colText, faceUI, font.Normal)
							}),
							layout.Rigid(gap(8, 0).Layout),
							layout.Rigid(func(gtx layout.Context) layout.Dimensions {
								if remote.Encrypted {
									return badge(gtx, a.th, "encrypted", colAccent)
								}
								return layout.Dimensions{}
							}),
						)
					}),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return button(gtx, a.th, a.click("link:"+remote.ID), "Link", btnGhost, func() {
							a.linkRemote(remote)
						})
					}),
				)
			})
		}))
	}
	return out
}

func (a *App) settingsAutomation(gtx layout.Context) layout.Dimensions {
	th := a.th
	on := a.svc.Store.Setting("sync.auto") == "1"
	mins := atoiSafe(a.svc.Store.Setting("sync.interval"))
	if mins <= 0 {
		mins = 15
	}
	save := func(auto bool, interval int) {
		val := "0"
		if auto {
			val = "1"
		}
		_ = a.svc.Store.SetSetting("sync.auto", val)
		_ = a.svc.Store.SetSetting("sync.interval", itoa(interval))
	}
	intervals := []int{5, 15, 30, 60}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return a.settingsHeading(gtx, "Automatic sync")
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					label := "Off"
					if on {
						label = "On"
					}
					return button(gtx, th, a.click("set-auto-toggle"), "Auto sync: "+label, pick(on, btnSecondary, btnGhost), func() {
						save(!on, mins)
					})
				}),
				layout.Rigid(gap(10, 0).Layout),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					if !on {
						return layout.Dimensions{}
					}
					var kids []layout.FlexChild
					for _, m := range intervals {
						m := m
						kids = append(kids, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return layout.Inset{Right: unit.Dp(4)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
								return button(gtx, th, a.click("set-auto-"+itoa(m)), itoa(m)+"m", pick(mins == m, btnSecondary, btnGhost), func() {
									save(true, m)
								})
							})
						}))
					}
					return layout.Flex{Axis: layout.Horizontal}.Layout(gtx, kids...)
				}),
			)
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return txt(gtx, th, "Syncs on startup and then on the interval while this workspace is linked. The server only sees ciphertext when the vault is on.", 11, colFaint, faceUI, font.Normal)
		}),
	)
}

func (a *App) settingsDevices(gtx layout.Context) layout.Dimensions {
	th := a.th
	Heading := func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				return a.settingsHeading(gtx, "Signed-in devices")
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				if !a.syn.LoggedIn() {
					return layout.Dimensions{}
				}
				label := "Refresh"
				if a.settings.devicesBusy {
					label = "Loading…"
				}
				return button(gtx, th, a.click("set-devices-refresh"), label, btnGhost, func() {
					a.loadDevices()
				})
			}),
		)
	}
	if !a.syn.LoggedIn() {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(Heading),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return txt(gtx, th, "Sign in to manage devices.", 12, colFaint, faceUI, font.Normal)
			}),
		)
	}
	var kids []layout.FlexChild
	kids = append(kids, layout.Rigid(Heading))
	acct := a.syn.Account()
	if len(a.settings.devices) == 0 {
		kids = append(kids, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return txt(gtx, th, "No devices loaded. Press Refresh.", 12, colFaint, faceUI, font.Normal)
		}))
	}
	for i := range a.settings.devices {
		dev := a.settings.devices[i]
		kids = append(kids, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Top: unit.Dp(3), Bottom: unit.Dp(3)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
					layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
						label := dev.Name
						if dev.ID == acct.DeviceID {
							label += " (this device)"
						}
						if dev.LastSeen > 0 {
							label += " · " + humanAgo(dev.LastSeen)
						}
						return txtClip(gtx, th, label, 12, colText, faceUI, font.Normal)
					}),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						if dev.Revoked {
							return badge(gtx, th, "revoked", colMuted)
						}
						if dev.ID == acct.DeviceID {
							return layout.Dimensions{}
						}
						return button(gtx, th, a.click("set-device-revoke:"+dev.ID), "Revoke", btnGhost, func() {
							go func() {
								ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
								defer cancel()
								err := a.syn.RevokeDevice(ctx, dev.ID)
								a.Post(func() {
									if err != nil {
										a.settings.note = err.Error()
										return
									}
									a.settings.note = "Device revoked."
									a.loadDevices()
								})
							}()
						})
					}),
				)
			})
		}))
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx, kids...)
}

func (a *App) settingsVault(gtx layout.Context) layout.Dimensions {
	th := a.th
	status := "Not enabled"
	if a.vaultOn {
		status = "Enabled"
		if a.svc.Store.Unlocked() {
			status += " · unlocked"
		} else {
			status += " · locked"
		}
	}
	rows := []layout.Widget{
		func(gtx layout.Context) layout.Dimensions {
			return a.settingsHeading(gtx, "Local encryption")
		},
		func(gtx layout.Context) layout.Dimensions {
			return a.kvLine(gtx, "Status", status)
		},
		func(gtx layout.Context) layout.Dimensions {
			return txt(gtx, th, "The vault encrypts entity data and request history at rest with XChaCha20-Poly1305. The key is derived from your passphrase with Argon2id.", 12, colMuted, faceUI, font.Normal)
		},
		func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Top: unit.Dp(10)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Horizontal}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						if !a.vaultOn {
							return button(gtx, th, a.click("vault-setup"), "Enable vault", btnPrimary, func() {
								a.openVault("vault-setup")
							})
						}
						if a.locked {
							return button(gtx, th, a.click("vault-unlock"), "Unlock", btnPrimary, func() {
								a.openVault("vault-unlock")
							})
						}
						return button(gtx, th, a.click("vault-lock"), "Lock now", btnSecondary, func() {
							a.lockNow()
						})
					}),
					layout.Rigid(gap(8, 0).Layout),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						if !a.vaultOn || a.locked {
							return layout.Dimensions{}
						}
						return button(gtx, th, a.click("vault-change"), "Change passphrase", btnGhost, func() {
							a.openVault("vault-change")
						})
					}),
				)
			})
		},
		func(gtx layout.Context) layout.Dimensions {
			return a.settingsHeading(gtx, "Auto-lock")
		},
		func(gtx layout.Context) layout.Dimensions {
			mins := atoiSafe(a.svc.Store.Setting("vault.autolock"))
			opts := []struct {
				label string
				value int
			}{{"Off", 0}, {"5 min", 5}, {"15 min", 15}, {"30 min", 30}, {"60 min", 60}}
			var kids []layout.FlexChild
			for _, o := range opts {
				o := o
				kids = append(kids, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.Inset{Right: unit.Dp(4)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return button(gtx, th, a.click("vault-autolock-"+itoa(o.value)), o.label, pick(mins == o.value && a.vaultOn, btnSecondary, btnGhost), func() {
							_ = a.svc.Store.SetSetting("vault.autolock", itoa(o.value))
							a.lastActive = time.Now()
						})
					})
				}))
			}
			return layout.Flex{Axis: layout.Horizontal}.Layout(gtx, kids...)
		},
		func(gtx layout.Context) layout.Dimensions {
			return txt(gtx, th, "The vault locks after this much inactivity and asks for the passphrase again. Keyboard and pointer activity reset the timer.", 11, colFaint, faceUI, font.Normal)
		},
		func(gtx layout.Context) layout.Dimensions {
			return a.settingsHeading(gtx, "What the sync server sees")
		},
		func(gtx layout.Context) layout.Dimensions {
			return txt(gtx, th, "Only opaque ciphertext, document IDs, versions, sizes and timestamps. Names, URLs, headers, tokens, bodies and variables are encrypted before they leave this device.", 12, colMuted, faceUI, font.Normal)
		},
	}
	return a.settingsStack(gtx, rows)
}

func (a *App) settingsStack(gtx layout.Context, widgets []layout.Widget) layout.Dimensions {
	var children []layout.FlexChild
	for i, w := range widgets {
		if i > 0 {
			children = append(children, layout.Rigid(gap(0, 6).Layout))
		}
		w := w
		children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions { return w(gtx) }))
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx, children...)
}

func (a *App) settingsHeading(gtx layout.Context, text string) layout.Dimensions {
	return layout.Inset{Top: unit.Dp(8), Bottom: unit.Dp(2)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return sectionLabel(gtx, a.th, strings.ToUpper(text))
	})
}

func (a *App) kvLine(gtx layout.Context, key, value string) layout.Dimensions {
	return layout.Inset{Top: unit.Dp(2), Bottom: unit.Dp(2)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Horizontal}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return widthOf(gtx, 130, func(gtx layout.Context) layout.Dimensions {
					return txt(gtx, a.th, key, 12, colFaint, faceUI, font.Normal)
				})
			}),
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				return txt(gtx, a.th, value, 12, colText, faceMono, font.Normal)
			}),
		)
	})
}

/* ------------------------------------------------------------------ */
/* Menu layout                                                        */
/* ------------------------------------------------------------------ */

func (a *App) layoutMenu(gtx layout.Context) {
	if a.menu == nil {
		return
	}
	m := a.menu
	backdrop := a.click("menu-backdrop")
	clicked := backdrop.Clicked(gtx)
	layout.Stack{Alignment: layout.Center}.Layout(gtx,
		layout.Expanded(func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Min = gtx.Constraints.Max
			return backdrop.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				fillRect(gtx, gtx.Constraints.Max, alpha(colBg, 0x66))
				return layout.Dimensions{Size: gtx.Constraints.Max}
			})
		}),
		layout.Stacked(func(gtx layout.Context) layout.Dimensions {
			w := gtx.Dp(unit.Dp(300))
			if gtx.Constraints.Max.X-40 < w {
				w = gtx.Constraints.Max.X - 40
			}
			gtx.Constraints.Min.X = w
			gtx.Constraints.Max.X = w
			return panel(gtx, colPanel2, unit.Dp(12), func(gtx layout.Context) layout.Dimensions {
				return layout.UniformInset(unit.Dp(10)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return layout.Inset{Bottom: unit.Dp(8), Left: unit.Dp(6)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
								return txtClip(gtx, a.th, m.title, 12, colFaint, faceUI, font.Medium)
							})
						}),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							var kids []layout.FlexChild
							for i, item := range m.items {
								item := item
								kids = append(kids, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
									return a.menuRow(gtx, i, item)
								}))
							}
							return layout.Flex{Axis: layout.Vertical}.Layout(gtx, kids...)
						}),
					)
				})
			})
		}),
	)
	if clicked {
		a.menu = nil
	}
}

func (a *App) menuRow(gtx layout.Context, i int, item menuItem) layout.Dimensions {
	click := a.click("menu-item-" + itoa(i))
	if click.Clicked(gtx) {
		if a.menu != nil {
			a.menu = nil
		}
		if item.onClick != nil {
			item.onClick()
		}
	}
	c := colText
	if item.danger {
		c = colRed
	}
	return click.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		macro := op.Record(gtx.Ops)
		dims := layout.Inset{Top: unit.Dp(8), Bottom: unit.Dp(8), Left: unit.Dp(8), Right: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					if item.icon == nil {
						return layout.Dimensions{Size: image.Pt(gtx.Dp(unit.Dp(18)), 0)}
					}
					return iconPx(gtx, item.icon, 16, c)
				}),
				layout.Rigid(gap(10, 0).Layout),
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					return txt(gtx, a.th, item.label, 13, c, faceUI, font.Normal)
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

/* ------------------------------------------------------------------ */
/* Small helpers                                                      */
/* ------------------------------------------------------------------ */

func pick[T any](cond bool, a, b T) T {
	if cond {
		return a
	}
	return b
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

func linkedLabel(w *model.Workspace) string {
	if w == nil {
		return "—"
	}
	if !w.Linked() {
		return "local only"
	}
	s := "linked to " + w.Sync.Server
	if w.Sync.LastSyncAt > 0 {
		s += " · " + humanAgo(w.Sync.LastSyncAt)
	}
	return s
}

func mustWorkspaces(list []*model.Workspace, err error) []*model.Workspace {
	if err != nil {
		return nil
	}
	return list
}

func safeFileName(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "workspace"
	}
	repl := func(r rune) rune {
		switch r {
		case '/', '\\', ':', '*', '?', '"', '<', '>', '|', '\n', '\r', '\t':
			return '-'
		}
		return r
	}
	return strings.Map(repl, s)
}

func readLimited(r io.Reader, max int64) ([]byte, error) {
	var buf []byte
	tmp := make([]byte, 32*1024)
	for int64(len(buf)) < max {
		n, err := r.Read(tmp)
		buf = append(buf, tmp[:n]...)
		if err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, os.ErrClosed) {
				return buf, nil
			}
			return buf, err
		}
	}
	return buf, nil
}

func (a *App) wsName() string {
	if w := a.workspace(); w != nil {
		return w.Name
	}
	return "—"
}

var _ = filepath.Join
