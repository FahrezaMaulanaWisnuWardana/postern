package server

import (
	"context"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTeamSchemaUpgradeAndExpiredInvitation(t *testing.T) {
	cfg := Config{DataDir: t.TempDir(), JWTSecret: "0123456789abcdef0123456789abcdef"}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	s, err := New(cfg, logger)
	if err != nil {
		t.Fatal(err)
	}
	// Simulate an existing installation with the pre-team schema.
	if _, err := s.db.Exec(`ALTER TABLE workspaces DROP COLUMN key_scope`); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = New(cfg, logger)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, query := range []string{
		`INSERT INTO users(id,email,pass_hash,created_at) VALUES('owner','owner@test.example','unused',1),('recipient','recipient@test.example','unused',1)`,
		`INSERT INTO workspaces(id,owner_id,name,encrypted,created_at) VALUES('workspace','owner','Team',0,1)`,
		`INSERT INTO workspace_members(workspace_id,user_id,role,kdf_salt,wrapped_key) VALUES('workspace','owner','owner','','')`,
		`INSERT INTO workspace_invitations(id,workspace_id,email,role,invited_by,expires_at) VALUES('expired','workspace','recipient@test.example','editor','owner',1)`,
	} {
		if _, err := s.db.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	var scope string
	if err := s.db.QueryRow(`SELECT key_scope FROM workspaces WHERE id='workspace'`).Scan(&scope); err != nil || scope != "legacy" {
		t.Fatalf("legacy schema was not safely upgraded: %q %v", scope, err)
	}
	request := httptest.NewRequest("POST", "/v1/invitations/expired/accept", strings.NewReader("{}"))
	request.SetPathValue("invite", "expired")
	request = request.WithContext(context.WithValue(request.Context(), identityKey{}, identity{UID: "recipient"}))
	recorder := httptest.NewRecorder()
	s.handleAcceptInvitation(recorder, request)
	if recorder.Code != 404 {
		t.Fatalf("expired invitation accepted: %d %s", recorder.Code, recorder.Body.String())
	}
	var members int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM workspace_members WHERE user_id='recipient'`).Scan(&members); err != nil || members != 0 {
		t.Fatal("expired invitation created membership")
	}
}
