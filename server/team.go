package server

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/mail"
	"strings"
	"time"

	"yourpm/core/crypto"
	"yourpm/core/model"
)

func manages(role string) bool { return role == "owner" || role == "admin" }
func validMemberRole(role string) bool {
	return role == "admin" || role == "editor" || role == "viewer"
}

func validEnvelope(salt, wrapped string) bool {
	s, e1 := base64.RawStdEncoding.DecodeString(salt)
	k, e2 := base64.RawStdEncoding.DecodeString(wrapped)
	return e1 == nil && e2 == nil && len(s) == crypto.SaltSize && len(k) == 24+crypto.KeySize+16
}

func (s *Server) handleTeam(w http.ResponseWriter, r *http.Request) {
	idn, _ := identityFrom(r)
	wsID := r.PathValue("id")
	role, err := s.roleFor(wsID, idn.UID)
	if err != nil {
		writeErr(w, http.StatusForbidden, "no access to this workspace")
		return
	}
	out := model.WorkspaceTeam{Role: role, Members: []model.WorkspaceMember{}, Invitations: []model.WorkspaceInvitation{}}
	rows, err := s.db.Query(`SELECT u.id, u.email, CASE WHEN w.owner_id=u.id THEN 'owner' ELSE m.role END
		FROM workspace_members m JOIN users u ON u.id=m.user_id JOIN workspaces w ON w.id=m.workspace_id
		WHERE m.workspace_id=? ORDER BY u.email`, wsID)
	if err != nil {
		writeErr(w, 500, "cannot list members")
		return
	}
	for rows.Next() {
		var m model.WorkspaceMember
		if err := rows.Scan(&m.ID, &m.Email, &m.Role); err != nil {
			rows.Close()
			writeErr(w, 500, "cannot read members")
			return
		}
		out.Members = append(out.Members, m)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		writeErr(w, 500, "cannot read members")
		return
	}
	if manages(role) {
		rows, err = s.db.Query(`SELECT id,email,role,expires_at FROM workspace_invitations WHERE workspace_id=? AND expires_at>? ORDER BY email`, wsID, nowMs())
		if err != nil {
			writeErr(w, 500, "cannot list invitations")
			return
		}
		defer rows.Close()
		for rows.Next() {
			inv := model.WorkspaceInvitation{WorkspaceID: wsID}
			if err := rows.Scan(&inv.ID, &inv.Email, &inv.Role, &inv.ExpiresAt); err != nil {
				writeErr(w, 500, "cannot read invitations")
				return
			}
			out.Invitations = append(out.Invitations, inv)
		}
		if rows.Err() != nil {
			writeErr(w, 500, "cannot read invitations")
			return
		}
	}
	writeJSON(w, 200, out)
}

