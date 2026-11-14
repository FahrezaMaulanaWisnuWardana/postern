package server_test

import (
	"bytes"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"yourpm/server"
)

type api struct {
	t     *testing.T
	base  string
	token string
}

func newAPI(t *testing.T) *api {
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
	return &api{t: t, base: ts.URL}
}

func (a *api) do(method, path string, body any) (int, map[string]any) {
	a.t.Helper()
	var rdr io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			a.t.Fatal(err)
		}
		rdr = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, a.base+path, rdr)
	if err != nil {
		a.t.Fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if a.token != "" {
		req.Header.Set("Authorization", "Bearer "+a.token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		a.t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	out := map[string]any{}
	if len(raw) > 0 && strings.HasPrefix(resp.Header.Get("Content-Type"), "application/json") {
		if err := json.Unmarshal(raw, &out); err != nil {
			a.t.Fatalf("%s %s: response is not JSON: %s", method, path, raw)
		}
	}
	return resp.StatusCode, out
}

func TestAuthAndSyncProtocol(t *testing.T) {
	a := newAPI(t)

	status, body := a.do("POST", "/v1/auth/register", map[string]any{
		"email":    "tester@example.com",
		"password": "supersecret",
		"device":   "test",
	})
	if status != http.StatusCreated {
		t.Fatalf("register = %d %v", status, body)
	}
	a.token, _ = body["token"].(string)
	if a.token == "" {
		t.Fatal("register returned no token")
	}

	// Wrong password must be rejected with the same shape as unknown users.
	status, _ = a.do("POST", "/v1/auth/login", map[string]any{"email": "tester@example.com", "password": "wrongpass"})
	if status != http.StatusUnauthorized {
		t.Fatalf("bad login = %d", status)
	}
	status, body = a.do("POST", "/v1/auth/login", map[string]any{"email": "tester@example.com", "password": "supersecret"})
	if status != http.StatusOK || body["token"] == "" {
		t.Fatalf("login = %d %v", status, body)
	}
	a.token, _ = body["token"].(string)

	salt := base64.RawStdEncoding.EncodeToString([]byte("0123456789abcdef"))
	wrapped := base64.RawStdEncoding.EncodeToString([]byte("wrapped-key-bytes"))
	status, body = a.do("POST", "/v1/workspaces", map[string]any{
		"name": "Team", "encrypted": true, "kdfSalt": salt, "wrappedKey": wrapped,
	})
	if status != http.StatusCreated {
		t.Fatalf("create workspace = %d %v", status, body)
	}
	wsID, _ := body["id"].(string)
	if wsID == "" {
		t.Fatal("workspace id missing")
	}

	push := func(opID, docID string, baseVersion int64, ct string) (int, map[string]any) {
		return a.do("POST", "/v1/workspaces/"+wsID+"/push", map[string]any{
			"deviceId": "dev-1",
			"ops": []map[string]any{{
				"opId": opID, "docId": docID, "kind": "request",
				"baseVersion": baseVersion, "ciphertext": ct, "updatedAt": 1,
			}},
		})
	}

	status, body = push("op-1", "doc-1", 0, "ciphertext-1")
	if status != http.StatusOK {
		t.Fatalf("push = %d %v", status, body)
	}
	accepted := body["accepted"].([]any)
	if len(accepted) != 1 || accepted[0].(map[string]any)["version"].(float64) != 1 {
		t.Fatalf("accepted = %v", accepted)
	}

	// A stale base version must come back as a conflict, not overwrite.
	status, body = push("op-2", "doc-1", 0, "stale")
	if status != http.StatusOK {
		t.Fatalf("stale push = %d %v", status, body)
	}
	conflicts := body["conflicts"].([]any)
	if len(conflicts) != 1 {
		t.Fatalf("conflicts = %v", conflicts)
	}
	if conflicts[0].(map[string]any)["ciphertext"] != "ciphertext-1" {
		t.Fatalf("conflict did not return the current ciphertext: %v", conflicts[0])
	}

	status, body = push("op-3", "doc-1", 1, "ciphertext-2")
	if status != http.StatusOK || len(body["accepted"].([]any)) != 1 {
		t.Fatalf("retry = %d %v", status, body)
	}
	if got := body["accepted"].([]any)[0].(map[string]any)["version"].(float64); got != 2 {
		t.Fatalf("retry version = %v", got)
	}

	// Idempotency: replaying an accepted op must not create another revision.
	status, body = push("op-3", "doc-1", 1, "ciphertext-2")
	if status != http.StatusOK {
		t.Fatalf("replay = %d", status)
	}
	if got := body["accepted"].([]any)[0].(map[string]any)["version"].(float64); got != 2 {
		t.Fatalf("replay produced version %v", got)
	}

	status, body = a.do("GET", "/v1/workspaces/"+wsID+"/pull?since=0", nil)
	if status != http.StatusOK {
		t.Fatalf("pull = %d %v", status, body)
	}
	ops := body["ops"].([]any)
	if len(ops) != 2 {
		t.Fatalf("pull returned %d ops, want 2", len(ops))
	}
	last := ops[len(ops)-1].(map[string]any)
	if last["docId"] != "doc-1" || last["ciphertext"] != "ciphertext-2" {
		t.Fatalf("last op = %v", last)
	}
	next := int64(body["next"].(float64))
	if next != 2 {
		t.Fatalf("next cursor = %d", next)
	}

	status, body = a.do("GET", "/v1/workspaces/"+wsID+"/pull?since=2", nil)
	if status != http.StatusOK {
		t.Fatalf("incremental pull = %d", status)
	}
	if len(body["ops"].([]any)) != 0 {
		t.Fatalf("incremental pull replayed ops: %v", body["ops"])
	}
}

func TestUnauthorizedAndRegistrationPolicy(t *testing.T) {
	a := newAPI(t)
	status, _ := a.do("GET", "/v1/workspaces", nil)
	if status != http.StatusUnauthorized {
		t.Fatalf("unauthenticated list = %d", status)
	}
	status, _ = a.do("GET", "/healthz", nil)
	if status != http.StatusOK {
		t.Fatalf("healthz = %d", status)
	}
}

func TestRegistrationCanBeClosed(t *testing.T) {
	srv, err := server.New(server.Config{
		Addr:         "127.0.0.1:0",
		DataDir:      t.TempDir(),
		JWTSecret:    "0123456789abcdef0123456789abcdef",
		Registration: "closed",
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	raw, _ := json.Marshal(map[string]string{"email": "x@example.com", "password": "supersecret"})
	resp, err := http.Post(ts.URL+"/v1/auth/register", "application/json", bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("closed registration = %d", resp.StatusCode)
	}
}

func TestMetricsCanRequireAToken(t *testing.T) {
	srv, err := server.New(server.Config{
		Addr:         "127.0.0.1:0",
		DataDir:      t.TempDir(),
		JWTSecret:    "0123456789abcdef0123456789abcdef",
		Registration: "closed",
		MetricsToken: "metrics-secret",
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	status, _ := http.Get(ts.URL + "/metrics")
	if status.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated metrics = %d", status.StatusCode)
	}
	status.Body.Close()

	req, err := http.NewRequest("GET", ts.URL+"/metrics", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer metrics-secret")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("authenticated metrics = %d", resp.StatusCode)
	}
	if !bytes.Contains(body, []byte("yourpm_requests_total")) {
		t.Fatalf("metrics body = %s", body)
	}

	resp, err = http.Get(ts.URL + "/metrics?token=metrics-secret")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("query-token metrics = %d", resp.StatusCode)
	}
}

func TestAuthRateLimitIgnoresForwardedForByDefault(t *testing.T) {
	a := newAPI(t)
	for i := 0; i < 20; i++ {
		req, err := http.NewRequest("POST", a.base+"/v1/auth/login", strings.NewReader(`{"email":"nobody@example.com","password":"wrong-password"}`))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		// Each request claims a different client IP. With TrustProxy off the
		// server must still rate limit them as one source.
		req.Header.Set("X-Forwarded-For", fmt.Sprintf("10.0.0.%d", i+1))
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode == http.StatusTooManyRequests {
			t.Fatalf("request %d was rate limited early", i+1)
		}
	}
	req, _ := http.NewRequest("POST", a.base+"/v1/auth/login", strings.NewReader(`{"email":"nobody@example.com","password":"wrong-password"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Forwarded-For", "10.0.0.99")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("21st attempt = %d, want 429", resp.StatusCode)
	}
}

func TestOpRetentionKeepsNewestOpPerDocument(t *testing.T) {
	dir := t.TempDir()
	cfg := server.Config{
		Addr:         "127.0.0.1:0",
		DataDir:      dir,
		JWTSecret:    "0123456789abcdef0123456789abcdef",
		Registration: "open",
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	srv1, err := server.New(cfg, log)
	if err != nil {
		t.Fatal(err)
	}
	ts1 := httptest.NewServer(srv1.Handler())
	a := &api{t: t, base: ts1.URL}
	status, body := a.do("POST", "/v1/auth/register", map[string]any{
		"email": "retention@example.com", "password": "supersecret", "device": "one",
	})
	if status != http.StatusCreated {
		t.Fatalf("register = %d %v", status, body)
	}
	a.token, _ = body["token"].(string)
	status, body = a.do("POST", "/v1/workspaces", map[string]any{"name": "Retention", "encrypted": false})
	if status != http.StatusCreated {
		t.Fatalf("workspace = %d %v", status, body)
	}
	wsID, _ := body["id"].(string)

	push := func(opID, docID string, base int, ct string) {
		status, body := a.do("POST", "/v1/workspaces/"+wsID+"/push", map[string]any{
			"deviceId": "dev-1",
			"ops": []map[string]any{{
				"opId": opID, "docId": docID, "kind": "request",
				"baseVersion": base, "ciphertext": ct, "updatedAt": 1,
			}},
		})
		if status != http.StatusOK {
			t.Fatalf("push %s = %d %v", opID, status, body)
		}
	}
	push("op-1", "doc-1", 0, "oldest")
	push("op-2", "doc-1", 1, "newest")
	push("op-3", "doc-2", 0, "other-doc")

	ts1.Close()
	if err := srv1.Close(); err != nil {
		t.Fatal(err)
	}

	// Backdate every operation so the retention window covers them.
	db, err := sql.Open("sqlite", filepath.Join(dir, "sync.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE ops SET created_at = 1`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	cfg.OpRetentionDays = 1
	srv2, err := server.New(cfg, log)
	if err != nil {
		t.Fatal(err)
	}
	defer srv2.Close()
	ts2 := httptest.NewServer(srv2.Handler())
	defer ts2.Close()
	a = &api{t: t, base: ts2.URL}
	status, body = a.do("POST", "/v1/auth/login", map[string]any{
		"email": "retention@example.com", "password": "supersecret", "device": "one",
	})
	if status != http.StatusOK {
		t.Fatalf("login = %d %v", status, body)
	}
	a.token, _ = body["token"].(string)

	status, body = a.do("GET", "/v1/workspaces/"+wsID+"/pull?since=0", nil)
	if status != http.StatusOK {
		t.Fatalf("pull = %d %v", status, body)
	}
	ops, _ := body["ops"].([]any)
	if len(ops) != 2 {
		t.Fatalf("pull returned %d ops after pruning, want 2: %v", len(ops), body)
	}
	got := map[string]string{}
	for _, raw := range ops {
		op := raw.(map[string]any)
		got[op["docId"].(string)] = op["ciphertext"].(string)
	}
	if got["doc-1"] != "newest" {
		t.Fatalf("doc-1 ciphertext = %q, want newest", got["doc-1"])
	}
	if got["doc-2"] != "other-doc" {
		t.Fatalf("doc-2 ciphertext = %q", got["doc-2"])
	}
}
