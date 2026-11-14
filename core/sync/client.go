// Package sync implements the client side of YourPM's end-to-end encrypted
// sync protocol.
//
// The server stores opaque ciphertext. A record's additional authenticated
// data is the entity ID, preventing ciphertext from being moved between
// records. New links keep remote workspace keys separate from private local
// vault keys and re-encrypt locally at the sync boundary.
package sync

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"yourpm/core/brand"
	"yourpm/core/crypto"
	"yourpm/core/id"
	"yourpm/core/model"
	"yourpm/core/store"
)

// Client talks to one sync server using credentials stored locally.
type Client struct {
	Store *store.Store
	HTTP  *http.Client
}

// New creates a sync client.
func New(st *store.Store) *Client {
	return &Client{Store: st, HTTP: &http.Client{Timeout: 45 * time.Second}}
}

// Account is the locally stored sync account.
type Account struct {
	Server   string `json:"server"`
	Token    string `json:"token"`
	Refresh  string `json:"refresh,omitempty"`
	Email    string `json:"email"`
	DeviceID string `json:"deviceId"`
}

// Account reads the stored account.
func (c *Client) Account() Account {
	return Account{
		Server:   strings.TrimRight(c.Store.Setting("sync.server"), "/"),
		Refresh:  c.Store.SettingSecret("sync.refresh"),
		Token:    c.Store.SettingSecret("sync.token"),
		Email:    c.Store.Setting("sync.email"),
		DeviceID: c.Store.Setting("sync.deviceId"),
	}
}

// LoggedIn reports whether a token is available.
func (c *Client) LoggedIn() bool {
	a := c.Account()
	return a.Server != "" && a.Token != ""
}

// Login authenticates (or registers) and stores the token.
func (c *Client) Login(ctx context.Context, server, email, password string, register bool) (*Account, error) {
	server = strings.TrimRight(strings.TrimSpace(server), "/")
	if server == "" {
		return nil, errors.New("server URL is required")
	}
	if !strings.HasPrefix(server, "http://") && !strings.HasPrefix(server, "https://") {
		server = "https://" + server
	}
	path := "/v1/auth/login"
	if register {
		path = "/v1/auth/register"
	}
	body := map[string]string{
		"email":    strings.TrimSpace(email),
		"password": password,
		"device":   deviceName(),
	}
	var out struct {
		Token        string `json:"token"`
		RefreshToken string `json:"refreshToken"`
		DeviceID     string `json:"deviceId"`
	}
	if err := c.do(ctx, server, http.MethodPost, path, "", body, &out); err != nil {
		return nil, err
	}
	if out.Token == "" {
		return nil, errors.New("server returned no token")
	}
	acct := Account{Server: server, Token: out.Token, Refresh: out.RefreshToken, Email: email, DeviceID: out.DeviceID}
	_ = c.Store.SetSetting("sync.server", acct.Server)
	_ = c.Store.SetSecretSetting("sync.token", acct.Token)
	_ = c.Store.SetSecretSetting("sync.refresh", acct.Refresh)
	_ = c.Store.SetSetting("sync.email", acct.Email)
	_ = c.Store.SetSetting("sync.deviceId", acct.DeviceID)
	if acct.DeviceID == "" {
		acct.DeviceID = c.Store.Setting("sync.deviceId")
	}
	return &acct, nil
}

// Logout forgets the local token.
func (c *Client) Logout(ctx context.Context) {
	acct := c.Account()
	if acct.Server != "" && acct.Token != "" {
		_ = c.do(ctx, acct.Server, http.MethodPost, "/v1/auth/logout", acct.Token, nil, nil)
	}
	_ = c.Store.DeleteSetting("sync.token")
	_ = c.Store.DeleteSetting("sync.refresh")
}

// RemoteWorkspaces lists workspaces available to the account.
func (c *Client) RemoteWorkspaces(ctx context.Context) ([]model.RemoteWorkspace, error) {
	acct := c.Account()
	if acct.Token == "" {
		return nil, errors.New("not signed in")
	}
	var out struct {
		Workspaces []model.RemoteWorkspace `json:"workspaces"`
	}
	if err := c.do(ctx, acct.Server, http.MethodGet, "/v1/workspaces", acct.Token, nil, &out); err != nil {
		return nil, err
	}
	return out.Workspaces, nil
}

