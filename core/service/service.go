// Package service is the UI-free application layer: it wires the local
// store, the HTTP engine, importers and the sync client together.
package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"yourpm/core/brand"
	"yourpm/core/crypto"
	"yourpm/core/httpx"
	"yourpm/core/id"
	"yourpm/core/model"
	"yourpm/core/store"
)

// Service is the application facade used by the desktop UI and the CLI.
type Service struct {
	Store   *store.Store
	Engine  *httpx.Executor
	DataDir string
}

// Open opens the local database inside dataDir.
func Open(dataDir string) (*Service, error) {
	if dataDir == "" {
		dir, err := DefaultDataDir()
		if err != nil {
			return nil, err
		}
		dataDir = dir
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, err
	}
	dbPath, err := dbPathFor(dataDir, brand.Slug)
	if err != nil {
		return nil, err
	}
	st, err := store.Open(dbPath)
	if err != nil {
		return nil, err
	}
	svc := &Service{Store: st, Engine: httpx.New(), DataDir: dataDir}
	// Authorization-code tokens live in the service layer so they can be
	// persisted and refreshed outside the request path.
	svc.Engine.SetOAuthProvider(svc.oauthAccessToken)
	return svc, nil
}

// legacySlugs only applies to the product's own history, never white-labels.
// Storage/encryption formats and YOURPM_* variables stay compatible.
func legacySlugs(slug string) []string {
	switch slug {
	case "postern":
		return []string{"delanapostman", "yourpm", "rezapostmanalt"}
	case "yourpm":
		return []string{"rezapostmanalt"}
	default:
		return nil
	}
}

