// Package store implements the local, single-user SQLite database.
//
// When the vault is enabled every entity payload and history body is
// encrypted with the vault key before it touches the disk, so the database
// file alone is not enough to read saved requests.
package store

import (
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"yourpm/core/crypto"
	"yourpm/core/model"

	_ "modernc.org/sqlite"
)

// ErrLocked is returned when encrypted data is read without an unlocked vault.
var ErrLocked = errors.New("store: vault is locked")

// ErrNotFound is returned when a record does not exist.
var ErrNotFound = errors.New("store: not found")

// Store is the local database handle.
type Store struct {
	db   *sql.DB
	path string

	mu      sync.RWMutex
	key     []byte
	enabled bool
}

// Open opens or creates the database at path.
func Open(path string) (*Store, error) {
	if path != ":memory:" {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return nil, fmt.Errorf("create data directory: %w", err)
		}
	}
	dsn := path
	if path != ":memory:" {
		abs, err := filepath.Abs(path)
		if err != nil {
			return nil, err
		}
		u := url.URL{Scheme: "file", Path: filepath.ToSlash(abs)}
		dsn = u.String()
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// A single connection keeps SQLite simple and avoids writer starvation.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0)

	for _, pragma := range []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA busy_timeout=5000",
		"PRAGMA synchronous=NORMAL",
		"PRAGMA foreign_keys=ON",
		"PRAGMA temp_store=MEMORY",
	} {
		if _, err := db.Exec(pragma); err != nil {
			db.Close()
			return nil, fmt.Errorf("%s: %w", pragma, err)
		}
	}

	s := &Store{db: db, path: path}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	// Keep the database and its WAL sidecars private on POSIX systems.
	if path != ":memory:" {
		for _, p := range []string{path, path + "-wal", path + "-shm"} {
			_ = os.Chmod(p, 0o600)
		}
	}
	s.enabled = s.SettingBool("vault.enabled")
	if ulid := s.Setting("sync.deviceId"); ulid == "" {
		_ = s.SetSetting("sync.deviceId", newDeviceID())
	}
	if err := s.ensureDefaultWorkspace(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

// Path returns the database path.
func (s *Store) Path() string { return s.path }

const schemaVersion = 1

var schema = []string{
	`CREATE TABLE IF NOT EXISTS workspaces (
		id TEXT PRIMARY KEY,
		name TEXT NOT NULL,
		description TEXT NOT NULL DEFAULT '',
		created_at INTEGER NOT NULL,
		updated_at INTEGER NOT NULL,
		sync_server TEXT NOT NULL DEFAULT '',
		sync_remote_id TEXT NOT NULL DEFAULT '',
		sync_encrypted INTEGER NOT NULL DEFAULT 0,
		sync_cursor INTEGER NOT NULL DEFAULT 0,
		sync_last_at INTEGER NOT NULL DEFAULT 0,
		sync_last_error TEXT NOT NULL DEFAULT ''
	)`,
	`CREATE TABLE IF NOT EXISTS entities (
		id TEXT PRIMARY KEY,
		workspace TEXT NOT NULL,
		kind TEXT NOT NULL,
		blob BLOB NOT NULL,
		sealed INTEGER NOT NULL DEFAULT 0,
		deleted INTEGER NOT NULL DEFAULT 0,
		created_at INTEGER NOT NULL,
		updated_at INTEGER NOT NULL,
		rev INTEGER NOT NULL DEFAULT 0,
		dirty INTEGER NOT NULL DEFAULT 0
	)`,
	`CREATE INDEX IF NOT EXISTS entities_ws ON entities(workspace, deleted)`,
	`CREATE INDEX IF NOT EXISTS entities_dirty ON entities(workspace, dirty)`,
	`CREATE TABLE IF NOT EXISTS history (
		id TEXT PRIMARY KEY,
		workspace TEXT NOT NULL,
		collection_id TEXT NOT NULL DEFAULT '',
		request_id TEXT NOT NULL DEFAULT '',
		method TEXT NOT NULL,
		url TEXT NOT NULL,
		status INTEGER NOT NULL,
		time_ms INTEGER NOT NULL,
		size INTEGER NOT NULL,
		at INTEGER NOT NULL,
		request BLOB,
		response BLOB,
		sealed INTEGER NOT NULL DEFAULT 0
	)`,
	`CREATE INDEX IF NOT EXISTS history_ws ON history(workspace, at DESC)`,
	`CREATE TABLE IF NOT EXISTS settings (
		key TEXT PRIMARY KEY,
		value TEXT NOT NULL
	)`,
}

func (s *Store) migrate() error {
	var v int
	if err := s.db.QueryRow("PRAGMA user_version").Scan(&v); err != nil {
		return err
	}
	if v >= schemaVersion {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, stmt := range schema {
		if _, err := tx.Exec(stmt); err != nil {
			return fmt.Errorf("migrate: %w", err)
		}
	}
	if _, err := tx.Exec(fmt.Sprintf("PRAGMA user_version = %d", schemaVersion)); err != nil {
		return err
	}
	return tx.Commit()
}

func now() int64 { return time.Now().UnixMilli() }

func newDeviceID() string {
	return base64.RawURLEncoding.EncodeToString([]byte(crypto.RandomToken(9)))
}

/* ------------------------------------------------------------------ */
/* Vault                                                              */
/* ------------------------------------------------------------------ */

// VaultEnabled reports whether at-rest encryption is configured.
func (s *Store) VaultEnabled() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.enabled
}

// Unlocked reports whether the vault key is available in memory.
func (s *Store) Unlocked() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.key) == crypto.KeySize
}

