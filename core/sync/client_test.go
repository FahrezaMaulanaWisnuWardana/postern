package sync_test

import (
	"context"
	"encoding/base64"
	"io"
	"log/slog"
	"net/http/httptest"
	"testing"

	"yourpm/core/crypto"
	"yourpm/core/model"
	"yourpm/core/service"
	"yourpm/core/sync"
	"yourpm/server"
)

func newServer(t *testing.T) string {
	t.Helper()
	srv, err := server.New(server.Config{
		Addr:         "127.0.0.1:0",
		DataDir:      t.TempDir(),
		JWTSecret:    "0123456789abcdef0123456789abcdef",
		Registration: "open",
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts.URL
}

type device struct {
	svc    *service.Service
	client *sync.Client
	ws     *model.Workspace
}

func openDevice(t *testing.T) *device {
	t.Helper()
	svc, err := service.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = svc.Close() })
	boot, err := svc.Load("")
	if err != nil {
		t.Fatal(err)
	}
	list, err := svc.Store.ListWorkspaces()
	if err != nil {
		t.Fatal(err)
	}
	var ws *model.Workspace
	for _, w := range list {
		if w.ID == boot.Active {
			ws = w
		}
	}
	if ws == nil {
		t.Fatal("no local workspace")
	}
	return &device{svc: svc, client: sync.New(svc.Store), ws: ws}
}

func TestTwoDevicePlaintextSync(t *testing.T) {
	url := newServer(t)
	a := openDevice(t)
	ctx := context.Background()

	if _, err := a.client.Login(ctx, url, "sync@example.com", "supersecret", true); err != nil {
		t.Fatal(err)
	}
	remote, err := a.client.CreateRemote(ctx, "Team", false, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.client.Link(ctx, a.ws, *remote, ""); err != nil {
		t.Fatal(err)
	}

	ent, err := a.svc.NewEntity(a.ws.ID, model.KindRequest, "", "Shared request")
	if err != nil {
		t.Fatal(err)
	}
	req, _ := service.RequestOf(ent)
	req.Method = "PUT"
	req.URL = "https://api.example.com/shared"
	_ = service.SetRequest(ent, req)
	if err := a.svc.SaveEntity(ent); err != nil {
		t.Fatal(err)
	}

	res, err := a.client.Run(ctx, a.ws)
	if err != nil {
		t.Fatal(err)
	}
	if res.Pushed == 0 {
		t.Fatalf("first device pushed nothing: %+v", res)
	}
	if res.Errors != nil {
		t.Fatalf("sync errors: %v", res.Errors)
	}

	// Second device signs into the same account and links to the same
	// remote workspace.
	b := openDevice(t)
	if _, err := b.client.Login(ctx, url, "sync@example.com", "supersecret", false); err != nil {
		t.Fatal(err)
	}
	if _, err := b.client.Link(ctx, b.ws, *remote, ""); err != nil {
		t.Fatal(err)
	}
	res, err = b.client.Run(ctx, b.ws)
	if err != nil {
		t.Fatal(err)
	}
	if res.Pulled == 0 {
		t.Fatalf("second device pulled nothing: %+v", res)
	}

	ents, err := b.svc.Store.ListEntities(b.ws.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	var got *model.Entity
	for _, e := range ents {
		if e.ID == ent.ID {
			got = e
		}
	}
	if got == nil {
		t.Fatal("entity did not arrive on the second device")
	}
	if got.Name != "Shared request" {
		t.Fatalf("name = %q", got.Name)
	}
	req, err = service.RequestOf(got)
	if err != nil {
		t.Fatal(err)
	}
	if req.Method != "PUT" || req.URL != "https://api.example.com/shared" {
		t.Fatalf("payload = %+v", req)
	}

	// An expired or corrupted access token must be refreshed transparently
	// using the refresh token stored at login.
	if err := b.client.Store.SetSetting("sync.token", "not-a-valid-token"); err != nil {
		t.Fatal(err)
	}

	// A second run must be a no-op: the cursor and revisions are persisted.
	res, err = b.client.Run(ctx, b.ws)
	if err != nil {
		t.Fatal(err)
	}
	if res.Pushed != 0 || res.Pulled != 0 {
		t.Fatalf("idle sync did work: %+v", res)
	}
	if b.client.Store.Setting("sync.token") == "not-a-valid-token" {
		t.Fatal("expired access token was not refreshed")
	}
}

func TestEncryptedLinkCreatesVault(t *testing.T) {
	url := newServer(t)
	a := openDevice(t)
	ctx := context.Background()
	if _, err := a.client.Login(ctx, url, "enc@example.com", "supersecret", true); err != nil {
		t.Fatal(err)
	}

	salt, wrapped, _, err := vaultKey("workspace passphrase")
	if err != nil {
		t.Fatal(err)
	}
	remote, err := a.client.CreateRemote(ctx, "Encrypted", true, salt, wrapped)
	if err != nil {
		t.Fatal(err)
	}
	res, err := a.client.Link(ctx, a.ws, *remote, "workspace passphrase")
	if err != nil {
		t.Fatal(err)
	}
	if !res.VaultCreated {
		t.Fatalf("link did not enable the vault: %+v", res)
	}
	if !a.svc.Store.VaultEnabled() || !a.svc.Store.Unlocked() {
		t.Fatal("vault should be enabled and unlocked after linking")
	}

	ent, err := a.svc.NewEntity(a.ws.ID, model.KindCollection, "", "Sealed")
	if err != nil {
		t.Fatal(err)
	}
	if err := a.svc.SaveEntity(ent); err != nil {
		t.Fatal(err)
	}
	run, err := a.client.Run(ctx, a.ws)
	if err != nil {
		t.Fatal(err)
	}
	if run.Pushed == 0 {
		t.Fatalf("nothing pushed: %+v", run)
	}

	// A second device linking with the same passphrase unlocks the same data.
	b := openDevice(t)
	if _, err := b.client.Login(ctx, url, "enc@example.com", "supersecret", false); err != nil {
		t.Fatal(err)
	}
	if _, err := b.client.Link(ctx, b.ws, *remote, "workspace passphrase"); err != nil {
		t.Fatal(err)
	}
	if _, err := b.client.Run(ctx, b.ws); err != nil {
		t.Fatal(err)
	}
	ents, err := b.svc.Store.ListEntities(b.ws.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, e := range ents {
		if e.ID == ent.ID && e.Name == "Sealed" {
			found = true
		}
	}
	if !found {
		t.Fatal("sealed entity did not sync")
	}
}

// vaultKey creates the salt and wrapped key an encrypted workspace publishes
// to the server; the salt is base64 because the server validates its shape.
func vaultKey(passphrase string) (salt, wrapped string, dek []byte, err error) {
	rawSalt, w, key, err := crypto.SetupVault(passphrase)
	if err != nil {
		return "", "", nil, err
	}
	return base64.RawStdEncoding.EncodeToString(rawSalt), w, key, nil
}