// existingFile distinguishes absence from unreadable/invalid paths. A failure
// must not silently select an empty replacement database.
func existingFile(path string) (bool, error) {
	st, err := os.Stat(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !st.Mode().IsRegular() {
		return false, fmt.Errorf("database path is not a regular file: %s", path)
	}
	return true, nil
}

// dbPathFor reuses a legacy database in place; no copying or moving live SQLite
// files (including WAL sidecars). Ambiguous histories require explicit choice.
func dbPathFor(dataDir, slug string) (string, error) {
	path := filepath.Join(dataDir, slug+".db")
	if found, err := existingFile(path); found || err != nil {
		return path, err
	}
	var matches []string
	for _, legacy := range legacySlugs(slug) {
		candidate := filepath.Join(dataDir, legacy+".db")
		found, err := existingFile(candidate)
		if err != nil {
			return "", err
		}
		if found {
			matches = append(matches, candidate)
		}
	}
	if len(matches) > 1 {
		return "", fmt.Errorf("multiple legacy databases in %s; back up and select one before upgrading", dataDir)
	}
	if len(matches) == 1 {
		return matches[0], nil
	}
	return path, nil
}

// DefaultDataDir returns the per-user data directory.
func DefaultDataDir() (string, error) {
	if v := os.Getenv("YOURPM_DATA"); v != "" {
		return v, nil
	}
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return defaultDataDir(base, brand.Slug)
}

func defaultDataDir(base, slug string) (string, error) {
	dir := filepath.Join(base, slug)
	path, err := dbPathFor(dir, slug)
	if err != nil {
		return "", err
	}
	if found, err := existingFile(path); found || err != nil {
		return dir, err
	}
	var matches []string
	for _, legacy := range legacySlugs(slug) {
		candidate := filepath.Join(base, legacy)
		path, err := dbPathFor(candidate, slug)
		if err != nil {
			return "", err
		}
		found, err := existingFile(path)
		if err != nil {
			return "", err
		}
		if found {
			matches = append(matches, candidate)
		}
	}
	if len(matches) > 1 {
		return "", errors.New("multiple legacy data directories found; set YOURPM_DATA or --data-dir to the one you want to use")
	}
	if len(matches) == 1 {
		return matches[0], nil
	}
	return dir, nil
}

// Close closes the store.
func (s *Service) Close() error { return s.Store.Close() }

/* ------------------------------------------------------------------ */
/* Bootstrap                                                          */
/* ------------------------------------------------------------------ */

// Bootstrap is the initial state the UI needs.
type Bootstrap struct {
	Workspaces    []*model.Workspace `json:"workspaces"`
	Active        string             `json:"active"`
	Entities      []*model.Entity    `json:"entities"`
	Environments  []*model.Entity    `json:"environments"`
	VaultEnabled  bool               `json:"vaultEnabled"`
	VaultUnlocked bool               `json:"vaultUnlocked"`
	Settings      map[string]string  `json:"settings"`
	Version       string             `json:"version"`
}

// Load returns the state for a workspace.
func (s *Service) Load(active string) (*Bootstrap, error) {
	ws, err := s.Store.ListWorkspaces()
	if err != nil {
		return nil, err
	}
	if len(ws) == 0 {
		return nil, errors.New("no workspaces")
	}
	if active == "" {
		active = ws[0].ID
	}
	b := &Bootstrap{
		Workspaces:    ws,
		Active:        active,
		Settings:      s.Store.PublicSettings(),
		VaultEnabled:  s.Store.VaultEnabled(),
		VaultUnlocked: s.Store.Unlocked(),
		Version:       brand.Version,
	}
	ents, err := s.Store.ListEntities(active, false)
	if err != nil {
		if errors.Is(err, store.ErrLocked) {
			return b, nil
		}
		return nil, err
	}
	b.Entities = ents
	for _, e := range ents {
		if e.Kind == model.KindEnvironment {
			b.Environments = append(b.Environments, e)
		}
	}
	return b, nil
}

// CreateWorkspace makes a new local workspace.
func (s *Service) CreateWorkspace(name string) (*model.Workspace, error) {
	if strings.TrimSpace(name) == "" {
		name = "Workspace"
	}
	w := &model.Workspace{ID: id.New(), Name: name}
	if err := s.Store.CreateWorkspace(w); err != nil {
		return nil, err
	}
	return w, nil
}

// DeleteWorkspace removes a workspace.
func (s *Service) DeleteWorkspace(id string) error {
	return s.Store.DeleteWorkspace(id)
}

/* ------------------------------------------------------------------ */
/* Entities                                                           */
/* ------------------------------------------------------------------ */

// NewEntity creates a node with sensible defaults for its kind.
func (s *Service) NewEntity(ws, kind, parentID, name string) (*model.Entity, error) {
	e := &model.Entity{
		ID:        id.New(),
		Workspace: ws,
		Kind:      kind,
		ParentID:  parentID,
		Position:  float64(time.Now().UnixMilli()),
		Name:      strings.TrimSpace(name),
	}
	if e.Name == "" {
		switch kind {
		case model.KindCollection:
			e.Name = "New collection"
		case model.KindFolder:
			e.Name = "New folder"
		case model.KindRequest:
			e.Name = "Untitled request"
		case model.KindEnvironment:
			e.Name = "New environment"
		default:
			e.Name = "Untitled"
		}
	}
	switch kind {
	case model.KindCollection:
		e.Data = mustJSON(model.CollectionData{Auth: model.Auth{Type: model.AuthNone}, Variables: []model.KV{}})
	case model.KindFolder:
		e.Data = mustJSON(model.FolderData{Auth: model.Auth{Type: model.AuthInherit}})
	case model.KindRequest:
		e.Data = mustJSON(model.RequestData{
			Method: "GET",
			Body:   model.Body{Mode: model.BodyNone, Lang: "json"},
			Auth:   model.Auth{Type: model.AuthInherit},
		})
	case model.KindEnvironment:
		e.Data = mustJSON(model.EnvironmentData{Variables: []model.KV{}})
	}
	if kind == model.KindRequest && parentID == "" {
		// Requests belong in a collection; create a default one if needed.
		col, err := s.defaultCollection(ws)
		if err == nil {
			e.ParentID = col.ID
		}
	}
	if err := s.Store.CreateEntity(e); err != nil {
		return nil, err
	}
	return e, nil
}

func (s *Service) defaultCollection(ws string) (*model.Entity, error) {
	ents, err := s.Store.ListEntities(ws, false)
	if err != nil {
		return nil, err
	}
	for _, e := range ents {
		if e.Kind == model.KindCollection && e.ParentID == "" {
			return e, nil
		}
	}
	col := &model.Entity{
		ID: id.New(), Workspace: ws, Kind: model.KindCollection,
		Name: "My collection", Position: float64(time.Now().UnixMilli()),
		Data: mustJSON(model.CollectionData{Auth: model.Auth{Type: model.AuthNone}, Variables: []model.KV{}}),
	}
	if err := s.Store.CreateEntity(col); err != nil {
		return nil, err
	}
	return col, nil
}

// SaveEntity persists name, parent, position and data.
func (s *Service) SaveEntity(e *model.Entity) error { return s.Store.UpdateEntity(e) }

// RenameEntity changes an entity's name.
func (s *Service) RenameEntity(ent *model.Entity, name string) error {
	ent.Name = strings.TrimSpace(name)
	return s.Store.UpdateEntity(ent)
}

// MoveEntity reparents an entity.
func (s *Service) MoveEntity(ent *model.Entity, parentID string, position float64) error {
	ent.ParentID = parentID
	ent.Position = position
	return s.Store.UpdateEntity(ent)
}

// DeleteEntity removes an entity and all of its descendants in one transaction.
func (s *Service) DeleteEntity(ent *model.Entity) error {
	ents, err := s.Store.ListEntities(ent.Workspace, false)
	if err != nil {
		return err
	}
	children := map[string][]*model.Entity{}
	for _, e := range ents {
		children[e.ParentID] = append(children[e.ParentID], e)
	}
	var ids []string
	var walk func(id string)
	walk = func(id string) {
		ids = append(ids, id)
		for _, child := range children[id] {
			walk(child.ID)
		}
	}
	walk(ent.ID)
	return s.Store.DeleteEntities(ids)
}

// Trash lists deleted entities that can still be restored.
func (s *Service) Trash(ws string) ([]*model.Entity, error) {
	return s.Store.ListTrash(ws)
}

// RestoreEntity revives a deleted entity and every trashed descendant. This
// intentionally restores a whole subtree so a folder does not come back empty.
// A descendant that was deleted earlier is revived as well; the trash does not
// record separate deletion batches.
func (s *Service) RestoreEntity(ent *model.Entity) error {
	all, err := s.Store.ListTrash(ent.Workspace)
	if err != nil {
		return err
	}
	children := map[string][]*model.Entity{}
	for _, e := range all {
		children[e.ParentID] = append(children[e.ParentID], e)
	}
	var walk func(id string) error
	walk = func(id string) error {
		if err := s.Store.RestoreEntity(id); err != nil {
			return err
		}
		for _, child := range children[id] {
			if err := walk(child.ID); err != nil {
				return err
			}
		}
		return nil
	}
	return walk(ent.ID)
}

// RequestOf decodes a request entity.
func RequestOf(e *model.Entity) (model.RequestData, error) {
	var r model.RequestData
	if len(e.Data) == 0 {
		return r, nil
	}
	err := json.Unmarshal(e.Data, &r)
	if r.Body.Mode == "" {
		r.Body.Mode = model.BodyNone
	}
	if r.Auth.Type == "" {
		r.Auth.Type = model.AuthInherit
	}
	return r, err
}

// EnvironmentOf decodes an environment entity.
func EnvironmentOf(e *model.Entity) (model.EnvironmentData, error) {
	var env model.EnvironmentData
	if len(e.Data) == 0 {
		return env, nil
	}
	err := json.Unmarshal(e.Data, &env)
	return env, err
}

// CollectionOf decodes a collection entity.
func CollectionOf(e *model.Entity) (model.CollectionData, error) {
	var c model.CollectionData
	if len(e.Data) == 0 {
		return c, nil
	}
	err := json.Unmarshal(e.Data, &c)
	return c, err
}

// SetRequest updates an entity's request payload.
func SetRequest(e *model.Entity, r model.RequestData) error {
	e.Data = mustJSON(r)
	return nil
}

// SetEnvironment updates an environment entity's payload.
func SetEnvironment(e *model.Entity, env model.EnvironmentData) error {
	e.Data = mustJSON(env)
	return nil
}

// SetCollection updates a collection entity's payload.
func SetCollection(e *model.Entity, c model.CollectionData) error {
	e.Data = mustJSON(c)
	return nil
}

func mustJSON(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		return json.RawMessage("{}")
	}
	return b
}

