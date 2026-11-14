// Package engine executes HTTP requests: it resolves variables, applies
// authentication, builds the body, records timings and runs scripts.
package httpx

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptrace"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"yourpm/core/brand"
	"yourpm/core/model"
	"yourpm/core/script"
	"yourpm/core/vars"
)

const (
	defaultTimeout = 30 * time.Second
	defaultMaxBody = 32 << 20 // 32 MiB
)

// Executor runs requests and caches OAuth2 tokens plus HTTP transports.
type Executor struct {
	mu            sync.Mutex
	tokens        map[string]tokenEntry
	transports    map[string]*http.Transport
	jar           http.CookieJar
	oauthProvider OAuthProvider
}

// OAuthProvider resolves an access token for the authorization-code grant.
// Those tokens are persisted by the application layer, so the engine delegates
// to it instead of performing the token request itself.
type OAuthProvider func(ctx context.Context, cfg model.OAuth2) (string, error)

// SetOAuthProvider installs the callback used for authorization-code tokens.
func (x *Executor) SetOAuthProvider(p OAuthProvider) {
	x.mu.Lock()
	x.oauthProvider = p
	x.mu.Unlock()
}

type tokenEntry struct {
	token   string
	expires time.Time
}

// New creates an executor with a shared cookie jar.
func New() *Executor {
	jar, _ := cookiejar.New(nil)
	return &Executor{
		tokens:     map[string]tokenEntry{},
		transports: map[string]*http.Transport{},
		jar:        jar,
	}
}

// Options tunes a single execution.
type Options struct {
	MaxBodyBytes int64
	UserAgent    string
	EnvName      string
}

// Send executes a request and always returns a Response. Transport errors are
// reported in Response.Error rather than as a Go error so the UI can display
// them next to the request.
func (x *Executor) Send(ctx context.Context, r model.RequestData, base map[string]string, opt Options) *model.Response {
	res := &model.Response{Headers: []model.KV{}, EnvUpdates: map[string]string{}}

	state := make(map[string]string, len(base)+8)
	for k, v := range base {
		state[k] = v
	}

	// Pre-request script: can define variables used by the request itself.
	if strings.TrimSpace(r.PreRequestScript) != "" {
		out := script.Run(r.PreRequestScript, &script.Context{
			Vars:    state,
			EnvName: opt.EnvName,
			Request: script.RequestInfo{Method: r.Method, URL: r.URL, Headers: r.Headers},
		})
		res.Console = append(res.Console, out.Console...)
		for k, v := range out.EnvUpdates {
			res.EnvUpdates[k] = v
		}
		res.EnvUnsets = append(res.EnvUnsets, out.EnvUnsets...)
		if out.Error != "" {
			res.Console = append(res.Console, "script error: "+out.Error)
		}
	}

	lookup := vars.MapLookup(state)
	iv := func(s string) string { return vars.Interpolate(s, lookup) }

	// Record the resolved URL even when the request never leaves the process,
	// so history and errors show what would have been called.
	res.URL = iv(r.URL)

	sent, err := x.do(ctx, r, iv, opt)
	if err != nil {
		res.Error = friendlyError(err)
		res.TimeMs = 0
		return res
	}

	res.Status = sent.status
	res.StatusText = sent.statusText
	res.Headers = sent.headers
	res.Body = sent.bodyText
	res.BodyBase64 = sent.bodyBase64
	res.Binary = sent.binary
	res.Size = sent.size
	res.Truncated = sent.truncated
	res.TimeMs = sent.timeMs
	res.Timings = sent.timings
	res.Cookies = sent.cookies
	res.URL = sent.url
	res.RequestSnapshot = sent.snapshot

	// Test script: sees the response and can assert on it.
	if strings.TrimSpace(r.TestScript) != "" {
		out := script.Run(r.TestScript, &script.Context{
			Vars:    state,
			EnvName: opt.EnvName,
			Request: script.RequestInfo{Method: r.Method, URL: r.URL, Headers: r.Headers},
			Response: &script.ResponseInfo{
				Code:    sent.status,
				Status:  sent.statusText,
				Body:    sent.bodyText,
				Headers: headerMap(sent.headers),
				TimeMs:  sent.timeMs,
			},
		})
		res.Console = append(res.Console, out.Console...)
		for k, v := range out.EnvUpdates {
			res.EnvUpdates[k] = v
		}
		res.EnvUnsets = append(res.EnvUnsets, out.EnvUnsets...)
		res.Tests = out.Tests
		if out.Error != "" {
			res.Console = append(res.Console, "script error: "+out.Error)
		}
	}

	return res
}

