package server

import (
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
)

type remoteWorkspace struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Role       string `json:"role,omitempty"`
	KeyScope   string `json:"keyScope,omitempty"`
	Encrypted  bool   `json:"encrypted"`
	KDFSalt    string `json:"kdfSalt,omitempty"`
	WrappedKey string `json:"wrappedKey,omitempty"`
	CreatedAt  int64  `json:"createdAt"`
}

func (s *Server) roleFor(wsID, uid string) (string, error) {
	return roleFor(s.db, wsID, uid)
}

type rowQuerier interface {
	QueryRow(string, ...any) *sql.Row
}

func roleFor(db rowQuerier, wsID, uid string) (string, error) {
	var owner string
	if err := db.QueryRow(`SELECT owner_id FROM workspaces WHERE id = ?`, wsID).Scan(&owner); err != nil {
		return "", err
	}
	if owner == uid {
		return "owner", nil
	}
	var role string
	if err := db.QueryRow(`SELECT role FROM workspace_members WHERE workspace_id = ? AND user_id = ?`, wsID, uid).Scan(&role); err != nil {
		return "", errors.New("forbidden")
	}
	return role, nil
}

func canWrite(role string) bool {
	return role == "owner" || role == "admin" || role == "editor"
}

func (s *Server) handleListWorkspaces(w http.ResponseWriter, r *http.Request) {
	idn, _ := identityFrom(r)
	rows, err := s.db.Query(`
		SELECT w.id, w.name, w.encrypted, w.created_at, w.key_scope,
		       COALESCE(m.kdf_salt, w.kdf_salt), COALESCE(m.wrapped_key, w.wrapped_key),
		       CASE WHEN w.owner_id = ? THEN 'owner' ELSE COALESCE(m.role, 'viewer') END
		FROM workspaces w
		LEFT JOIN workspace_members m ON m.workspace_id = w.id AND m.user_id = ?
		WHERE w.owner_id = ? OR m.user_id IS NOT NULL
		ORDER BY w.created_at DESC`, idn.UID, idn.UID, idn.UID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "cannot list workspaces")
		return
	}
	defer rows.Close()
	out := []remoteWorkspace{}
	for rows.Next() {
		var ws remoteWorkspace
		var enc int
		if err := rows.Scan(&ws.ID, &ws.Name, &enc, &ws.CreatedAt, &ws.KeyScope, &ws.KDFSalt, &ws.WrappedKey, &ws.Role); err != nil {
			continue
		}
		ws.Encrypted = enc == 1
		out = append(out, ws)
	}
	writeJSON(w, http.StatusOK, map[string]any{"workspaces": out})
}

