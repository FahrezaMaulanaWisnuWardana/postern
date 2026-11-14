// Package exporter converts YourPM entities and requests into shareable
// formats: Postman collections, environments and cURL commands.
package exporter

import (
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strings"

	"yourpm/core/model"
	"yourpm/core/vars"
)

// BuildPostman renders a collection and everything below it as a Postman
// Collection v2.1 document.
func BuildPostman(collection *model.Entity, ents []*model.Entity) ([]byte, error) {
	var col model.CollectionData
	if len(collection.Data) > 0 {
		_ = json.Unmarshal(collection.Data, &col)
	}
	byParent := map[string][]*model.Entity{}
	for _, e := range ents {
		if e.Deleted || e.ID == collection.ID {
			continue
		}
		byParent[e.ParentID] = append(byParent[e.ParentID], e)
	}
	for parent := range byParent {
		sort.SliceStable(byParent[parent], func(i, j int) bool {
			a, b := byParent[parent][i], byParent[parent][j]
			if a.Position == b.Position {
				return a.Name < b.Name
			}
			return a.Position < b.Position
		})
	}

	doc := map[string]any{
		"info": map[string]any{
			"name":        collection.Name,
			"description": col.Description,
			"schema":      "https://schema.getpostman.com/json/collection/v2.1.0/collection.json",
		},
		"item":     items(collection.ID, byParent),
		"variable": variables(col.Variables),
	}
	if col.Auth.Type != "" && col.Auth.Type != model.AuthNone {
		doc["auth"] = auth(col.Auth)
	}
	if evs := eventsFor(col.PreRequestScript, col.TestScript); len(evs) > 0 {
		doc["event"] = evs
	}
	return json.MarshalIndent(doc, "", "  ")
}

func items(parent string, byParent map[string][]*model.Entity) []any {
	var out []any
	for _, e := range byParent[parent] {
		switch e.Kind {
		case model.KindFolder:
			var f model.FolderData
			_ = json.Unmarshal(e.Data, &f)
			node := map[string]any{
				"name": e.Name,
				"item": items(e.ID, byParent),
			}
			if f.Description != "" {
				node["description"] = f.Description
			}
			if f.Auth.Type != "" && f.Auth.Type != model.AuthNone && f.Auth.Type != model.AuthInherit {
				node["auth"] = auth(f.Auth)
			}
			if evs := eventsFor(f.PreRequestScript, f.TestScript); len(evs) > 0 {
				node["event"] = evs
			}
			out = append(out, node)
		case model.KindRequest:
			var r model.RequestData
			_ = json.Unmarshal(e.Data, &r)
			out = append(out, requestItem(e.Name, r, byParent[e.ID]))
		}
	}
	return out
}

func requestItem(name string, r model.RequestData, children []*model.Entity) map[string]any {
	rawURL := r.URL
	if len(r.Params) > 0 {
		if u, err := url.Parse(rawURL); err == nil {
			q := u.Query()
			for _, p := range r.Params {
				if p.Key != "" && !p.Disabled {
					q.Set(p.Key, p.Value)
				}
			}
			u.RawQuery = q.Encode()
			rawURL = u.String()
		}
	}
	req := map[string]any{
		"method": r.Method,
		"header": headers(r.Headers),
		"url":    map[string]any{"raw": rawURL},
	}
	if r.Body.Mode != "" && r.Body.Mode != model.BodyNone {
		req["body"] = body(r.Body)
	}
	if r.Auth.Type != "" && r.Auth.Type != model.AuthNone && r.Auth.Type != model.AuthInherit {
		req["auth"] = auth(r.Auth)
	}
	if r.Description != "" {
		req["description"] = r.Description
	}
	item := map[string]any{"name": name, "request": req}
	if evs := eventsFor(r.PreRequestScript, r.TestScript); len(evs) > 0 {
		item["event"] = evs
	}
	if len(children) > 0 {
		item["item"] = items("", map[string][]*model.Entity{"": children})
	}
	return item
}

