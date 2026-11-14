package sync

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"yourpm/core/crypto"
	"yourpm/core/model"
)

func (c *Client) keySetting(remoteID string) string {
	return "sync.key." + crypto.HashToken(c.Account().Server+"/"+remoteID)
}

func (c *Client) saveWorkspaceKey(remoteID string, key []byte) error {
	if !c.Store.Unlocked() || !c.Store.VaultEnabled() {
		return errors.New("unlock your local vault first")
	}
	return c.Store.SetSecretSetting(c.keySetting(remoteID), base64.RawStdEncoding.EncodeToString(key))
}

func (c *Client) workspaceKey(remoteID string) ([]byte, error) {
	if !c.Store.Unlocked() {
		return nil, errors.New("vault is locked")
	}
	raw := c.Store.SettingSecret(c.keySetting(remoteID))
	if raw == "" {
		// Backwards compatibility with links made before per-workspace keys.
		if key := c.Store.Key(); len(key) == crypto.KeySize {
			return key, nil
		}
		return nil, errors.New("workspace key unavailable; link the workspace again")
	}
	key, err := base64.RawStdEncoding.DecodeString(raw)
	if err != nil || len(key) != crypto.KeySize {
		return nil, errors.New("invalid local workspace key")
	}
	return key, nil
}

func (c *Client) ensureLocalVault(passphrase string) error {
	if c.Store.VaultEnabled() {
		if !c.Store.Unlocked() {
			return errors.New("unlock your local vault first")
		}
		return nil
	}
	salt, wrapped, key, err := crypto.SetupVault(passphrase)
	if err != nil {
		return err
	}
	if err := c.Store.SealAll(key); err != nil {
		return err
	}
	for k, value := range map[string]string{
		"vault.enabled": "1", "vault.salt": base64.RawStdEncoding.EncodeToString(salt), "vault.wrapped": wrapped,
	} {
		if err := c.Store.SetSetting(k, value); err != nil {
			return err
		}
	}
	return nil
}

// CreateEncryptedRemote creates a separate random key, never the local vault DEK.
func (c *Client) CreateEncryptedRemote(ctx context.Context, name, passphrase string) (*model.RemoteWorkspace, error) {
	if len(passphrase) < 10 {
		return nil, errors.New("use a workspace passphrase of at least 10 characters")
	}
	salt, wrapped, _, err := crypto.SetupVault(passphrase)
	if err != nil {
		return nil, err
	}
	acct := c.Account()
	var out model.RemoteWorkspace
	err = c.do(ctx, acct.Server, http.MethodPost, "/v1/workspaces", acct.Token,
		map[string]any{"name": name, "encrypted": true, "keyScope": "workspace", "kdfSalt": base64.RawStdEncoding.EncodeToString(salt), "wrappedKey": wrapped}, &out)
	return &out, err
}

func (c *Client) RemoteWorkspace(ctx context.Context, remoteID string) (*model.RemoteWorkspace, error) {
	acct := c.Account()
	var out model.RemoteWorkspace
	err := c.do(ctx, acct.Server, http.MethodGet, "/v1/workspaces/"+url.PathEscape(remoteID), acct.Token, nil, &out)
	return &out, err
}

func (c *Client) Team(ctx context.Context, remoteID string) (*model.WorkspaceTeam, error) {
	acct := c.Account()
	var out model.WorkspaceTeam
	err := c.do(ctx, acct.Server, http.MethodGet, "/v1/workspaces/"+url.PathEscape(remoteID)+"/team", acct.Token, nil, &out)
	return &out, err
}

func (c *Client) Invitations(ctx context.Context) ([]model.WorkspaceInvitation, error) {
	acct := c.Account()
	var out struct {
		Invitations []model.WorkspaceInvitation `json:"invitations"`
	}
	err := c.do(ctx, acct.Server, http.MethodGet, "/v1/invitations", acct.Token, nil, &out)
	return out.Invitations, err
}

