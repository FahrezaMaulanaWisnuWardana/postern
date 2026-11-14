package crypto

import (
	"bytes"
	"strings"
	"testing"
)

func TestSealOpenRoundTrip(t *testing.T) {
	key, err := NewKey()
	if err != nil {
		t.Fatal(err)
	}
	aad := EntityAAD("entity-1")
	sealed, err := Seal(key, []byte(`{"name":"hello"}`), aad)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(sealed, "hello") {
		t.Fatal("ciphertext leaks plaintext")
	}
	plain, err := Open(key, sealed, aad)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(plain, []byte(`{"name":"hello"}`)) {
		t.Fatalf("got %q", plain)
	}
}

func TestOpenRejectsWrongAAD(t *testing.T) {
	key, _ := NewKey()
	sealed, err := Seal(key, []byte("secret"), EntityAAD("a"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Open(key, sealed, EntityAAD("b")); err == nil {
		t.Fatal("opening with a different AAD must fail")
	}
}

func TestVaultPassphrase(t *testing.T) {
	salt, wrapped, dek, err := SetupVault("correct horse")
	if err != nil {
		t.Fatal(err)
	}
	got, err := UnlockVault("correct horse", salt, wrapped)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, dek) {
		t.Fatal("unlocked key does not match")
	}
	if _, err := UnlockVault("wrong horse", salt, wrapped); err == nil {
		t.Fatal("wrong passphrase must fail")
	}
}

func TestRewrapVault(t *testing.T) {
	salt, wrapped, dek, err := SetupVault("first pass")
	if err != nil {
		t.Fatal(err)
	}
	newSalt, newWrapped, gotDEK, err := RewrapVault("first pass", "second pass", salt, wrapped)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(gotDEK, dek) {
		t.Fatal("data key changed during rewrap")
	}
	if _, err := UnlockVault("first pass", newSalt, newWrapped); err == nil {
		t.Fatal("old passphrase must no longer work")
	}
	back, err := UnlockVault("second pass", newSalt, newWrapped)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(back, dek) {
		t.Fatal("new passphrase returns a different key")
	}
}

func TestPasswordHash(t *testing.T) {
	h, err := HashPassword("hunter2")
	if err != nil {
		t.Fatal(err)
	}
	if !VerifyPassword(h, "hunter2") {
		t.Fatal("valid password rejected")
	}
	if VerifyPassword(h, "hunter3") {
		t.Fatal("invalid password accepted")
	}
}

func TestTokenHashStable(t *testing.T) {
	if HashToken("abc") != HashToken("abc") {
		t.Fatal("token hash is not deterministic")
	}
	if HashToken("abc") == HashToken("abd") {
		t.Fatal("token hash collides")
	}
	if strings.Contains(HashToken("abc"), "abc") {
		t.Fatal("token hash leaks the token")
	}
}
