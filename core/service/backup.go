package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"yourpm/core/crypto"
	"yourpm/core/id"
	"yourpm/core/model"
)

// backupFormat identifies a portable, plaintext workspace archive. It is meant
// for local backups and migration, not for sharing over the sync server.
const backupFormat = "yourpm.workspace.backup"

// encryptedBackupFormat wraps a plaintext archive in an authenticated,
// passphrase-encrypted envelope. The inner JSON never touches disk in that
// form when the caller asks for encryption.
const encryptedBackupFormat = "yourpm.workspace.backup.encrypted"

const (
	backupKDF    = "argon2id"
	backupCipher = "xchacha20poly1305"
)

// ErrEncryptedBackup is returned by ImportWorkspace when the archive is
// encrypted. Call ImportWorkspaceEncrypted with the passphrase instead.
var ErrEncryptedBackup = errors.New("backup is encrypted; a passphrase is required")

type backupEntity struct {
	ID       string          `json:"id"`
	Kind     string          `json:"kind"`
	Name     string          `json:"name"`
	ParentID string          `json:"parentId,omitempty"`
	Position float64         `json:"position"`
	Data     json.RawMessage `json:"data,omitempty"`
}

type backupFile struct {
	Format      string         `json:"format"`
	Version     int            `json:"version"`
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	ExportedAt  int64          `json:"exportedAt"`
	Entities    []backupEntity `json:"entities"`
}

type backupEnvelope struct {
	Format  string `json:"format"`
	Version int    `json:"version"`
	KDF     string `json:"kdf"`
	Cipher  string `json:"cipher"`
	Salt    []byte `json:"salt"`
	Data    string `json:"data"`
}

// ExportWorkspace renders a complete workspace as plaintext JSON. The vault
// must be unlocked when encryption is enabled, because the archive is
// plaintext. Use ExportWorkspaceEncrypted to protect it with a passphrase.
func (s *Service) ExportWorkspace(wsID string) ([]byte, error) {
	return s.ExportWorkspaceEncrypted(wsID, "")
}

// ExportWorkspaceEncrypted renders a complete workspace as JSON. When
// passphrase is non-empty the archive is compressed into the encrypted
// envelope format, so the exported file is unreadable without it.
func (s *Service) ExportWorkspaceEncrypted(wsID, passphrase string) ([]byte, error) {
	plain, err := s.exportPlain(wsID)
	if err != nil {
		return nil, err
	}
	passphrase = strings.TrimSpace(passphrase)
	if passphrase == "" {
		return plain, nil
	}
	if len(passphrase) < 8 {
		return nil, errors.New("backup passphrase must be at least 8 characters")
	}
	salt, err := crypto.NewSalt()
	if err != nil {
		return nil, err
	}
	key := crypto.DeriveKEK(passphrase, salt)
	data, err := crypto.Seal(key, plain, crypto.BackupAAD())
	if err != nil {
		return nil, err
	}
	return json.MarshalIndent(backupEnvelope{
		Format:  encryptedBackupFormat,
		Version: 1,
		KDF:     backupKDF,
		Cipher:  backupCipher,
		Salt:    salt,
		Data:    data,
	}, "", "  ")
}

func (s *Service) exportPlain(wsID string) ([]byte, error) {
	ws, err := s.Store.GetWorkspace(wsID)
	if err != nil {
		return nil, err
	}
	ents, err := s.Store.ListEntities(wsID, false)
	if err != nil {
		return nil, err
	}
	out := backupFile{
		Format:      backupFormat,
		Version:     1,
		Name:        ws.Name,
		Description: ws.Description,
		ExportedAt:  time.Now().UnixMilli(),
		Entities:    make([]backupEntity, 0, len(ents)),
	}
	for _, e := range ents {
		out.Entities = append(out.Entities, backupEntity{
			ID:       e.ID,
			Kind:     e.Kind,
			Name:     e.Name,
			ParentID: e.ParentID,
			Position: e.Position,
			Data:     e.Data,
		})
	}
	return json.MarshalIndent(out, "", "  ")
}