type sentResponse struct {
	status     int
	statusText string
	headers    []model.KV
	bodyText   string
	bodyBase64 string
	binary     bool
	size       int64
	truncated  bool
	timeMs     int64
	timings    model.Timings
	cookies    []model.KV
	url        string
	snapshot   *model.RequestData
}

func (x *Executor) do(ctx context.Context, r model.RequestData, iv func(string) string, opt Options) (*sentResponse, error) {
	method := strings.ToUpper(strings.TrimSpace(r.Method))
	if method == "" {
		method = http.MethodGet
	}

	rawURL := strings.TrimSpace(iv(r.URL))
	if rawURL == "" {
		return nil, fmt.Errorf("request URL is empty")
	}
	if !strings.Contains(rawURL, "://") {
		rawURL = "http://" + rawURL
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("invalid URL: %w", err)
	}
	if u.Host == "" {
		return nil, fmt.Errorf("invalid URL: missing host")
	}

	q := u.Query()
	for _, p := range r.Params {
		if !p.Enabled() {
			continue
		}
		q.Add(iv(p.Key), iv(p.Value))
	}

	// API key auth in the query string is applied before the URL is built.
	if r.Auth.Type == model.AuthAPIKey && r.Auth.In == "query" && r.Auth.Key != "" {
		q.Set(iv(r.Auth.Key), iv(r.Auth.Value))
	}
	u.RawQuery = q.Encode()

	body, contentType, bodyNote, err := buildBody(r.Body, iv)
	if err != nil {
		return nil, err
	}

	timeout := defaultTimeout
	if r.Settings.TimeoutMs > 0 {
		timeout = time.Duration(r.Settings.TimeoutMs) * time.Millisecond
	}
	reqCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, method, u.String(), body)
	if err != nil {
		return nil, err
	}

	// Resolved headers.
	for _, h := range r.Headers {
		if !h.Enabled() {
			continue
		}
		key, val := iv(h.Key), iv(h.Value)
		if strings.EqualFold(key, "host") {
			req.Host = val
			continue
		}
		if strings.EqualFold(key, "content-length") {
			continue
		}
		req.Header.Add(key, val)
	}
	if contentType != "" && req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", contentType)
	}
	if req.Header.Get("User-Agent") == "" {
		ua := opt.UserAgent
		if ua == "" {
			ua = brand.Name + "/" + brand.Version
		}
		req.Header.Set("User-Agent", ua)
	}

	if err := x.applyAuth(reqCtx, req, r, iv); err != nil {
		return nil, err
	}
	req.Header.Set("Accept-Encoding", "gzip, deflate")

	tr := x.transport(r.Settings)
	maxRedirects := r.Settings.MaxRedirects
	if maxRedirects <= 0 {
		maxRedirects = 10
	}
	redirects := int64(0)
	client := &http.Client{Transport: tr, Timeout: 0}
	if r.Settings.DisableCookies {
		client.Jar = nil
	} else {
		client.Jar = x.jar
	}
	client.CheckRedirect = func(next *http.Request, via []*http.Request) error {
		redirects = int64(len(via))
		if r.Settings.DisableRedirects {
			return http.ErrUseLastResponse
		}
		if len(via) >= maxRedirects {
			return fmt.Errorf("stopped after %d redirects", maxRedirects)
		}
		// Preserve auth headers on same-host redirects without scheme downgrade.
		if len(via) > 0 && next.URL.Host == via[0].URL.Host && (via[0].URL.Scheme != "https" || next.URL.Scheme == "https") {
			for k, vv := range via[0].Header {
				if next.Header.Get(k) == "" {
					next.Header[k] = vv
				}
			}
		}
		return nil
	}

	var (
		mu        sync.Mutex
		start     = time.Now()
		dnsStart  time.Time
		dnsDone   time.Time
		connStart time.Time
		connDone  time.Time
		tlsStart  time.Time
		tlsDone   time.Time
		firstByte time.Time
	)
	trace := &httptrace.ClientTrace{
		DNSStart:             func(httptrace.DNSStartInfo) { dnsStart = time.Now() },
		DNSDone:              func(httptrace.DNSDoneInfo) { dnsDone = time.Now() },
		ConnectStart:         func(string, string) { connStart = time.Now() },
		ConnectDone:          func(string, string, error) { connDone = time.Now() },
		TLSHandshakeStart:    func() { tlsStart = time.Now() },
		TLSHandshakeDone:     func(tls.ConnectionState, error) { tlsDone = time.Now() },
		GotFirstResponseByte: func() { mu.Lock(); firstByte = time.Now(); mu.Unlock() },
	}
	req = req.WithContext(httptrace.WithClientTrace(reqCtx, trace))

	resp, err := client.Do(req)
	total := time.Since(start)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	maxBody := opt.MaxBodyBytes
	if maxBody <= 0 {
		maxBody = defaultMaxBody
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}
	truncated := false
	if int64(len(raw)) > maxBody {
		raw = raw[:maxBody]
		truncated = true
	}

	mu.Lock()
	fb := firstByte
	mu.Unlock()
	if fb.IsZero() {
		fb = time.Now()
	}

	timings := model.Timings{Total: total.Milliseconds(), Redirects: redirects}
	if !dnsStart.IsZero() && !dnsDone.IsZero() {
		timings.DNS = dnsDone.Sub(dnsStart).Milliseconds()
	}
	if !connStart.IsZero() && !connDone.IsZero() {
		timings.Connect = connDone.Sub(connStart).Milliseconds()
	}
	if !tlsStart.IsZero() && !tlsDone.IsZero() {
		timings.TLS = tlsDone.Sub(tlsStart).Milliseconds()
	}
	timings.TTFB = fb.Sub(start).Milliseconds()
	if timings.TTFB < 0 {
		timings.TTFB = 0
	}

	ct := resp.Header.Get("Content-Type")
	binary := isBinary(ct, raw)
	bodyText, bodyB64 := "", ""
	if binary {
		bodyB64 = base64.StdEncoding.EncodeToString(raw)
	} else {
		bodyText = string(raw)
	}

	res := &sentResponse{
		status:     resp.StatusCode,
		statusText: http.StatusText(resp.StatusCode),
		headers:    flattenHeaders(resp.Header),
		bodyText:   bodyText,
		bodyBase64: bodyB64,
		binary:     binary,
		size:       int64(len(raw)),
		truncated:  truncated,
		timeMs:     total.Milliseconds(),
		timings:    timings,
		cookies:    cookies(resp),
		url:        resp.Request.URL.String(),
	}

	// Snapshot of what was actually sent, for history and codegen.
	snap := model.RequestData{
		Method:  method,
		URL:     u.String(),
		Headers: []model.KV{},
		Body:    model.Body{Mode: model.BodyNone},
	}
	keys := make([]string, 0, len(req.Header))
	for k := range req.Header {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		for _, v := range req.Header[k] {
			snap.Headers = append(snap.Headers, model.KV{Key: k, Value: v})
		}
	}
	if bodyNote != "" {
		snap.Body = r.Body
		snap.Body.Raw = bodyNote
	}
	res.snapshot = &snap
	return res, nil
}

