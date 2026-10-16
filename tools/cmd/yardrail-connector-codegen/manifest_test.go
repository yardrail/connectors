package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// writeManifest writes body to manifest.yaml in a fresh temp dir and returns
// its path.
func writeManifest(t *testing.T, body string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "manifest.yaml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	return path
}

func TestManifestUnresolvedSecurityScheme(t *testing.T) {
	t.Parallel()

	m := &Manifest{Interfaces: []ManifestInterface{{ID: "api", SecurityScheme: "missing"}}}

	_, err := deriveAuthStrategy(m, mergeSecuritySchemes(map[string]SecurityScheme{"other": {Type: "oauth2"}}, nil))
	if !errors.Is(err, errUnresolvedSecurityScheme) {
		t.Fatalf("deriveAuthStrategy error = %v, want errUnresolvedSecurityScheme", err)
	}
}

func TestManifestSecuritySchemes(t *testing.T) {
	t.Parallel()

	path := writeManifest(t, `
connector: {id: monday, name: Monday}
security_schemes:
  mondayToken: {type: apiKey, in: header, name: Authorization}
  shared: {type: apiKey, in: query, name: key}
interfaces:
  - id: api
    protocol: rest
    auth_method: api_key
    auth_scope: org
    security_scheme: mondayToken
    cred_mapping: {Authorization: token}
`)

	m, err := parseManifest(path)
	if err != nil {
		t.Fatalf("parseManifest: %v", err)
	}

	tests := []struct {
		name        string
		spec        map[string]SecurityScheme
		scheme      string
		wantType    string
		wantCredKey string
	}{
		{"manifest only", nil, "mondayToken", "apikey_header", "token"},
		{"manifest wins on clash", map[string]SecurityScheme{"mondayToken": {Type: "http", Scheme: "bearer"}}, "mondayToken", "apikey_header", "token"},
		{"spec scheme still available", map[string]SecurityScheme{"specOnly": {Type: "http", Scheme: "bearer"}}, "specOnly", "bearer", "access_token"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ifaceManifest := *m
			ifaceManifest.Interfaces = []ManifestInterface{m.Interfaces[0]}
			ifaceManifest.Interfaces[0].SecurityScheme = tt.scheme

			strategies, err := deriveAuthStrategy(&ifaceManifest, mergeSecuritySchemes(tt.spec, m.SecuritySchemes))
			if err != nil {
				t.Fatalf("deriveAuthStrategy: %v", err)
			}

			strat := strategies["api"]
			if got := authSchemeType(strat); got != tt.wantType {
				t.Errorf("authSchemeType = %q, want %q", got, tt.wantType)
			}

			if strat.CredKey != tt.wantCredKey {
				t.Errorf("CredKey = %q, want %q", strat.CredKey, tt.wantCredKey)
			}
		})
	}
}

func TestManifestValidation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		source  string
		wantErr error
	}{
		{"graphql without endpoint", "source: {schema: s.graphql, operations: ops/}", errMissingSource},
		{"graphql without operations", "source: {endpoint: https://example.com/graphql}", errMissingSource},
		{"graphql without source", "", errMissingSource},
		{"graphql without schema", "source: {operations: ops/, endpoint: https://example.com/graphql}", nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			path := writeManifest(t, `
connector: {id: fixture, name: Fixture}
interfaces:
  - id: api
    protocol: graphql
    auth_method: none
    auth_scope: org
    `+tt.source+`
`)

			_, err := parseManifest(path)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("parseManifest error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

const (
	graphqlScalarSource = "source: {operations: ops/, endpoint: https://example.com/graphql, scalars: "
	restScalarSource    = "source: {openapi: spec/openapi.json, client: example.com/client, scalars: "
)

func TestManifestScalars(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		protocol string
		source   string
		wantErr  error
	}{
		{"graphql declared", "graphql", graphqlScalarSource + "{JSON: string}}", nil},
		{"graphql invalid type", "graphql", graphqlScalarSource + "{JSON: date}}", errInvalidScalarType},
		{"rest declared", "rest", restScalarSource + "{JSON: string}}", errScalarsNotGraphQL},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			path := writeManifest(t, `
connector: {id: fixture, name: Fixture}
interfaces:
  - id: api
    protocol: `+tt.protocol+`
    auth_method: none
    auth_scope: org
    `+tt.source+`
`)

			_, err := parseManifest(path)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("parseManifest error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

const (
	graphqlDescriptionSource = "source: {operations: ops/, endpoint: https://example.com/graphql, descriptions: "
	restDescriptionSource    = "source: {openapi: spec/openapi.json, client: example.com/client, descriptions: "
)

func TestManifestDescriptions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		protocol string
		source   string
		wantErr  error
	}{
		{"graphql declared", "graphql", graphqlDescriptionSource + "{GetItem: Gets an item.}}", nil},
		{"rest declared", "rest", restDescriptionSource + "{GetItem: Gets an item.}}", errDescriptionsNotGraphQL},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			path := writeManifest(t, `
connector: {id: fixture, name: Fixture}
interfaces:
  - id: api
    protocol: `+tt.protocol+`
    auth_method: none
    auth_scope: org
    `+tt.source+`
`)

			_, err := parseManifest(path)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("parseManifest error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestManifestSourcePaths(t *testing.T) {
	t.Parallel()

	path := writeManifest(t, `
connector: {id: fixture, name: Fixture}
interfaces:
  - id: gql
    protocol: graphql
    auth_method: none
    auth_scope: org
    source: {schema: spec/schema.graphql, operations: operations/, endpoint: https://example.com/graphql}
  - id: rest
    protocol: rest
    auth_method: none
    auth_scope: org
    source: {openapi: spec/openapi.json, client: example.com/client}
`)
	dir := filepath.Dir(path)

	m, err := parseManifest(path)
	if err != nil {
		t.Fatalf("parseManifest: %v", err)
	}

	tests := []struct {
		name string
		got  string
		want string
	}{
		{"schema", m.Interfaces[0].Source.Schema, filepath.Join(dir, "spec", "schema.graphql")},
		{"operations", m.Interfaces[0].Source.Operations, filepath.Join(dir, "operations")},
		{"endpoint unchanged", m.Interfaces[0].Source.Endpoint, "https://example.com/graphql"},
		{"openapi", m.Interfaces[1].Source.OpenAPI, filepath.Join(dir, "spec", "openapi.json")},
		{"client unchanged", m.Interfaces[1].Source.Client, "example.com/client"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if tt.got != tt.want {
				t.Errorf("got %q, want %q", tt.got, tt.want)
			}
		})
	}
}
