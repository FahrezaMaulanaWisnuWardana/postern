// Package crypto implements the key derivation, authenticated encryption
// and password hashing used by YourPM.
//
// Two independent concerns live here:
//
//   - Vault encryption (end-to-end): a random data encryption key (DEK) is
//     wrapped with a key derived from a user passphrase (KEK) using
//     Argon2id. The KEK never leaves the client, so a sync server that
//     stores the wrapped DEK cannot read any workspace data.
//   - Password hashing for the sync server's own account database.
package crypto

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"golang.org/x/crypto/argon2"
	"golang.org/x/crypto/chacha20poly1305"
)

const (
	// KeySize is the byte length of vault keys.
	KeySize = 32
	// SaltSize is the byte length of KDF salts.
	SaltSize = 16

	argonTime    = 2
	argonMemory  = 64 * 1024 // 64 MiB
	argonThreads = 4
)

var (
	ErrDecrypt = errors.New("crypto: decryption failed")
	ErrFormat  = errors.New("crypto: malformed ciphertext")
)

// vaultAAD binds a wrapped key to its purpose so ciphertexts cannot be
// replayed in another context.
const vaultAAD = "yourpm:vault:v1"

// NewKey returns a fresh random data encryption key.
func NewKey() ([]byte, error) {
	k := make([]byte, KeySize)
	if _, err := rand.Read(k); err != nil {
		return nil, err
	}
	return k, nil
}

// NewSalt returns a fresh random salt.
func NewSalt() ([]byte, error) {
	s := make([]byte, SaltSize)
	if _, err := rand.Read(s); err != nil {
		return nil, err
	}
	return s, nil
}

// DeriveKEK stretches a passphrase into a 32-byte key-encryption key.
func DeriveKEK(passphrase string, salt []byte) []byte {
	return argon2.IDKey([]byte(passphrase), salt, argonTime, argonMemory, argonThreads, KeySize)
}

// Seal encrypts plaintext with XChaCha20-Poly1305 and returns base64 text
// of nonce||ciphertext. The additional data is authenticated but not stored.
func Seal(key, plaintext, aad []byte) (string, error) {
	aead, err := chacha20poly1305.NewX(key)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	out := aead.Seal(nonce, nonce, plaintext, aad)
	return base64.RawStdEncoding.EncodeToString(out), nil
}

// Open decrypts a value produced by Seal.
func Open(key []byte, sealed string, aad []byte) ([]byte, error) {
	raw, err := base64.RawStdEncoding.DecodeString(sealed)
	if err != nil {
		return nil, ErrFormat
	}
	aead, err := chacha20poly1305.NewX(key)
	if err != nil {
		return nil, err
	}
	if len(raw) < aead.NonceSize() {
		return nil, ErrFormat
	}
	nonce, ct := raw[:aead.NonceSize()], raw[aead.NonceSize():]
	pt, err := aead.Open(nil, nonce, ct, aad)
	if err != nil {
		return nil, ErrDecrypt
	}
	return pt, nil
}

// SetupVault creates a new vault: a fresh DEK wrapped by a passphrase.
// It returns the salt and wrapped key to persist, plus the DEK for the
// current session.
func SetupVault(passphrase string) (salt []byte, wrapped string, dek []byte, err error) {
	salt, err = NewSalt()
	if err != nil {
		return nil, "", nil, err
	}
	dek, err = NewKey()
	if err != nil {
		return nil, "", nil, err
	}
	kek := DeriveKEK(passphrase, salt)
	wrapped, err = Seal(kek, dek, []byte(vaultAAD))
	if err != nil {
		return nil, "", nil, err
	}
	return salt, wrapped, dek, nil
}

// UnlockVault recovers the DEK from a wrapped key and passphrase.
func UnlockVault(passphrase string, salt []byte, wrapped string) ([]byte, error) {
	kek := DeriveKEK(passphrase, salt)
	dek, err := Open(kek, wrapped, []byte(vaultAAD))
	if err != nil {
		return nil, ErrDecrypt
	}
	if len(dek) != KeySize {
		return nil, ErrDecrypt
	}
	return dek, nil
}

// RewrapVault re-wraps an existing DEK under a new passphrase. No data
// re-encryption is needed because the DEK itself is unchanged.
func RewrapVault(oldPass, newPass string, salt []byte, wrapped string) (newSalt []byte, newWrapped string, dek []byte, err error) {
	dek, err = UnlockVault(oldPass, salt, wrapped)
	if err != nil {
		return nil, "", nil, err
	}
	newSalt, err = NewSalt()
	if err != nil {
		return nil, "", nil, err
	}
	kek := DeriveKEK(newPass, newSalt)
	newWrapped, err = Seal(kek, dek, []byte(vaultAAD))
	if err != nil {
		return nil, "", nil, err
	}
	return newSalt, newWrapped, dek, nil
}

// EntityAAD returns the additional authenticated data for a record. It is
// bound to the entity ID, which is stable across devices and workspaces, so
// ciphertext pulled from a server can be stored locally without re-sealing
// and a malicious server cannot move ciphertext between records.
func EntityAAD(entityID string) []byte {
	return []byte("yourpm:entity:v1:" + entityID)
}

// HistoryAAD returns the additional authenticated data for a history blob.
func HistoryAAD(historyID string) []byte {
	return []byte("yourpm:history:v1:" + historyID)
}

// SettingAAD returns the additional authenticated data for a secret setting.
// Settings use their own domain so a sealed token cannot be replayed as an
// entity or history payload.
func SettingAAD(key string) []byte {
	return []byte("yourpm:setting:v1:" + key)
}

// BackupAAD returns the additional authenticated data for a passphrase-encrypted
// workspace backup. A backup ciphertext cannot be reused as vault, entity, or
// history data.
func BackupAAD() []byte {
	return []byte("yourpm:backup:v1")
}

// HashPassword hashes a password with Argon2id and returns a PHC string.
func HashPassword(password string) (string, error) {
	salt, err := NewSalt()
	if err != nil {
		return "", err
	}
	key := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, KeySize)
	return fmt.Sprintf("$argon2id$v=19$m=%d,t=%d,p=%d$%s$%s",
		argonMemory, argonTime, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key),
	), nil
}

// VerifyPassword checks a password against a PHC string in constant time.
func VerifyPassword(encoded, password string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false
	}
	var m, t, p uint64
	for _, kv := range strings.Split(parts[3], ",") {
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			return false
		}
		n, err := strconv.ParseUint(v, 10, 32)
		if err != nil {
			return false
		}
		switch k {
		case "m":
			m = n
		case "t":
			t = n
		case "p":
			p = n
		default:
			return false
		}
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return false
	}
	got := argon2.IDKey([]byte(password), salt, uint32(t), uint32(m), uint8(p), uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1
}

// RandomToken returns a URL-safe random token with n bytes of entropy.
func RandomToken(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic("crypto: cannot read random bytes: " + err.Error())
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// HashToken hashes a bearer token for storage. Tokens carry 256 bits of
// entropy, so a fast hash is sufficient and keeps lookups cheap.
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}
