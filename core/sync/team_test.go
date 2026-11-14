package sync_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"strings"
	"testing"

	"yourpm/core/crypto"
	"yourpm/core/model"
	"yourpm/core/service"
)

func TestEncryptedTeamSyncWithIndependentVaults(t *testing.T) {
	url := newServer(t)
	ctx := context.Background()
	owner, editor, viewer := openDevice(t), openDevice(t), openDevice(t)
	for i, d := range []*device{owner, editor, viewer} {
		email := []string{"owner@team.test", "editor@team.test", "viewer@team.test"}[i]
		if _, err := d.client.Login(ctx, url, email, "account-password", true); err != nil {
			t.Fatal(err)
		}
		if err := d.svc.VaultSetup("private vault " + email); err != nil {
			t.Fatal(err)
		}
	}
	ownerKey := append([]byte(nil), owner.svc.Store.Key()...)
	editorKey := append([]byte(nil), editor.svc.Store.Key()...)
	remote, err := owner.client.CreateEncryptedRemote(ctx, "Engineering", "owner workspace pass")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := owner.client.Link(ctx, owner.ws, *remote, "owner workspace pass"); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(ownerKey, owner.svc.Store.Key()) {
		t.Fatal("link replaced owner's private vault key")
	}
	secret, err := owner.svc.NewEntity(owner.ws.ID, model.KindRequest, "", "Team request")
	if err != nil {
		t.Fatal(err)
	}
	_, err = owner.client.Run(ctx, owner.ws)
	if err != nil {
		t.Fatal(err)
	}
	join := func(d *device, email, role, pass string) *model.RemoteWorkspace {
		t.Helper()
		inv, code, err := owner.client.InviteMember(ctx, owner.ws, email, role)
		if err != nil {
			t.Fatal(err)
		}
		pending, err := d.client.Invitations(ctx)
		if err != nil || len(pending) != 1 {
			t.Fatalf("pending invitations: %v %v", pending, err)
		}
		if _, err := d.client.AcceptInvitation(ctx, pending[0], "wrong code", pass); err == nil {
			t.Fatal("accepted an incorrect unlock code")
		}
		joined, err := d.client.AcceptInvitation(ctx, pending[0], code, pass)
		if err != nil {
			t.Fatal(err)
		}
		if joined.ID != inv.WorkspaceID || joined.Role != role {
			t.Fatalf("unexpected membership: %+v", joined)
		}
		if joined.WrappedKey == remote.WrappedKey {
			t.Fatal("recipient received owner's passphrase envelope")
		}
		if _, err := d.client.Link(ctx, d.ws, *joined, pass); err != nil {
			t.Fatal(err)
		}
		res, err := d.client.Run(ctx, d.ws)
		if err != nil || len(res.Errors) > 0 {
			t.Fatalf("joined sync: %+v %v", res, err)
		}
		got, err := d.svc.Store.GetEntity(secret.ID)
		if err != nil || got.Name != "Team request" {
			t.Fatalf("shared request did not arrive: %+v %v", got, err)
		}
		return joined
	}
	editorRemote := join(editor, "editor@team.test", "editor", "editor workspace pass")
	join(viewer, "viewer@team.test", "viewer", "viewer workspace pass")
	got, err := editor.svc.Store.GetEntity(secret.ID)
	if err != nil {
		t.Fatal(err)
	}
	got.Name = "Updated by teammate"
	if err := editor.svc.SaveEntity(got); err != nil {
		t.Fatal(err)
	}
	if res, err := editor.client.Run(ctx, editor.ws); err != nil || res.Pushed == 0 {
		t.Fatalf("editor write did not sync: %+v %v", res, err)
	}
	if _, err := owner.client.Run(ctx, owner.ws); err != nil {
		t.Fatal(err)
	}
	got, err = owner.svc.Store.GetEntity(secret.ID)
	if err != nil || got.Name != "Updated by teammate" {
		t.Fatalf("owner did not receive teammate's change: %+v %v", got, err)
	}
	if !bytes.Equal(editorKey, editor.svc.Store.Key()) {
		t.Fatal("joining a team replaced editor's private vault key")
	}
	// The shared key cannot open private local ciphertext.
	salt, _ := base64.RawStdEncoding.DecodeString(editorRemote.KDFSalt)
	teamKey, err := crypto.UnlockVault("editor workspace pass", salt, editorRemote.WrappedKey)
	if err != nil || bytes.Equal(teamKey, editorKey) || bytes.Equal(teamKey, ownerKey) {
		t.Fatal("team and private vault keys are not isolated")
	}
	private, err := editor.svc.CreateWorkspace("Private")
	if err != nil {
		t.Fatal(err)
	}
	privateEntity, err := editor.svc.NewEntity(private.ID, model.KindRequest, "", "Private token")
	if err != nil {
		t.Fatal(err)
	}
	blob, _, _, _, err := editor.svc.Store.EntityBlob(privateEntity.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := crypto.Open(teamKey, string(blob), crypto.EntityAAD(privateEntity.ID)); err == nil {
		t.Fatal("team key decrypts private data")
	}
	for key := range editor.svc.Store.PublicSettings() {
		if strings.HasPrefix(key, "sync.key.") {
			t.Fatal("public settings expose workspace key material")
		}
	}
	// A teammate can recover their membership on another device without the
	// invitation code or the owner's workspace/vault passphrase.
	second := openDevice(t)
	if _, err := second.client.Login(ctx, url, "editor@team.test", "account-password", false); err != nil {
		t.Fatal(err)
	}
	list, err := second.client.RemoteWorkspaces(ctx)
	if err != nil || len(list) != 1 {
		t.Fatalf("second-device memberships: %+v %v", list, err)
	}
	if _, err := second.client.Link(ctx, second.ws, list[0], "editor workspace pass"); err != nil {
		t.Fatal(err)
	}
	if res, err := second.client.Run(ctx, second.ws); err != nil || len(res.Errors) > 0 {
		t.Fatalf("second-device sync: %+v %v", res, err)
	}
	if got, err := second.svc.Store.GetEntity(secret.ID); err != nil || got.Name != "Updated by teammate" {
		t.Fatalf("second device did not decrypt team data: %+v %v", got, err)
	}
	// Check the stored key survives locking, reopening and unlocking the
	// recipient's local vault with its original private passphrase.
	dir := editor.svc.DataDir
	if err := editor.svc.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := service.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	if err := reopened.VaultUnlock("private vault editor@team.test"); err != nil {
		t.Fatal(err)
	}
	// Keep subsequent operations on the reopened store.
	editor.svc = reopened
	editor.client.Store = reopened.Store
	// A viewer with local experiments can still pull, but never pushes.
	if _, err := viewer.svc.NewEntity(viewer.ws.ID, model.KindRequest, "", "Local experiment"); err != nil {
		t.Fatal(err)
	}
	res, err := viewer.client.Run(ctx, viewer.ws)
	if err != nil || res.Pushed != 0 {
		t.Fatalf("viewer must be pull-only: %+v %v", res, err)
	}
	team, err := owner.client.Team(ctx, remote.ID)
	if err != nil {
		t.Fatal(err)
	}
	var editorID string
	for _, m := range team.Members {
		if m.Email == "editor@team.test" {
			editorID = m.ID
		}
	}
	if err := owner.client.ChangeMemberRole(ctx, remote.ID, editorID, "viewer"); err != nil {
		t.Fatal(err)
	}
	res, err = editor.client.Run(ctx, editor.ws)
	if err != nil || res.Pushed != 0 {
		t.Fatalf("demotion not respected: %+v %v", res, err)
	}
	if err := owner.client.RemoveMember(ctx, remote.ID, editorID); err != nil {
		t.Fatal(err)
	}
	if _, err := editor.client.Run(ctx, editor.ws); err == nil {
		t.Fatal("removed member can still sync")
	}
}

func TestLegacyWorkspaceCannotSharePrivateVaultKey(t *testing.T) {
	url := newServer(t)
	ctx := context.Background()
	d := openDevice(t)
	if _, err := d.client.Login(ctx, url, "legacy@example.com", "account-password", true); err != nil {
		t.Fatal(err)
	}
	salt, wrapped, _, err := vaultKey("legacy workspace pass")
	if err != nil {
		t.Fatal(err)
	}
	remote, err := d.client.CreateRemote(ctx, "Legacy", true, salt, wrapped)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.client.Link(ctx, d.ws, *remote, "legacy workspace pass"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := d.client.InviteMember(ctx, d.ws, "new@example.com", "editor"); err == nil || !strings.Contains(err.Error(), "legacy") {
		t.Fatalf("legacy sharing should require migration: %v", err)
	}
}
