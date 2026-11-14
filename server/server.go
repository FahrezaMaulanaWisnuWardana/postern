// Package server implements the self-hosted YourPM sync service.
//
// It is deliberately small: one binary, SQLite, no Redis or S3. The server
// never receives plaintext workspace data when encryption is enabled.
package server

import (
	"crypto/rand"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	_ "modernc.org/sqlite"

	"yourpm/core/crypto"
)

// Config configures the server.
type Config struct {
	Addr          string
	DataDir       string
	JWTSecret     string
	Registration  string // open | closed
	AdminEmail    string
	AdminPassword string
	// TrustProxy allows X-Forwarded-For to identify the client. Enable it
	// only when the server is behind a reverse proxy you control.
	TrustProxy bool
	// MetricsToken, when set, requires a bearer token for /metrics.
	MetricsToken string
	// OpRetentionDays prunes historical sync operations older than this.
	// Zero keeps everything. Devices offline longer than the window must
	// re-link; see docs/self-hosting.md.
	OpRetentionDays int
}

// LoadConfig reads configuration from the environment.
func LoadConfig() (Config, error) {
	cfg := Config{
		Addr:            env("YOURPM_ADDR", ":8080"),
		DataDir:         env("YOURPM_DATA", "./data"),
		JWTSecret:       os.Getenv("YOURPM_JWT_SECRET"),
		Registration:    env("YOURPM_REGISTRATION", "open"),
		AdminEmail:      os.Getenv("YOURPM_ADMIN_EMAIL"),
		AdminPassword:   os.Getenv("YOURPM_ADMIN_PASSWORD"),
		TrustProxy:      envBool("YOURPM_TRUST_PROXY"),
		MetricsToken:    os.Getenv("YOURPM_METRICS_TOKEN"),
		OpRetentionDays: envInt("YOURPM_OP_RETENTION_DAYS"),
	}
	if err := os.MkdirAll(cfg.DataDir, 0o700); err != nil {
		return cfg, err
	}
	if cfg.JWTSecret == "" {
		path := filepath.Join(cfg.DataDir, "jwt.key")
		if raw, err := os.ReadFile(path); err == nil && len(raw) >= 32 {
			cfg.JWTSecret = string(raw)
		} else {
			b := make([]byte, 32)
			if _, err := rand.Read(b); err != nil {
				return cfg, err
			}
			if err := os.WriteFile(path, b, 0o600); err != nil {
				return cfg, err
			}
			cfg.JWTSecret = string(b)
		}
	}
	return cfg, nil
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envBool(key string) bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv(key)))
	return v == "1" || v == "true" || v == "yes" || v == "on"
}

func envInt(key string) int {
	v, err := strconv.Atoi(strings.TrimSpace(os.Getenv(key)))
	if err != nil || v < 0 {
		return 0
	}
	return v
}

// Server is the HTTP service.
type Server struct {
	cfg    Config
	db     *sql.DB
	log    *slog.Logger
	lim    *limiter
	apiLim *limiter
	reqs   atomic.Int64
	errs   atomic.Int64
	auth   atomic.Int64
	sync   atomic.Int64
}

