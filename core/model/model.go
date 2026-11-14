// Package model defines the YourPM domain types shared by the desktop
// app, the sync client and the sync server.
package model

import "encoding/json"

// Entity kinds.
const (
	KindCollection  = "collection"
	KindFolder      = "folder"
	KindRequest     = "request"
	KindEnvironment = "environment"
)

// KV is a key/value pair used for headers, query params, form fields,
// variables and cookies. Disabled pairs stay in the model but are skipped
// when a request is built.
type KV struct {
	Key         string `json:"key"`
	Value       string `json:"value"`
	Description string `json:"description,omitempty"`
	Disabled    bool   `json:"disabled,omitempty"`
	Secret      bool   `json:"secret,omitempty"`
	// Type is "text" or "file" for multipart form fields.
	Type string `json:"type,omitempty"`
	// Path is the local file path for file fields.
	Path string `json:"path,omitempty"`
}

// Enabled reports whether the pair takes part in a request.
func (kv KV) Enabled() bool { return !kv.Disabled && kv.Key != "" }

// Body modes.
const (
	BodyNone       = "none"
	BodyRaw        = "raw"
	BodyFormData   = "formdata"
	BodyURLEncoded = "urlencoded"
	BodyBinary     = "binary"
)

// Body describes a request body.
type Body struct {
	Mode       string `json:"mode"`
	Raw        string `json:"raw,omitempty"`
	Lang       string `json:"lang,omitempty"` // json | xml | html | text | javascript
	FormData   []KV   `json:"formData,omitempty"`
	URLEncoded []KV   `json:"urlencoded,omitempty"`
	FilePath   string `json:"filePath,omitempty"`
}

// Auth types.
const (
	AuthNone    = "none"
	AuthInherit = "inherit"
	AuthBearer  = "bearer"
	AuthBasic   = "basic"
	AuthAPIKey  = "apikey"
	AuthOAuth2  = "oauth2"
)

// Auth describes request authentication.
type Auth struct {
	Type     string  `json:"type"`
	Token    string  `json:"token,omitempty"`
	Username string  `json:"username,omitempty"`
	Password string  `json:"password,omitempty"`
	Key      string  `json:"key,omitempty"`
	Value    string  `json:"value,omitempty"`
	In       string  `json:"in,omitempty"` // header | query
	OAuth2   *OAuth2 `json:"oauth2,omitempty"`
}

// OAuth2 grant types.
const (
	GrantClientCredentials = "client_credentials"
	GrantPassword          = "password"
	GrantAuthorizationCode = "authorization_code"
)

// OAuth2 holds the OAuth 2.0 configuration. The authorization-code grant runs
// an interactive browser flow from the desktop app or `yourpm authorize`; the
// client-credentials and password grants fetch a token directly from the
// request path.
type OAuth2 struct {
	GrantType        string `json:"grantType"` // client_credentials | password | authorization_code
	AuthorizationURL string `json:"authorizationUrl,omitempty"`
	AccessTokenURL   string `json:"accessTokenUrl"`
	RedirectURL      string `json:"redirectUrl,omitempty"` // empty = automatic loopback listener
	ClientID         string `json:"clientId"`
	ClientSecret     string `json:"clientSecret"`
	Scope            string `json:"scope,omitempty"`
	Username         string `json:"username,omitempty"`
	Password         string `json:"password,omitempty"`
	ClientAuth       string `json:"clientAuth,omitempty"` // header | body
	TokenName        string `json:"tokenName,omitempty"`
	HeaderPrefix     string `json:"headerPrefix,omitempty"`
}

// RequestSettings controls transport behaviour.
type RequestSettings struct {
	TimeoutMs        int    `json:"timeoutMs,omitempty"`
	DisableRedirects bool   `json:"disableRedirects,omitempty"`
	MaxRedirects     int    `json:"maxRedirects,omitempty"`
	SkipTLSVerify    bool   `json:"skipTlsVerify,omitempty"`
	DisableCookies   bool   `json:"disableCookies,omitempty"`
	Proxy            string `json:"proxy,omitempty"`
}