func (x *Executor) transport(s model.RequestSettings) *http.Transport {
	key := fmt.Sprintf("%v|%s", s.SkipTLSVerify, s.Proxy)
	x.mu.Lock()
	defer x.mu.Unlock()
	if tr, ok := x.transports[key]; ok {
		return tr
	}
	tr := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   10 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          32,
		MaxIdleConnsPerHost:   8,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		ResponseHeaderTimeout: 0,
	}
	if s.Proxy != "" {
		if pu, err := url.Parse(s.Proxy); err == nil {
			tr.Proxy = http.ProxyURL(pu)
		}
	}
	if s.SkipTLSVerify {
		tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // explicit user opt-in
	}
	x.transports[key] = tr
	return tr
}

func buildBody(b model.Body, iv func(string) string) (io.Reader, string, string, error) {
	switch b.Mode {
	case "", model.BodyNone:
		return nil, "", "", nil
	case model.BodyRaw:
		raw := iv(b.Raw)
		ct := ""
		switch b.Lang {
		case "json":
			ct = "application/json"
		case "xml":
			ct = "application/xml"
		case "html":
			ct = "text/html"
		case "javascript":
			ct = "application/javascript"
		case "text":
			ct = "text/plain"
		}
		return strings.NewReader(raw), ct, raw, nil
	case model.BodyURLEncoded:
		vals := url.Values{}
		for _, kv := range b.URLEncoded {
			if kv.Enabled() {
				vals.Add(iv(kv.Key), iv(kv.Value))
			}
		}
		encoded := vals.Encode()
		return strings.NewReader(encoded), "application/x-www-form-urlencoded", encoded, nil
	case model.BodyFormData:
		var buf bytes.Buffer
		w := multipart.NewWriter(&buf)
		for _, kv := range b.FormData {
			if !kv.Enabled() {
				continue
			}
			key := iv(kv.Key)
			if kv.Type == "file" {
				path := iv(kv.Path)
				if path == "" {
					continue
				}
				f, err := os.Open(path)
				if err != nil {
					return nil, "", "", fmt.Errorf("form-data file %q: %w", filepath.Base(path), err)
				}
				part, err := w.CreateFormFile(key, filepath.Base(path))
				if err == nil {
					_, err = io.Copy(part, f)
				}
				f.Close()
				if err != nil {
					return nil, "", "", fmt.Errorf("form-data file %q: %w", filepath.Base(path), err)
				}
				continue
			}
			if err := w.WriteField(key, iv(kv.Value)); err != nil {
				return nil, "", "", err
			}
		}
		if err := w.Close(); err != nil {
			return nil, "", "", err
		}
		return bytes.NewReader(buf.Bytes()), w.FormDataContentType(), "[multipart/form-data body]", nil
	case model.BodyBinary:
		path := iv(b.FilePath)
		if path == "" {
			return nil, "", "", fmt.Errorf("binary body: no file selected")
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, "", "", fmt.Errorf("binary body: %w", err)
		}
		return bytes.NewReader(data), "application/octet-stream", "[binary file: " + filepath.Base(path) + "]", nil
	}
	return nil, "", "", fmt.Errorf("unknown body mode %q", b.Mode)
}