func (s *Server) handleInvite(w http.ResponseWriter, r *http.Request) {
	idn, _ := identityFrom(r)
	wsID := r.PathValue("id")
	var req model.WorkspaceInvitation
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&req) != nil {
		writeErr(w, 400, "invalid JSON")
		return
	}
	req.Email = strings.ToLower(strings.TrimSpace(req.Email))
	addr, err := mail.ParseAddress(req.Email)
	if err != nil || addr.Address != req.Email || len(req.Email) > 254 || !validMemberRole(req.Role) {
		writeErr(w, 400, "valid email and role (admin, editor, viewer) are required")
		return
	}
	tx, err := s.db.Begin()
	if err != nil {
		writeErr(w, 500, "database busy")
		return
	}
	defer tx.Rollback()
	role, err := roleFor(tx, wsID, idn.UID)
	if err != nil || !manages(role) || (req.Role == "admin" && role != "owner") {
		writeErr(w, 403, "only owners can invite admins; owners and admins can invite editors or viewers")
		return
	}
	var enc int
	var keyScope string
	if err := tx.QueryRow(`SELECT name,encrypted,key_scope FROM workspaces WHERE id=?`, wsID).Scan(&req.WorkspaceName, &enc, &keyScope); err != nil {
		writeErr(w, 404, "workspace not found")
		return
	}
	if enc == 1 && keyScope != "workspace" {
		writeErr(w, 409, "legacy workspace uses a private vault key; create a new encrypted remote workspace before inviting teammates")
		return
	}
	if enc == 1 && !validEnvelope(req.KDFSalt, req.WrappedKey) {
		writeErr(w, 400, "encrypted invitations require a valid wrapped key and salt")
		return
	}
	var exists int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM workspace_members m JOIN users u ON u.id=m.user_id WHERE m.workspace_id=? AND u.email=?`, wsID, req.Email).Scan(&exists); err != nil {
		writeErr(w, 500, "cannot check membership")
		return
	}
	if exists > 0 {
		writeErr(w, 409, "this account is already a member")
		return
	}
	req.ID, req.WorkspaceID, req.Encrypted = newID(), wsID, enc == 1
	req.ExpiresAt = time.Now().Add(7 * 24 * time.Hour).UnixMilli()
	// Reissuing an invitation invalidates its previous ID and unlock code.
	_, err = tx.Exec(`INSERT INTO workspace_invitations(id,workspace_id,email,role,invited_by,kdf_salt,wrapped_key,expires_at)
		VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(workspace_id,email) DO UPDATE SET id=excluded.id,role=excluded.role,
		invited_by=excluded.invited_by,kdf_salt=excluded.kdf_salt,wrapped_key=excluded.wrapped_key,expires_at=excluded.expires_at`,
		req.ID, wsID, req.Email, req.Role, idn.UID, req.KDFSalt, req.WrappedKey, req.ExpiresAt)
	if err != nil || tx.Commit() != nil {
		writeErr(w, 500, "cannot create invitation")
		return
	}
	req.KDFSalt, req.WrappedKey = "", ""
	writeJSON(w, 201, req)
}

func (s *Server) handleInvitations(w http.ResponseWriter, r *http.Request) {
	idn, _ := identityFrom(r)
	rows, err := s.db.Query(`SELECT i.id,i.workspace_id,w.name,i.email,i.role,w.encrypted,i.kdf_salt,i.wrapped_key,i.expires_at
		FROM workspace_invitations i JOIN workspaces w ON w.id=i.workspace_id JOIN users u ON u.email=i.email
		WHERE u.id=? AND i.expires_at>? ORDER BY i.expires_at`, idn.UID, nowMs())
	if err != nil {
		writeErr(w, 500, "cannot list invitations")
		return
	}
	defer rows.Close()
	out := []model.WorkspaceInvitation{}
	for rows.Next() {
		var inv model.WorkspaceInvitation
		var enc int
		if err := rows.Scan(&inv.ID, &inv.WorkspaceID, &inv.WorkspaceName, &inv.Email, &inv.Role, &enc, &inv.KDFSalt, &inv.WrappedKey, &inv.ExpiresAt); err != nil {
			writeErr(w, 500, "cannot read invitations")
			return
		}
		inv.Encrypted = enc == 1
		out = append(out, inv)
	}
	if rows.Err() != nil {
		writeErr(w, 500, "cannot read invitations")
		return
	}
	writeJSON(w, 200, map[string]any{"invitations": out})
}

func (s *Server) handleAcceptInvitation(w http.ResponseWriter, r *http.Request) {
	idn, _ := identityFrom(r)
	var req struct {
		KDFSalt    string `json:"kdfSalt"`
		WrappedKey string `json:"wrappedKey"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&req) != nil {
		writeErr(w, 400, "invalid JSON")
		return
	}
	tx, err := s.db.Begin()
	if err != nil {
		writeErr(w, 500, "database busy")
		return
	}
	defer tx.Rollback()
	var wsID, role, inviter string
	var enc int
	err = tx.QueryRow(`SELECT i.workspace_id,i.role,i.invited_by,w.encrypted FROM workspace_invitations i
		JOIN workspaces w ON w.id=i.workspace_id JOIN users u ON u.email=i.email
		WHERE i.id=? AND u.id=? AND i.expires_at>?`, r.PathValue("invite"), idn.UID, nowMs()).Scan(&wsID, &role, &inviter, &enc)
	if err != nil {
		writeErr(w, 404, "invitation not found or expired")
		return
	}
	inviterRole, _ := roleFor(tx, wsID, inviter)
	if !manages(inviterRole) || (role == "admin" && inviterRole != "owner") {
		writeErr(w, 403, "inviter no longer has permission")
		return
	}
	if enc == 1 && !validEnvelope(req.KDFSalt, req.WrappedKey) {
		writeErr(w, 400, "a personal wrapped key and salt are required")
		return
	}
	if _, err := tx.Exec(`INSERT INTO workspace_members(workspace_id,user_id,role,kdf_salt,wrapped_key) VALUES(?,?,?,?,?)`,
		wsID, idn.UID, role, req.KDFSalt, req.WrappedKey); err != nil {
		writeErr(w, 409, "already a member")
		return
	}
	if _, err := tx.Exec(`DELETE FROM workspace_invitations WHERE id=?`, r.PathValue("invite")); err != nil {
		writeErr(w, 500, "cannot accept invitation")
		return
	}
	if tx.Commit() != nil {
		writeErr(w, 500, "cannot accept invitation")
		return
	}
	r.SetPathValue("id", wsID)
	s.handleGetWorkspace(w, r)
}

