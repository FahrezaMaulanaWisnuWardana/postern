package ui

import (
	"image"
	"testing"

	"gioui.org/layout"
	"gioui.org/widget"

	"yourpm/core/model"
	"yourpm/core/service"
	syncl "yourpm/core/sync"
)

func TestTeamSettingsFitSmallWindowAndScroll(t *testing.T) {
	svc, err := service.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer svc.Close()
	for key, value := range map[string]string{"sync.server": "https://sync.example.com", "sync.email": "owner@example.com", "sync.token": "test-token"} {
		if err := svc.Store.SetSetting(key, value); err != nil {
			t.Fatal(err)
		}
	}
	a := &App{svc: svc, syn: syncl.New(svc.Store), th: newTheme(), ic: loadIcons(), clicks: map[string]*widget.Clickable{}, wsID: "local"}
	a.workspaces = []*model.Workspace{{ID: "local", Sync: model.Sync{Server: "https://sync.example.com", RemoteID: "remote"}}}
	a.settings.tab = 3
	a.settings.teamRemote, a.settings.teamAccount = a.teamContext()
	a.settings.team = &model.WorkspaceTeam{Role: "owner"}
	for i := 0; i < 30; i++ {
		a.settings.team.Members = append(a.settings.team.Members, model.WorkspaceMember{ID: itoa(i), Email: "teammate" + itoa(i) + "@example.com", Role: "editor"})
	}
	a.settings.scroll.Axis = layout.Vertical
	gtx := testContext(image.Pt(736, 440), 1)
	dims := a.dialogBody(gtx, &dialog{kind: "settings", title: "Settings"})
	if dims.Size.Y > 440 || dims.Size.X > 736 {
		t.Fatalf("settings overflow the dialog: %v", dims.Size)
	}
	if a.settings.scroll.Position.Length <= 440 {
		t.Fatal("long member list should have scrollable content")
	}
}
