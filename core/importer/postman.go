// Package importer reads third-party API client formats into YourPM's
// entity model.
package importer

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"yourpm/core/model"
)

// Node is an imported tree node.
type Node struct {
	Kind     string
	Name     string
	Data     json.RawMessage
	Children []*Node
}

// ParsePostmanCollection imports a Postman Collection v2.1 document.
func ParsePostmanCollection(raw []byte) (*Node, error) {
	var doc postmanCollection
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("invalid Postman collection: %w", err)
	}
	if doc.Info.Name == "" && len(doc.Item) == 0 {
		return nil, fmt.Errorf("this does not look like a Postman collection")
	}
	root := &Node{Kind: model.KindCollection, Name: fallback(doc.Info.Name, "Imported collection")}
	var vars []model.KV
	for _, v := range doc.Variable {
		vars = append(vars, model.KV{Key: v.Key, Value: v.Value, Disabled: v.Disabled})
	}
	data := model.CollectionData{
		Description: textOf(doc.Info.Description),
		Variables:   vars,
		Auth:        parseAuth(doc.Auth),
	}
	data.PreRequestScript, data.TestScript = parseEvents(doc.Event)
	root.Data = marshal(data)
	for i := range doc.Item {
		root.Children = append(root.Children, parseItem(&doc.Item[i]))
	}
	return root, nil
}

