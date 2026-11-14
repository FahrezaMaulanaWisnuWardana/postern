package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"yourpm/core/brand"
	"yourpm/core/model"
	"yourpm/core/vars"
)

// oauthFlowTimeout bounds the time the user has to finish the browser consent
// screen before the loopback listener is closed.
const oauthFlowTimeout = 3 * time.Minute

// ErrOAuthAuthorizationRequired is returned when a request uses the
// authorization-code grant but no usable token is stored.
var ErrOAuthAuthorizationRequired = errors.New("oauth2: authorization required")

// OAuthTokenStatus describes the stored token for an authorization-code
// request. It never exposes the token itself.
type OAuthTokenStatus struct {
	Authorized  bool
	ExpiresAt   int64 // unix milliseconds; 0 means the provider did not say
	Refreshable bool
}

// oauthTokenRecord is the sealed setting payload. Access and refresh tokens
// are stored together so a refresh can rotate either one atomically.
type oauthTokenRecord struct {
	AccessToken  string `json:"accessToken"`
	RefreshToken string `json:"refreshToken,omitempty"`
	TokenType    string `json:"tokenType,omitempty"`
	Scope        string `json:"scope,omitempty"`
	ExpiresAt    int64  `json:"expiresAt,omitempty"`
}

// oauthTokenKey identifies one authorization-code session. It deliberately
// uses the configured redirect URL (empty for the automatic loopback) rather
// than the ephemeral port, so a later send finds the token that was stored.
func oauthTokenKey(cfg model.OAuth2) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{
		model.GrantAuthorizationCode,
		cfg.AuthorizationURL,
		cfg.AccessTokenURL,
		cfg.RedirectURL,
		cfg.ClientID,
		cfg.Scope,
	}, "\x00")))
	return "oauth.token." + hex.EncodeToString(sum[:16])
}

// AuthorizeOAuth runs the browser authorization-code flow with PKCE and stores
// the resulting token. It returns the expiry reported by the provider, if any.
func (s *Service) AuthorizeOAuth(ctx context.Context, ws string, req model.RequestData, envID, collectionID string) (time.Time, error) {
	return s.authorizeOAuth(ctx, ws, req, envID, collectionID, openSystemBrowser)
}

// AuthorizeOAuthWith is AuthorizeOAuth with an injectable browser opener, used
// by tests and by environments without a desktop session.
func (s *Service) AuthorizeOAuthWith(ctx context.Context, ws string, req model.RequestData, envID, collectionID string, open func(string) error) (time.Time, error) {
	return s.authorizeOAuth(ctx, ws, req, envID, collectionID, open)
}

