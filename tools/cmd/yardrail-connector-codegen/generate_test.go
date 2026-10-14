package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMixedFunctionNameCollision(t *testing.T) {
	t.Parallel()

	basicOps, err := filepath.Abs(filepath.Join("testdata", "graphql", "basic", "operations"))
	if err != nil {
		t.Fatal(err)
	}

	notionSpec, err := filepath.Abs(filepath.Join(connectorsRoot, "notion", "notion", "spec", "openapi.json"))
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name       string
		interfaces string
		opFile     string // written to operations/ in the manifest's dir when set
		wantIDs    []string
	}{
		{
			name: "two GraphQL interfaces",
			interfaces: `
  - {id: first, protocol: graphql, auth_method: none, auth_scope: org, source: {operations: ` + basicOps + `, endpoint: https://a.invalid}}
  - {id: second, protocol: graphql, auth_method: none, auth_scope: org, source: {operations: ` + basicOps + `, endpoint: https://b.invalid}}`,
			wantIDs: []string{"first", "second"},
		},
		{
			name: "REST and GraphQL interfaces",
			interfaces: `
  - {id: notion, protocol: rest, auth_method: none, auth_scope: org, source: {openapi: ` + notionSpec + `, client: github.com/yardrail/connectors/notion/notion/client}}
  - {id: gql, protocol: graphql, auth_method: none, auth_scope: org, source: {operations: operations/, endpoint: https://a.invalid}}`,
			opFile:  "mutation MovePage($id: ID!) { move_page(id: $id) { id } }\n",
			wantIDs: []string{"notion", "gql"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			path := writeManifest(t, "connector: {id: collide, name: Collide}\ninterfaces:"+tt.interfaces+"\n")

			if tt.opFile != "" {
				writeOperation(t, filepath.Join(filepath.Dir(path), "operations", "MovePage.graphql"), tt.opFile)
			}

			err := run(runConfig{ManifestPath: path, OutDir: t.TempDir()})
			if !errors.Is(err, errDuplicateFunction) {
				t.Fatalf("run error = %v, want errDuplicateFunction", err)
			}

			for _, id := range tt.wantIDs {
				if !strings.Contains(err.Error(), `"`+id+`"`) {
					t.Errorf("error %q does not name interface %q", err, id)
				}
			}
		})
	}
}

func writeOperation(t *testing.T, path, body string) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