// RequestData is the payload of a request entity.
type RequestData struct {
	Method           string          `json:"method"`
	URL              string          `json:"url"`
	Params           []KV            `json:"params,omitempty"`
	Headers          []KV            `json:"headers,omitempty"`
	Body             Body            `json:"body"`
	Auth             Auth            `json:"auth"`
	PreRequestScript string          `json:"preRequestScript,omitempty"`
	TestScript       string          `json:"testScript,omitempty"`
	Settings         RequestSettings `json:"settings"`
	Description      string          `json:"description,omitempty"`
}

// CollectionData is the payload of a collection entity.
type CollectionData struct {
	Description string `json:"description,omitempty"`
	Variables   []KV   `json:"variables,omitempty"`
	Auth        Auth   `json:"auth"`
	// PreRequestScript and TestScript run for every request in the collection.
	PreRequestScript string `json:"preRequestScript,omitempty"`
	TestScript       string `json:"testScript,omitempty"`
}

// FolderData is the payload of a folder entity.
type FolderData struct {
	Description      string `json:"description,omitempty"`
	Auth             Auth   `json:"auth"`
	PreRequestScript string `json:"preRequestScript,omitempty"`
	TestScript       string `json:"testScript,omitempty"`
}

// EnvironmentData is the payload of an environment entity.
type EnvironmentData struct {
	Variables []KV   `json:"variables,omitempty"`
	Color     string `json:"color,omitempty"`
}

// EntityPayload is the content that gets encrypted for a synced entity.
// Nothing in here is visible to the sync server when end-to-end
// encryption is enabled.
type EntityPayload struct {
	Name     string          `json:"name"`
	ParentID string          `json:"parentId,omitempty"`
	Position float64         `json:"position"`
	Data     json.RawMessage `json:"data,omitempty"`
}

// Entity is a local record: metadata plus an encrypted or plaintext blob.
type Entity struct {
	ID        string          `json:"id"`
	Workspace string          `json:"workspaceId"`
	Kind      string          `json:"kind"`
	Name      string          `json:"name"`
	ParentID  string          `json:"parentId,omitempty"`
	Position  float64         `json:"position"`
	Data      json.RawMessage `json:"data,omitempty"`
	Deleted   bool            `json:"deleted"`
	CreatedAt int64           `json:"createdAt"`
	UpdatedAt int64           `json:"updatedAt"`
	// Rev is the server revision this record was last synced at; 0 means
	// it has never been synced.
	Rev int64 `json:"rev"`
	// Dirty marks records with local changes that still need pushing.
	Dirty bool `json:"dirty"`
	// Sealed reports whether the blob is encrypted with the vault key.
	Sealed bool `json:"sealed"`
}

// Payload converts an entity to its sync payload.
func (e *Entity) Payload() EntityPayload {
	return EntityPayload{Name: e.Name, ParentID: e.ParentID, Position: e.Position, Data: e.Data}
}

// ApplyPayload copies payload fields onto an entity.
func (e *Entity) ApplyPayload(p EntityPayload) {
	e.Name = p.Name
	e.ParentID = p.ParentID
	e.Position = p.Position
	e.Data = p.Data
}

// Workspace is a top-level container, optionally linked to a sync server.
type Workspace struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	CreatedAt   int64  `json:"createdAt"`
	UpdatedAt   int64  `json:"updatedAt"`
	Sync        Sync   `json:"sync"`
}

// Sync holds per-workspace sync linkage.
type Sync struct {
	Server     string `json:"server,omitempty"`
	RemoteID   string `json:"remoteId,omitempty"`
	Encrypted  bool   `json:"encrypted"`
	Cursor     int64  `json:"cursor"`
	LastSyncAt int64  `json:"lastSyncAt,omitempty"`
	LastError  string `json:"lastError,omitempty"`
}