func (s *Service) authorizeOAuth(ctx context.Context, ws string, req model.RequestData, envID, collectionID string, open func(string) error) (time.Time, error) {
	if enabled, unlocked := s.VaultStatus(); enabled && !unlocked {
		return time.Time{}, errors.New("the vault is locked; unlock it before authorizing")
	}
	cfg, err := s.resolveOAuthConfig(ws, &req, envID, collectionID)
	if err != nil {
		return time.Time{}, err
	}
	if cfg.GrantType != model.GrantAuthorizationCode {
		return time.Time{}, errors.New("set the OAuth 2.0 grant type to Authorization code first")
	}
	if strings.TrimSpace(cfg.AuthorizationURL) == "" {
		return time.Time{}, errors.New("authorization URL is empty")
	}
	if strings.TrimSpace(cfg.AccessTokenURL) == "" {
		return time.Time{}, errors.New("access token URL is empty")
	}
	if strings.TrimSpace(cfg.ClientID) == "" {
		return time.Time{}, errors.New("client ID is empty")
	}
	if err := requireHTTPURL("authorization URL", cfg.AuthorizationURL); err != nil {
		return time.Time{}, err
	}
	if err := requireHTTPURL("access token URL", cfg.AccessTokenURL); err != nil {
		return time.Time{}, err
	}

	// The token is keyed by the configured values, before the loopback port is
	// known, so a subsequent send derives the same key.
	key := oauthTokenKey(cfg)

	ln, callbackPath, redirectURL, err := oauthLoopback(cfg.RedirectURL)
	if err != nil {
		return time.Time{}, err
	}
	defer ln.Close()

	verifier, err := randomURLToken(32)
	if err != nil {
		return time.Time{}, err
	}
	state, err := randomURLToken(16)
	if err != nil {
		return time.Time{}, err
	}

	authorizeURL, err := buildAuthorizeURL(cfg, redirectURL, state, pkceChallenge(verifier))
	if err != nil {
		return time.Time{}, err
	}

	type callback struct {
		code  string
		state string
		err   string
	}
	result := make(chan callback, 1)
	srv := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != callbackPath {
				http.NotFound(w, r)
				return
			}
			q := r.URL.Query()
			cb := callback{code: q.Get("code"), state: q.Get("state"), err: q.Get("error")}
			if detail := q.Get("error_description"); detail != "" && cb.err != "" {
				cb.err += ": " + detail
			}
			select {
			case result <- cb:
			default:
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Header().Set("Cache-Control", "no-store")
			if cb.err != "" || cb.code == "" {
				fmt.Fprintf(w, oauthCallbackHTML, brand.Name, "Authorization failed", "You can close this tab and return to "+brand.Name+".")
				return
			}
			fmt.Fprintf(w, oauthCallbackHTML, brand.Name, "Authorization complete", "You can close this tab and return to "+brand.Name+".")
		}),
		ReadHeaderTimeout: 5 * time.Second,
	}
	serveErr := make(chan error, 1)
	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErr <- err
		}
	}()
	defer srv.Close()

	if open == nil {
		open = openSystemBrowser
	}
	if err := open(authorizeURL); err != nil {
		return time.Time{}, fmt.Errorf("could not open the browser: %w (open this URL manually: %s)", err, authorizeURL)
	}

	timer := time.NewTimer(oauthFlowTimeout)
	defer timer.Stop()

	var cb callback
	select {
	case <-ctx.Done():
		return time.Time{}, ctx.Err()
	case err := <-serveErr:
		return time.Time{}, fmt.Errorf("the authorization listener stopped: %w", err)
	case <-timer.C:
		return time.Time{}, errors.New("authorization timed out after 3 minutes")
	case cb = <-result:
	}

	if cb.err != "" {
		return time.Time{}, fmt.Errorf("the provider returned an error: %s", cb.err)
	}
	if cb.state != state {
		return time.Time{}, errors.New("authorization failed: state mismatch; the callback did not belong to this request")
	}
	if cb.code == "" {
		return time.Time{}, errors.New("authorization failed: no code in the callback")
	}

	rec, err := s.exchangeOAuthCode(ctx, cfg, cb.code, verifier, redirectURL)
	if err != nil {
		return time.Time{}, err
	}
	if err := s.saveOAuthToken(key, rec); err != nil {
		return time.Time{}, err
	}
	return expiryTime(rec), nil
}

// OAuthTokenStatus reports whether a request has a stored authorization-code
// token. It resolves variables the same way a send does.
func (s *Service) OAuthTokenStatus(ws string, req model.RequestData, envID, collectionID string) OAuthTokenStatus {
	cfg, err := s.resolveOAuthConfig(ws, &req, envID, collectionID)
	if err != nil || cfg.GrantType != model.GrantAuthorizationCode {
		return OAuthTokenStatus{}
	}
	rec, ok := s.loadOAuthToken(oauthTokenKey(cfg))
	if !ok {
		return OAuthTokenStatus{}
	}
	return OAuthTokenStatus{
		Authorized:  rec.AccessToken != "" || rec.RefreshToken != "",
		ExpiresAt:   rec.ExpiresAt,
		Refreshable: rec.RefreshToken != "",
	}
}

// oauthAccessToken is installed on the HTTP engine. It returns a valid access
// token, refreshing it when possible, and never starts a browser.
func (s *Service) oauthAccessToken(ctx context.Context, cfg model.OAuth2) (string, error) {
	if cfg.GrantType != model.GrantAuthorizationCode {
		return "", ErrOAuthAuthorizationRequired
	}
	if enabled, unlocked := s.VaultStatus(); enabled && !unlocked {
		return "", errors.New("the vault is locked; unlock it to use the stored OAuth 2.0 token")
	}
	key := oauthTokenKey(cfg)
	rec, ok := s.loadOAuthToken(key)
	if ok && rec.AccessToken != "" && !tokenExpired(rec) {
		return rec.AccessToken, nil
	}
	if ok && rec.RefreshToken != "" {
		refreshed, err := s.refreshOAuthToken(ctx, cfg, rec)
		if err == nil {
			if err := s.saveOAuthToken(key, refreshed); err != nil {
				return "", err
			}
			return refreshed.AccessToken, nil
		}
		return "", fmt.Errorf("authorization required: the stored token expired and refresh failed (%v); run %s authorize", err, brand.Slug)
	}
	return "", fmt.Errorf("authorization required: connect it from the desktop app or run %s authorize", brand.Slug)
}

