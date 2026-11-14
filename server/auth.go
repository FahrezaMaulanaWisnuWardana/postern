package server

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/mail"
	"strings"
	"time"

	"yourpm/core/crypto"
)

func jsonEncode(w http.ResponseWriter, v any) error {
	return json.NewEncoder(w).Encode(v)
}

type identity struct {
	UID string
	DID string
}

type identityKey struct{}

func identityFrom(r *http.Request) (identity, bool) {
	id, ok := r.Context().Value(identityKey{}).(identity)
	return id, ok
}

/* ------------------------------- tokens ------------------------------- */

func (s *Server) signAccess(uid, did string) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`))
	payload := base64.RawURLEncoding.EncodeToString(mustMarshal(map[string]any{
		"uid": uid,
		"did": did,
		"iat": time.Now().Unix(),
		"exp": time.Now().Add(time.Hour).Unix(),
	}))
	signing := header + "." + payload
	mac := hmac.New(sha256.New, s.tokenKey())
	mac.Write([]byte(signing))
	return signing + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (s *Server) parseAccess(token string) (identity, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return identity{}, errUnauthorized
	}
	signing := parts[0] + "." + parts[1]
	mac := hmac.New(sha256.New, s.tokenKey())
	mac.Write([]byte(signing))
	want := mac.Sum(nil)
	got, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || subtle.ConstantTimeCompare(got, want) != 1 {
		return identity{}, errUnauthorized
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return identity{}, errUnauthorized
	}
	var claims struct {
		UID string `json:"uid"`
		DID string `json:"did"`
		Exp int64  `json:"exp"`
	}
	if err := json.Unmarshal(raw, &claims); err != nil || claims.UID == "" || claims.DID == "" {
		return identity{}, errUnauthorized
	}
	if time.Now().Unix() > claims.Exp {
		return identity{}, errors.New("token expired")
	}
	return identity{UID: claims.UID, DID: claims.DID}, nil
}

func (s *Server) withAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		header := r.Header.Get("Authorization")
		if !strings.HasPrefix(header, "Bearer ") {
			writeErr(w, http.StatusUnauthorized, "missing bearer token")
			return
		}
		idn, err := s.parseAccess(strings.TrimSpace(strings.TrimPrefix(header, "Bearer ")))
		if err != nil {
			writeErr(w, http.StatusUnauthorized, "invalid or expired token")
			return
		}
		var revoked *int64
		if err := s.db.QueryRow(`SELECT revoked_at FROM devices WHERE id = ? AND user_id = ?`, idn.DID, idn.UID).Scan(&revoked); err != nil {
			writeErr(w, http.StatusUnauthorized, "unknown device")
			return
		}
		if revoked != nil {
			writeErr(w, http.StatusUnauthorized, "device revoked")
			return
		}
		_, _ = s.db.Exec(`UPDATE devices SET last_seen = ? WHERE id = ?`, nowMs(), idn.DID)
		ctx := context.WithValue(r.Context(), identityKey{}, idn)
		next(w, r.WithContext(ctx))
	}
}

/* ------------------------------- handlers ----------------------------- */

type authRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
	Device   string `json:"device"`
}

func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	s.auth.Add(1)
	if s.cfg.Registration != "open" {
		writeErr(w, http.StatusForbidden, "registration is disabled")
		return
	}
	if !s.lim.allow("register:" + s.clientIP(r)) {
		writeErr(w, http.StatusTooManyRequests, "too many attempts, try again later")
		return
	}
	var req authRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	req.Email = strings.ToLower(strings.TrimSpace(req.Email))
	if _, err := mail.ParseAddress(req.Email); err != nil || !strings.Contains(req.Email, "@") {
		writeErr(w, http.StatusBadRequest, "a valid email is required")
		return
	}
	if len(req.Password) < 8 {
		writeErr(w, http.StatusBadRequest, "password must be at least 8 characters")
		return
	}
	hash, err := crypto.HashPassword(req.Password)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "cannot hash password")
		return
	}
	uid := newID()
	deviceID := newID()
	if _, err := s.db.Exec(`INSERT INTO users(id, email, pass_hash, created_at) VALUES(?,?,?,?)`,
		uid, req.Email, hash, nowMs()); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			writeErr(w, http.StatusConflict, "an account with that email already exists")
			return
		}
		s.log.Error("create user", "err", err)
		writeErr(w, http.StatusInternalServerError, "cannot create account")
		return
	}
	refresh := crypto.RandomToken(32)
	if _, err := s.db.Exec(`INSERT INTO devices(id, user_id, name, refresh_hash, created_at) VALUES(?,?,?,?,?)`,
		deviceID, uid, fallbackDevice(req.Device), crypto.HashToken(refresh), nowMs()); err != nil {
		writeErr(w, http.StatusInternalServerError, "cannot create device")
		return
	}
	s.log.Info("account created", "user", uid)
	writeJSON(w, http.StatusCreated, map[string]any{
		"token":        s.signAccess(uid, deviceID),
		"refreshToken": refresh,
		"deviceId":     deviceID,
		"user":         map[string]string{"id": uid, "email": req.Email},
	})
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	s.auth.Add(1)
	if !s.lim.allow("login:" + s.clientIP(r)) {
		writeErr(w, http.StatusTooManyRequests, "too many attempts, try again later")
		return
	}
	var req authRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	email := strings.ToLower(strings.TrimSpace(req.Email))
	var (
		uid, hash string
		disabled  int
	)
	err := s.db.QueryRow(`SELECT id, pass_hash, disabled FROM users WHERE email = ?`, email).Scan(&uid, &hash, &disabled)
	if err != nil || disabled == 1 || !crypto.VerifyPassword(hash, req.Password) {
		// Deliberately identical response for unknown user and wrong password.
		writeErr(w, http.StatusUnauthorized, "invalid email or password")
		return
	}
	deviceID := newID()
	refresh := crypto.RandomToken(32)
	if _, err := s.db.Exec(`INSERT INTO devices(id, user_id, name, refresh_hash, created_at) VALUES(?,?,?,?,?)`,
		deviceID, uid, fallbackDevice(req.Device), crypto.HashToken(refresh), nowMs()); err != nil {
		writeErr(w, http.StatusInternalServerError, "cannot create device")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"token":        s.signAccess(uid, deviceID),
		"refreshToken": refresh,
		"deviceId":     deviceID,
		"user":         map[string]string{"id": uid, "email": email},
	})
}

func (s *Server) handleRefresh(w http.ResponseWriter, r *http.Request) {
	if !s.lim.allow("refresh:" + s.clientIP(r)) {
		writeErr(w, http.StatusTooManyRequests, "too many attempts")
		return
	}
	var req struct {
		RefreshToken string `json:"refreshToken"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil || req.RefreshToken == "" {
		writeErr(w, http.StatusBadRequest, "refreshToken is required")
		return
	}
	var uid, did string
	err := s.db.QueryRow(`SELECT user_id, id FROM devices WHERE refresh_hash = ? AND revoked_at IS NULL`,
		crypto.HashToken(req.RefreshToken)).Scan(&uid, &did)
	if err != nil {
		writeErr(w, http.StatusUnauthorized, "invalid refresh token")
		return
	}
	next := crypto.RandomToken(32)
	if _, err := s.db.Exec(`UPDATE devices SET refresh_hash = ? WHERE id = ?`, crypto.HashToken(next), did); err != nil {
		writeErr(w, http.StatusInternalServerError, "cannot rotate token")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"token":        s.signAccess(uid, did),
		"refreshToken": next,
		"deviceId":     did,
	})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	idn, _ := identityFrom(r)
	_, _ = s.db.Exec(`UPDATE devices SET revoked_at = ?, refresh_hash = '' WHERE id = ?`, nowMs(), idn.DID)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	idn, _ := identityFrom(r)
	var email string
	var createdAt int64
	_ = s.db.QueryRow(`SELECT email, created_at FROM users WHERE id = ?`, idn.UID).Scan(&email, &createdAt)
	writeJSON(w, http.StatusOK, map[string]any{
		"id":        idn.UID,
		"email":     email,
		"deviceId":  idn.DID,
		"createdAt": createdAt,
	})
}