func eventsFor(pre, test string) []any {
	var events []any
	if pre != "" {
		events = append(events, map[string]any{
			"listen": "prerequest",
			"script": map[string]any{"exec": strings.Split(pre, "\n"), "type": "text/javascript"},
		})
	}
	if test != "" {
		events = append(events, map[string]any{
			"listen": "test",
			"script": map[string]any{"exec": strings.Split(test, "\n"), "type": "text/javascript"},
		})
	}
	return events
}

func headers(kvs []model.KV) []any {
	out := []any{}
	for _, h := range kvs {
		if h.Key == "" {
			continue
		}
		out = append(out, map[string]any{"key": h.Key, "value": h.Value, "disabled": h.Disabled})
	}
	return out
}

func variables(kvs []model.KV) []any {
	out := []any{}
	for _, v := range kvs {
		if v.Key == "" {
			continue
		}
		item := map[string]any{"key": v.Key, "value": v.Value, "disabled": v.Disabled}
		if v.Secret {
			item["type"] = "secret"
		}
		out = append(out, item)
	}
	return out
}

func body(b model.Body) map[string]any {
	switch b.Mode {
	case model.BodyRaw:
		return map[string]any{
			"mode":    "raw",
			"raw":     b.Raw,
			"options": map[string]any{"raw": map[string]any{"language": b.Lang}},
		}
	case model.BodyURLEncoded:
		list := []any{}
		for _, kv := range b.URLEncoded {
			list = append(list, map[string]any{"key": kv.Key, "value": kv.Value, "disabled": kv.Disabled})
		}
		return map[string]any{"mode": "urlencoded", "urlencoded": list}
	case model.BodyFormData:
		list := []any{}
		for _, kv := range b.FormData {
			item := map[string]any{"key": kv.Key, "value": kv.Value, "type": kv.Type, "disabled": kv.Disabled}
			if kv.Type == "file" {
				item["src"] = kv.Path
			}
			list = append(list, item)
		}
		return map[string]any{"mode": "formdata", "formdata": list}
	case model.BodyBinary:
		return map[string]any{"mode": "file", "file": map[string]any{"src": b.FilePath}}
	}
	return map[string]any{"mode": "none"}
}

func auth(a model.Auth) map[string]any {
	kv := func(pairs ...string) []any {
		out := []any{}
		for i := 0; i+1 < len(pairs); i += 2 {
			out = append(out, map[string]any{"key": pairs[i], "value": pairs[i+1], "type": "string"})
		}
		return out
	}
	switch a.Type {
	case model.AuthBearer:
		return map[string]any{"type": "bearer", "bearer": kv("token", a.Token)}
	case model.AuthBasic:
		return map[string]any{"type": "basic", "basic": kv("username", a.Username, "password", a.Password)}
	case model.AuthAPIKey:
		return map[string]any{"type": "apikey", "apikey": kv("key", a.Key, "value", a.Value, "in", a.In)}
	case model.AuthOAuth2:
		o := a.OAuth2
		if o == nil {
			o = &model.OAuth2{}
		}
		grant := o.GrantType
		if grant == "" {
			grant = model.GrantClientCredentials
		}
		return map[string]any{"type": "oauth2", "oauth2": kv(
			"authUrl", o.AuthorizationURL,
			"accessTokenUrl", o.AccessTokenURL,
			"redirectUrl", o.RedirectURL,
			"clientId", o.ClientID,
			"clientSecret", o.ClientSecret,
			"scope", o.Scope,
			"grant_type", grant,
			"clientAuth", o.ClientAuth,
		)}
	}
	return map[string]any{"type": "noauth"}
}

// BuildPostmanEnvironment renders an environment entity.
func BuildPostmanEnvironment(env *model.Entity) ([]byte, error) {
	var data model.EnvironmentData
	if len(env.Data) > 0 {
		_ = json.Unmarshal(env.Data, &data)
	}
	vals := make([]any, 0, len(data.Variables))
	for _, v := range data.Variables {
		if v.Key == "" {
			continue
		}
		item := map[string]any{"key": v.Key, "value": v.Value, "enabled": !v.Disabled}
		if v.Secret {
			item["type"] = "secret"
		}
		vals = append(vals, item)
	}
	doc := map[string]any{
		"name":                    env.Name,
		"values":                  vals,
		"_postman_variable_scope": "environment",
	}
	return json.MarshalIndent(doc, "", "  ")
}

