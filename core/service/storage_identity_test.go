package service

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"yourpm/core/brand"
	"yourpm/core/store"
)

func touchDatabase(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("identity test"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestPosternFreshStorageIdentity(t *testing.T) {
	base := t.TempDir()
	dir, err := defaultDataDir(base, "postern")
	if err != nil || dir != filepath.Join(base, "postern") {
		t.Fatalf("directory = %q, %v", dir, err)
	}
	path, err := dbPathFor(dir, "postern")
	if err != nil || path != filepath.Join(dir, "postern.db") {
		t.Fatalf("database = %q, %v", path, err)
	}
}

func TestPosternReusesEveryLegacyIdentity(t *testing.T) {
	for _, legacy := range legacySlugs("postern") {
		t.Run(legacy, func(t *testing.T) {
			base := t.TempDir()
			dir := filepath.Join(base, legacy)
			path := filepath.Join(dir, legacy+".db")
			touchDatabase(t, path)
			got, err := defaultDataDir(base, "postern")
			if err != nil || got != dir {
				t.Fatalf("directory = %q, %v", got, err)
			}
			got, err = dbPathFor(dir, "postern")
			if err != nil || got != path {
				t.Fatalf("database = %q, %v", got, err)
			}
			// Selection never moves, copies, or modifies a database.
			raw, err := os.ReadFile(path)
			if err != nil || string(raw) != "identity test" {
				t.Fatalf("legacy database changed: %q, %v", raw, err)
			}
			if _, err := os.Stat(filepath.Join(base, "postern")); !os.IsNotExist(err) {
				t.Fatal("selection created a new data directory")
			}
		})
	}
}

func TestPosternAmbiguityAndExplicitSelection(t *testing.T) {
	base := t.TempDir()
	for _, slug := range []string{"yourpm", "delanapostman"} {
		touchDatabase(t, filepath.Join(base, slug, slug+".db"))
	}
	if _, err := defaultDataDir(base, "postern"); err == nil || !strings.Contains(err.Error(), "multiple") {
		t.Fatalf("expected ambiguity error, got %v", err)
	}
	touchDatabase(t, filepath.Join(base, "postern", "postern.db"))
	if got, err := defaultDataDir(base, "postern"); err != nil || got != filepath.Join(base, "postern") {
		t.Fatalf("current identity should win: %q, %v", got, err)
	}
	dir := filepath.Join(base, "explicit")
	touchDatabase(t, filepath.Join(dir, "yourpm.db"))
	touchDatabase(t, filepath.Join(dir, "delanapostman.db"))
	if _, err := dbPathFor(dir, "postern"); err == nil {
		t.Fatal("ambiguous database filenames were silently selected")
	}
}

func TestWhiteLabelDoesNotAdoptPosternData(t *testing.T) {
	base := t.TempDir()
	touchDatabase(t, filepath.Join(base, "yourpm", "yourpm.db"))
	dir, err := defaultDataDir(base, "acme")
	if err != nil || dir != filepath.Join(base, "acme") {
		t.Fatalf("white-label directory = %q, %v", dir, err)
	}
}

func TestPosternReopensLegacyVaultWithoutDataMigration(t *testing.T) {
	if brand.Slug != "postern" {
		t.Skip("test exercises the default Postern identity")
	}
	dir := t.TempDir()
	legacy := filepath.Join(dir, "delanapostman.db")
	st, err := store.Open(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetSetting("release.sentinel", "preserved"); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	svc, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if svc.Store.Path() != legacy || svc.Store.Setting("release.sentinel") != "preserved" {
		t.Fatal("Postern did not reuse the populated legacy database")
	}
	if err := svc.VaultSetup("legacy vault test passphrase"); err != nil {
		t.Fatal(err)
	}
	if err := svc.Store.SetSecretSetting("oauth.tokens.identity-test", "fixture-secret"); err != nil {
		t.Fatal(err)
	}
	if err := svc.Close(); err != nil {
		t.Fatal(err)
	}
	svc, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer svc.Close()
	if enabled, unlocked := svc.VaultStatus(); !enabled || unlocked {
		t.Fatal("legacy vault did not reopen locked")
	}
	if err := svc.VaultUnlock("legacy vault test passphrase"); err != nil {
		t.Fatal(err)
	}
	if svc.Store.SettingSecret("oauth.tokens.identity-test") != "fixture-secret" {
		t.Fatal("legacy vault secret did not survive reopening")
	}
	if _, err := os.Stat(filepath.Join(dir, "postern.db")); !os.IsNotExist(err) {
		t.Fatal("a replacement database was created")
	}
}