// ParsePostmanEnvironment imports a Postman environment file.
func ParsePostmanEnvironment(raw []byte) (*Node, error) {
	var doc struct {
		Name   string `json:"name"`
		Values []struct {
			Key     string `json:"key"`
			Value   string `json:"value"`
			Enabled *bool  `json:"enabled"`
			Type    string `json:"type"`
		} `json:"values"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("invalid Postman environment: %w", err)
	}
	env := model.EnvironmentData{}
	for _, v := range doc.Values {
		disabled := v.Enabled != nil && !*v.Enabled
		env.Variables = append(env.Variables, model.KV{
			Key:      v.Key,
			Value:    v.Value,
			Disabled: disabled,
			Secret:   v.Type == "secret",
		})
	}
	return &Node{Kind: model.KindEnvironment, Name: fallback(doc.Name, "Imported environment"), Data: marshal(env)}, nil
}

func parseItem(it *postmanItem) *Node {
	if it.Request == nil {
		node := &Node{Kind: model.KindFolder, Name: fallback(it.Name, "Folder")}
		fd := model.FolderData{
			Description: textOf(it.Description),
			Auth:        parseAuth(it.Auth),
		}
		if fd.Auth.Type == "" {
			fd.Auth = model.Auth{Type: model.AuthInherit}
		}
		fd.PreRequestScript, fd.TestScript = parseEvents(it.Event)
		node.Data = marshal(fd)
		for i := range it.Item {
			node.Children = append(node.Children, parseItem(&it.Item[i]))
		}
		return node
	}
	req := parseRequest(it.Request)
	req.Description = textOf(it.Description)
	req.PreRequestScript, req.TestScript = parseEvents(it.Event)
	node := &Node{Kind: model.KindRequest, Name: fallback(it.Name, "Request"), Data: marshal(req)}
	// Nested items under a request are rare but valid; keep them.
	for i := range it.Item {
		node.Children = append(node.Children, parseItem(&it.Item[i]))
	}
	return node
}

func parseRequest(r *postmanRequest) model.RequestData {
	out := model.RequestData{
		Method: strings.ToUpper(fallback(r.Method, "GET")),
		Body:   model.Body{Mode: model.BodyNone, Lang: "json"},
		Auth:   parseAuth(r.Auth),
	}
	if out.Auth.Type == "" {
		out.Auth = model.Auth{Type: model.AuthInherit}
	}

	switch u := r.URL.(type) {
	case string:
		out.URL = u
	case map[string]any:
		out.URL = stringOf(u["raw"])
		if out.URL == "" {
			out.URL = structuredURL(u)
		}
	}
	// Move query parameters out of the raw URL so the engine does not
	// duplicate them when it appends enabled params.
	if parsed, err := url.Parse(out.URL); err == nil {
		if parsed.RawQuery != "" {
			q := parsed.Query()
			out.URL = strings.TrimSuffix(out.URL, "?"+parsed.RawQuery)
			for k, vals := range q {
				for _, v := range vals {
					out.Params = append(out.Params, model.KV{Key: k, Value: v})
				}
			}
		}
	}
	if u, ok := r.URL.(map[string]any); ok {
		if qs, ok := u["query"].([]any); ok {
			for _, item := range qs {
				m, _ := item.(map[string]any)
				if m == nil {
					continue
				}
				out.Params = append(out.Params, model.KV{
					Key:      stringOf(m["key"]),
					Value:    stringOf(m["value"]),
					Disabled: boolOf(m["disabled"]),
				})
			}
		}
	}

	out.Params = dedupeParams(out.Params)

	for _, h := range r.Header {
		if h.Key == "" {
			continue
		}
		out.Headers = append(out.Headers, model.KV{
			Key: h.Key, Value: h.Value,
			Disabled:    h.Disabled,
			Description: h.Description,
		})
	}

	if r.Body != nil {
		switch r.Body.Mode {
		case "raw":
			out.Body.Mode = model.BodyRaw
			out.Body.Raw = r.Body.Raw
			out.Body.Lang = "text"
			if r.Body.Options != nil && r.Body.Options.Raw != nil && r.Body.Options.Raw.Language != "" {
				out.Body.Lang = r.Body.Options.Raw.Language
			}
		case "urlencoded":
			out.Body.Mode = model.BodyURLEncoded
			for _, f := range r.Body.URLEncoded {
				out.Body.URLEncoded = append(out.Body.URLEncoded, model.KV{Key: f.Key, Value: f.Value, Disabled: f.Disabled})
			}
		case "formdata":
			out.Body.Mode = model.BodyFormData
			for _, f := range r.Body.FormData {
				kv := model.KV{Key: f.Key, Value: f.Value, Disabled: f.Disabled, Type: f.Type}
				if len(f.Src) > 0 {
					kv.Path = f.Src[0]
					kv.Type = "file"
				}
				out.Body.FormData = append(out.Body.FormData, kv)
			}
		case "file":
			out.Body.Mode = model.BodyBinary
			if r.Body.File != nil && len(r.Body.File.Src) > 0 {
				out.Body.FilePath = r.Body.File.Src[0]
			}
		}
	}
	return out
}

func parseAuth(a *postmanAuth) model.Auth {
	if a == nil || a.Type == "" || a.Type == "noauth" {
		return model.Auth{Type: model.AuthNone}
	}
	get := func(key string) string {
		for _, kv := range a.Bearer {
			if kv.Key == key {
				return kv.Value
			}
		}
		for _, kv := range a.Values {
			if kv.Key == key {
				return kv.Value
			}
		}
		return ""
	}
	switch a.Type {
	case "bearer":
		return model.Auth{Type: model.AuthBearer, Token: get("token")}
	case "basic":
		return model.Auth{Type: model.AuthBasic, Username: get("username"), Password: get("password")}
	case "apikey":
		in := get("in")
		if in == "" {
			in = "header"
		}
		return model.Auth{Type: model.AuthAPIKey, Key: get("key"), Value: get("value"), In: in}
	case "oauth2":
		grant := firstNonEmpty(get("grant_type"), get("grantType"), model.GrantClientCredentials)
		return model.Auth{
			Type: model.AuthOAuth2,
			OAuth2: &model.OAuth2{
				GrantType:        grant,
				AuthorizationURL: firstNonEmpty(get("authUrl"), get("authorizationUrl")),
				AccessTokenURL:   firstNonEmpty(get("accessTokenUrl"), get("tokenUrl")),
				RedirectURL:      firstNonEmpty(get("redirectUrl"), get("redirect_uri")),
				ClientID:         get("clientId"),
				ClientSecret:     get("clientSecret"),
				Scope:            get("scope"),
				ClientAuth:       get("clientAuth"),
				TokenName:        get("tokenName"),
				HeaderPrefix:     get("headerPrefix"),
			},
		}
	}
	return model.Auth{Type: model.AuthNone}
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func marshal(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}

func fallback(s, def string) string {
	if strings.TrimSpace(s) == "" {
		return def
	}
	return s
}

func stringOf(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case float64:
		return fmt.Sprintf("%v", x)
	case json.Number:
		return x.String()
	}
	return ""
}

func boolOf(v any) bool {
	b, _ := v.(bool)
	return b
}

func parseEvents(events []postmanEvent) (pre, test string) {
	for _, ev := range events {
		script := strings.Join(ev.Script.Exec, "\n")
		switch ev.Listen {
		case "prerequest":
			pre = script
		case "test":
			test = script
		}
	}
	return pre, test
}

func structuredURL(u map[string]any) string {
	host := joinParts(u["host"], ".")
	if host == "" {
		return ""
	}
	proto := stringOf(u["protocol"])
	if proto == "" {
		proto = "https"
	}
	path := joinParts(u["path"], "/")
	if path != "" {
		return proto + "://" + host + "/" + strings.TrimPrefix(path, "/")
	}
	return proto + "://" + host
}

func joinParts(v any, sep string) string {
	switch x := v.(type) {
	case string:
		return x
	case []any:
		var parts []string
		for _, item := range x {
			if s := stringOf(item); s != "" {
				parts = append(parts, s)
			}
		}
		return strings.Join(parts, sep)
	}
	return ""
}

func textOf(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case map[string]any:
		return stringOf(x["content"])
	}
	return ""
}

/* ---------------------------- wire types ---------------------------- */

type postmanCollection struct {
	Info struct {
		Name        string `json:"name"`
		Description any    `json:"description"`
		Schema      string `json:"schema"`
	} `json:"info"`
	Item     []postmanItem `json:"item"`
	Variable []struct {
		Key      string `json:"key"`
		Value    string `json:"value"`
		Disabled bool   `json:"disabled"`
	} `json:"variable"`
	Auth  *postmanAuth   `json:"auth"`
	Event []postmanEvent `json:"event"`
}

type postmanItem struct {
	Name        string          `json:"name"`
	Description any             `json:"description"`
	Item        []postmanItem   `json:"item"`
	Request     *postmanRequest `json:"request"`
	Auth        *postmanAuth    `json:"auth"`
	Event       []postmanEvent  `json:"event"`
}

// stringList unmarshals either a JSON string or a JSON array of strings.
type stringList []string

func (s *stringList) UnmarshalJSON(b []byte) error {
	if string(b) == "null" {
		*s = nil
		return nil
	}
	var single string
	if err := json.Unmarshal(b, &single); err == nil {
		if single != "" {
			*s = []string{single}
		} else {
			*s = nil
		}
		return nil
	}
	var many []string
	if err := json.Unmarshal(b, &many); err != nil {
		return err
	}
	*s = many
	return nil
}

type postmanEvent struct {
	Listen string `json:"listen"`
	Script struct {
		Exec stringList `json:"exec"`
	} `json:"script"`
}

type postmanRequest struct {
	Method      string       `json:"method"`
	URL         any          `json:"url"`
	Header      []postmanKV  `json:"header"`
	Body        *postmanBody `json:"body"`
	Auth        *postmanAuth `json:"auth"`
	Description any          `json:"description"`
}

type postmanKV struct {
	Key         string     `json:"key"`
	Value       string     `json:"value"`
	Disabled    bool       `json:"disabled"`
	Description string     `json:"description"`
	Type        string     `json:"type"`
	Src         stringList `json:"src"`
}

type postmanBody struct {
	Mode       string      `json:"mode"`
	Raw        string      `json:"raw"`
	URLEncoded []postmanKV `json:"urlencoded"`
	FormData   []postmanKV `json:"formdata"`
	File       *struct {
		Src stringList `json:"src"`
	} `json:"file"`
	Options *struct {
		Raw *struct {
			Language string `json:"language"`
		} `json:"raw"`
	} `json:"options"`
}

type postmanAuth struct {
	Type   string      `json:"type"`
	Bearer []postmanKV `json:"bearer"`
	Values []postmanKV `json:"-"`
}

// UnmarshalJSON flattens the Postman auth type-keyed arrays (bearer, basic,
// apikey, ...) into a single Values slice.
func (a *postmanAuth) UnmarshalJSON(b []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	_ = json.Unmarshal(raw["type"], &a.Type)
	for key, val := range raw {
		if key == "type" {
			continue
		}
		var kvs []postmanKV
		if err := json.Unmarshal(val, &kvs); err == nil {
			a.Values = append(a.Values, kvs...)
			if key == "bearer" {
				a.Bearer = kvs
			}
		}
	}
	return nil
}

// dedupeParams removes duplicate query entries. Postman exports often carry
// the same parameters both in the raw URL and in the structured query array;
// sending both would duplicate them on the wire.
func dedupeParams(list []model.KV) []model.KV {
	if len(list) < 2 {
		return list
	}
	seen := make(map[string]bool, len(list))
	out := list[:0]
	for _, kv := range list {
		k := kv.Key + "\x00" + kv.Value
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, kv)
	}
	return out
}
