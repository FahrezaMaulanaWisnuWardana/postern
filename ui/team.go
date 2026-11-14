package ui

import (
	"context"
	"io"
	"strings"
	"time"

	"gioui.org/font"
	"gioui.org/io/clipboard"
	"gioui.org/layout"

	"yourpm/core/model"
)

func (a *App) teamContext() (string, string) {
	acct := a.syn.Account()
	remote := ""
	if ws := a.workspace(); ws != nil && ws.Linked() && ws.Sync.Server == acct.Server {
		remote = ws.Sync.RemoteID
	}
	return remote, acct.Server + "\n" + acct.Email
}

func (a *App) loadTeam() {
	if a.settings.teamBusy || !a.syn.LoggedIn() {
		return
	}
	remote, account := a.teamContext()
	if remote != a.settings.teamRemote || account != a.settings.teamAccount {
		a.settings.team = nil
		a.settings.invitations = nil
		a.settings.inviteCode.SetText("")
	}
	a.settings.teamRemote, a.settings.teamAccount = remote, account
	a.settings.teamBusy = true
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		invitations, err := a.syn.Invitations(ctx)
		var team *model.WorkspaceTeam
		if err == nil && remote != "" {
			team, err = a.syn.Team(ctx, remote)
		}
		a.Post(func() {
			a.settings.teamBusy = false
			currentRemote, currentAccount := a.teamContext()
			if currentRemote != remote || currentAccount != account {
				return
			}
			if invitations != nil {
				a.settings.invitations = invitations
			}
			if err != nil {
				a.settings.teamNote = err.Error()
				a.settings.team = nil
				return
			}
			a.settings.invitations, a.settings.team = invitations, team
		})
	}()
}

type teamActionResult struct {
	message string
	code    string
	localID string
}

func (a *App) teamAction(action func(context.Context) (teamActionResult, error)) {
	if a.settings.teamBusy {
		return
	}
	if a.syncing || a.settings.busy {
		a.settings.teamNote = "Wait for the current sync or sign-in to finish, then try again."
		return
	}
	remote, account := a.teamContext()
	a.settings.teamBusy = true
	a.settings.teamNote = ""
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		res, err := action(ctx)
		a.Post(func() {
			a.settings.teamBusy = false
			currentRemote, currentAccount := a.teamContext()
			if account != currentAccount {
				return
			}
			if res.localID == "" && remote != currentRemote {
				a.settings.teamNote = "Team action finished for the previous workspace. Refresh to see the current team."
				a.settings.inviteCode.SetText("")
				return
			}
			if err != nil {
				a.settings.teamNote = err.Error()
				return
			}
			a.settings.teamNote = res.message
			if res.code != "" && !a.locked {
				a.settings.inviteCode.SetText(res.code)
			}
			a.settings.joinCode.SetText("")
			a.settings.joinPass.SetText("")
			if res.localID != "" {
				a.vaultOn = a.svc.Store.VaultEnabled()
				a.workspaces = mustWorkspaces(a.svc.Store.ListWorkspaces())
				a.setWorkspace(res.localID)
			}
			a.loadTeam()
			a.loadRemoteWorkspaces()
		})
	}()
}