// SetKey installs the vault key for this session.
func (s *Store) SetKey(key []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(key) == 0 {
		s.key = nil
		return
	}
	s.key = append([]byte(nil), key...)
}

// Key returns a copy of the in-memory vault key, or nil.
func (s *Store) Key() []byte {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if len(s.key) != crypto.KeySize {
		return nil
	}
	return append([]byte(nil), s.key...)
}

// SetEnabled flips the vault flag. Callers persist the setting separately.
func (s *Store) SetEnabled(v bool) {
	s.mu.Lock()
	s.enabled = v
	s.mu.Unlock()
}

// secretSettingKeys are values that must never sit in the settings table as
// plaintext when the vault is enabled. Sync tokens are bearer credentials;
// anyone with a copy of the database could otherwise use them.
var secretSettingKeys = []string{"sync.token", "sync.refresh"}

type settingsQuerier interface {
	Query(string, ...any) (*sql.Rows, error)
}

func allSecretSettingKeys(db settingsQuerier) ([]string, error) {
	keys := append([]string(nil), secretSettingKeys...)
	rows, err := db.Query(`SELECT key FROM settings WHERE key LIKE 'sync.key.%'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			return nil, err
		}
		keys = append(keys, key)
	}
	return keys, rows.Err()
}

const sealedSettingPrefix = "v1:"

// SettingSecret reads a secret setting. Legacy plaintext values are returned
// as-is so existing installations keep working until the next unlock or write.
func (s *Store) SettingSecret(key string) string {
	v := s.Setting(key)
	if !strings.HasPrefix(v, sealedSettingPrefix) {
		return v
	}
	keyBytes := s.Key()
	if keyBytes == nil {
		return ""
	}
	plain, err := crypto.Open(keyBytes, strings.TrimPrefix(v, sealedSettingPrefix), crypto.SettingAAD(key))
	if err != nil {
		return ""
	}
	return string(plain)
}

// SetSecretSetting writes a secret setting, sealing it with the vault key when
// encryption is enabled. An empty value deletes the setting.
func (s *Store) SetSecretSetting(key, value string) error {
	if value == "" {
		return s.DeleteSetting(key)
	}
	if !s.VaultEnabled() {
		return s.SetSetting(key, value)
	}
	keyBytes := s.Key()
	if keyBytes == nil {
		return ErrLocked
	}
	ct, err := crypto.Seal(keyBytes, []byte(value), crypto.SettingAAD(key))
	if err != nil {
		return err
	}
	return s.SetSetting(key, sealedSettingPrefix+ct)
}

// SealSettings encrypts legacy plaintext secret settings with dek.
func (s *Store) SealSettings(dek []byte) error {
	if len(dek) != crypto.KeySize {
		return errors.New("store: invalid vault key")
	}
	keys, err := allSecretSettingKeys(s.db)
	if err != nil {
		return err
	}
	for _, key := range keys {
		v := s.Setting(key)
		if v == "" || strings.HasPrefix(v, sealedSettingPrefix) {
			continue
		}
		ct, err := crypto.Seal(dek, []byte(v), crypto.SettingAAD(key))
		if err != nil {
			return err
		}
		if err := s.SetSetting(key, sealedSettingPrefix+ct); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) resealSettings(tx *sql.Tx, oldKey, newKey []byte) error {
	keys, err := allSecretSettingKeys(tx)
	if err != nil {
		return err
	}
	for _, key := range keys {
		var v string
		if err := tx.QueryRow(`SELECT value FROM settings WHERE key = ?`, key).Scan(&v); err != nil {
			continue
		}
		if !strings.HasPrefix(v, sealedSettingPrefix) {
			ct, err := crypto.Seal(newKey, []byte(v), crypto.SettingAAD(key))
			if err != nil {
				return err
			}
			if _, err := tx.Exec(`UPDATE settings SET value = ? WHERE key = ?`, sealedSettingPrefix+ct, key); err != nil {
				return err
			}
			continue
		}
		if oldKey == nil {
			return ErrLocked
		}
		plain, err := crypto.Open(oldKey, strings.TrimPrefix(v, sealedSettingPrefix), crypto.SettingAAD(key))
		if err != nil {
			return err
		}
		ct, err := crypto.Seal(newKey, plain, crypto.SettingAAD(key))
		if err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE settings SET value = ? WHERE key = ?`, sealedSettingPrefix+ct, key); err != nil {
			return err
		}
	}
	return nil
}

