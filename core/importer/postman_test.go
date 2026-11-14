package importer_test

import (
	"encoding/json"
	"testing"

	"yourpm/core/importer"
	"yourpm/core/model"
)

func TestParseOAuth2AuthorizationCode(t *testing.T) {
	raw := []byte(`{
	  "info": {"name": "API"},
	  "item": [{
	    "name": "Me",
	    "request": {
	      "method": "GET",
	      "url": "https://api.example/me",
	      "auth": {
	        "type": "oauth2",
	        "oauth2": [
	          {"key": "grant_type", "value": "authorization_code"},
	          {"key": "authUrl", "value": "https://auth.example/authorize"},
	          {"key": "accessTokenUrl", "value": "https://auth.example/token"},
	          {"key": "redirectUrl", "value": "http://127.0.0.1:8787/callback"},
	          {"key": "clientId", "value": "abc"},
	          {"key": "clientSecret", "value": "def"},
	          {"key": "scope", "value": "read write"},
	          {"key": "clientAuth", "value": "body"}
	        ]
	      }
	    }
	  }]
	}`)
	root, err := importer.ParsePostmanCollection(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(root.Children) != 1 {
		t.Fatalf("imported %d children", len(root.Children))
	}
	var req model.RequestData
	if err := json.Unmarshal(root.Children[0].Data, &req); err != nil {
		t.Fatal(err)
	}
	o := req.Auth.OAuth2
	if req.Auth.Type != model.AuthOAuth2 || o == nil {
		t.Fatalf("auth = %+v, want OAuth2", req.Auth)
	}
	if o.GrantType != model.GrantAuthorizationCode {
		t.Fatalf("grant = %q", o.GrantType)
	}
	if o.AuthorizationURL != "https://auth.example/authorize" ||
		o.AccessTokenURL != "https://auth.example/token" ||
		o.RedirectURL != "http://127.0.0.1:8787/callback" ||
		o.ClientID != "abc" || o.ClientSecret != "def" ||
		o.Scope != "read write" || o.ClientAuth != "body" {
		t.Fatalf("oauth config = %+v", o)
	}
}
