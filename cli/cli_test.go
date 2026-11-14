package cli

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"yourpm/core/model"
	"yourpm/core/service"
)

func TestCmdRunWritesDecodedBinary(t *testing.T) {
	raw := []byte{0x00, 0x01, 0xff, 0xfe, 0x89, 'P', 'N', 'G'}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write(raw)
	}))
	defer srv.Close()

	dir := t.TempDir()
	out := filepath.Join(dir, "body.bin")
	if err := cmdRun([]string{"--data-dir", dir, "--output", out, srv.URL}); err != nil {
		t.Fatalf("cmdRun: %v", err)
	}
	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, raw) {
		t.Fatalf("binary output = %v, want %v", got, raw)
	}
}

func TestCmdRunCollectionFailsOnFailedTests(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	dir := t.TempDir()
	svc, err := service.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	boot, err := svc.Load("")
	if err != nil {
		t.Fatal(err)
	}
	col, err := svc.NewEntity(boot.Active, model.KindCollection, "", "CLI Runner")
	if err != nil {
		t.Fatal(err)
	}
	ent, err := svc.NewEntity(boot.Active, model.KindRequest, col.ID, "Failing test")
	if err != nil {
		t.Fatal(err)
	}
	req := model.RequestData{
		Method: "GET",
		URL:    srv.URL + "/thing",
		Auth:   model.Auth{Type: model.AuthNone},
		Body:   model.Body{Mode: model.BodyNone},
		TestScript: `pm.test("status is 201", function () {
			pm.expect(pm.response.code).to.equal(201);
		});`,
	}
	if err := service.SetRequest(ent, req); err != nil {
		t.Fatal(err)
	}
	if err := svc.SaveEntity(ent); err != nil {
		t.Fatal(err)
	}
	if err := svc.Close(); err != nil {
		t.Fatal(err)
	}

	err = cmdRunCollection([]string{"--data-dir", dir, "--fail-tests", "--json", "CLI Runner"})
	var code exitCode
	if !errors.As(err, &code) || int(code) != 3 {
		t.Fatalf("run-collection error = %v, want exit code 3", err)
	}
}

func TestCmdBackupEncryptedRoundTrip(t *testing.T) {
	dir := t.TempDir()
	svc, err := service.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	boot, err := svc.Load("")
	if err != nil {
		t.Fatal(err)
	}
	col, err := svc.NewEntity(boot.Active, model.KindCollection, "", "CLI Backup")
	if err != nil {
		t.Fatal(err)
	}
	ent, err := svc.NewEntity(boot.Active, model.KindRequest, col.ID, "Secret request")
	if err != nil {
		t.Fatal(err)
	}
	req := model.RequestData{
		Method: "GET",
		URL:    "https://secret.example/value",
		Auth:   model.Auth{Type: model.AuthNone},
		Body:   model.Body{Mode: model.BodyNone},
	}
	if err := service.SetRequest(ent, req); err != nil {
		t.Fatal(err)
	}
	if err := svc.SaveEntity(ent); err != nil {
		t.Fatal(err)
	}
	if err := svc.Close(); err != nil {
		t.Fatal(err)
	}

	backup := filepath.Join(dir, "backup.json")
	if err := cmdBackup([]string{"--data-dir", dir, "--out", backup, "--passphrase", "backup passphrase"}); err != nil {
		t.Fatalf("cmdBackup: %v", err)
	}
	raw, err := os.ReadFile(backup)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte("secret.example")) {
		t.Fatal("encrypted backup contains the plaintext URL")
	}

	restoreDir := filepath.Join(dir, "restore")
	err = cmdRestore([]string{"--data-dir", restoreDir, backup})
	if err == nil || !strings.Contains(err.Error(), "encrypted") {
		t.Fatalf("restore without passphrase = %v, want an encryption hint", err)
	}
	if err := cmdRestore([]string{"--data-dir", restoreDir, "--passphrase", "backup passphrase", backup}); err != nil {
		t.Fatalf("cmdRestore: %v", err)
	}

	err = cmdRestore([]string{"--data-dir", filepath.Join(dir, "wrong"), "--passphrase", "wrong passphrase", backup})
	if err == nil || !strings.Contains(err.Error(), "decrypt") {
		t.Fatalf("restore with wrong passphrase = %v, want a decryption error", err)
	}
}

func TestCmdAuthorizeRequiresRequest(t *testing.T) {
	err := cmdAuthorize([]string{"--data-dir", t.TempDir()})
	if err == nil || !strings.Contains(err.Error(), "usage:") {
		t.Fatalf("authorize without --request = %v, want usage", err)
	}
}