// InviteMember returns an unlock code to share privately, not through the sync
// server. The invite contains only a workspace key wrapped with this code.
func (c *Client) InviteMember(ctx context.Context, local *model.Workspace, email, role string) (*model.WorkspaceInvitation, string, error) {
	acct := c.Account()
	if !local.Linked() || acct.Server != local.Sync.Server {
		return nil, "", errors.New("sign in to the linked workspace's server first")
	}
	inv := model.WorkspaceInvitation{Email: email, Role: role}
	code := ""
	if local.Sync.Encrypted {
		key, err := c.workspaceKey(local.Sync.RemoteID)
		if err != nil {
			return nil, "", err
		}
		if bytes.Equal(key, c.Store.Key()) {
			return nil, "", errors.New("this legacy workspace uses your private vault key; create and link a new encrypted remote before inviting teammates")
		}
		code = crypto.RandomToken(32)
		salt, err := crypto.NewSalt()
		if err != nil {
			return nil, "", err
		}
		inv.KDFSalt = base64.RawStdEncoding.EncodeToString(salt)
		inv.WrappedKey, err = crypto.Seal(crypto.DeriveKEK(code, salt), key, []byte("yourpm:vault:v1"))
		if err != nil {
			return nil, "", err
		}
	}
	var out model.WorkspaceInvitation
	err := c.do(ctx, acct.Server, http.MethodPost, "/v1/workspaces/"+url.PathEscape(local.Sync.RemoteID)+"/invitations", acct.Token, inv, &out)
	return &out, code, err
}

// AcceptInvitation wraps the workspace key under the recipient's own workspace
// passphrase. Neither this passphrase nor the invitation code leaves the device.
func (c *Client) AcceptInvitation(ctx context.Context, inv model.WorkspaceInvitation, code, passphrase string) (*model.RemoteWorkspace, error) {
	acct := c.Account()
	body := map[string]string{}
	if inv.Encrypted {
		if len(passphrase) < 10 {
			return nil, errors.New("choose a personal workspace passphrase of at least 10 characters")
		}
		if c.Store.VaultEnabled() && !c.Store.Unlocked() {
			return nil, errors.New("unlock your local vault first")
		}
		salt, err := base64.RawStdEncoding.DecodeString(inv.KDFSalt)
		if err != nil || len(salt) != crypto.SaltSize {
			return nil, errors.New("invalid invitation salt")
		}
		newSalt, wrapped, _, err := crypto.RewrapVault(strings.TrimSpace(code), passphrase, salt, inv.WrappedKey)
		if err != nil {
			return nil, errors.New("incorrect invitation unlock code")
		}
		body["kdfSalt"], body["wrappedKey"] = base64.RawStdEncoding.EncodeToString(newSalt), wrapped
	}
	var remote model.RemoteWorkspace
	if err := c.do(ctx, acct.Server, http.MethodPost, "/v1/invitations/"+url.PathEscape(inv.ID)+"/accept", acct.Token, body, &remote); err != nil {
		return nil, err
	}
	return &remote, nil
}

func (c *Client) ChangeMemberRole(ctx context.Context, remoteID, memberID, role string) error {
	acct := c.Account()
	return c.do(ctx, acct.Server, http.MethodPatch, "/v1/workspaces/"+url.PathEscape(remoteID)+"/members/"+url.PathEscape(memberID), acct.Token, map[string]string{"role": role}, nil)
}

func (c *Client) RemoveMember(ctx context.Context, remoteID, memberID string) error {
	acct := c.Account()
	return c.do(ctx, acct.Server, http.MethodDelete, "/v1/workspaces/"+url.PathEscape(remoteID)+"/members/"+url.PathEscape(memberID), acct.Token, nil, nil)
}

func (c *Client) RevokeInvitation(ctx context.Context, remoteID, inviteID string) error {
	acct := c.Account()
	return c.do(ctx, acct.Server, http.MethodDelete, "/v1/workspaces/"+url.PathEscape(remoteID)+"/invitations/"+url.PathEscape(inviteID), acct.Token, nil, nil)
}
