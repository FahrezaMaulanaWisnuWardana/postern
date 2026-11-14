package service_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"yourpm/core/model"
	"yourpm/core/service"
)

func openTestService(t *testing.T) (*service.Service, string) {
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
	if boot.Active == "" {
		t.Fatal("bootstrap returned no workspace")
	}
	return svc, boot.Active
}

// echoServer records the last request and returns a JSON body.
type captured struct {
	Method  string
	Path    string
	Query   map[string][]string
	Headers http.Header
	Body    string
}

func newEcho(t *testing.T) (*httptest.Server, *captured) {
	t.Helper()
	last := &captured{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		last.Method = r.Method
		last.Path = r.URL.Path
		last.Query = r.URL.Query()
		last.Headers = r.Header.Clone()
		last.Body = string(body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(srv.Close)
	return srv, last
}

func TestSendUsesEnvironmentAndCollection(t *testing.T) {
	svc, ws := openTestService(t)
	srv, last := newEcho(t)

	env, err := svc.NewEntity(ws, model.KindEnvironment, "", "Local")
	if err != nil {
		t.Fatal(err)
	}
	if err := service.SetEnvironment(env, model.EnvironmentData{Variables: []model.KV{
		{Key: "host", Value: srv.URL},
		{Key: "token", Value: "s3cret"},
	}}); err != nil {
		t.Fatal(err)
	}
	if err := svc.SaveEntity(env); err != nil {
		t.Fatal(err)
	}

	col, err := svc.NewEntity(ws, model.KindCollection, "", "API")
	if err != nil {
		t.Fatal(err)
	}
	if err := service.SetCollection(col, model.CollectionData{
		Auth: model.Auth{Type: model.AuthBearer, Token: "{{token}}"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := svc.SaveEntity(col); err != nil {
		t.Fatal(err)
	}

	reqEnt, err := svc.NewEntity(ws, model.KindRequest, col.ID, "Echo")
	if err != nil {
		t.Fatal(err)
	}
	req := model.RequestData{
		Method: "POST",
		URL:    "{{host}}/echo",
		Params: []model.KV{{Key: "q", Value: "1"}},
		Headers: []model.KV{
			{Key: "X-Test", Value: "yes"},
			{Key: "X-Off", Value: "no", Disabled: true},
		},
		Body: model.Body{Mode: model.BodyRaw, Lang: "json", Raw: `{"hello":"world"}`},
		Auth: model.Auth{Type: model.AuthInherit},
	}
	if err := service.SetRequest(reqEnt, req); err != nil {
		t.Fatal(err)
	}
	if err := svc.SaveEntity(reqEnt); err != nil {
		t.Fatal(err)
	}

	// Overrides behave like the CLI's --var.
	resp := svc.SendWith(context.Background(), ws, req, env.ID, col.ID, reqEnt.ID, map[string]string{"token": "override"})
	if resp.Error != "" {
		t.Fatalf("send error: %s", resp.Error)
	}
	if resp.Status != 200 || resp.Body != `{"ok":true}` {
		t.Fatalf("unexpected response: %d %q", resp.Status, resp.Body)
	}
	if last.Method != "POST" || last.Path != "/echo" {
		t.Fatalf("request line = %s %s", last.Method, last.Path)
	}
	if got := last.Query["q"]; len(got) != 1 || got[0] != "1" {
		t.Fatalf("query = %v", last.Query)
	}
	if last.Headers.Get("X-Test") != "yes" {
		t.Fatal("enabled header missing")
	}
	if last.Headers.Get("X-Off") != "" {
		t.Fatal("disabled header was sent")
	}
	if got := last.Headers.Get("Authorization"); got != "Bearer override" {
		t.Fatalf("authorization = %q", got)
	}
	if !strings.Contains(last.Headers.Get("Content-Type"), "json") {
		t.Fatalf("content type = %q", last.Headers.Get("Content-Type"))
	}
	if last.Body != `{"hello":"world"}` {
		t.Fatalf("body = %q", last.Body)
	}

	hist, err := svc.History(ws, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(hist) != 1 || hist[0].Status != 200 || hist[0].Method != "POST" {
		t.Fatalf("history = %+v", hist)
	}
	if strings.Contains(hist[0].URL, "{{") {
		t.Fatalf("history URL is not resolved: %q", hist[0].URL)
	}
}

func TestSendErrorIsReported(t *testing.T) {
	svc, ws := openTestService(t)
	resp := svc.Send(context.Background(), ws, model.RequestData{
		Method: "GET",
		URL:    "http://127.0.0.1:1/nothing",
		Body:   model.Body{Mode: model.BodyNone},
		Auth:   model.Auth{Type: model.AuthNone},
	}, "", "", "")
	if resp == nil {
		t.Fatal("nil response")
	}
	if resp.Error == "" {
		t.Fatal("unreachable host should produce an error")
	}
}

func TestVaultLocksData(t *testing.T) {
	svc, ws := openTestService(t)
	e, err := svc.NewEntity(ws, model.KindRequest, "", "Secret request")
	if err != nil {
		t.Fatal(err)
	}
	req, _ := service.RequestOf(e)
	req.URL = "https://example.com/private"
	_ = service.SetRequest(e, req)
	if err := svc.SaveEntity(e); err != nil {
		t.Fatal(err)
	}

	if err := svc.VaultSetup("a strong passphrase"); err != nil {
		t.Fatal(err)
	}
	if enabled, unlocked := svc.VaultStatus(); !enabled || !unlocked {
		t.Fatalf("vault status = %v %v", enabled, unlocked)
	}

	svc.VaultLock()
	if _, err := svc.Store.ListEntities(ws, false); err == nil {
		t.Fatal("reading a locked vault should fail")
	}
	boot, err := svc.Load(ws)
	if err != nil {
		t.Fatal(err)
	}
	if !boot.VaultEnabled || boot.VaultUnlocked {
		t.Fatalf("bootstrap vault flags = %v %v", boot.VaultEnabled, boot.VaultUnlocked)
	}
	if len(boot.Entities) != 0 {
		t.Fatal("locked bootstrap leaked entities")
	}

	if err := svc.VaultUnlock("wrong passphrase"); err == nil {
		t.Fatal("wrong passphrase unlocked the vault")
	}
	if err := svc.VaultUnlock("a strong passphrase"); err != nil {
		t.Fatal(err)
	}
	ents, err := svc.Store.ListEntities(ws, false)
	if err != nil {
		t.Fatal(err)
	}
	var found *model.Entity
	for _, ent := range ents {
		if ent.ID == e.ID {
			found = ent
		}
	}
	if found == nil {
		t.Fatal("entity missing after unlock")
	}
	got, _ := service.RequestOf(found)
	if got.URL != "https://example.com/private" {
		t.Fatalf("URL after unlock = %q", got.URL)
	}
}

func TestSoftDeleteKeepsTombstoneAndHidesChildren(t *testing.T) {
	svc, ws := openTestService(t)
	root, err := svc.NewEntity(ws, model.KindCollection, "", "Root")
	if err != nil {
		t.Fatal(err)
	}
	child, err := svc.NewEntity(ws, model.KindRequest, root.ID, "Child")
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.DeleteEntity(root); err != nil {
		t.Fatal(err)
	}
	visible, err := svc.Store.ListEntities(ws, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range visible {
		if e.ID == root.ID || e.ID == child.ID {
			t.Fatalf("deleted entity still visible: %s", e.Name)
		}
	}
	all, err := svc.Store.ListEntities(ws, true)
	if err != nil {
		t.Fatal(err)
	}
	var seen int
	for _, e := range all {
		if e.ID == root.ID || e.ID == child.ID {
			if !e.Deleted {
				t.Fatalf("%s is not marked deleted", e.Name)
			}
			seen++
		}
	}
	if seen != 2 {
		t.Fatalf("tombstones = %d, want 2", seen)
	}
}

func TestImportExportRoundTrip(t *testing.T) {
	svc, ws := openTestService(t)
	doc := []byte(`{
	  "info": {"name": "Round trip", "schema": "https://schema.getpostman.com/json/collection/v2.1.0/collection.json"},
	  "variable": [{"key": "baseUrl", "value": "https://api.example.com"}],
	  "item": [{
	    "name": "Users",
	    "item": [{
	      "name": "List users",
	      "request": {
	        "method": "GET",
	        "header": [{"key": "Accept", "value": "application/json"}],
	        "url": "{{baseUrl}}/users?limit=10"
	      }
	    }]
	  }]
	}`)
	col, err := svc.ImportPostman(ws, doc)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := svc.ExportCollection(col)
	if err != nil {
		t.Fatal(err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("export is not JSON: %v", err)
	}
	if !strings.Contains(string(raw), "List users") || !strings.Contains(string(raw), "/users") {
		t.Fatalf("export lost request data: %s", raw)
	}

	// Re-import into a second workspace-free copy and ensure the request is
	// still executable in model form.
	col2, err := svc.ImportPostman(ws, raw)
	if err != nil {
		t.Fatal(err)
	}
	ents, err := svc.Store.ListEntities(ws, false)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, e := range ents {
		if e.Kind != model.KindRequest || e.ParentID == "" {
			continue
		}
		req, err := service.RequestOf(e)
		if err == nil && strings.Contains(req.URL, "/users") {
			found = true
		}
	}
	if !found {
		t.Fatalf("re-imported collection %q has no users request", col2.Name)
	}
}

func TestSecretSettingsAreSealedWithVault(t *testing.T) {
	svc, _ := openTestService(t)

	// Simulate a token written by an older build: plaintext until the vault is
	// enabled or the next unlock migrates it.
	if err := svc.Store.SetSetting("sync.token", "legacy-token"); err != nil {
		t.Fatal(err)
	}
	if err := svc.VaultSetup("a strong passphrase"); err != nil {
		t.Fatal(err)
	}
	raw := svc.Store.Setting("sync.token")
	if !strings.HasPrefix(raw, "v1:") {
		t.Fatalf("sync token was not sealed: %q", raw)
	}
	if raw == "legacy-token" {
		t.Fatal("sealed token still equals plaintext")
	}
	if got := svc.Store.SettingSecret("sync.token"); got != "legacy-token" {
		t.Fatalf("SettingSecret = %q", got)
	}
	if _, ok := svc.Store.PublicSettings()["sync.token"]; ok {
		t.Fatal("public settings leaked the sync token key")
	}

	if err := svc.Store.SetSecretSetting("sync.refresh", "refresh-1"); err != nil {
		t.Fatal(err)
	}
	if got := svc.Store.SettingSecret("sync.refresh"); got != "refresh-1" {
		t.Fatalf("refresh = %q", got)
	}

	svc.VaultLock()
	if got := svc.Store.SettingSecret("sync.token"); got != "" {
		t.Fatalf("locked vault exposed token %q", got)
	}
	if err := svc.VaultUnlock("a strong passphrase"); err != nil {
		t.Fatal(err)
	}
	if got := svc.Store.SettingSecret("sync.token"); got != "legacy-token" {
		t.Fatalf("token after unlock = %q", got)
	}

	// A plaintext value written while unlocked is re-sealed on the next unlock.
	if err := svc.Store.SetSetting("sync.token", "legacy-again"); err != nil {
		t.Fatal(err)
	}
	svc.VaultLock()
	if err := svc.VaultUnlock("a strong passphrase"); err != nil {
		t.Fatal(err)
	}
	if raw := svc.Store.Setting("sync.token"); !strings.HasPrefix(raw, "v1:") {
		t.Fatalf("legacy token was not resealed on unlock: %q", raw)
	}
	if got := svc.Store.SettingSecret("sync.token"); got != "legacy-again" {
		t.Fatalf("resealed token = %q", got)
	}
}

func TestTrashRestoreBringsBackSubtree(t *testing.T) {
	svc, ws := openTestService(t)
	root, err := svc.NewEntity(ws, model.KindCollection, "", "Root")
	if err != nil {
		t.Fatal(err)
	}
	folder, err := svc.NewEntity(ws, model.KindFolder, root.ID, "Folder")
	if err != nil {
		t.Fatal(err)
	}
	req, err := svc.NewEntity(ws, model.KindRequest, folder.ID, "Nested")
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.DeleteEntity(root); err != nil {
		t.Fatal(err)
	}

	trash, err := svc.Trash(ws)
	if err != nil {
		t.Fatal(err)
	}
	if len(trash) != 3 {
		t.Fatalf("trash = %d items, want 3", len(trash))
	}
	var rootTomb *model.Entity
	for _, e := range trash {
		if e.ID == root.ID {
			rootTomb = e
		}
	}
	if rootTomb == nil {
		t.Fatal("deleted root missing from trash")
	}
	if err := svc.RestoreEntity(rootTomb); err != nil {
		t.Fatal(err)
	}

	visible, err := svc.Store.ListEntities(ws, false)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{root.ID: false, folder.ID: false, req.ID: false}
	for _, e := range visible {
		if _, ok := want[e.ID]; ok {
			want[e.ID] = true
		}
	}
	for id, seen := range want {
		if !seen {
			t.Fatalf("entity %s was not restored", id)
		}
	}
	trash, err = svc.Trash(ws)
	if err != nil {
		t.Fatal(err)
	}
	if len(trash) != 0 {
		t.Fatalf("trash still has %d items", len(trash))
	}
}

func TestWorkspaceBackupRoundTripRemapsIDs(t *testing.T) {
	svc, ws := openTestService(t)
	col, err := svc.NewEntity(ws, model.KindCollection, "", "Backup API")
	if err != nil {
		t.Fatal(err)
	}
	folder, err := svc.NewEntity(ws, model.KindFolder, col.ID, "Nested")
	if err != nil {
		t.Fatal(err)
	}
	reqEnt, err := svc.NewEntity(ws, model.KindRequest, folder.ID, "Get thing")
	if err != nil {
		t.Fatal(err)
	}
	req := model.RequestData{Method: "GET", URL: "https://example.com/thing", Body: model.Body{Mode: model.BodyNone}}
	if err := service.SetRequest(reqEnt, req); err != nil {
		t.Fatal(err)
	}
	if err := svc.SaveEntity(reqEnt); err != nil {
		t.Fatal(err)
	}

	raw, err := svc.ExportWorkspace(ws)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "yourpm.workspace.backup") {
		t.Fatalf("backup is missing its format marker: %s", raw)
	}
	restored, err := svc.ImportWorkspace(raw, "Restored API")
	if err != nil {
		t.Fatal(err)
	}
	if restored.ID == ws {
		t.Fatal("restore reused the source workspace ID")
	}
	if restored.Name != "Restored API" {
		t.Fatalf("restored name = %q", restored.Name)
	}

	orig, err := svc.Store.ListEntities(ws, false)
	if err != nil {
		t.Fatal(err)
	}
	copyEnts, err := svc.Store.ListEntities(restored.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(copyEnts) != len(orig) {
		t.Fatalf("restored %d entities, want %d", len(copyEnts), len(orig))
	}
	origIDs := map[string]bool{}
	for _, e := range orig {
		origIDs[e.ID] = true
	}
	copyIDs := map[string]bool{}
	for _, e := range copyEnts {
		if origIDs[e.ID] {
			t.Fatalf("restored entity kept source ID %s", e.ID)
		}
		copyIDs[e.ID] = true
	}
	for _, e := range copyEnts {
		if e.ParentID != "" && !copyIDs[e.ParentID] {
			t.Fatalf("restored entity %q points at missing parent %q", e.Name, e.ParentID)
		}
	}

	var restoredReq *model.Entity
	for _, e := range copyEnts {
		if e.Kind == model.KindRequest && e.Name == "Get thing" {
			restoredReq = e
		}
	}
	if restoredReq == nil {
		t.Fatal("restored request missing")
	}
	got, err := service.RequestOf(restoredReq)
	if err != nil {
		t.Fatal(err)
	}
	if got.URL != "https://example.com/thing" {
		t.Fatalf("restored URL = %q", got.URL)
	}
}

func TestEncryptedWorkspaceBackupRoundTrip(t *testing.T) {
	svc, ws := openTestService(t)
	col, err := svc.NewEntity(ws, model.KindCollection, "", "Secret API")
	if err != nil {
		t.Fatal(err)
	}
	ent, err := svc.NewEntity(ws, model.KindRequest, col.ID, "Secret request")
	if err != nil {
		t.Fatal(err)
	}
	req := model.RequestData{
		Method: "GET",
		URL:    "https://secret.example/token-value",
		Auth:   model.Auth{Type: model.AuthNone},
		Body:   model.Body{Mode: model.BodyNone},
	}
	if err := service.SetRequest(ent, req); err != nil {
		t.Fatal(err)
	}
	if err := svc.SaveEntity(ent); err != nil {
		t.Fatal(err)
	}

	if _, err := svc.ExportWorkspaceEncrypted(ws, "short"); err == nil {
		t.Fatal("a short backup passphrase was accepted")
	}

	enc, err := svc.ExportWorkspaceEncrypted(ws, "correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(enc, []byte("yourpm.workspace.backup.encrypted")) {
		t.Fatalf("missing encrypted envelope marker: %s", enc)
	}
	if bytes.Contains(enc, []byte("secret.example")) {
		t.Fatal("encrypted backup contains the plaintext URL")
	}

	if _, err := svc.ImportWorkspace(enc, "No passphrase"); !errors.Is(err, service.ErrEncryptedBackup) {
		t.Fatalf("ImportWorkspace without a passphrase = %v, want ErrEncryptedBackup", err)
	}
	if _, err := svc.ImportWorkspaceEncrypted(enc, "Wrong passphrase", "wrong horse battery"); err == nil {
		t.Fatal("a wrong passphrase decrypted the backup")
	}

	restored, err := svc.ImportWorkspaceEncrypted(enc, "Encrypted Restore", "correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	if restored.Name != "Encrypted Restore" {
		t.Fatalf("restored name = %q", restored.Name)
	}
	restoredEnts, err := svc.Store.ListEntities(restored.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(restoredEnts) != 2 {
		t.Fatalf("restored %d entities, want 2", len(restoredEnts))
	}
	var gotURL string
	for _, e := range restoredEnts {
		if e.Kind != model.KindRequest {
			continue
		}
		r, err := service.RequestOf(e)
		if err != nil {
			t.Fatal(err)
		}
		gotURL = r.URL
	}
	if gotURL != "https://secret.example/token-value" {
		t.Fatalf("restored URL = %q", gotURL)
	}

	// Plaintext archives keep working and ignore a supplied passphrase.
	plain, err := svc.ExportWorkspace(ws)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(plain, []byte("yourpm.workspace.backup.encrypted")) {
		t.Fatal("ExportWorkspace produced an encrypted envelope")
	}
	if _, err := svc.ImportWorkspaceEncrypted(plain, "Plain Restore", "ignored"); err != nil {
		t.Fatalf("plaintext import with passphrase: %v", err)
	}
}

func TestRunCollectionPassAndFail(t *testing.T) {
	svc, ws := openTestService(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/error" {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(srv.Close)

	col, err := svc.NewEntity(ws, model.KindCollection, "", "Runner")
	if err != nil {
		t.Fatal(err)
	}
	pass, err := svc.NewEntity(ws, model.KindRequest, col.ID, "Passing")
	if err != nil {
		t.Fatal(err)
	}
	passReq := model.RequestData{
		Method: "GET",
		URL:    srv.URL + "/ok",
		Auth:   model.Auth{Type: model.AuthNone},
		Body:   model.Body{Mode: model.BodyNone},
		TestScript: `pm.test("status is 200", function () {
			pm.expect(pm.response.code).to.equal(200);
		});`,
	}
	if err := service.SetRequest(pass, passReq); err != nil {
		t.Fatal(err)
	}
	if err := svc.SaveEntity(pass); err != nil {
		t.Fatal(err)
	}

	fail, err := svc.NewEntity(ws, model.KindRequest, col.ID, "Failing")
	if err != nil {
		t.Fatal(err)
	}
	failReq := model.RequestData{
		Method: "GET",
		URL:    srv.URL + "/bad",
		Auth:   model.Auth{Type: model.AuthNone},
		Body:   model.Body{Mode: model.BodyNone},
		TestScript: `pm.test("status is 201", function () {
			pm.expect(pm.response.code).to.equal(201);
		});`,
	}
	if err := service.SetRequest(fail, failReq); err != nil {
		t.Fatal(err)
	}
	if err := svc.SaveEntity(fail); err != nil {
		t.Fatal(err)
	}

	httpFail, err := svc.NewEntity(ws, model.KindRequest, col.ID, "HTTP error")
	if err != nil {
		t.Fatal(err)
	}
	httpReq := model.RequestData{
		Method: "GET",
		URL:    srv.URL + "/error",
		Auth:   model.Auth{Type: model.AuthNone},
		Body:   model.Body{Mode: model.BodyNone},
	}
	if err := service.SetRequest(httpFail, httpReq); err != nil {
		t.Fatal(err)
	}
	if err := svc.SaveEntity(httpFail); err != nil {
		t.Fatal(err)
	}

	sum, err := svc.RunCollection(context.Background(), ws, col.ID, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if sum.Total != 3 || sum.Passed != 1 || sum.Failed != 2 || sum.Errors != 0 {
		t.Fatalf("summary = %+v", sum)
	}
	byName := map[string]service.RunOutcome{}
	for _, item := range sum.Items {
		byName[item.Name] = item
	}
	if got := byName["Passing"]; got.Failed || got.TestFail || got.Status != 200 {
		t.Fatalf("passing item = %+v", got)
	}
	if got := byName["Failing"]; !got.Failed || !got.TestFail {
		t.Fatalf("failing item = %+v", got)
	}
	if got := byName["HTTP error"]; !got.Failed || !got.HTTPFail || got.TestFail {
		t.Fatalf("HTTP failure item = %+v", got)
	}
}

// oauthMock is a minimal authorization server: /authorize redirects back with
// a code and /token validates PKCE before issuing rotating tokens.
type oauthMock struct {
	mu            sync.Mutex
	challenge     string
	authorizeHits int
	codeHits      int
	refreshHits   int
}

func (m *oauthMock) handler(t *testing.T) http.Handler {
	t.Helper()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/authorize":
			q := r.URL.Query()
			if q.Get("response_type") != "code" ||
				q.Get("client_id") != "test-client" ||
				q.Get("code_challenge_method") != "S256" ||
				q.Get("scope") != "read write" ||
				q.Get("state") == "" || q.Get("code_challenge") == "" {
				t.Errorf("unexpected authorize query: %v", q)
				http.Error(w, "bad authorize request", http.StatusBadRequest)
				return
			}
			redirect := q.Get("redirect_uri")
			u, err := url.Parse(redirect)
			if err != nil || u.Hostname() != "127.0.0.1" {
				t.Errorf("redirect_uri = %q, want a loopback URL", redirect)
				http.Error(w, "bad redirect", http.StatusBadRequest)
				return
			}
			m.mu.Lock()
			m.challenge = q.Get("code_challenge")
			m.authorizeHits++
			m.mu.Unlock()
			cb, _ := url.Parse(redirect)
			cq := cb.Query()
			cq.Set("code", "auth-code")
			cq.Set("state", q.Get("state"))
			cb.RawQuery = cq.Encode()
			http.Redirect(w, r, cb.String(), http.StatusFound)

		case "/token":
			_ = r.ParseForm()
			form := r.PostForm
			if id, secret, ok := r.BasicAuth(); ok {
				if id != "test-client" || secret != "test-secret" {
					t.Errorf("basic auth = %q/%q", id, secret)
				}
			}
			switch form.Get("grant_type") {
			case "authorization_code":
				if form.Get("code") != "auth-code" || form.Get("redirect_uri") == "" {
					t.Errorf("code exchange form = %v", form)
				}
				sum := sha256.Sum256([]byte(form.Get("code_verifier")))
				m.mu.Lock()
				want := m.challenge
				m.codeHits++
				m.mu.Unlock()
				if base64.RawURLEncoding.EncodeToString(sum[:]) != want {
					t.Errorf("PKCE verifier does not match the challenge")
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"access_token":"access-1","refresh_token":"refresh-1","token_type":"Bearer","expires_in":1}`)
			case "refresh_token":
				if form.Get("refresh_token") != "refresh-1" {
					t.Errorf("refresh token = %q", form.Get("refresh_token"))
				}
				m.mu.Lock()
				m.refreshHits++
				m.mu.Unlock()
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"access_token":"access-2","expires_in":3600}`)
			default:
				t.Errorf("unexpected grant type %q", form.Get("grant_type"))
				http.Error(w, "bad grant", http.StatusBadRequest)
			}

		default:
			http.NotFound(w, r)
		}
	})
}

// browserOpener simulates the system browser: call /authorize and follow its
// redirect to the loopback callback.
func browserOpener(t *testing.T) func(string) error {
	t.Helper()
	return func(raw string) error {
		client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		}}
		resp, err := client.Get(raw)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusFound {
			return fmt.Errorf("authorize returned %d", resp.StatusCode)
		}
		location := resp.Header.Get("Location")
		if location == "" {
			return errors.New("authorize returned no redirect")
		}
		cb, err := http.Get(location)
		if err != nil {
			return err
		}
		defer cb.Body.Close()
		if cb.StatusCode != http.StatusOK {
			return fmt.Errorf("callback returned %d", cb.StatusCode)
		}
		return nil
	}
}

func oauthRequest(tokenURL, authURL string) model.RequestData {
	return model.RequestData{
		Method: "GET",
		URL:    "http://127.0.0.1:1/whoami",
		Body:   model.Body{Mode: model.BodyNone},
		Auth: model.Auth{
			Type: model.AuthOAuth2,
			OAuth2: &model.OAuth2{
				GrantType:        model.GrantAuthorizationCode,
				AuthorizationURL: authURL,
				AccessTokenURL:   tokenURL,
				ClientID:         "test-client",
				ClientSecret:     "test-secret",
				Scope:            "read write",
			},
		},
	}
}

func TestOAuthAuthorizationCodeFlowAndRefresh(t *testing.T) {
	svc, ws := openTestService(t)
	mock := &oauthMock{}
	srv := httptest.NewServer(mock.handler(t))
	defer srv.Close()

	req := oauthRequest(srv.URL+"/token", srv.URL+"/authorize")
	expiry, err := svc.AuthorizeOAuthWith(context.Background(), ws, req, "", "", browserOpener(t))
	if err != nil {
		t.Fatalf("AuthorizeOAuthWith: %v", err)
	}
	if expiry.IsZero() {
		t.Fatal("authorization returned no expiry")
	}
	status := svc.OAuthTokenStatus(ws, req, "", "")
	if !status.Authorized || !status.Refreshable {
		t.Fatalf("status = %+v, want an authorized, refreshable token", status)
	}

	// The issued token has a one-second lifetime, so the next send must use
	// the refresh token instead of failing.
	target, got := newEcho(t)
	req.URL = target.URL + "/whoami"
	resp := svc.Send(context.Background(), ws, req, "", "", "")
	if resp.Error != "" {
		t.Fatalf("send after authorization: %s", resp.Error)
	}
	if auth := got.Headers.Get("Authorization"); auth != "Bearer access-2" {
		t.Fatalf("Authorization = %q, want the refreshed token", auth)
	}

	mock.mu.Lock()
	defer mock.mu.Unlock()
	if mock.authorizeHits != 1 || mock.codeHits != 1 || mock.refreshHits != 1 {
		t.Fatalf("server hits = authorize %d, code %d, refresh %d", mock.authorizeHits, mock.codeHits, mock.refreshHits)
	}
}

func TestOAuthSendWithoutAuthorizationIsActionable(t *testing.T) {
	svc, ws := openTestService(t)
	target, _ := newEcho(t)
	req := oauthRequest("https://auth.example/token", "https://auth.example/authorize")
	req.URL = target.URL
	resp := svc.Send(context.Background(), ws, req, "", "", "")
	if resp.Error == "" {
		t.Fatal("a send without a stored token should fail")
	}
	if !strings.Contains(resp.Error, "authorization required") || !strings.Contains(resp.Error, "authorize") {
		t.Fatalf("error = %q, want an actionable authorization hint", resp.Error)
	}
}

func TestOAuthStateMismatchIsRejected(t *testing.T) {
	svc, ws := openTestService(t)
	srv := httptest.NewServer((&oauthMock{}).handler(t))
	defer srv.Close()

	req := oauthRequest(srv.URL+"/token", srv.URL+"/authorize")
	open := func(raw string) error {
		u, err := url.Parse(raw)
		if err != nil {
			return err
		}
		cb, err := url.Parse(u.Query().Get("redirect_uri"))
		if err != nil {
			return err
		}
		q := cb.Query()
		q.Set("code", "auth-code")
		q.Set("state", "not-the-state")
		cb.RawQuery = q.Encode()
		resp, err := http.Get(cb.String())
		if err != nil {
			return err
		}
		resp.Body.Close()
		return nil
	}
	_, err := svc.AuthorizeOAuthWith(context.Background(), ws, req, "", "", open)
	if err == nil || !strings.Contains(err.Error(), "state mismatch") {
		t.Fatalf("error = %v, want a state mismatch", err)
	}
}

func TestOAuthExplicitLoopbackRedirect(t *testing.T) {
	svc, ws := openTestService(t)
	mock := &oauthMock{}
	srv := httptest.NewServer(mock.handler(t))
	defer srv.Close()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()

	req := oauthRequest(srv.URL+"/token", srv.URL+"/authorize")
	req.Auth.OAuth2.RedirectURL = fmt.Sprintf("http://127.0.0.1:%d/callback", port)
	if _, err := svc.AuthorizeOAuthWith(context.Background(), ws, req, "", "", browserOpener(t)); err != nil {
		t.Fatalf("AuthorizeOAuthWith with an explicit redirect: %v", err)
	}

	// The configured redirect is part of the token key; a send must find it.
	target, got := newEcho(t)
	req.URL = target.URL
	resp := svc.Send(context.Background(), ws, req, "", "", "")
	if resp.Error != "" {
		t.Fatalf("send: %s", resp.Error)
	}
	if auth := got.Headers.Get("Authorization"); !strings.HasPrefix(auth, "Bearer access-") {
		t.Fatalf("Authorization = %q", auth)
	}

	bad := oauthRequest(srv.URL+"/token", srv.URL+"/authorize")
	bad.Auth.OAuth2.RedirectURL = "https://evil.example/callback"
	if _, err := svc.AuthorizeOAuthWith(context.Background(), ws, bad, "", "", browserOpener(t)); err == nil ||
		!strings.Contains(err.Error(), "127.0.0.1 or localhost") {
		t.Fatalf("non-loopback redirect error = %v", err)
	}
}