/* ------------------------------------------------------------------ */
/* Variables and sending                                              */
/* ------------------------------------------------------------------ */

// VariableMap builds the effective variables for a request.
func (s *Service) VariableMap(ws, envID, collectionID string, overrides map[string]string) (map[string]string, string) {
	ents, err := s.Store.ListEntities(ws, false)
	if err != nil {
		return overrides, ""
	}
	byID := map[string]*model.Entity{}
	for _, e := range ents {
		byID[e.ID] = e
	}
	out := map[string]string{}
	envName := ""

	// Collection variables, walking up to the root collection.
	seen := map[string]bool{}
	for cur := collectionID; cur != "" && !seen[cur]; {
		seen[cur] = true
		e := byID[cur]
		if e == nil {
			break
		}
		if e.Kind == model.KindCollection {
			if c, err := CollectionOf(e); err == nil {
				for _, kv := range c.Variables {
					if kv.Enabled() {
						out[kv.Key] = kv.Value
					}
				}
			}
			break
		}
		cur = e.ParentID
	}

	if envID != "" {
		if e := byID[envID]; e != nil && e.Kind == model.KindEnvironment {
			envName = e.Name
			if env, err := EnvironmentOf(e); err == nil {
				for _, kv := range env.Variables {
					if kv.Enabled() {
						out[kv.Key] = kv.Value
					}
				}
			}
		}
	}
	for k, v := range overrides {
		out[k] = v
	}
	return out, envName
}