// ImportWorkspace restores a plaintext archive. It returns ErrEncryptedBackup
// when the file was exported with a passphrase.
func (s *Service) ImportWorkspace(raw []byte, name string) (*model.Workspace, error) {
	return s.ImportWorkspaceEncrypted(raw, name, "")
}

// ImportWorkspaceEncrypted restores an archive into a new local workspace. IDs
// are remapped so a backup can be imported next to the workspace it came from
// without collisions. The new workspace is not linked to a sync server until
// the user explicitly links it. The passphrase is ignored for plaintext
// archives.
func (s *Service) ImportWorkspaceEncrypted(raw []byte, name, passphrase string) (*model.Workspace, error) {
	plain, err := decodeBackup(raw, passphrase)
	if err != nil {
		return nil, err
	}
	return s.importPlain(plain, name)
}

func decodeBackup(raw []byte, passphrase string) ([]byte, error) {
	passphrase = strings.TrimSpace(passphrase)
	var head struct {
		Format  string `json:"format"`
		Version int    `json:"version"`
	}
	if err := json.Unmarshal(raw, &head); err != nil {
		return nil, errors.New("backup is not valid JSON")
	}
	switch head.Format {
	case backupFormat:
		return raw, nil
	case encryptedBackupFormat:
		var env backupEnvelope
		if err := json.Unmarshal(raw, &env); err != nil {
			return nil, errors.New("backup envelope is not valid JSON")
		}
		if env.Version != 1 {
			return nil, fmt.Errorf("unsupported encrypted backup version %d", env.Version)
		}
		if env.KDF != backupKDF || env.Cipher != backupCipher {
			return nil, errors.New("unsupported backup encryption")
		}
		if strings.TrimSpace(passphrase) == "" {
			return nil, ErrEncryptedBackup
		}
		if len(env.Salt) != crypto.SaltSize {
			return nil, errors.New("backup salt is invalid")
		}
		key := crypto.DeriveKEK(passphrase, env.Salt)
		plain, err := crypto.Open(key, env.Data, crypto.BackupAAD())
		if err != nil {
			return nil, errors.New("could not decrypt the backup: wrong passphrase or corrupted file")
		}
		return plain, nil
	default:
		return nil, errors.New("not a workspace backup")
	}
}

func (s *Service) importPlain(raw []byte, name string) (*model.Workspace, error) {
	var in backupFile
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, errors.New("backup is not valid JSON")
	}
	if in.Format != backupFormat {
		return nil, errors.New("not a workspace backup")
	}
	if in.Version != 1 {
		return nil, fmt.Errorf("unsupported backup version %d", in.Version)
	}
	if strings.TrimSpace(name) == "" {
		name = in.Name
	}
	if strings.TrimSpace(name) == "" {
		name = "Restored workspace"
	}
	ws, err := s.CreateWorkspace(name)
	if err != nil {
		return nil, err
	}
	ws.Description = in.Description
	_ = s.Store.UpdateWorkspace(ws)

	ids := map[string]string{}
	for _, e := range in.Entities {
		ids[e.ID] = id.New()
	}
	// Create parents before children when the archive is ordered strangely.
	pending := append([]backupEntity(nil), in.Entities...)
	for len(pending) > 0 {
		next := pending[:0]
		progress := false
		for _, src := range pending {
			if src.ParentID != "" && ids[src.ParentID] == "" {
				next = append(next, src)
				continue
			}
			e := &model.Entity{
				ID:        ids[src.ID],
				Workspace: ws.ID,
				Kind:      src.Kind,
				Name:      src.Name,
				ParentID:  ids[src.ParentID],
				Position:  src.Position,
				Data:      src.Data,
			}
			if e.Kind == "" {
				e.Kind = model.KindRequest
			}
			if len(e.Data) == 0 {
				e.Data = json.RawMessage(`{}`)
			}
			if err := s.Store.CreateEntity(e); err != nil {
				return ws, err
			}
			progress = true
		}
		if !progress {
			return ws, errors.New("backup contains a parent cycle")
		}
		pending = next
	}
	return ws, nil
}