// PublicSettings returns settings that are safe to hand to the UI bootstrap.
// Secret values are omitted entirely rather than exposing ciphertext.
func (s *Store) PublicSettings() map[string]string {
	all := s.Settings()
	for _, key := range secretSettingKeys {
		delete(all, key)
	}
	for key := range all {
		if strings.HasPrefix(key, "sync.key.") {
			delete(all, key)
		}
	}
	return all
}

func (s *Store) sealBlob(id string, payload []byte) ([]byte, bool, error) {
	key, enabled := s.Key(), s.VaultEnabled()
	if !enabled {
		return payload, false, nil
	}
	if key == nil {
		return nil, false, ErrLocked
	}
	ct, err := crypto.Seal(key, payload, crypto.EntityAAD(id))
	if err != nil {
		return nil, false, err
	}
	return []byte(ct), true, nil
}

func (s *Store) openBlob(id string, blob []byte, sealed bool) ([]byte, error) {
	if !sealed {
		return blob, nil
	}
	key := s.Key()
	if key == nil {
		return nil, ErrLocked
	}
	pt, err := crypto.Open(key, string(blob), crypto.EntityAAD(id))
	if err != nil {
		return nil, err
	}
	return pt, nil
}

// SealAll encrypts every plaintext entity and history payload with dek.
func (s *Store) SealAll(dek []byte) error {
	if len(dek) != crypto.KeySize {
		return errors.New("store: invalid vault key")
	}
	type item struct {
		id   string
		blob []byte
	}
	var entities []item
	rows, err := s.db.Query(`SELECT id, blob FROM entities WHERE sealed = 0`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var it item
		if err := rows.Scan(&it.id, &it.blob); err != nil {
			rows.Close()
			return err
		}
		entities = append(entities, it)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	var history []item
	hrows, err := s.db.Query(`SELECT id, request, response FROM history WHERE sealed = 0`)
	if err != nil {
		return err
	}
	for hrows.Next() {
		var it item
		var req, resp []byte
		if err := hrows.Scan(&it.id, &req, &resp); err != nil {
			hrows.Close()
			return err
		}
		wrapped, err := json.Marshal([2]json.RawMessage{json.RawMessage(orEmpty(req)), json.RawMessage(orEmpty(resp))})
		if err != nil {
			hrows.Close()
			return err
		}
		it.blob = wrapped
		history = append(history, it)
	}
	hrows.Close()
	if err := hrows.Err(); err != nil {
		return err
	}

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, it := range entities {
		ct, err := crypto.Seal(dek, it.blob, crypto.EntityAAD(it.id))
		if err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE entities SET blob = ?, sealed = 1 WHERE id = ?`, []byte(ct), it.id); err != nil {
			return err
		}
	}
	for _, it := range history {
		ct, err := crypto.Seal(dek, it.blob, crypto.HistoryAAD(it.id))
		if err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE history SET request = ?, response = NULL, sealed = 1 WHERE id = ?`, []byte(ct), it.id); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	s.SetKey(dek)
	s.SetEnabled(true)
	if err := s.SealSettings(dek); err != nil {
		return err
	}
	return nil
}

func orEmpty(b []byte) []byte {
	if len(b) == 0 {
		return []byte("null")
	}
	return b
}

// ReSeal decrypts everything with oldKey and re-encrypts with newKey for local
// vault-key migrations. Joining a workspace no longer replaces the vault key.
func (s *Store) ReSeal(oldKey, newKey []byte) error {
	if len(newKey) != crypto.KeySize {
		return errors.New("store: invalid vault key")
	}
	type item struct {
		id     string
		blob   []byte
		sealed bool
	}
	var entities []item
	rows, err := s.db.Query(`SELECT id, blob, sealed FROM entities`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var it item
		if err := rows.Scan(&it.id, &it.blob, &it.sealed); err != nil {
			rows.Close()
			return err
		}
		entities = append(entities, it)
	}
	rows.Close()

	type histItem struct {
		id      string
		request []byte
		resp    []byte
		sealed  bool
	}
	var history []histItem
	hrows, err := s.db.Query(`SELECT id, request, response, sealed FROM history`)
	if err != nil {
		return err
	}
	for hrows.Next() {
		var it histItem
		if err := hrows.Scan(&it.id, &it.request, &it.resp, &it.sealed); err != nil {
			hrows.Close()
			return err
		}
		history = append(history, it)
	}
	hrows.Close()

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, it := range entities {
		plain := it.blob
		if it.sealed {
			if oldKey == nil {
				return ErrLocked
			}
			plain, err = crypto.Open(oldKey, string(it.blob), crypto.EntityAAD(it.id))
			if err != nil {
				return err
			}
		}
		ct, err := crypto.Seal(newKey, plain, crypto.EntityAAD(it.id))
		if err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE entities SET blob = ?, sealed = 1 WHERE id = ?`, []byte(ct), it.id); err != nil {
			return err
		}
	}
	for _, it := range history {
		var plain []byte
		if it.sealed {
			if oldKey == nil {
				return ErrLocked
			}
			plain, err = crypto.Open(oldKey, string(it.request), crypto.HistoryAAD(it.id))
			if err != nil {
				return err
			}
		} else {
			if len(it.request) == 0 && len(it.resp) == 0 {
				continue
			}
			plain, err = json.Marshal([2]json.RawMessage{json.RawMessage(orEmpty(it.request)), json.RawMessage(orEmpty(it.resp))})
			if err != nil {
				return err
			}
		}
		ct, err := crypto.Seal(newKey, plain, crypto.HistoryAAD(it.id))
		if err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE history SET request = ?, response = NULL, sealed = 1 WHERE id = ?`, []byte(ct), it.id); err != nil {
			return err
		}
	}
	if err := s.resealSettings(tx, oldKey, newKey); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	s.SetKey(newKey)
	s.SetEnabled(true)
	return nil
}