func isBinary(contentType string, body []byte) bool {
	ct := strings.ToLower(contentType)
	for _, prefix := range []string{"image/", "audio/", "video/", "application/octet-stream", "application/pdf", "application/zip", "font/"} {
		if strings.HasPrefix(ct, prefix) {
			return true
		}
	}
	if strings.Contains(ct, "application/json") || strings.Contains(ct, "text/") ||
		strings.Contains(ct, "xml") || strings.Contains(ct, "javascript") {
		return false
	}
	if len(body) == 0 {
		return false
	}
	if !utf8.Valid(body) {
		return true
	}
	sample := body
	if len(sample) > 1024 {
		sample = sample[:1024]
	}
	if bytes.IndexByte(sample, 0) >= 0 {
		return true
	}
	return false
}

func flattenHeaders(h http.Header) []model.KV {
	keys := make([]string, 0, len(h))
	for k := range h {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]model.KV, 0, len(keys))
	for _, k := range keys {
		for _, v := range h[k] {
			out = append(out, model.KV{Key: k, Value: v})
		}
	}
	return out
}

func headerMap(h []model.KV) map[string]string {
	out := map[string]string{}
	for _, kv := range h {
		out[kv.Key] = kv.Value
	}
	return out
}

func cookies(resp *http.Response) []model.KV {
	out := []model.KV{}
	for _, c := range resp.Cookies() {
		out = append(out, model.KV{Key: c.Name, Value: c.Value})
	}
	return out
}

func friendlyError(err error) string {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "context deadline exceeded"):
		return "request timed out"
	case strings.Contains(msg, "no such host"):
		return "could not resolve host: " + msg
	}
	if ue, ok := err.(*url.Error); ok {
		if ue.Timeout() {
			return "request timed out"
		}
		return ue.Err.Error()
	}
	return msg
}

// tokenCacheKey builds a stable, non-reversible cache key for OAuth2 config.
func tokenCacheKey(cfg model.OAuth2) string {
	sum := sha256.Sum256([]byte(cfg.AccessTokenURL + "|" + cfg.ClientID + "|" + cfg.Scope + "|" +
		cfg.GrantType + "|" + cfg.Username + "|" + cfg.Password + "|" + cfg.ClientSecret))
	return hex.EncodeToString(sum[:])
}

func prettyJSON(s string) string {
	var buf bytes.Buffer
	if err := json.Indent(&buf, []byte(s), "", "  "); err != nil {
		return s
	}
	return buf.String()
}