// CollectionIDFor walks up to the enclosing collection.
func (s *Service) CollectionIDFor(ws, entityID string) string {
	ents, err := s.Store.ListEntities(ws, false)
	if err != nil {
		return ""
	}
	byID := map[string]*model.Entity{}
	for _, e := range ents {
		byID[e.ID] = e
	}
	seen := map[string]bool{}
	for cur := entityID; cur != "" && !seen[cur]; {
		seen[cur] = true
		e := byID[cur]
		if e == nil {
			return ""
		}
		if e.Kind == model.KindCollection {
			return e.ID
		}
		cur = e.ParentID
	}
	return ""
}

// Send executes a request, persists history and applies environment updates
// from scripts.
func (s *Service) Send(ctx context.Context, ws string, req model.RequestData, envID, collectionID, requestID string) *model.Response {
	return s.send(ctx, ws, req, envID, collectionID, requestID, nil)
}

// SendWith runs a request with extra variable overrides, which is how the CLI
// implements --var key=value. Overrides win over environment and collection
// variables.
func (s *Service) SendWith(ctx context.Context, ws string, req model.RequestData, envID, collectionID, requestID string, overrides map[string]string) *model.Response {
	return s.send(ctx, ws, req, envID, collectionID, requestID, overrides)
}

func (s *Service) send(ctx context.Context, ws string, req model.RequestData, envID, collectionID, requestID string, overrides map[string]string) *model.Response {
	s.PrepareRequest(ws, &req, collectionID)
	resp := s.Engine.Send(ctx, req, s.varsFor(ws, envID, collectionID, overrides), httpx.Options{EnvName: s.envName(ws, envID)})

	// Persist variables set or unset by scripts back into the environment.
	if (len(resp.EnvUpdates) > 0 || len(resp.EnvUnsets) > 0) && envID != "" {
		if e, err := s.Store.GetEntity(envID); err == nil && e != nil {
			if env, err := EnvironmentOf(e); err == nil {
				if len(resp.EnvUnsets) > 0 {
					unsetSet := make(map[string]bool, len(resp.EnvUnsets))
					for _, k := range resp.EnvUnsets {
						unsetSet[k] = true
					}
					filtered := env.Variables[:0]
					for _, kv := range env.Variables {
						if !unsetSet[kv.Key] {
							filtered = append(filtered, kv)
						}
					}
					env.Variables = filtered
				}
				for k, v := range resp.EnvUpdates {
					found := false
					for i := range env.Variables {
						if env.Variables[i].Key == k {
							env.Variables[i].Value = v
							env.Variables[i].Disabled = false
							found = true
							break
						}
					}
					if !found {
						env.Variables = append(env.Variables, model.KV{Key: k, Value: v})
					}
				}
				_ = SetEnvironment(e, env)
				_ = s.Store.UpdateEntity(e)
			}
		}
	}

	history := &model.HistoryEntry{
		ID:           id.New(),
		Workspace:    ws,
		CollectionID: collectionID,
		RequestID:    requestID,
		Method:       req.Method,
		URL:          resp.URL,

		Status: resp.Status,
		TimeMs: resp.TimeMs,
		Size:   int(resp.Size),
		At:     time.Now().UnixMilli(),
	}
	if b, err := json.Marshal(req); err == nil {
		history.Request = b
	}
	if b, err := json.Marshal(trimResponse(resp)); err == nil {
		history.Response = b
	}
	_ = s.Store.AddHistory(history)
	return resp
}