func (a *App) settingsTeam(gtx layout.Context) layout.Dimensions {
	th := a.th
	rows := []layout.Widget{
		func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions { return a.settingsHeading(gtx, "Team workspace") }),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return button(gtx, th, a.click("team-refresh"), pick(a.settings.teamBusy, "Working…", "Refresh"), btnGhost, a.loadTeam)
				}),
			)
		},
		func(gtx layout.Context) layout.Dimensions {
			return txt(gtx, th, "Share collections and environments through your sync server. Each teammate uses their own account. Sync is manual or interval-based, not live editing.", 12, colMuted, faceUI, font.Normal)
		},
	}
	if !a.syn.LoggedIn() {
		rows = append(rows, func(gtx layout.Context) layout.Dimensions {
			return txt(gtx, th, "Sign in under Sync first. Everyone must use the same server.", 12, colFaint, faceUI, font.Normal)
		})
		return a.settingsStack(gtx, rows)
	}
	remote, account := a.teamContext()
	team := a.settings.team
	if a.settings.teamRemote != remote || a.settings.teamAccount != account {
		team = nil
	}
	if remote == "" {
		rows = append(rows, func(gtx layout.Context) layout.Dimensions {
			return txt(gtx, th, "To invite teammates, create or link a remote workspace under Sync. You can accept invitations below without linking your personal workspace.", 12, colMuted, faceUI, font.Normal)
		})
	}
	if team != nil {
		rows = append(rows, func(gtx layout.Context) layout.Dimensions {
			return a.kvLine(gtx, "Your role", team.Role)
		})
		manage := team.Role == "owner" || team.Role == "admin"
		for _, member := range team.Members {
			member := member
			rows = append(rows, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
					layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
						return txtClip(gtx, th, member.Email+" · "+member.Role, 12, colText, faceUI, font.Normal)
					}),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						if !manage || member.Role == "owner" || (team.Role != "owner" && member.Role == "admin") {
							return layout.Dimensions{}
						}
						return button(gtx, th, a.click("team-role:"+member.ID), "Role", btnGhost, func() {
							m := &menu{title: "Member role"}
							for _, role := range []string{"admin", "editor", "viewer"} {
								role := role
								if role == "admin" && team.Role != "owner" {
									continue
								}
								m.items = append(m.items, menuItem{label: role, onClick: func() {
									a.teamAction(func(ctx context.Context) (teamActionResult, error) {
										return teamActionResult{message: "Member role updated."}, a.syn.ChangeMemberRole(ctx, remote, member.ID, role)
									})
								}})
							}
							a.menu = m
						})
					}),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						if !manage || member.Role == "owner" || (team.Role != "owner" && member.Role == "admin") {
							return layout.Dimensions{}
						}
						return button(gtx, th, a.click("team-remove:"+member.ID), "Remove", btnGhost, func() {
							a.confirm("Remove "+member.Email+"?", "This stops future sync access. It cannot erase downloaded data or rotate the workspace key. Rotate exposed API credentials separately.", "Remove", func() {
								a.dialog = nil
								a.teamAction(func(ctx context.Context) (teamActionResult, error) {
									return teamActionResult{message: "Member removed."}, a.syn.RemoveMember(ctx, remote, member.ID)
								})
							})
						})
					}),
				)
			})
		}
		if manage {
			rows = append(rows, a.teamInviteForm)
			for _, inv := range team.Invitations {
				inv := inv
				rows = append(rows, func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
						layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
							return txtClip(gtx, th, "Pending: "+inv.Email+" · "+inv.Role, 12, colMuted, faceUI, font.Normal)
						}),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							if inv.Role == "admin" && team.Role != "owner" {
								return layout.Dimensions{}
							}
							return button(gtx, th, a.click("team-revoke:"+inv.ID), "Revoke", btnGhost, func() {
								a.teamAction(func(ctx context.Context) (teamActionResult, error) {
									return teamActionResult{message: "Invitation revoked."}, a.syn.RevokeInvitation(ctx, remote, inv.ID)
								})
							})
						}),
					)
				})
			}
		}
	}
	rows = append(rows,
		func(gtx layout.Context) layout.Dimensions {
			return a.settingsHeading(gtx, "Invitations for your account")
		},
		func(gtx layout.Context) layout.Dimensions {
			if len(a.settings.invitations) != 0 {
				return layout.Dimensions{}
			}
			return txt(gtx, th, "No pending invitations. Press Refresh after a teammate invites your sign-in email.", 12, colFaint, faceUI, font.Normal)
		})
	if len(a.settings.invitations) > 0 {
		rows = append(rows,
			func(gtx layout.Context) layout.Dimensions {
				a.settings.joinCode.SingleLine, a.settings.joinCode.Mask = true, '•'
				return input(gtx, th, &a.settings.joinCode, "Invitation unlock code (from your teammate)", defaultInput())
			},
			func(gtx layout.Context) layout.Dimensions {
				a.settings.joinPass.SingleLine, a.settings.joinPass.Mask = true, '•'
				return input(gtx, th, &a.settings.joinPass, "Choose your own workspace passphrase (10+ characters)", defaultInput())
			},
			func(gtx layout.Context) layout.Dimensions {
				return txt(gtx, th, "For encrypted invitations, enter the privately shared code and choose your own passphrase. If you have no local vault, this passphrase also sets one up. Keep it for linking another device.", 11, colFaint, faceUI, font.Normal)
			})
	}
	for _, inv := range a.settings.invitations {
		inv := inv
		rows = append(rows, func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					return txtClip(gtx, th, inv.WorkspaceName+" · "+inv.Role+pick(inv.Encrypted, " · encrypted", " · unencrypted"), 12, colText, faceUI, font.Normal)
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return button(gtx, th, a.click("team-accept:"+inv.ID), "Accept", btnSecondary, func() {
						code, pass := a.settings.joinCode.Text(), a.settings.joinPass.Text()
						a.teamAction(func(ctx context.Context) (teamActionResult, error) {
							remote, err := a.syn.AcceptInvitation(ctx, inv, code, pass)
							if err != nil {
								return teamActionResult{}, err
							}
							local, err := a.svc.CreateWorkspace(remote.Name)
							if err != nil {
								return teamActionResult{}, err
							}
							if _, err = a.syn.Link(ctx, local, *remote, pass); err != nil {
								return teamActionResult{}, err
							}
							res, err := a.syn.Run(ctx, local)
							if err == nil && len(res.Errors) > 0 {
								return teamActionResult{localID: local.ID, message: "Joined; sync needs attention: " + res.Errors[0]}, nil
							}
							if err != nil {
								return teamActionResult{localID: local.ID, message: "Joined; retry Sync now: " + err.Error()}, nil
							}
							return teamActionResult{localID: local.ID, message: "Joined “" + remote.Name + "”."}, nil
						})
					})
				}),
			)
		})
	}
	rows = append(rows, func(gtx layout.Context) layout.Dimensions {
		return txt(gtx, th, a.settings.teamNote, 12, colMuted, faceUI, font.Normal)
	})
	return a.settingsStack(gtx, rows)
}