// New opens the database and prepares the service.
func New(cfg Config, log *slog.Logger) (*Server, error) {
	if log == nil {
		log = slog.Default()
	}
	abs, err := filepath.Abs(filepath.Join(cfg.DataDir, "sync.db"))
	if err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(abs))
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	for _, p := range []string{"PRAGMA journal_mode=WAL", "PRAGMA busy_timeout=5000", "PRAGMA synchronous=NORMAL"} {
		if _, err := db.Exec(p); err != nil {
			db.Close()
			return nil, err
		}
	}
	s := &Server{cfg: cfg, db: db, log: log, lim: newLimiter(20, time.Minute), apiLim: newLimiter(600, time.Minute)}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	if cfg.OpRetentionDays > 0 {
		log.Warn("sync op-log retention is enabled; devices offline longer than the window must re-link",
			"days", cfg.OpRetentionDays)
		if err := s.pruneOps(cfg.OpRetentionDays); err != nil {
			log.Warn("could not prune old sync operations", "error", err)
		}
	}
	for _, p := range []string{abs, abs + "-wal", abs + "-shm"} {
		_ = os.Chmod(p, 0o600)
	}
	if err := s.bootstrapAdmin(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// Close closes the database.
func (s *Server) Close() error { return s.db.Close() }

func (s *Server) migrate() error {
	_, err := s.db.Exec(`
CREATE TABLE IF NOT EXISTS users(
  id TEXT PRIMARY KEY,
  email TEXT NOT NULL COLLATE NOCASE UNIQUE,
  pass_hash TEXT NOT NULL,
  created_at INTEGER NOT NULL,
  disabled INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS devices(
  id TEXT PRIMARY KEY,
  user_id TEXT NOT NULL,
  name TEXT NOT NULL,
  refresh_hash TEXT NOT NULL,
  created_at INTEGER NOT NULL,
  last_seen INTEGER,
  revoked_at INTEGER
);
CREATE INDEX IF NOT EXISTS devices_user ON devices(user_id);
CREATE TABLE IF NOT EXISTS workspaces(
  id TEXT PRIMARY KEY,
  owner_id TEXT NOT NULL,
  name TEXT NOT NULL,
  encrypted INTEGER NOT NULL DEFAULT 1,
  key_scope TEXT NOT NULL DEFAULT 'legacy',
  kdf_salt TEXT NOT NULL DEFAULT '',
  wrapped_key TEXT NOT NULL DEFAULT '',
  key_epoch INTEGER NOT NULL DEFAULT 1,
  seq INTEGER NOT NULL DEFAULT 0,
  created_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS workspace_members(
  workspace_id TEXT NOT NULL,
  user_id TEXT NOT NULL,
  role TEXT NOT NULL,
  wrapped_key TEXT NOT NULL,
  kdf_salt TEXT NOT NULL,
  key_epoch INTEGER NOT NULL DEFAULT 1,
  PRIMARY KEY(workspace_id, user_id)
);
CREATE TABLE IF NOT EXISTS docs(
  workspace_id TEXT NOT NULL,
  doc_id TEXT NOT NULL,
  version INTEGER NOT NULL,
  kind TEXT NOT NULL,
  ciphertext TEXT NOT NULL DEFAULT '',
  updated_at INTEGER NOT NULL,
  deleted INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY(workspace_id, doc_id)
);
CREATE TABLE IF NOT EXISTS ops(
  workspace_id TEXT NOT NULL,
  seq INTEGER NOT NULL,
  op_id TEXT NOT NULL,
  doc_id TEXT NOT NULL,
  device_id TEXT NOT NULL,
  version INTEGER NOT NULL,
  kind TEXT NOT NULL,
  ciphertext TEXT NOT NULL DEFAULT '',
  updated_at INTEGER NOT NULL,
  deleted INTEGER NOT NULL DEFAULT 0,
  created_at INTEGER NOT NULL,
  PRIMARY KEY(workspace_id, seq),
  UNIQUE(workspace_id, op_id)
);
CREATE INDEX IF NOT EXISTS ops_doc ON ops(workspace_id, doc_id);
CREATE TABLE IF NOT EXISTS workspace_invitations(
  id TEXT PRIMARY KEY,
  workspace_id TEXT NOT NULL,
  email TEXT NOT NULL COLLATE NOCASE,
  role TEXT NOT NULL,
  invited_by TEXT NOT NULL,
  kdf_salt TEXT NOT NULL DEFAULT '',
  wrapped_key TEXT NOT NULL DEFAULT '',
  expires_at INTEGER NOT NULL,
  UNIQUE(workspace_id, email)
);
`)
	if err != nil {
		return err
	}
	// Upgrade databases created before isolated team workspace keys.
	_, err = s.db.Exec(`ALTER TABLE workspaces ADD COLUMN key_scope TEXT NOT NULL DEFAULT 'legacy'`)
	if err != nil && !strings.Contains(err.Error(), "duplicate column") {
		return err
	}
	return nil
}

// pruneOps removes historical operations older than the retention window but
// keeps the newest operation for every document, so a fresh pull still gets a
// complete picture. Devices that have been offline longer than the window will
// miss intermediate revisions and should re-link.
func (s *Server) pruneOps(days int) error {
	if days <= 0 {
		return nil
	}
	cutoff := time.Now().AddDate(0, 0, -days).UnixMilli()
	res, err := s.db.Exec(`DELETE FROM ops
		WHERE created_at < ?
		  AND seq <> (SELECT MAX(o2.seq) FROM ops o2
		              WHERE o2.workspace_id = ops.workspace_id
		                AND o2.doc_id = ops.doc_id)`, cutoff)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n > 0 {
		s.log.Info("pruned old sync operations", "count", n, "days", days)
	}
	return nil
}

func (s *Server) bootstrapAdmin() error {
	if s.cfg.AdminEmail == "" || s.cfg.AdminPassword == "" {
		return nil
	}
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	hash, err := crypto.HashPassword(s.cfg.AdminPassword)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`INSERT INTO users(id, email, pass_hash, created_at) VALUES(?,?,?,?)`,
		newID(), strings.ToLower(s.cfg.AdminEmail), hash, nowMs())
	if err == nil {
		s.log.Info("created bootstrap admin", "email", s.cfg.AdminEmail)
	}
	return err
}