func (s *Server) handleDevices(w http.ResponseWriter, r *http.Request) {
	idn, _ := identityFrom(r)
	rows, err := s.db.Query(`SELECT id, name, created_at, last_seen, revoked_at FROM devices WHERE user_id = ? ORDER BY created_at DESC`, idn.UID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "cannot list devices")
		return
	}
	defer rows.Close()
	type device struct {
		ID        string `json:"id"`
		Name      string `json:"name"`
		CreatedAt int64  `json:"createdAt"`
		LastSeen  int64  `json:"lastSeen"`
		Revoked   bool   `json:"revoked"`
	}
	out := []device{}
	for rows.Next() {
		var d device
		var lastSeen, revoked *int64
		if err := rows.Scan(&d.ID, &d.Name, &d.CreatedAt, &lastSeen, &revoked); err != nil {
			continue
		}
		if lastSeen != nil {
			d.LastSeen = *lastSeen
		}
		d.Revoked = revoked != nil
		out = append(out, d)
	}
	writeJSON(w, http.StatusOK, map[string]any{"devices": out})
}

func (s *Server) handleRevokeDevice(w http.ResponseWriter, r *http.Request) {
	idn, _ := identityFrom(r)
	id := r.PathValue("id")
	res, err := s.db.Exec(`UPDATE devices SET revoked_at = ?, refresh_hash = '' WHERE id = ? AND user_id = ?`,
		nowMs(), id, idn.UID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "cannot revoke device")
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		writeErr(w, http.StatusNotFound, "device not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func fallbackDevice(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "desktop"
	}
	if len(name) > 80 {
		return name[:80]
	}
	return name
}

func mustMarshal(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}