// Linked reports whether the workspace is connected to a sync server.
func (w *Workspace) Linked() bool { return w.Sync.Server != "" && w.Sync.RemoteID != "" }

// HistoryEntry is a locally stored request/response pair.
type HistoryEntry struct {
	ID           string          `json:"id"`
	Workspace    string          `json:"workspaceId"`
	CollectionID string          `json:"collectionId,omitempty"`
	RequestID    string          `json:"requestId,omitempty"`
	Method       string          `json:"method"`
	URL          string          `json:"url"`
	Status       int             `json:"status"`
	TimeMs       int64           `json:"timeMs"`
	Size         int             `json:"size"`
	At           int64           `json:"at"`
	Request      json.RawMessage `json:"request,omitempty"`
	Response     json.RawMessage `json:"response,omitempty"`
	Sealed       bool            `json:"sealed"`
}

// TestResult is the outcome of one pm.test() call.
type TestResult struct {
	Name   string `json:"name"`
	Passed bool   `json:"passed"`
	Error  string `json:"error,omitempty"`
}

// Timings breaks down where request time was spent, in milliseconds.
type Timings struct {
	DNS       int64 `json:"dns"`
	Connect   int64 `json:"connect"`
	TLS       int64 `json:"tls"`
	TTFB      int64 `json:"ttfb"`
	Total     int64 `json:"total"`
	Redirects int64 `json:"redirects"`
}

// Response is the result of executing a request.
type Response struct {
	Status     int    `json:"status"`
	StatusText string `json:"statusText"`
	Headers    []KV   `json:"headers"`
	Body       string `json:"body"`
	BodyBase64 string `json:"bodyBase64,omitempty"`
	Binary     bool   `json:"binary"`
	// Truncated is set when the body exceeded the capture limit.
	Truncated  bool              `json:"truncated,omitempty"`
	Size       int64             `json:"size"`
	TimeMs     int64             `json:"timeMs"`
	Timings    Timings           `json:"timings"`
	Cookies    []KV              `json:"cookies"`
	Tests      []TestResult      `json:"tests"`
	Console    []string          `json:"console"`
	EnvUpdates map[string]string `json:"envUpdates,omitempty"`
	EnvUnsets  []string          `json:"envUnsets,omitempty"`
	Error      string            `json:"error,omitempty"`
	URL        string            `json:"url,omitempty"`
	// RequestSnapshot is what was actually sent, for history and codegen.
	RequestSnapshot *RequestData `json:"requestSnapshot,omitempty"`
}

// RemoteWorkspace is a workspace as advertised by a sync server.
type RemoteWorkspace struct {
	ID         string `json:"id"`
	Role       string `json:"role,omitempty"`
	KeyScope   string `json:"keyScope,omitempty"`
	Name       string `json:"name"`
	Encrypted  bool   `json:"encrypted"`
	KDFSalt    string `json:"kdfSalt,omitempty"`
	WrappedKey string `json:"wrappedKey,omitempty"`
	CreatedAt  int64  `json:"createdAt"`
}

type WorkspaceMember struct {
	ID    string `json:"id"`
	Email string `json:"email"`
	Role  string `json:"role"`
}

type WorkspaceInvitation struct {
	ID            string `json:"id"`
	WorkspaceID   string `json:"workspaceId"`
	WorkspaceName string `json:"workspaceName"`
	Email         string `json:"email"`
	Role          string `json:"role"`
	Encrypted     bool   `json:"encrypted"`
	KDFSalt       string `json:"kdfSalt,omitempty"`
	WrappedKey    string `json:"wrappedKey,omitempty"`
	ExpiresAt     int64  `json:"expiresAt"`
}

type WorkspaceTeam struct {
	Role        string                `json:"role"`
	Members     []WorkspaceMember     `json:"members"`
	Invitations []WorkspaceInvitation `json:"invitations"`
}