/* ------------------------------------------------------------------ */
/* Settings                                                           */
/* ------------------------------------------------------------------ */

// Setting reads a setting, returning "" when absent.
func (s *Store) Setting(key string) string {
	var v string
	_ = s.db.QueryRow(`SELECT value FROM settings WHERE key = ?`, key).Scan(&v)
	return v
}

// SettingBool reads a boolean setting.
func (s *Store) SettingBool(key string) bool {
	v := s.Setting(key)
	return v == "1" || v == "true"
}

// SetSetting writes a setting.
func (s *Store) SetSetting(key, value string) error {
	_, err := s.db.Exec(`INSERT INTO settings(key, value) VALUES(?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	return err
}

// DeleteSetting removes a setting.
func (s *Store) DeleteSetting(key string) error {
	_, err := s.db.Exec(`DELETE FROM settings WHERE key = ?`, key)
	return err
}

// Settings returns all settings.
func (s *Store) Settings() map[string]string {
	out := map[string]string{}
	rows, err := s.db.Query(`SELECT key, value FROM settings`)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var k, v string
		if rows.Scan(&k, &v) == nil {
			out[k] = v
		}
	}
	return out
}

/* ------------------------------------------------------------------ */
/* Workspaces                                                         */
/* ------------------------------------------------------------------ */

func (s *Store) ensureDefaultWorkspace() error {
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM workspaces`).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	ts := now()
	_, err := s.db.Exec(`INSERT INTO workspaces(id, name, created_at, updated_at) VALUES(?, ?, ?, ?)`,
		"default", "Personal", ts, ts)
	return err
}