func (s *Service) resolveOAuthConfig(ws string, req *model.RequestData, envID, collectionID string) (model.OAuth2, error) {
	s.PrepareRequest(ws, req, collectionID)
	if req.Auth.Type != model.AuthOAuth2 || req.Auth.OAuth2 == nil {
		return model.OAuth2{}, errors.New("this request does not use OAuth 2.0 authentication")
	}
	lookup, _ := s.VariableMap(ws, envID, collectionID, nil)
	iv := func(v string) string { return vars.Interpolate(v, vars.MapLookup(lookup)) }
	raw := *req.Auth.OAuth2
	cfg := model.OAuth2{
		GrantType:        strings.ToLower(strings.TrimSpace(iv(raw.GrantType))),
		AuthorizationURL: strings.TrimSpace(iv(raw.AuthorizationURL)),
		AccessTokenURL:   strings.TrimSpace(iv(raw.AccessTokenURL)),
		RedirectURL:      strings.TrimSpace(iv(raw.RedirectURL)),
		ClientID:         strings.TrimSpace(iv(raw.ClientID)),
		ClientSecret:     iv(raw.ClientSecret),
		Scope:            strings.TrimSpace(iv(raw.Scope)),
		Username:         iv(raw.Username),
		Password:         iv(raw.Password),
		ClientAuth:       raw.ClientAuth,
		TokenName:        raw.TokenName,
		HeaderPrefix:     raw.HeaderPrefix,
	}
	if cfg.GrantType == "" {
		cfg.GrantType = model.GrantClientCredentials
	}
	return cfg, nil
}

// oauthLoopback prepares the local redirect listener. An empty redirect URL
// asks the OS for a free port and uses /callback; an explicit URL must point
// at the loopback interface so the app never exposes a listener publicly.
func oauthLoopback(configured string) (net.Listener, string, string, error) {
	if configured == "" {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return nil, "", "", fmt.Errorf("could not start the loopback listener: %w", err)
		}
		addr := ln.Addr().(*net.TCPAddr)
		return ln, "/callback", fmt.Sprintf("http://127.0.0.1:%d/callback", addr.Port), nil
	}

	u, err := url.Parse(configured)
	if err != nil || u.Scheme != "http" || u.Host == "" {
		return nil, "", "", errors.New("redirect URL must use http:// with 127.0.0.1 or localhost and include a port")
	}
	host := u.Hostname()
	switch host {
	case "127.0.0.1", "::1", "localhost":
	default:
		return nil, "", "", errors.New("redirect URL must use 127.0.0.1 or localhost")
	}
	port := u.Port()
	if port == "" {
		return nil, "", "", errors.New("redirect URL must include a port")
	}
	path := u.Path
	if path == "" {
		path = "/callback"
	}
	listenAddr := net.JoinHostPort(host, port)
	ln, err := net.Listen("tcp", listenAddr)
	if err != nil {
		return nil, "", "", fmt.Errorf("could not listen on %s: %w", listenAddr, err)
	}
	return ln, path, u.String(), nil
}