func (s *Server) handleRevokeInvitation(w http.ResponseWriter, r *http.Request) {
	idn, _ := identityFrom(r)
	tx, err := s.db.Begin()
	if err != nil {
		writeErr(w, 500, "database busy")
		return
	}
	defer tx.Rollback()
	role, _ := roleFor(tx, r.PathValue("id"), idn.UID)
	var invitedRole string
	err = tx.QueryRow(`SELECT role FROM workspace_invitations WHERE workspace_id=? AND id=?`, r.PathValue("id"), r.PathValue("invite")).Scan(&invitedRole)
	if err != nil || !manages(role) || (invitedRole == "admin" && role != "owner") {
		writeErr(w, 403, "cannot revoke this invitation")
		return
	}
	if _, err := tx.Exec(`DELETE FROM workspace_invitations WHERE id=?`, r.PathValue("invite")); err != nil || tx.Commit() != nil {
		writeErr(w, 500, "cannot revoke invitation")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleMember(w http.ResponseWriter, r *http.Request) {
	idn, _ := identityFrom(r)
	wsID, memberID := r.PathValue("id"), r.PathValue("member")
	var req struct {
		Role string `json:"role"`
	}
	if r.Method == http.MethodPatch && (json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&req) != nil || !validMemberRole(req.Role)) {
		writeErr(w, 400, "role must be admin, editor, or viewer")
		return
	}
	tx, err := s.db.Begin()
	if err != nil {
		writeErr(w, 500, "database busy")
		return
	}
	defer tx.Rollback()
	actorRole, _ := roleFor(tx, wsID, idn.UID)
	targetRole, _ := roleFor(tx, wsID, memberID)
	selfLeave := r.Method == http.MethodDelete && memberID == idn.UID && targetRole != ""
	if targetRole == "" || targetRole == "owner" || (!selfLeave && (!manages(actorRole) ||
		(actorRole != "owner" && (targetRole == "admin" || req.Role == "admin")))) {
		writeErr(w, 403, "cannot change this member; workspace ownership is protected")
		return
	}
	if r.Method == http.MethodDelete {
		_, err = tx.Exec(`DELETE FROM workspace_members WHERE workspace_id=? AND user_id=?`, wsID, memberID)
		if err == nil {
			_, err = tx.Exec(`DELETE FROM workspace_invitations WHERE workspace_id=? AND invited_by=?`, wsID, memberID)
		}
	} else {
		_, err = tx.Exec(`UPDATE workspace_members SET role=? WHERE workspace_id=? AND user_id=?`, req.Role, wsID, memberID)
	}
	if err != nil || tx.Commit() != nil {
		writeErr(w, 500, "cannot update membership")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