func (a *App) teamInviteForm(gtx layout.Context) layout.Dimensions {
	if a.settings.inviteRole == "" {
		a.settings.inviteRole = "editor"
	}
	rows := []layout.Widget{
		func(gtx layout.Context) layout.Dimensions { return a.settingsHeading(gtx, "Invite a teammate") },
		func(gtx layout.Context) layout.Dimensions {
			a.settings.inviteEmail.SingleLine = true
			return input(gtx, a.th, &a.settings.inviteEmail, "Teammate's sign-in email", defaultInput())
		},
		func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return button(gtx, a.th, a.click("team-invite-role"), "Role: "+a.settings.inviteRole, btnSecondary, func() {
						m := &menu{title: "Invitation role"}
						for _, role := range []string{"admin", "editor", "viewer"} {
							role := role
							if role == "admin" && a.settings.team.Role != "owner" {
								continue
							}
							m.items = append(m.items, menuItem{label: role, onClick: func() { a.settings.inviteRole = role }})
						}
						a.menu = m
					})
				}),
				layout.Rigid(gap(8, 0).Layout),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return button(gtx, a.th, a.click("team-invite"), "Create invitation", btnPrimary, func() {
						ws := a.workspace()
						if ws == nil {
							return
						}
						local := *ws
						email, role := a.settings.inviteEmail.Text(), a.settings.inviteRole
						a.settings.inviteCode.SetText("")
						a.teamAction(func(ctx context.Context) (teamActionResult, error) {
							_, code, err := a.syn.InviteMember(ctx, &local, email, role)
							return teamActionResult{message: "Invitation created (expires in 7 days). No email is sent. Ask your teammate to sign in and refresh Team; send the unlock code privately.", code: code}, err
						})
					})
				}),
			)
		},
		func(gtx layout.Context) layout.Dimensions {
			return txt(gtx, a.th, "Editors can sync changes; viewers can only pull. Admins can manage editors and viewers. Shared environments and request credentials are visible to members.", 11, colFaint, faceUI, font.Normal)
		},
	}
	if a.settings.inviteCode.Text() != "" {
		rows = append(rows,
			func(gtx layout.Context) layout.Dimensions {
				a.settings.inviteCode.ReadOnly, a.settings.inviteCode.SingleLine = true, true
				return input(gtx, a.th, &a.settings.inviteCode, "", defaultInput())
			},
			func(gtx layout.Context) layout.Dimensions {
				return button(gtx, a.th, a.click("team-code-copy"), "Copy unlock code", btnSecondary, func() {
					gtx.Execute(clipboard.WriteCmd{Type: "application/text", Data: io.NopCloser(strings.NewReader(a.settings.inviteCode.Text()))})
				})
			})
	}
	return a.settingsStack(gtx, rows)
}