// Device is a signed-in client as listed by the server.
type Device struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	CreatedAt int64  `json:"createdAt"`
	LastSeen  int64  `json:"lastSeen"`
	Revoked   bool   `json:"revoked"`
}

// Devices lists the account's signed-in devices.
func (c *Client) Devices(ctx context.Context) ([]Device, error) {
	acct := c.Account()
	if acct.Token == "" {
		return nil, errors.New("not signed in")
	}
	var out struct {
		Devices []Device `json:"devices"`
	}
	if err := c.do(ctx, acct.Server, http.MethodGet, "/v1/devices", acct.Token, nil, &out); err != nil {
		return nil, err
	}
	return out.Devices, nil
}

// RevokeDevice invalidates another device's refresh token.
func (c *Client) RevokeDevice(ctx context.Context, id string) error {
	acct := c.Account()
	if acct.Token == "" {
		return errors.New("not signed in")
	}
	return c.do(ctx, acct.Server, http.MethodDelete, "/v1/devices/"+id, acct.Token, nil, nil)
}

// CreateRemote creates a workspace on the server. For encrypted workspaces the
// client supplies its KDF salt and wrapped workspace key.
func (c *Client) CreateRemote(ctx context.Context, name string, encrypted bool, salt, wrapped string) (*model.RemoteWorkspace, error) {
	acct := c.Account()
	body := map[string]any{"name": name, "encrypted": encrypted, "kdfSalt": salt, "wrappedKey": wrapped}
	var out model.RemoteWorkspace
	if err := c.do(ctx, acct.Server, http.MethodPost, "/v1/workspaces", acct.Token, body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// LinkResult describes a completed link.
type LinkResult struct {
	Remote       model.RemoteWorkspace `json:"remote"`
	ReSealed     bool                  `json:"reSealed"`
	VaultCreated bool                  `json:"vaultCreated"`
}

// Link keeps the remote encryption key separate from the local vault key.
func (c *Client) Link(ctx context.Context, local *model.Workspace, remote model.RemoteWorkspace, passphrase string) (*LinkResult, error) {
	current, err := c.RemoteWorkspace(ctx, remote.ID)
	if err != nil {
		return nil, err
	}
	remote = *current
	if local.Linked() && (local.Sync.RemoteID != remote.ID || local.Sync.Server != c.Account().Server) {
		return nil, errors.New("unlink the current workspace before linking another")
	}
	workspaces, err := c.Store.ListWorkspaces()
	if err != nil {
		return nil, err
	}
	for _, ws := range workspaces {
		if ws.ID != local.ID && ws.Sync.RemoteID == remote.ID && ws.Sync.Server == c.Account().Server {
			return nil, errors.New("this remote is already linked to local workspace “" + ws.Name + "”; select that workspace instead")
		}
	}
	res := &LinkResult{Remote: remote}
	if remote.Encrypted {
		if passphrase == "" {
			return nil, errors.New("passphrase is required for an encrypted workspace")
		}
		salt, err := base64.RawStdEncoding.DecodeString(remote.KDFSalt)
		if err != nil || len(salt) != crypto.SaltSize {
			return nil, errors.New("server returned an invalid key salt")
		}
		remoteKey, err := crypto.UnlockVault(passphrase, salt, remote.WrappedKey)
		if err != nil {
			return nil, errors.New("incorrect passphrase for this workspace")
		}
		res.VaultCreated = !c.Store.VaultEnabled()
		if err := c.ensureLocalVault(passphrase); err != nil {
			return nil, err
		}
		if err := c.saveWorkspaceKey(remote.ID, remoteKey); err != nil {
			return nil, err
		}
	}

	if !local.Linked() {
		if err := c.Store.ResetWorkspaceSync(local.ID); err != nil {
			return nil, err
		}
	}
	local.Sync.RemoteID = remote.ID
	local.Sync.Server = c.Account().Server
	local.Sync.Encrypted = remote.Encrypted
	local.Sync.Cursor = 0
	local.Sync.LastError = ""
	return res, c.Store.UpdateWorkspace(local)
}

// Unlink disconnects a local workspace from its server.
func (c *Client) Unlink(local *model.Workspace) error {
	local.Sync = model.Sync{}
	return c.Store.UpdateWorkspace(local)
}

/* ------------------------------------------------------------------ */
/* Sync                                                               */
/* ------------------------------------------------------------------ */

// Result summarises a sync run.
type Result struct {
	Pushed    int      `json:"pushed"`
	Pulled    int      `json:"pulled"`
	Conflicts int      `json:"conflicts"`
	Errors    []string `json:"errors,omitempty"`
	At        int64    `json:"at"`
}

type pushOp struct {
	OpID        string `json:"opId"`
	DocID       string `json:"docId"`
	Kind        string `json:"kind"`
	BaseVersion int64  `json:"baseVersion"`
	Ciphertext  string `json:"ciphertext,omitempty"`
	UpdatedAt   int64  `json:"updatedAt"`
	Deleted     bool   `json:"deleted,omitempty"`
}

type pushResponse struct {
	Accepted []struct {
		OpID    string `json:"opId"`
		Seq     int64  `json:"seq"`
		Version int64  `json:"version"`
	} `json:"accepted"`
	Conflicts []remoteOp `json:"conflicts"`
}

type remoteOp struct {
	Seq        int64  `json:"seq"`
	DocID      string `json:"docId"`
	Version    int64  `json:"version"`
	Kind       string `json:"kind"`
	Ciphertext string `json:"ciphertext"`
	UpdatedAt  int64  `json:"updatedAt"`
	Deleted    bool   `json:"deleted"`
}

type pullResponse struct {
	Ops     []remoteOp `json:"ops"`
	Next    int64      `json:"next"`
	HasMore bool       `json:"hasMore"`
}

// Run pushes local changes then pulls remote ones.
func (c *Client) Run(ctx context.Context, local *model.Workspace) (*Result, error) {
	acct := c.Account()
	if acct.Token == "" {
		return nil, errors.New("not signed in")
	}
	if local.Sync.RemoteID == "" {
		return nil, errors.New("workspace is not linked")
	}
	if local.Sync.Server != acct.Server {
		return nil, errors.New("sign in to this workspace's sync server first")
	}
	if local.Sync.Encrypted && !c.Store.Unlocked() {
		return nil, errors.New("vault is locked")
	}
	res := &Result{At: time.Now().UnixMilli()}
	remote, err := c.RemoteWorkspace(ctx, local.Sync.RemoteID)
	if err != nil {
		return res, err
	}
	if remote.Encrypted != local.Sync.Encrypted {
		return res, errors.New("remote encryption settings do not match the local link")
	}
	if remote.Encrypted && remote.KeyScope == "workspace" && c.Store.SettingSecret(c.keySetting(remote.ID)) == "" {
		return res, errors.New("workspace key unavailable; link the workspace again")
	}

	dirty, err := c.Store.DirtyEntities(local.ID)
	if err != nil {
		return res, err
	}
	if remote.Role == "viewer" {
		// Viewers may experiment locally, but must still be able to pull when
		// their local copy contains edits. Never send those edits to the server.
		dirty = nil
	}

	// Push in batches to keep requests small.
	const batch = 200
	var conflicts []remoteOp
	for start := 0; start < len(dirty); start += batch {
		end := start + batch
		if end > len(dirty) {
			end = len(dirty)
		}
		ops := make([]pushOp, 0, end-start)
		for _, e := range dirty[start:end] {
			op := pushOp{
				OpID:        id.New(),
				DocID:       e.ID,
				Kind:        e.Kind,
				BaseVersion: e.Rev,
				UpdatedAt:   e.UpdatedAt,
				Deleted:     e.Deleted,
			}
			if !e.Deleted {
				ct, err := c.cipherForPush(local, e)
				if err != nil {
					res.Errors = append(res.Errors, fmt.Sprintf("%s: %v", e.ID, err))
					continue
				}
				op.Ciphertext = ct
			}
			ops = append(ops, op)
		}
		if len(ops) == 0 {
			continue
		}
		var out pushResponse
		if err := c.do(ctx, acct.Server, http.MethodPost, "/v1/workspaces/"+local.Sync.RemoteID+"/push", acct.Token,
			map[string]any{"deviceId": acct.DeviceID, "ops": ops}, &out); err != nil {
			return res, err
		}
		revs := map[string]int64{}
		for _, a := range out.Accepted {
			// Accepted ops carry the doc id only in the request; map by order.
			for _, op := range ops {
				if op.OpID == a.OpID {
					revs[op.DocID] = a.Version
					break
				}
			}
			res.Pushed++
		}
		if err := c.Store.MarkSynced(revs); err != nil {
			return res, err
		}
		conflicts = append(conflicts, out.Conflicts...)
	}

	// Resolve conflicts: keep the local version as a conflict copy, accept
	// the server's version for the original record.
	for _, op := range conflicts {
		res.Conflicts++
		if err := c.resolveConflict(local, op); err != nil {
			res.Errors = append(res.Errors, err.Error())
		}
	}

	// Pull everything new.
	cursor := local.Sync.Cursor
	for i := 0; i < 1000; i++ {
		var out pullResponse
		path := fmt.Sprintf("/v1/workspaces/%s/pull?since=%d&limit=500", local.Sync.RemoteID, cursor)
		if err := c.do(ctx, acct.Server, http.MethodGet, path, acct.Token, nil, &out); err != nil {
			return res, err
		}
		pageErr := false
		for _, op := range out.Ops {
			if op.DocID == "" {
				continue
			}
			if err := c.applyRemote(local, op, true); err != nil {
				res.Errors = append(res.Errors, err.Error())
				pageErr = true
			} else {
				res.Pulled++
			}
		}
		if pageErr {
			break
		}
		if out.Next > cursor {
			cursor = out.Next
		}
		if !out.HasMore || len(out.Ops) == 0 {
			break
		}
	}

	local.Sync.Cursor = cursor
	local.Sync.LastSyncAt = res.At
	local.Sync.LastError = ""
	if len(res.Errors) > 0 {
		local.Sync.LastError = res.Errors[0]
	}
	if err := c.Store.UpdateWorkspace(local); err != nil {
		return res, err
	}
	return res, nil
}

func (c *Client) cipherForPush(local *model.Workspace, e *model.Entity) (string, error) {
	blob, sealed, _, _, err := c.Store.EntityBlob(e.ID)
	if err != nil {
		return "", err
	}
	if sealed {
		blob, err = crypto.Open(c.Store.Key(), string(blob), crypto.EntityAAD(e.ID))
		if err != nil {
			return "", err
		}
	}
	if local.Sync.Encrypted {
		key, err := c.workspaceKey(local.Sync.RemoteID)
		if err != nil {
			return "", err
		}
		return crypto.Seal(key, blob, crypto.EntityAAD(e.ID))
	}
	return base64.RawStdEncoding.EncodeToString(blob), nil
}

func (c *Client) resolveConflict(local *model.Workspace, server remoteOp) error {
	cur, err := c.Store.GetEntity(server.DocID)
	if err != nil {
		return c.applyRemote(local, server, false)
	}
	// Preserve local edits as a new entity.
	if cur.Dirty && !cur.Deleted {
		copyEnt := &model.Entity{
			ID:        id.New(),
			Workspace: cur.Workspace,
			Kind:      cur.Kind,
			Name:      cur.Name + " (conflicted copy)",
			ParentID:  cur.ParentID,
			Position:  cur.Position + 0.5,
			Data:      cur.Data,
		}
		if err := c.Store.CreateEntity(copyEnt); err != nil {
			return err
		}
	}
	return c.applyRemote(local, server, false)
}

func (c *Client) applyRemote(local *model.Workspace, op remoteOp, preserveDirty bool) error {
	if existing, err := c.Store.GetEntity(op.DocID); err == nil {
		if existing.Workspace != local.ID {
			return errors.New("remote record conflicts with a different local workspace; link into the existing workspace instead")
		}
		if existing.Rev >= op.Version {
			return nil
		}
	}
	var (
		blob   []byte
		sealed bool
	)
	if op.Deleted {
		return c.Store.ApplyRemote(local.ID, op.DocID, fallbackKind(op.Kind), nil, local.Sync.Encrypted, true,
			op.UpdatedAt, op.UpdatedAt, op.Version)
	}
	if local.Sync.Encrypted {
		if op.Ciphertext == "" {
			return fmt.Errorf("server sent an empty record for %s", op.DocID)
		}
		key, err := c.workspaceKey(local.Sync.RemoteID)
		if err != nil {
			return err
		}
		blob, err = crypto.Open(key, op.Ciphertext, crypto.EntityAAD(op.DocID))
		if err != nil {
			return fmt.Errorf("decrypt record %s: %w", op.DocID, err)
		}
	} else {
		plain, err := base64.RawStdEncoding.DecodeString(op.Ciphertext)
		if err != nil {
			return fmt.Errorf("decode record %s: %w", op.DocID, err)
		}
		blob = plain
	}
	if !json.Valid(blob) {
		return fmt.Errorf("invalid JSON payload for %s", op.DocID)
	}
	if c.Store.VaultEnabled() {
		ct, err := crypto.Seal(c.Store.Key(), blob, crypto.EntityAAD(op.DocID))
		if err != nil {
			return err
		}
		blob, sealed = []byte(ct), true
	}

	// If the local copy has unpushed edits, preserve it before overwriting.
	if preserveDirty {
		if cur, err := c.Store.GetEntity(op.DocID); err == nil && cur.Dirty && !cur.Deleted {
			copyEnt := &model.Entity{
				ID:        id.New(),
				Workspace: cur.Workspace,
				Kind:      cur.Kind,
				Name:      cur.Name + " (conflicted copy)",
				ParentID:  cur.ParentID,
				Position:  cur.Position + 0.5,
				Data:      cur.Data,
			}
			if err := c.Store.CreateEntity(copyEnt); err != nil {
				return err
			}
		}
	}

	createdAt := op.UpdatedAt
	if existing, err := c.Store.GetEntity(op.DocID); err == nil && existing != nil {
		createdAt = existing.CreatedAt
	}
	return c.Store.ApplyRemote(local.ID, op.DocID, fallbackKind(op.Kind), blob, sealed, false,
		createdAt, op.UpdatedAt, op.Version)
}

func fallbackKind(k string) string {
	if k == "" {
		return model.KindRequest
	}
	return k
}

func deviceName() string {
	host, err := os.Hostname()
	if err != nil || host == "" {
		return "desktop"
	}
	return host
}

/* ------------------------------------------------------------------ */
/* HTTP                                                               */
/* ------------------------------------------------------------------ */

// errUnauthorized marks a rejected access token so do can refresh once.
var errUnauthorized = errors.New("sync: unauthorized")

// do performs an authenticated request and transparently refreshes an expired
// access token. Refresh tokens rotate, so the new pair is persisted before the
// request is retried.
func (c *Client) do(ctx context.Context, server, method, path, token string, body, out any) error {
	err := c.send(ctx, server, method, path, token, body, out)
	if err == nil || token == "" || !errors.Is(err, errUnauthorized) {
		return err
	}
	if c.Store.SettingSecret("sync.refresh") == "" {
		return err
	}
	fresh, rerr := c.refresh(ctx, server)
	if rerr != nil {
		return err
	}
	return c.send(ctx, server, method, path, fresh, body, out)
}

func (c *Client) refresh(ctx context.Context, server string) (string, error) {
	refresh := c.Store.SettingSecret("sync.refresh")
	if refresh == "" {
		return "", errUnauthorized
	}
	var out struct {
		Token        string `json:"token"`
		RefreshToken string `json:"refreshToken"`
	}
	if err := c.send(ctx, server, http.MethodPost, "/v1/auth/refresh", "", map[string]string{"refreshToken": refresh}, &out); err != nil {
		return "", err
	}
	if out.Token == "" {
		return "", errUnauthorized
	}
	_ = c.Store.SetSecretSetting("sync.token", out.Token)
	if out.RefreshToken != "" {
		_ = c.Store.SetSecretSetting("sync.refresh", out.RefreshToken)
	}
	return out.Token, nil
}

func (c *Client) send(ctx context.Context, server, method, path, token string, body, out any) error {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, server+path, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", brand.Name+"/"+brand.Version)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("cannot reach sync server: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		var e struct {
			Error string `json:"error"`
		}
		msg := ""
		if json.Unmarshal(raw, &e) == nil {
			msg = e.Error
		}
		if resp.StatusCode == http.StatusUnauthorized {
			if msg == "" {
				msg = "access token rejected"
			}
			return fmt.Errorf("%w: %s", errUnauthorized, msg)
		}
		if msg != "" {
			return errors.New(msg)
		}
		return fmt.Errorf("server returned %d", resp.StatusCode)
	}
	if out != nil {
		if err := json.Unmarshal(raw, out); err != nil {
			return fmt.Errorf("invalid server response: %w", err)
		}
	}
	return nil
}