// Handler returns the HTTP handler with all routes.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("ok\n"))
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		if err := s.db.Ping(); err != nil {
			http.Error(w, "database unavailable", http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte("ready\n"))
	})
	mux.HandleFunc("GET /metrics", s.handleMetrics)

	mux.HandleFunc("POST /v1/auth/register", s.handleRegister)
	mux.HandleFunc("POST /v1/auth/login", s.handleLogin)
	mux.HandleFunc("POST /v1/auth/refresh", s.handleRefresh)
	mux.HandleFunc("POST /v1/auth/logout", s.withAuth(s.handleLogout))
	mux.HandleFunc("GET /v1/me", s.withAuth(s.handleMe))
	mux.HandleFunc("GET /v1/devices", s.withAuth(s.handleDevices))
	mux.HandleFunc("DELETE /v1/devices/{id}", s.withAuth(s.handleRevokeDevice))

	mux.HandleFunc("GET /v1/workspaces", s.withAuth(s.handleListWorkspaces))
	mux.HandleFunc("POST /v1/workspaces", s.withAuth(s.handleCreateWorkspace))
	mux.HandleFunc("GET /v1/workspaces/{id}", s.withAuth(s.handleGetWorkspace))
	mux.HandleFunc("DELETE /v1/workspaces/{id}", s.withAuth(s.handleDeleteWorkspace))
	mux.HandleFunc("POST /v1/workspaces/{id}/push", s.withAuth(s.handlePush))
	mux.HandleFunc("GET /v1/workspaces/{id}/pull", s.withAuth(s.handlePull))
	mux.HandleFunc("GET /v1/workspaces/{id}/team", s.withAuth(s.handleTeam))
	mux.HandleFunc("POST /v1/workspaces/{id}/invitations", s.withAuth(s.handleInvite))
	mux.HandleFunc("DELETE /v1/workspaces/{id}/invitations/{invite}", s.withAuth(s.handleRevokeInvitation))
	mux.HandleFunc("PATCH /v1/workspaces/{id}/members/{member}", s.withAuth(s.handleMember))
	mux.HandleFunc("DELETE /v1/workspaces/{id}/members/{member}", s.withAuth(s.handleMember))
	mux.HandleFunc("GET /v1/invitations", s.withAuth(s.handleInvitations))
	mux.HandleFunc("POST /v1/invitations/{invite}/accept", s.withAuth(s.handleAcceptInvitation))

	return s.limitAPI(s.logRequests(mux))
}