// Curl renders a request as a cURL command with variables resolved.
func Curl(r model.RequestData, lookup map[string]string) string {
	iv := func(s string) string { return vars.Interpolate(s, vars.MapLookup(lookup)) }
	var b strings.Builder
	b.WriteString("curl")
	method := strings.ToUpper(r.Method)
	if method == "" {
		method = "GET"
	}
	if method != "GET" {
		b.WriteString(" -X " + method)
	}
	rawURL := iv(r.URL)
	if len(r.Params) > 0 || (r.Auth.Type == model.AuthAPIKey && r.Auth.In == "query" && r.Auth.Key != "") {
		if u, err := url.Parse(rawURL); err == nil {
			q := u.Query()
			for _, p := range r.Params {
				if p.Key != "" && !p.Disabled {
					q.Add(iv(p.Key), iv(p.Value))
				}
			}
			if r.Auth.Type == model.AuthAPIKey && r.Auth.In == "query" && r.Auth.Key != "" {
				q.Set(iv(r.Auth.Key), iv(r.Auth.Value))
			}
			u.RawQuery = q.Encode()
			rawURL = u.String()
		}
	}
	b.WriteString(" '" + escape(rawURL) + "'")

	for _, h := range r.Headers {
		if h.Key == "" || h.Disabled {
			continue
		}
		if strings.EqualFold(h.Key, "content-length") {
			continue
		}
		b.WriteString(" \\\n  -H '" + escape(iv(h.Key)) + ": " + escape(iv(h.Value)) + "'")
	}

	switch r.Auth.Type {
	case model.AuthBearer:
		b.WriteString(" \\\n  -H 'Authorization: Bearer " + escape(iv(r.Auth.Token)) + "'")
	case model.AuthBasic:
		b.WriteString(" \\\n  -u '" + escape(iv(r.Auth.Username)) + ":" + escape(iv(r.Auth.Password)) + "'")
	case model.AuthAPIKey:
		if r.Auth.In != "query" && r.Auth.Key != "" {
			b.WriteString(" \\\n  -H '" + escape(iv(r.Auth.Key)) + ": " + escape(iv(r.Auth.Value)) + "'")
		}
	case model.AuthOAuth2:
		b.WriteString(" \\\n  -H 'Authorization: Bearer <oauth2-token>'")
	}

	switch r.Body.Mode {
	case model.BodyRaw:
		if r.Body.Raw != "" {
			b.WriteString(" \\\n  --data-raw '" + escape(iv(r.Body.Raw)) + "'")
		}
	case model.BodyURLEncoded:
		for _, kv := range r.Body.URLEncoded {
			if kv.Key == "" || kv.Disabled {
				continue
			}
			b.WriteString(" \\\n  --data-urlencode '" + escape(iv(kv.Key)) + "=" + escape(iv(kv.Value)) + "'")
		}
	case model.BodyFormData:
		for _, kv := range r.Body.FormData {
			if kv.Key == "" || kv.Disabled {
				continue
			}
			if kv.Type == "file" {
				b.WriteString(" \\\n  -F '" + escape(iv(kv.Key)) + "=@" + escape(iv(kv.Path)) + "'")
			} else {
				b.WriteString(" \\\n  -F '" + escape(iv(kv.Key)) + "=" + escape(iv(kv.Value)) + "'")
			}
		}
	case model.BodyBinary:
		if r.Body.FilePath != "" {
			b.WriteString(" \\\n  --data-binary '@" + escape(iv(r.Body.FilePath)) + "'")
		}
	}

	if r.Settings.DisableRedirects {
		b.WriteString(" \\\n  --max-redirs 0")
	}
	if r.Settings.SkipTLSVerify {
		b.WriteString(" \\\n  -k")
	}
	if r.Settings.TimeoutMs > 0 {
		b.WriteString(fmt.Sprintf(" \\\n  --max-time %.2f", float64(r.Settings.TimeoutMs)/1000))
	}
	return b.String()
}

func escape(s string) string {
	return strings.ReplaceAll(s, "'", `'\''`)
}