func (s *Service) varsFor(ws, envID, collectionID string, overrides map[string]string) map[string]string {
	vars, _ := s.VariableMap(ws, envID, collectionID, overrides)
	return vars
}

func (s *Service) envName(ws, envID string) string {
	if envID == "" {
		return ""
	}
	if e, err := s.Store.GetEntity(envID); err == nil && e != nil {
		return e.Name
	}
	return ""
}

// PrepareRequest applies collection-level inheritance (auth and scripts) and
// is called by the UI before Send.
func (s *Service) PrepareRequest(ws string, req *model.RequestData, collectionID string) {
	if collectionID == "" {
		return
	}
	e, err := s.Store.GetEntity(collectionID)
	if err != nil || e == nil || e.Kind != model.KindCollection {
		return
	}
	c, err := CollectionOf(e)
	if err != nil {
		return
	}
	if (req.Auth.Type == "" || req.Auth.Type == model.AuthInherit) && c.Auth.Type != "" && c.Auth.Type != model.AuthInherit {
		req.Auth = c.Auth
	}
	if c.PreRequestScript != "" {
		if req.PreRequestScript != "" {
			req.PreRequestScript = c.PreRequestScript + "\n" + req.PreRequestScript
		} else {
			req.PreRequestScript = c.PreRequestScript
		}
	}
	if c.TestScript != "" {
		if req.TestScript != "" {
			req.TestScript = c.TestScript + "\n" + req.TestScript
		} else {
			req.TestScript = c.TestScript
		}
	}
}

func trimResponse(r *model.Response) *model.Response {
	if r == nil {
		return r
	}
	c := *r
	if len(c.Body) > 1<<20 {
		c.Body = c.Body[:1<<20] + "\n… truncated for history"
	}
	c.BodyBase64 = ""
	if len(c.Headers) > 200 {
		c.Headers = c.Headers[:200]
	}
	return &c
}