// ListWorkspaces returns all workspaces.
func (s *Store) ListWorkspaces() ([]*model.Workspace, error) {
	rows, err := s.db.Query(`SELECT id, name, description, created_at, updated_at,
		sync_server, sync_remote_id, sync_encrypted, sync_cursor, sync_last_at, sync_last_error
		FROM workspaces ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.Workspace
	for rows.Next() {
		var w model.Workspace
		var enc int
		if err := rows.Scan(&w.ID, &w.Name, &w.Description, &w.CreatedAt, &w.UpdatedAt,
			&w.Sync.Server, &w.Sync.RemoteID, &enc, &w.Sync.Cursor, &w.Sync.LastSyncAt, &w.Sync.LastError); err != nil {
			return nil, err
		}
		w.Sync.Encrypted = enc == 1
		out = append(out, &w)
	}
	return out, rows.Err()
}

// GetWorkspace returns one workspace.
func (s *Store) GetWorkspace(id string) (*model.Workspace, error) {
	all, err := s.ListWorkspaces()
	if err != nil {
		return nil, err
	}
	for _, w := range all {
		if w.ID == id {
			return w, nil
		}
	}
	return nil, ErrNotFound
}

// CreateWorkspace inserts a workspace.
func (s *Store) CreateWorkspace(w *model.Workspace) error {
	if w.CreatedAt == 0 {
		w.CreatedAt = now()
	}
	w.UpdatedAt = now()
	_, err := s.db.Exec(`INSERT INTO workspaces(id, name, description, created_at, updated_at) VALUES(?,?,?,?,?)`,
		w.ID, w.Name, w.Description, w.CreatedAt, w.UpdatedAt)
	return err
}

// UpdateWorkspace saves workspace metadata and sync linkage.
func (s *Store) UpdateWorkspace(w *model.Workspace) error {
	enc := 0
	if w.Sync.Encrypted {
		enc = 1
	}
	w.UpdatedAt = now()
	_, err := s.db.Exec(`UPDATE workspaces SET name=?, description=?, updated_at=?,
		sync_server=?, sync_remote_id=?, sync_encrypted=?, sync_cursor=?, sync_last_at=?, sync_last_error=?
		WHERE id=?`,
		w.Name, w.Description, w.UpdatedAt,
		w.Sync.Server, w.Sync.RemoteID, enc, w.Sync.Cursor, w.Sync.LastSyncAt, w.Sync.LastError, w.ID)
	return err
}

// DeleteWorkspace removes a workspace and everything in it.
func (s *Store) DeleteWorkspace(id string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, q := range []string{
		`DELETE FROM entities WHERE workspace = ?`,
		`DELETE FROM history WHERE workspace = ?`,
		`DELETE FROM workspaces WHERE id = ?`,
	} {
		if _, err := tx.Exec(q, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

/* ------------------------------------------------------------------ */
/* Entities                                                           */
/* ------------------------------------------------------------------ */

// CreateEntity inserts an entity and marks it dirty for sync.
func (s *Store) CreateEntity(e *model.Entity) error {
	ts := now()
	if e.CreatedAt == 0 {
		e.CreatedAt = ts
	}
	e.UpdatedAt = ts
	e.Dirty = true
	payload, err := json.Marshal(e.Payload())
	if err != nil {
		return err
	}
	blob, sealed, err := s.sealBlob(e.ID, payload)
	if err != nil {
		return err
	}
	e.Sealed = sealed
	_, err = s.db.Exec(`INSERT INTO entities(id, workspace, kind, blob, sealed, deleted, created_at, updated_at, rev, dirty)
		VALUES(?,?,?,?,?,0,?,?,0,1)`,
		e.ID, e.Workspace, e.Kind, blob, boolInt(sealed), e.CreatedAt, e.UpdatedAt)
	return err
}

// UpdateEntity saves an entity and marks it dirty for sync.
func (s *Store) UpdateEntity(e *model.Entity) error {
	e.UpdatedAt = now()
	e.Dirty = true
	payload, err := json.Marshal(e.Payload())
	if err != nil {
		return err
	}
	blob, sealed, err := s.sealBlob(e.ID, payload)
	if err != nil {
		return err
	}
	e.Sealed = sealed
	res, err := s.db.Exec(`UPDATE entities SET workspace=?, kind=?, blob=?, sealed=?, updated_at=?, dirty=1 WHERE id=?`,
		e.Workspace, e.Kind, blob, boolInt(sealed), e.UpdatedAt, e.ID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteEntity tombstones an entity so the deletion can be synced.
func (s *Store) DeleteEntity(id string) error {
	return s.DeleteEntities([]string{id})
}

// DeleteEntities tombstones all given entity IDs inside a single transaction.
func (s *Store) DeleteEntities(ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	ts := now()
	var total int64
	for _, id := range ids {
		res, err := tx.Exec(`UPDATE entities SET deleted=1, dirty=1, updated_at=? WHERE id=?`, ts, id)
		if err != nil {
			return err
		}
		n, _ := res.RowsAffected()
		total += n
	}
	if total == 0 {
		return ErrNotFound
	}
	return tx.Commit()
}

// RestoreEntity clears a tombstone so the entity is visible again.
func (s *Store) RestoreEntity(id string) error {
	var blob []byte
	err := s.db.QueryRow(`SELECT blob FROM entities WHERE id = ?`, id).Scan(&blob)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if len(blob) == 0 {
		return errors.New("store: cannot restore remote tombstone without payload")
	}
	res, err := s.db.Exec(`UPDATE entities SET deleted = 0, dirty = 1, updated_at = ? WHERE id = ?`, now(), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ListTrash returns deleted entities with their payloads decoded, so the UI can
// show names and hierarchy. A remote tombstone without a payload is still
// listed with the kind and an empty name.
func (s *Store) ListTrash(workspace string) ([]*model.Entity, error) {
	rows, err := s.db.Query(`SELECT `+entityCols+` FROM entities WHERE workspace = ? AND deleted = 1 ORDER BY updated_at DESC`, workspace)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.Entity
	for rows.Next() {
		var e model.Entity
		var blob []byte
		var sealed, deleted, dirty int
		if err := rows.Scan(&e.ID, &e.Workspace, &e.Kind, &blob, &sealed, &deleted,
			&e.CreatedAt, &e.UpdatedAt, &e.Rev, &dirty); err != nil {
			return nil, err
		}
		e.Deleted = true
		e.Dirty = dirty == 1
		e.Sealed = sealed == 1
		if len(blob) > 0 {
			plain, err := s.openBlob(e.ID, blob, e.Sealed)
			if err != nil {
				if errors.Is(err, ErrLocked) {
					return nil, ErrLocked
				}
			} else {
				var p model.EntityPayload
				if json.Unmarshal(plain, &p) == nil {
					e.ApplyPayload(p)
				}
			}
		}
		out = append(out, &e)
	}
	return out, rows.Err()
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func (s *Store) scanEntity(scanner interface{ Scan(...any) error }) (*model.Entity, error) {
	var e model.Entity
	var blob []byte
	var sealed, deleted, dirty int
	if err := scanner.Scan(&e.ID, &e.Workspace, &e.Kind, &blob, &sealed, &deleted,
		&e.CreatedAt, &e.UpdatedAt, &e.Rev, &dirty); err != nil {
		return nil, err
	}
	e.Sealed = sealed == 1
	e.Deleted = deleted == 1
	e.Dirty = dirty == 1
	if !e.Deleted {
		plain, err := s.openBlob(e.ID, blob, e.Sealed)
		if err != nil {
			return nil, err
		}
		var p model.EntityPayload
		if err := json.Unmarshal(plain, &p); err != nil {
			return nil, fmt.Errorf("decode entity %s: %w", e.ID, err)
		}
		e.ApplyPayload(p)
	}
	return &e, nil
}

const entityCols = `id, workspace, kind, blob, sealed, deleted, created_at, updated_at, rev, dirty`

// ListEntities returns entities for a workspace. When the vault is locked,
// ErrLocked is returned rather than leaking entity metadata.
func (s *Store) ListEntities(workspace string, includeDeleted bool) ([]*model.Entity, error) {
	q := `SELECT ` + entityCols + ` FROM entities WHERE workspace = ?`
	if !includeDeleted {
		q += ` AND deleted = 0`
	}
	q += ` ORDER BY created_at`
	rows, err := s.db.Query(q, workspace)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.Entity
	for rows.Next() {
		e, err := s.scanEntity(rows)
		if err != nil {
			if errors.Is(err, ErrLocked) {
				return nil, ErrLocked
			}
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// GetEntity returns one entity.
func (s *Store) GetEntity(id string) (*model.Entity, error) {
	row := s.db.QueryRow(`SELECT `+entityCols+` FROM entities WHERE id = ?`, id)
	e, err := s.scanEntity(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return e, err
}

// EntitySecret returns the sealed ciphertext for sync, or the base64 of the
// plaintext payload when the entity is not sealed.
func (s *Store) EntitySecret(e *model.Entity) (string, error) {
	var blob []byte
	var sealed int
	if err := s.db.QueryRow(`SELECT blob, sealed FROM entities WHERE id = ?`, e.ID).Scan(&blob, &sealed); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", ErrNotFound
		}
		return "", err
	}
	if sealed == 1 {
		return string(blob), nil
	}
	return base64.RawStdEncoding.EncodeToString(blob), nil
}

// DirtyEntities returns unsynced entities for a workspace.
func (s *Store) DirtyEntities(workspace string) ([]*model.Entity, error) {
	rows, err := s.db.Query(`SELECT `+entityCols+` FROM entities WHERE workspace = ? AND dirty = 1`, workspace)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.Entity
	for rows.Next() {
		var e model.Entity
		var blob []byte
		var sealed, deleted, dirty int
		if err := rows.Scan(&e.ID, &e.Workspace, &e.Kind, &blob, &sealed, &deleted,
			&e.CreatedAt, &e.UpdatedAt, &e.Rev, &dirty); err != nil {
			return nil, err
		}
		e.Sealed = sealed == 1
		e.Deleted = deleted == 1
		e.Dirty = dirty == 1
		out = append(out, &e)
	}
	return out, rows.Err()
}

// ResetWorkspaceSync prepares local records for a new remote namespace.
func (s *Store) ResetWorkspaceSync(workspace string) error {
	_, err := s.db.Exec(`UPDATE entities SET dirty=1,rev=0 WHERE workspace=?`, workspace)
	return err
}

// MarkSynced records the server revision for pushed entities.
func (s *Store) MarkSynced(revs map[string]int64) error {
	if len(revs) == 0 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for id, rev := range revs {
		if _, err := tx.Exec(`UPDATE entities SET dirty = 0, rev = ? WHERE id = ?`, rev, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ApplyRemote stores a record pulled from the sync server.
func (s *Store) ApplyRemote(workspace, id, kind string, blob []byte, sealed, deleted bool, createdAt, updatedAt, rev int64) error {
	if createdAt == 0 {
		createdAt = updatedAt
	}
	_, err := s.db.Exec(`INSERT INTO entities(id, workspace, kind, blob, sealed, deleted, created_at, updated_at, rev, dirty)
		VALUES(?,?,?,?,?,?,?,?,?,0)
		ON CONFLICT(id) DO UPDATE SET workspace=excluded.workspace, kind=excluded.kind, blob=excluded.blob,
			sealed=excluded.sealed, deleted=excluded.deleted, updated_at=excluded.updated_at,
			rev=excluded.rev, dirty=0`,
		id, workspace, kind, blob, boolInt(sealed), boolInt(deleted), createdAt, updatedAt, rev)
	return err
}

// EntityBlob returns the raw stored payload and seal state.
func (s *Store) EntityBlob(id string) ([]byte, bool, int64, int64, error) {
	var blob []byte
	var sealed int
	var updated, rev int64
	err := s.db.QueryRow(`SELECT blob, sealed, updated_at, rev FROM entities WHERE id = ?`, id).
		Scan(&blob, &sealed, &updated, &rev)
	return blob, sealed == 1, updated, rev, err
}

/* ------------------------------------------------------------------ */
/* History                                                            */
/* ------------------------------------------------------------------ */

// AddHistory stores a request/response pair, encrypting bodies when the
// vault is enabled.
func (s *Store) AddHistory(h *model.HistoryEntry) error {
	if h.ID == "" {
		return errors.New("store: history id required")
	}
	if h.At == 0 {
		h.At = now()
	}
	var reqPlain, respPlain []byte
	if len(h.Request) > 0 || len(h.Response) > 0 {
		reqPlain = orEmpty(h.Request)
		respPlain = orEmpty(h.Response)
	}
	var reqBlob, respBlob []byte
	var sealed bool
	if s.VaultEnabled() {
		if !s.Unlocked() {
			return ErrLocked
		}
		wrapped, err := json.Marshal([2]json.RawMessage{json.RawMessage(reqPlain), json.RawMessage(respPlain)})
		if err != nil {
			return err
		}
		ct, err := crypto.Seal(s.Key(), wrapped, crypto.HistoryAAD(h.ID))
		if err != nil {
			return err
		}
		reqBlob, respBlob, sealed = []byte(ct), nil, true
	} else {
		reqBlob, respBlob = reqPlain, respPlain
	}
	_, err := s.db.Exec(`INSERT INTO history(id, workspace, collection_id, request_id, method, url, status, time_ms, size, at, request, response, sealed)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		h.ID, h.Workspace, h.CollectionID, h.RequestID, h.Method, h.URL, h.Status, h.TimeMs, h.Size, h.At,
		reqBlob, respBlob, boolInt(sealed))
	if err != nil {
		return err
	}
	// Bound the local history.
	_, _ = s.db.Exec(`DELETE FROM history WHERE workspace = ? AND id NOT IN (
		SELECT id FROM history WHERE workspace = ? ORDER BY at DESC LIMIT 2000)`, h.Workspace, h.Workspace)
	return nil
}

