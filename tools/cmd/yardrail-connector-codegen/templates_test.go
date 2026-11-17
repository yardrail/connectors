package main

import (
	"errors"
	"testing"

	"github.com/yardrail/connectors/tools/googleintrospect"
)

func TestConfigParamFunc(t *testing.T) {
	t.Parallel()

	fn, ok := tmplFuncs["configParam"].(func(map[string]string, string) string)
	if !ok {
		t.Fatal("configParam not found or wrong type in tmplFuncs")
	}

	tests := []struct {
		name     string
		params   map[string]string
		jsonName string
		want     string
	}{
		{"found", map[string]string{"companyid": "realm_id"}, "companyid", "realm_id"},
		{"not found", map[string]string{"companyid": "realm_id"}, "otherparam", ""},
		{"nil map", nil, "companyid", ""},
		{"empty map", map[string]string{}, "companyid", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := fn(tt.params, tt.jsonName)
			if got != tt.want {
				t.Errorf("configParam(%v, %q) = %q, want %q",
					tt.params, tt.jsonName, got, tt.want)
			}
		})
	}
}

func TestDeriveAuthStrategyValidation(t *testing.T) {
	t.Parallel()

	apiKey := func(name string) SecurityScheme { return SecurityScheme{Type: "apiKey", In: "header", Name: name} }
	bearer := SecurityScheme{Type: "http", Scheme: "bearer"}
	basic := SecurityScheme{Type: "http", Scheme: "basic"}

	tests := []struct {
		name    string
		scheme  SecurityScheme
		mapping map[string]string
		headers map[string]string
		wantErr error
	}{
		{"apiKey default source not stored", apiKey("private-app"), nil, nil, errUnstoredCredKey},
		{"bearer mapping Authorization", bearer, map[string]string{"Authorization": "token"}, nil, errInvalidCredTarget},
		{"static Authorization header", bearer, map[string]string{"access_token": "token"}, map[string]string{"Authorization": "x"}, errStaticAuthHeader},
		{"monday apiKey Authorization", apiKey("Authorization"), map[string]string{"Authorization": "token"}, map[string]string{"API-Version": "2026-07"}, nil},
		{"mailchimp basic", basic, map[string]string{"username": "token", "password": "token"}, nil, nil},
		{"api_key bearer reads access_token", bearer, nil, nil, errPastedSecretKey},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			m := &Manifest{Interfaces: []ManifestInterface{{
				ID: "api", Protocol: "rest", AuthMethod: "api_key",
				SecurityScheme: "s", CredMapping: tt.mapping, Headers: tt.headers,
			}}}

			_, err := deriveAuthStrategy(m, map[string]SecurityScheme{"s": tt.scheme})
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("deriveAuthStrategy error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestHasGoogleSDKMediaSupport(t *testing.T) {
	t.Parallel()

	fn := tmplFuncs["hasGoogleSDKMediaSupport"].(func([]googleSDKToolDef) bool)

	if fn(nil) {
		t.Error("nil tools should return false")
	}
	if fn([]googleSDKToolDef{{ToolDef: googleintrospect.ToolDef{Name: "x"}}}) {
		t.Error("tool without media should return false")
	}
	if !fn([]googleSDKToolDef{{ToolDef: googleintrospect.ToolDef{Name: "x", SupportsMedia: true}}}) {
		t.Error("tool with media should return true")
	}
}