func (s *Server) limitAPI(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/healthz", "/readyz":
			next.ServeHTTP(w, r)
			return
		}
		if !s.apiLim.allow(s.clientIP(r)) {
			w.Header().Set("Retry-After", "60")
			writeErr(w, http.StatusTooManyRequests, "too many requests")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: 200}
		next.ServeHTTP(rec, r)
		s.reqs.Add(1)
		if rec.status >= 500 {
			s.errs.Add(1)
		}
		// Never log bodies, query strings or headers: they can contain secrets.
		s.log.Info("request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", rec.status,
			"ms", time.Since(start).Milliseconds(),
		)
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	if s.cfg.MetricsToken != "" {
		token := ""
		if auth := r.Header.Get("Authorization"); len(auth) > 7 && strings.EqualFold(auth[:7], "bearer ") {
			token = strings.TrimSpace(auth[7:])
		}
		if token == "" {
			token = r.URL.Query().Get("token")
		}
		if subtle.ConstantTimeCompare([]byte(token), []byte(s.cfg.MetricsToken)) != 1 {
			writeErr(w, http.StatusUnauthorized, "metrics token required")
			return
		}
	}
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	fmt.Fprintf(w, "# HELP yourpm_requests_total Total HTTP requests.\n# TYPE yourpm_requests_total counter\nyourpm_requests_total %d\n", s.reqs.Load())
	fmt.Fprintf(w, "# HELP yourpm_errors_total Total 5xx responses.\n# TYPE yourpm_errors_total counter\nyourpm_errors_total %d\n", s.errs.Load())
	fmt.Fprintf(w, "# HELP yourpm_auth_total Auth attempts.\n# TYPE yourpm_auth_total counter\nyourpm_auth_total %d\n", s.auth.Load())
	fmt.Fprintf(w, "# HELP yourpm_sync_ops_total Sync operations.\n# TYPE yourpm_sync_ops_total counter\nyourpm_sync_ops_total %d\n", s.sync.Load())
}

func nowMs() int64 { return time.Now().UnixMilli() }

func newID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

func (s *Server) tokenKey() []byte {
	sum := crypto.HashToken(s.cfg.JWTSecret)
	raw, _ := base64.RawStdEncoding.DecodeString(sum)
	if len(raw) < 32 {
		return []byte(s.cfg.JWTSecret + s.cfg.JWTSecret)
	}
	return raw
}

/* ----------------------------- rate limit ----------------------------- */

type limiter struct {
	mu     sync.Mutex
	hits   map[string][]time.Time
	limit  int
	window time.Duration
}

func newLimiter(limit int, window time.Duration) *limiter {
	return &limiter{hits: map[string][]time.Time{}, limit: limit, window: window}
}

func (l *limiter) allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	cutoff := now.Add(-l.window)
	// Bound memory: drop keys that have no recent hits.
	if len(l.hits) > 4096 {
		for k, list := range l.hits {
			if len(list) == 0 || !list[len(list)-1].After(cutoff) {
				delete(l.hits, k)
			}
		}
	}
	list := l.hits[key]
	kept := list[:0]
	for _, t := range list {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	if len(kept) >= l.limit {
		l.hits[key] = kept
		return false
	}
	l.hits[key] = append(kept, now)
	return true
}

func (s *Server) clientIP(r *http.Request) string {
	if s.cfg.TrustProxy {
		if v := r.Header.Get("X-Forwarded-For"); v != "" {
			if i := strings.LastIndexByte(v, ','); i >= 0 {
				return strings.TrimSpace(v[i+1:])
			}
			return strings.TrimSpace(v)
		}
	}
	host := r.RemoteAddr
	if i := strings.LastIndexByte(host, ':'); i > 0 {
		host = host[:i]
	}
	return strings.Trim(host, "[]")
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = jsonEncode(w, v)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

var errUnauthorized = errors.New("unauthorized")