func buildAuthorizeURL(cfg model.OAuth2, redirectURL, state, challenge string) (string, error) {
	u, err := url.Parse(cfg.AuthorizationURL)
	if err != nil {
		return "", fmt.Errorf("authorization URL is invalid: %w", err)
	}
	q := u.Query()
	q.Set("response_type", "code")
	q.Set("client_id", cfg.ClientID)
	q.Set("redirect_uri", redirectURL)
	q.Set("state", state)
	q.Set("code_challenge", challenge)
	q.Set("code_challenge_method", "S256")
	if cfg.Scope != "" {
		q.Set("scope", cfg.Scope)
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}

func (s *Service) exchangeOAuthCode(ctx context.Context, cfg model.OAuth2, code, verifier, redirectURL string) (oauthTokenRecord, error) {
	form := url.Values{}
	form.Set("grant_type", model.GrantAuthorizationCode)
	form.Set("code", code)
	form.Set("redirect_uri", redirectURL)
	form.Set("client_id", cfg.ClientID)
	form.Set("code_verifier", verifier)
	if cfg.ClientAuth == "body" && cfg.ClientSecret != "" {
		form.Set("client_secret", cfg.ClientSecret)
	}
	return s.postOAuthToken(ctx, cfg, form)
}

func (s *Service) refreshOAuthToken(ctx context.Context, cfg model.OAuth2, prev oauthTokenRecord) (oauthTokenRecord, error) {
	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("refresh_token", prev.RefreshToken)
	form.Set("client_id", cfg.ClientID)
	if cfg.Scope != "" {
		form.Set("scope", cfg.Scope)
	}
	if cfg.ClientAuth == "body" && cfg.ClientSecret != "" {
		form.Set("client_secret", cfg.ClientSecret)
	}
	next, err := s.postOAuthToken(ctx, cfg, form)
	if err != nil {
		return oauthTokenRecord{}, err
	}
	if next.RefreshToken == "" {
		next.RefreshToken = prev.RefreshToken
	}
	if next.Scope == "" {
		next.Scope = prev.Scope
	}
	return next, nil
}

func (s *Service) postOAuthToken(ctx context.Context, cfg model.OAuth2, form url.Values) (oauthTokenRecord, error) {
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.AccessTokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return oauthTokenRecord{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", brand.Name+"/"+brand.Version)
	if cfg.ClientAuth != "body" && cfg.ClientSecret != "" {
		req.SetBasicAuth(cfg.ClientID, cfg.ClientSecret)
	}
	resp, err := (&http.Client{Timeout: 25 * time.Second}).Do(req)
	if err != nil {
		return oauthTokenRecord{}, fmt.Errorf("could not reach the token endpoint: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return oauthTokenRecord{}, err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return oauthTokenRecord{}, fmt.Errorf("token endpoint returned %d: %s", resp.StatusCode, snippet(body))
	}
	rec, err := parseOAuthToken(body)
	if err != nil {
		return oauthTokenRecord{}, err
	}
	if rec.ExpiresAt == 0 && rec.RefreshToken != "" {
		// Providers that omit expires_in and issue a refresh token still get a
		// refresh attempt after an hour instead of a token treated as valid
		// forever. Without a refresh token, keep the unknown lifetime so the
		// token is not discarded early.
		rec.ExpiresAt = time.Now().Add(time.Hour).UnixMilli()
	}
	return rec, nil
}

func parseOAuthToken(raw []byte) (oauthTokenRecord, error) {
	var payload struct {
		AccessToken  string      `json:"access_token"`
		RefreshToken string      `json:"refresh_token"`
		TokenType    string      `json:"token_type"`
		Scope        string      `json:"scope"`
		ExpiresIn    json.Number `json:"expires_in"`
	}
	if err := json.Unmarshal(raw, &payload); err == nil && payload.AccessToken != "" {
		rec := oauthTokenRecord{
			AccessToken:  payload.AccessToken,
			RefreshToken: payload.RefreshToken,
			TokenType:    payload.TokenType,
			Scope:        payload.Scope,
		}
		if n, err := payload.ExpiresIn.Int64(); err == nil && n > 0 {
			rec.ExpiresAt = time.Now().Add(time.Duration(n) * time.Second).UnixMilli()
		}
		return rec, nil
	}
	if vals, err := url.ParseQuery(string(raw)); err == nil && vals.Get("access_token") != "" {
		rec := oauthTokenRecord{
			AccessToken:  vals.Get("access_token"),
			RefreshToken: vals.Get("refresh_token"),
			TokenType:    vals.Get("token_type"),
			Scope:        vals.Get("scope"),
		}
		var seconds int64
		if _, err := fmt.Sscanf(vals.Get("expires_in"), "%d", &seconds); err == nil && seconds > 0 {
			rec.ExpiresAt = time.Now().Add(time.Duration(seconds) * time.Second).UnixMilli()
		}
		return rec, nil
	}
	return oauthTokenRecord{}, fmt.Errorf("no access_token in token response: %s", snippet(raw))
}

func (s *Service) loadOAuthToken(key string) (oauthTokenRecord, bool) {
	raw := s.Store.SettingSecret(key)
	if raw == "" {
		return oauthTokenRecord{}, false
	}
	var rec oauthTokenRecord
	if err := json.Unmarshal([]byte(raw), &rec); err != nil {
		return oauthTokenRecord{}, false
	}
	return rec, true
}

func (s *Service) saveOAuthToken(key string, rec oauthTokenRecord) error {
	raw, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	return s.Store.SetSecretSetting(key, string(raw))
}

func tokenExpired(rec oauthTokenRecord) bool {
	if rec.ExpiresAt == 0 {
		return false
	}
	return time.Now().Add(30*time.Second).UnixMilli() >= rec.ExpiresAt
}

func expiryTime(rec oauthTokenRecord) time.Time {
	if rec.ExpiresAt == 0 {
		return time.Time{}
	}
	return time.UnixMilli(rec.ExpiresAt)
}

func pkceChallenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func randomURLToken(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func requireHTTPURL(label, raw string) error {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("%s must be a full http(s) URL", label)
	}
	return nil
}

func openSystemBrowser(rawURL string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", rawURL)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", rawURL)
	default:
		cmd = exec.Command("xdg-open", rawURL)
	}
	return cmd.Start()
}

func snippet(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 200 {
		s = s[:200] + "..."
	}
	return s
}

const oauthCallbackHTML = `<!doctype html>
<html lang="en"><head><meta charset="utf-8"><title>%s</title>
<style>body{font-family:system-ui,sans-serif;background:#12140f;color:#e8e8e2;display:grid;place-items:center;min-height:100vh;margin:0}
main{max-width:34rem;padding:2rem;border:1px solid #333;border-radius:12px;background:#191c15}
h1{font-size:1.15rem;margin:0 0 .5rem}p{margin:0;color:#a8ad9c}</style></head>
<body><main><h1>%s</h1><p>%s</p></main></body></html>`