// ListHistory returns recent history entries, newest first. Bodies are
// omitted when the vault is locked.
func (s *Store) ListHistory(workspace string, limit int) ([]*model.HistoryEntry, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.db.Query(`SELECT id, workspace, collection_id, request_id, method, url, status, time_ms, size, at, request, response, sealed
		FROM history WHERE workspace = ? ORDER BY at DESC LIMIT ?`, workspace, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.HistoryEntry
	for rows.Next() {
		var h model.HistoryEntry
		var req, resp []byte
		var sealed int
		if err := rows.Scan(&h.ID, &h.Workspace, &h.CollectionID, &h.RequestID, &h.Method, &h.URL,
			&h.Status, &h.TimeMs, &h.Size, &h.At, &req, &resp, &sealed); err != nil {
			return nil, err
		}
		h.Sealed = sealed == 1
		if sealed == 1 {
			if key := s.Key(); key != nil {
				plain, err := crypto.Open(key, string(req), crypto.HistoryAAD(h.ID))
				if err == nil {
					var pair [2]json.RawMessage
					if json.Unmarshal(plain, &pair) == nil {
						if string(pair[0]) != "null" {
							h.Request = pair[0]
						}
						if string(pair[1]) != "null" {
							h.Response = pair[1]
						}
					}
				}
			}
		} else {
			if len(req) > 0 {
				h.Request = req
			}
			if len(resp) > 0 {
				h.Response = resp
			}
		}
		out = append(out, &h)
	}
	return out, rows.Err()
}

// ClearHistory deletes all history for a workspace.
func (s *Store) ClearHistory(workspace string) error {
	_, err := s.db.Exec(`DELETE FROM history WHERE workspace = ?`, workspace)
	return err
}

// DeleteHistory deletes one history entry.
func (s *Store) DeleteHistory(id string) error {
	_, err := s.db.Exec(`DELETE FROM history WHERE id = ?`, id)
	return err
}

/* ------------------------------------------------------------------ */
/* Stats                                                              */
/* ------------------------------------------------------------------ */

// Stats returns coarse counts used by the UI.
func (s *Store) Stats(workspace string) map[string]int64 {
	out := map[string]int64{}
	queries := map[string]string{
		"requests":     `SELECT COUNT(*) FROM entities WHERE workspace = ? AND kind = 'request' AND deleted = 0`,
		"collections":  `SELECT COUNT(*) FROM entities WHERE workspace = ? AND kind = 'collection' AND deleted = 0`,
		"environments": `SELECT COUNT(*) FROM entities WHERE workspace = ? AND kind = 'environment' AND deleted = 0`,
		"history":      `SELECT COUNT(*) FROM history WHERE workspace = ?`,
	}
	for k, q := range queries {
		var n int64
		_ = s.db.QueryRow(q, workspace).Scan(&n)
		out[k] = n
	}
	return out
}
