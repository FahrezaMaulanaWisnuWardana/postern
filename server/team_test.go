package server_test

import (
	"net/http"
	"testing"
)

func registerTeamAccount(t *testing.T, base, email string) *api {
	t.Helper()
	a := &api{t: t, base: base}
	status, body := a.do("POST", "/v1/auth/register", map[string]string{"email": email, "password": "team-password", "device": "team-test"})
	if status != http.StatusCreated {
		t.Fatalf("register %s: %d %v", email, status, body)
	}
	a.token = body["token"].(string)
	return a
}

func TestTeamInvitationAndRolePermissions(t *testing.T) {
	root := newAPI(t)
	owner := registerTeamAccount(t, root.base, "owner@example.com")
	admin := registerTeamAccount(t, root.base, "admin@example.com")
	editor := registerTeamAccount(t, root.base, "editor@example.com")
	viewer := registerTeamAccount(t, root.base, "viewer@example.com")
	outsider := registerTeamAccount(t, root.base, "outsider@example.com")
	status, body := owner.do("POST", "/v1/workspaces", map[string]any{"name": "Team", "encrypted": false})
	if status != 201 {
		t.Fatal(body)
	}
	wsID := body["id"].(string)
	path := "/v1/workspaces/" + wsID
	expect := func(actor *api, method, path string, body any, want int) map[string]any {
		t.Helper()
		status, out := actor.do(method, path, body)
		if status != want {
			t.Fatalf("%s %s: got %d want %d: %v", method, path, status, want, out)
		}
		return out
	}
	join := func(actor *api, email, role string) {
		t.Helper()
		inv := expect(owner, "POST", path+"/invitations", map[string]string{"email": email, "role": role}, 201)
		id := inv["id"].(string)
		expect(outsider, "POST", "/v1/invitations/"+id+"/accept", map[string]string{}, 404)
		expect(actor, "GET", path, nil, 404)
		accepted := expect(actor, "POST", "/v1/invitations/"+id+"/accept", map[string]string{}, 200)
		if accepted["role"] != role {
			t.Fatalf("accepted role = %v", accepted["role"])
		}
		expect(actor, "POST", "/v1/invitations/"+id+"/accept", map[string]string{}, 404)
	}
	join(admin, "admin@example.com", "admin")
	join(editor, "editor@example.com", "editor")
	join(viewer, "viewer@example.com", "viewer")
	expect(viewer, "POST", path+"/push", map[string]any{"ops": []any{}}, 403)
	expect(editor, "POST", path+"/push", map[string]any{"ops": []any{}}, 200)
	expect(viewer, "GET", path+"/pull", nil, 200)
	expect(outsider, "GET", path+"/pull", nil, 403)
	expect(editor, "POST", path+"/invitations", map[string]string{"email": "another@example.com", "role": "viewer"}, 403)
	expect(admin, "POST", path+"/invitations", map[string]string{"email": "another@example.com", "role": "admin"}, 403)
	expect(owner, "POST", path+"/invitations", map[string]string{"email": "another@example.com", "role": "owner"}, 400)
	team := expect(owner, "GET", path+"/team", nil, 200)
	members := map[string]string{}
	for _, raw := range team["members"].([]any) {
		m := raw.(map[string]any)
		members[m["email"].(string)] = m["id"].(string)
	}
	expect(admin, "DELETE", path+"/members/"+members["owner@example.com"], nil, 403)
	expect(owner, "PATCH", path+"/members/"+members["owner@example.com"], map[string]string{"role": "viewer"}, 403)
	expect(admin, "PATCH", path+"/members/"+members["editor@example.com"], map[string]string{"role": "admin"}, 403)
	expect(owner, "PATCH", path+"/members/"+members["editor@example.com"], map[string]string{"role": "viewer"}, 204)
	expect(editor, "POST", path+"/push", map[string]any{"ops": []any{}}, 403)
	expect(admin, "DELETE", path+"/members/"+members["viewer@example.com"], nil, 204)
	expect(viewer, "GET", path+"/pull", nil, 403)
	expect(viewer, "GET", path, nil, 404)
	expect(viewer, "GET", path+"/team", nil, 403)
	expect(outsider, "GET", path+"/team", nil, 403)

	inv := expect(admin, "POST", path+"/invitations", map[string]string{"email": "outsider@example.com", "role": "editor"}, 201)
	inviteID := inv["id"].(string)
	// Removing an inviter also invalidates invitations they issued.
	expect(owner, "DELETE", path+"/members/"+members["admin@example.com"], nil, 204)
	expect(outsider, "POST", "/v1/invitations/"+inviteID+"/accept", map[string]string{}, 404)

	inv = expect(owner, "POST", path+"/invitations", map[string]string{"email": "outsider@example.com", "role": "viewer"}, 201)
	inviteID = inv["id"].(string)
	again := expect(owner, "POST", path+"/invitations", map[string]string{"email": "outsider@example.com", "role": "editor"}, 201)
	if again["id"] == inviteID {
		t.Fatal("reissue must invalidate the previous invitation")
	}
	expect(outsider, "POST", "/v1/invitations/"+inviteID+"/accept", map[string]string{}, 404)
	expect(owner, "DELETE", path+"/invitations/"+again["id"].(string), nil, 204)
	expect(outsider, "POST", "/v1/invitations/"+again["id"].(string)+"/accept", map[string]string{}, 404)
}