/* ------------------------------------------------------------------ */
/* History                                                            */
/* ------------------------------------------------------------------ */

// History returns recent entries.
func (s *Service) History(ws string, limit int) ([]*model.HistoryEntry, error) {
	return s.Store.ListHistory(ws, limit)
}

// ClearHistory removes all history for a workspace.
func (s *Service) ClearHistory(ws string) error { return s.Store.ClearHistory(ws) }

/* ------------------------------------------------------------------ */
/* Vault                                                              */
/* ------------------------------------------------------------------ */

func (s *Service) VaultStatus() (enabled, unlocked bool) {
	return s.Store.VaultEnabled(), s.Store.Unlocked()
}

// VaultSetup enables at-rest encryption and seals all existing data.
func (s *Service) VaultSetup(passphrase string) error {
	if len(strings.TrimSpace(passphrase)) < 8 {
		return errors.New("passphrase must be at least 8 characters")
	}
	salt, wrapped, dek, err := crypto.SetupVault(passphrase)
	if err != nil {
		return err
	}
	if err := s.Store.SealAll(dek); err != nil {
		return err
	}
	if err := s.Store.SetSetting("vault.enabled", "1"); err != nil {
		return err
	}
	if err := s.Store.SetSetting("vault.salt", encodeB64(salt)); err != nil {
		return err
	}
	if err := s.Store.SetSetting("vault.wrapped", wrapped); err != nil {
		return err
	}
	return nil
}

// VaultUnlock derives the key and installs it for this session.
func (s *Service) VaultUnlock(passphrase string) error {
	salt, err := decodeB64(s.Store.Setting("vault.salt"))
	if err != nil {
		return errors.New("vault is not configured")
	}
	wrapped := s.Store.Setting("vault.wrapped")
	if wrapped == "" {
		return errors.New("vault is not configured")
	}
	dek, err := crypto.UnlockVault(passphrase, salt, wrapped)
	if err != nil {
		return errors.New("incorrect passphrase")
	}
	s.Store.SetKey(dek)
	// Migrate tokens saved by older versions while the key is available.
	_ = s.Store.SealSettings(dek)
	return nil
}

// VaultLock forgets the key for this session.
func (s *Service) VaultLock() { s.Store.SetKey(nil) }

// VaultChangePassphrase re-wraps the data key.
func (s *Service) VaultChangePassphrase(oldPass, newPass string) error {
	if len(strings.TrimSpace(newPass)) < 8 {
		return errors.New("new passphrase must be at least 8 characters")
	}
	salt, err := decodeB64(s.Store.Setting("vault.salt"))
	if err != nil {
		return err
	}
	newSalt, newWrapped, dek, err := crypto.RewrapVault(oldPass, newPass, salt, s.Store.Setting("vault.wrapped"))
	if err != nil {
		return errors.New("incorrect current passphrase")
	}
	if err := s.Store.SetSetting("vault.salt", encodeB64(newSalt)); err != nil {
		return err
	}
	if err := s.Store.SetSetting("vault.wrapped", newWrapped); err != nil {
		return err
	}
	s.Store.SetKey(dek)
	return nil
}

/* ------------------------------------------------------------------ */
/* Helpers                                                            */
/* ------------------------------------------------------------------ */

func encodeB64(b []byte) string { return base64.RawStdEncoding.EncodeToString(b) }

func decodeB64(s string) ([]byte, error) { return base64.RawStdEncoding.DecodeString(s) }

// SortEntities returns entities ordered by position and name.
func SortEntities(ents []*model.Entity) {
	sort.SliceStable(ents, func(i, j int) bool {
		if ents[i].Position == ents[j].Position {
			return ents[i].Name < ents[j].Name
		}
		return ents[i].Position < ents[j].Position
	})
}

// Explain is a small helper for the CLI.
func Explain(e *model.Entity) string {
	return fmt.Sprintf("%s %q (%s)", e.Kind, e.Name, e.ID)
}
