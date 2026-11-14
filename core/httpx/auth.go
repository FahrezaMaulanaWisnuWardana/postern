package httpx

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"yourpm/core/brand"
	"yourpm/core/model"
)

// applyAuth attaches credentials to a request. Values are interpolated here
// so tokens can come from {{variables}}.
func (x *Executor) applyAuth(ctx context.Context, req *http.Request, r model.RequestData, iv func(string) string) error {
	switch r.Auth.Type {
	case "", model.AuthNone, model.AuthInherit:
		return nil

	case model.AuthBearer:
		tok := strings.TrimSpace(iv(r.Auth.Token))
		if tok != "" {
			req.Header.Set("Authorization", "Bearer "+tok)
		}

	case model.AuthBasic:
		req.SetBasicAuth(iv(r.Auth.Username), iv(r.Auth.Password))

	case model.AuthAPIKey:
		key := strings.TrimSpace(iv(r.Auth.Key))
		if key == "" {
			return nil
		}
		val := iv(r.Auth.Value)
		if r.Auth.In == "query" {
			q := req.URL.Query()
			q.Set(key, val)
			req.URL.RawQuery = q.Encode()
		} else {
			req.Header.Set(key, val)
		}

	case model.AuthOAuth2:
		if r.Auth.OAuth2 == nil {
			return nil
		}
		token, err := x.oauthToken(ctx, *r.Auth.OAuth2, iv, r.Settings)
		if err != nil {
			return fmt.Errorf("oauth2: %w", err)
		}
		name := strings.TrimSpace(iv(r.Auth.OAuth2.TokenName))
		if name == "" {
			name = "Authorization"
		}
		prefix := strings.TrimSpace(iv(r.Auth.OAuth2.HeaderPrefix))
		if prefix == "" && name == "Authorization" {
			prefix = "Bearer"
		}
		value := token
		if prefix != "" {
			value = prefix + " " + token
		}
		req.Header.Set(name, value)

	default:
		return fmt.Errorf("unsupported auth type %q", r.Auth.Type)
	}
	return nil
}

// oauthToken returns a cached or freshly fetched OAuth2 access token.
func (x *Executor) oauthToken(ctx context.Context, cfg model.OAuth2, iv func(string) string, settings model.RequestSettings) (string, error) {
	resolved := model.OAuth2{
		GrantType:        iv(cfg.GrantType),
		AuthorizationURL: iv(cfg.AuthorizationURL),
		AccessTokenURL:   iv(cfg.AccessTokenURL),
		RedirectURL:      iv(cfg.RedirectURL),
		ClientID:         iv(cfg.ClientID),
		ClientSecret:     iv(cfg.ClientSecret),
		Scope:            iv(cfg.Scope),
		Username:         iv(cfg.Username),
		Password:         iv(cfg.Password),
		ClientAuth:       cfg.ClientAuth,
		TokenName:        cfg.TokenName,
		HeaderPrefix:     cfg.HeaderPrefix,
	}
	if resolved.GrantType == "" {
		resolved.GrantType = "client_credentials"
	}
	if resolved.AccessTokenURL == "" {
		return "", fmt.Errorf("access token URL is empty")
	}

	// Authorization-code tokens are owned by the service layer: it persists
	// them, refreshes them, and runs the interactive flow. The engine must
	// never open a browser in the middle of a send.
	if resolved.GrantType == model.GrantAuthorizationCode {
		x.mu.Lock()
		provider := x.oauthProvider
		x.mu.Unlock()
		if provider == nil {
			return "", fmt.Errorf("authorization required: connect it from the desktop app or run %s authorize", brand.Slug)
		}
		token, err := provider(ctx, resolved)
		if err != nil {
			return "", err
		}
		if strings.TrimSpace(token) == "" {
			return "", fmt.Errorf("authorization required: connect it from the desktop app or run %s authorize", brand.Slug)
		}
		return token, nil
	}

	key := tokenCacheKey(resolved)
	x.mu.Lock()
	if entry, ok := x.tokens[key]; ok && time.Now().Before(entry.expires) {
		x.mu.Unlock()
		return entry.token, nil
	}
	x.mu.Unlock()

	val := url.Values{}
	val.Set("grant_type", resolved.GrantType)
	if resolved.Scope != "" {
		val.Set("scope", resolved.Scope)
	}
	if resolved.GrantType == "password" {
		val.Set("username", resolved.Username)
		val.Set("password", resolved.Password)
	}
	if resolved.ClientAuth == "body" {
		val.Set("client_id", resolved.ClientID)
		val.Set("client_secret", resolved.ClientSecret)
	}

	reqCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, resolved.AccessTokenURL, strings.NewReader(val.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", brand.Name+"/"+brand.Version)
	if resolved.ClientAuth != "body" && resolved.ClientID != "" {
		req.SetBasicAuth(resolved.ClientID, resolved.ClientSecret)
	}

	client := &http.Client{Transport: x.transport(settings), Timeout: 20 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return "", fmt.Errorf("token endpoint returned %d: %s", resp.StatusCode, snippet(raw))
	}

	token, expiresIn, err := parseTokenResponse(raw)
	if err != nil {
		return "", err
	}
	ttl := time.Duration(expiresIn) * time.Second
	if ttl <= 0 {
		ttl = time.Hour
	}
	expires := time.Now().Add(ttl - 30*time.Second)
	if expires.Before(time.Now().Add(10 * time.Second)) {
		expires = time.Now().Add(10 * time.Second)
	}

	x.mu.Lock()
	x.tokens[key] = tokenEntry{token: token, expires: expires}
	x.mu.Unlock()
	return token, nil
}

func parseTokenResponse(raw []byte) (string, int64, error) {
	var payload struct {
		AccessToken string      `json:"access_token"`
		ExpiresIn   json.Number `json:"expires_in"`
		TokenType   string      `json:"token_type"`
	}
	if err := json.Unmarshal(raw, &payload); err == nil && payload.AccessToken != "" {
		var seconds int64 = 3600
		if payload.ExpiresIn != "" {
			if n, err := payload.ExpiresIn.Int64(); err == nil {
				seconds = n
			}
		}
		return payload.AccessToken, seconds, nil
	}
	// Some endpoints answer with form encoding.
	if vals, err := url.ParseQuery(string(raw)); err == nil {
		if tok := vals.Get("access_token"); tok != "" {
			var seconds int64 = 3600
			if v := vals.Get("expires_in"); v != "" {
				fmt.Sscanf(v, "%d", &seconds)
			}
			return tok, seconds, nil
		}
	}
	return "", 0, fmt.Errorf("no access_token in token response: %s", snippet(raw))
}

func snippet(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 200 {
		s = s[:200] + "..."
	}
	return s
}