func (s *Server) handleCreateWorkspace(w http.ResponseWriter, r *http.Request) {
	idn, _ := identityFrom(r)
	var req struct {
		Name       string `json:"name"`
		Encrypted  bool   `json:"encrypted"`
		KDFSalt    string `json:"kdfSalt"`
		WrappedKey string `json:"wrappedKey"`
		KeyScope   string `json:"keyScope"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.KeyScope == "" {
		req.KeyScope = "legacy"
	}
	if req.KeyScope != "legacy" && req.KeyScope != "workspace" {
		writeErr(w, 400, "invalid key scope")
		return
	}
	if req.Name == "" || len(req.Name) > 120 {
		writeErr(w, http.StatusBadRequest, "workspace name is required")
		return
	}
	if req.Encrypted {
		if req.KeyScope == "workspace" && !validEnvelope(req.KDFSalt, req.WrappedKey) {
			writeErr(w, http.StatusBadRequest, "invalid workspace key envelope")
			return
		}
		if req.WrappedKey == "" || req.KDFSalt == "" {
			writeErr(w, http.StatusBadRequest, "encrypted workspaces need a wrapped key and salt")
			return
		}
		if _, err := base64.RawStdEncoding.DecodeString(req.KDFSalt); err != nil {
			writeErr(w, http.StatusBadRequest, "invalid kdfSalt")
			return
		}
		if _, err := base64.RawStdEncoding.DecodeString(req.WrappedKey); err != nil {
			writeErr(w, http.StatusBadRequest, "invalid wrappedKey")
			return
		}
	}
	wsID := newID()
	tx, err := s.db.Begin()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "database busy")
		return
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`INSERT INTO workspaces(id, owner_id, name, encrypted, kdf_salt, wrapped_key, created_at, key_scope)
		VALUES(?,?,?,?,?,?,?,?)`, wsID, idn.UID, req.Name, boolInt(req.Encrypted), req.KDFSalt, req.WrappedKey, nowMs(), req.KeyScope); err != nil {
		writeErr(w, http.StatusInternalServerError, "cannot create workspace")
		return
	}
	if _, err := tx.Exec(`INSERT INTO workspace_members(workspace_id, user_id, role, wrapped_key, kdf_salt)
		VALUES(?,?,?,?,?)`, wsID, idn.UID, "owner", req.WrappedKey, req.KDFSalt); err != nil {
		writeErr(w, http.StatusInternalServerError, "cannot create workspace")
		return
	}
	if err := tx.Commit(); err != nil {
		writeErr(w, http.StatusInternalServerError, "cannot create workspace")
		return
	}
	writeJSON(w, http.StatusCreated, remoteWorkspace{
		ID:         wsID,
		Name:       req.Name,
		Role:       "owner",
		KeyScope:   req.KeyScope,
		Encrypted:  req.Encrypted,
		KDFSalt:    req.KDFSalt,
		WrappedKey: req.WrappedKey,
		CreatedAt:  nowMs(),
	})
}

func (s *Server) handleGetWorkspace(w http.ResponseWriter, r *http.Request) {
	idn, _ := identityFrom(r)
	wsID := r.PathValue("id")
	role, err := s.roleFor(wsID, idn.UID)
	if err != nil {
		writeErr(w, http.StatusNotFound, "workspace not found")
		return
	}
	var ws remoteWorkspace
	var enc int
	err = s.db.QueryRow(`
		SELECT w.id, w.name, w.encrypted, w.created_at, w.key_scope,
		       COALESCE(m.kdf_salt, w.kdf_salt), COALESCE(m.wrapped_key, w.wrapped_key)
		FROM workspaces w
		LEFT JOIN workspace_members m ON m.workspace_id = w.id AND m.user_id = ?
		WHERE w.id = ?`, idn.UID, wsID).
		Scan(&ws.ID, &ws.Name, &enc, &ws.CreatedAt, &ws.KeyScope, &ws.KDFSalt, &ws.WrappedKey)
	if err != nil {
		writeErr(w, http.StatusNotFound, "workspace not found")
		return
	}
	ws.Encrypted = enc == 1
	ws.Role = role
	writeJSON(w, http.StatusOK, ws)
}

func (s *Server) handleDeleteWorkspace(w http.ResponseWriter, r *http.Request) {
	idn, _ := identityFrom(r)
	wsID := r.PathValue("id")
	role, err := s.roleFor(wsID, idn.UID)
	if err != nil || role != "owner" {
		writeErr(w, http.StatusForbidden, "only the owner can delete a workspace")
		return
	}
	tx, err := s.db.Begin()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "database busy")
		return
	}
	defer tx.Rollback()
	for _, q := range []string{
		`DELETE FROM workspace_invitations WHERE workspace_id = ?`,
		`DELETE FROM ops WHERE workspace_id = ?`,
		`DELETE FROM docs WHERE workspace_id = ?`,
		`DELETE FROM workspace_members WHERE workspace_id = ?`,
		`DELETE FROM workspaces WHERE id = ?`,
	} {
		if _, err := tx.Exec(q, wsID); err != nil {
			writeErr(w, http.StatusInternalServerError, "cannot delete workspace")
			return
		}
	}
	if err := tx.Commit(); err != nil {
		writeErr(w, http.StatusInternalServerError, "cannot delete workspace")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

/* -------------------------------- push -------------------------------- */

type syncOp struct {
	OpID        string `json:"opId"`
	DocID       string `json:"docId"`
	Kind        string `json:"kind"`
	BaseVersion int64  `json:"baseVersion"`
	Ciphertext  string `json:"ciphertext"`
	UpdatedAt   int64  `json:"updatedAt"`
	Deleted     bool   `json:"deleted"`
}

func (s *Server) handlePush(w http.ResponseWriter, r *http.Request) {
	idn, _ := identityFrom(r)
	wsID := r.PathValue("id")
	role, err := s.roleFor(wsID, idn.UID)
	if err != nil || !canWrite(role) {
		writeErr(w, http.StatusForbidden, "you do not have write access to this workspace")
		return
	}
	var req struct {
		DeviceID string   `json:"deviceId"`
		Ops      []syncOp `json:"ops"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<20)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if len(req.Ops) > 500 {
		writeErr(w, http.StatusBadRequest, "too many ops in one push (max 500)")
		return
	}

	tx, err := s.db.Begin()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "database busy")
		return
	}
	defer tx.Rollback()

	var seq int64
	if err := tx.QueryRow(`SELECT seq FROM workspaces WHERE id = ?`, wsID).Scan(&seq); err != nil {
		writeErr(w, http.StatusNotFound, "workspace not found")
		return
	}

	type accepted struct {
		OpID    string `json:"opId"`
		Seq     int64  `json:"seq"`
		Version int64  `json:"version"`
	}
	type conflict struct {
		DocID      string `json:"docId"`
		Version    int64  `json:"version"`
		Kind       string `json:"kind"`
		Ciphertext string `json:"ciphertext"`
		UpdatedAt  int64  `json:"updatedAt"`
		Deleted    bool   `json:"deleted"`
	}
	acceptedOps := []accepted{}
	conflicts := []conflict{}

	for _, op := range req.Ops {
		if op.DocID == "" || op.OpID == "" || op.BaseVersion < 0 {
			continue
		}
		// Idempotency: a retried op returns its original result.
		var exSeq, exVersion int64
		if err := tx.QueryRow(`SELECT seq, version FROM ops WHERE workspace_id = ? AND op_id = ?`, wsID, op.OpID).
			Scan(&exSeq, &exVersion); err == nil {
			acceptedOps = append(acceptedOps, accepted{OpID: op.OpID, Seq: exSeq, Version: exVersion})
			continue
		}

		var (
			curVersion int64
			curKind    string
			curCipher  string
			curUpdated int64
			curDeleted int
		)
		exists := true
		err := tx.QueryRow(`SELECT version, kind, ciphertext, updated_at, deleted FROM docs WHERE workspace_id = ? AND doc_id = ?`,
			wsID, op.DocID).Scan(&curVersion, &curKind, &curCipher, &curUpdated, &curDeleted)
		if errors.Is(err, sql.ErrNoRows) {
			exists = false
		} else if err != nil {
			writeErr(w, http.StatusInternalServerError, "database error")
			return
		}

		if exists && curVersion != op.BaseVersion {
			conflicts = append(conflicts, conflict{
				DocID: op.DocID, Version: curVersion, Kind: curKind,
				Ciphertext: curCipher, UpdatedAt: curUpdated, Deleted: curDeleted == 1,
			})
			continue
		}
		if len(op.Ciphertext) > 16<<20 {
			continue
		}
		newVersion := op.BaseVersion + 1
		seq++
		kind := op.Kind
		if kind == "" {
			kind = "request"
		}
		updatedAt := op.UpdatedAt
		if updatedAt == 0 {
			updatedAt = nowMs()
		}
		if _, err := tx.Exec(`INSERT INTO ops(workspace_id, seq, op_id, doc_id, device_id, version, kind, ciphertext, updated_at, deleted, created_at)
			VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
			wsID, seq, op.OpID, op.DocID, req.DeviceID, newVersion, kind, op.Ciphertext, updatedAt, boolInt(op.Deleted), nowMs()); err != nil {
			writeErr(w, http.StatusInternalServerError, "cannot append op")
			return
		}
		if _, err := tx.Exec(`INSERT INTO docs(workspace_id, doc_id, version, kind, ciphertext, updated_at, deleted)
			VALUES(?,?,?,?,?,?,?)
			ON CONFLICT(workspace_id, doc_id) DO UPDATE SET
				version = excluded.version, kind = excluded.kind, ciphertext = excluded.ciphertext,
				updated_at = excluded.updated_at, deleted = excluded.deleted`,
			wsID, op.DocID, newVersion, kind, op.Ciphertext, updatedAt, boolInt(op.Deleted)); err != nil {
			writeErr(w, http.StatusInternalServerError, "cannot update document")
			return
		}
		acceptedOps = append(acceptedOps, accepted{OpID: op.OpID, Seq: seq, Version: newVersion})
	}
	if _, err := tx.Exec(`UPDATE workspaces SET seq = ? WHERE id = ?`, seq, wsID); err != nil {
		writeErr(w, http.StatusInternalServerError, "cannot update sequence")
		return
	}
	if err := tx.Commit(); err != nil {
		writeErr(w, http.StatusInternalServerError, "cannot commit")
		return
	}
	s.sync.Add(int64(len(acceptedOps)))
	writeJSON(w, http.StatusOK, map[string]any{"accepted": acceptedOps, "conflicts": conflicts})
}

/* -------------------------------- pull -------------------------------- */

func (s *Server) handlePull(w http.ResponseWriter, r *http.Request) {
	idn, _ := identityFrom(r)
	wsID := r.PathValue("id")
	if _, err := s.roleFor(wsID, idn.UID); err != nil {
		writeErr(w, http.StatusForbidden, "no access to this workspace")
		return
	}
	since, _ := strconv.ParseInt(r.URL.Query().Get("since"), 10, 64)
	if since < 0 {
		since = 0
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 1000 {
		limit = 500
	}
	rows, err := s.db.Query(`SELECT seq, op_id, doc_id, version, kind, ciphertext, updated_at, deleted
		FROM ops WHERE workspace_id = ? AND seq > ? ORDER BY seq LIMIT ?`, wsID, since, limit)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "cannot read ops")
		return
	}
	defer rows.Close()
	type outOp struct {
		Seq        int64  `json:"seq"`
		OpID       string `json:"opId"`
		DocID      string `json:"docId"`
		Version    int64  `json:"version"`
		Kind       string `json:"kind"`
		Ciphertext string `json:"ciphertext"`
		UpdatedAt  int64  `json:"updatedAt"`
		Deleted    bool   `json:"deleted"`
	}
	ops := []outOp{}
	next := since
	for rows.Next() {
		var op outOp
		var deleted int
		if err := rows.Scan(&op.Seq, &op.OpID, &op.DocID, &op.Version, &op.Kind, &op.Ciphertext, &op.UpdatedAt, &deleted); err != nil {
			writeErr(w, http.StatusInternalServerError, "cannot read ops")
			return
		}
		op.Deleted = deleted == 1
		next = op.Seq
		ops = append(ops, op)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ops":     ops,
		"next":    next,
		"hasMore": len(ops) == limit,
	})
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
