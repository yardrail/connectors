package main

import (
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const (
	graphqlTestdata = "testdata/graphql"
	jsonScalar      = "JSON"
)

func TestLoadGraphQL(t *testing.T) {
	t.Parallel()

	basicSchema := filepath.Join(graphqlTestdata, "basic", "schema.graphql")

	tests := []struct {
		name    string
		src     graphqlSource
		wantErr error
		errHas  string
	}{
		{"misnamed file", graphqlSource{Operations: filepath.Join(graphqlTestdata, "misnamed", "operations")}, errOperationName, "Wrong.graphql"},
		{"two operations", graphqlSource{Operations: filepath.Join(graphqlTestdata, "twoops", "operations")}, errOperationCount, "GetItem.graphql"},
		{
			"unknown field",
			graphqlSource{Schema: basicSchema, Operations: filepath.Join(graphqlTestdata, "unknownfield", "operations")},
			errInvalidOperation,
			filepath.Join("unknownfield", "operations", "GetItem.graphql"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := loadGraphQL(tt.src)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("loadGraphQL error = %v, want %v", err, tt.wantErr)
			}

			if !strings.Contains(err.Error(), tt.errHas) {
				t.Errorf("error %q does not name %q", err, tt.errHas)
			}
		})
	}
}

func TestLoadGraphQLTyped(t *testing.T) {
	t.Parallel()

	ops := filepath.Join(graphqlTestdata, "basic", "operations")

	tests := []struct {
		name        string
		schema      string
		scalars     map[string]string
		wantDesc    string
		wantInputs  []FieldInfo
		wantOutputs []FieldInfo
	}{
		{
			name:     "with schema",
			schema:   filepath.Join(graphqlTestdata, "basic", "schema.graphql"),
			scalars:  map[string]string{jsonScalar: "object"},
			wantDesc: "Get one item by ID.",
			wantInputs: []FieldInfo{
				{Name: "itemId", Type: "string", Required: true},
				{Name: "limit", Type: "integer", Required: false},
			},
			wantOutputs: []FieldInfo{
				{Name: "id", Type: "string"},
				{Name: "label", Type: "string"},
				{Name: "status", Type: "string"},
				{Name: "tags", Type: "array"},
				{Name: "data", Type: "object"},
				{Name: "count", Type: "integer"},
				{Name: "score", Type: "number"},
				{Name: "archived", Type: "boolean"},
				{Name: "owner", Type: "object"},
			},
		},
		{
			name: "without schema",
			wantInputs: []FieldInfo{
				{Name: "itemId", Type: "string", Required: true},
				{Name: "limit", Type: "integer", Required: false},
			},
			wantOutputs: []FieldInfo{
				{Name: "id"}, {Name: "label"}, {Name: "status"}, {Name: "tags"}, {Name: "data"},
				{Name: "count"}, {Name: "score"}, {Name: "archived"}, {Name: "owner"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			funcs, err := loadGraphQL(graphqlSource{Schema: tt.schema, Operations: ops, Scalars: tt.scalars})
			if err != nil {
				t.Fatalf("loadGraphQL: %v", err)
			}

			if len(funcs) != 1 {
				t.Fatalf("got %d functions, want 1", len(funcs))
			}

			fn := funcs[0]
			if fn.Name != "get-item" || fn.ClassName != "GetItem" {
				t.Errorf("name/class = %q/%q, want get-item/GetItem", fn.Name, fn.ClassName)
			}

			if !strings.Contains(fn.Query, "query GetItem(") {
				t.Errorf("Query does not hold the operation text: %q", fn.Query)
			}

			if fn.Op.Description != tt.wantDesc {
				t.Errorf("Description = %q, want %q", fn.Op.Description, tt.wantDesc)
			}

			if !reflect.DeepEqual(fn.Op.Inputs, tt.wantInputs) {
				t.Errorf("Inputs = %+v, want %+v", fn.Op.Inputs, tt.wantInputs)
			}

			if !reflect.DeepEqual(fn.Op.Outputs, tt.wantOutputs) {
				t.Errorf("Outputs = %+v, want %+v", fn.Op.Outputs, tt.wantOutputs)
			}
		})
	}
}

func TestLoadGraphQLScalars(t *testing.T) {
	t.Parallel()

	dir := filepath.Join(graphqlTestdata, "scalars")
	src := graphqlSource{Schema: filepath.Join(dir, "schema.graphql"), Operations: filepath.Join(dir, "operations")}

	t.Run("declared", func(t *testing.T) {
		t.Parallel()

		declared := src
		declared.Scalars = map[string]string{jsonScalar: "string"}

		funcs, err := loadGraphQL(declared)
		if err != nil {
			t.Fatalf("loadGraphQL: %v", err)
		}

		if got := inputType(t, funcs, "set-values", "values"); got != "string" {
			t.Errorf("values type = %q, want string", got)
		}
	})

	t.Run("undeclared", func(t *testing.T) {
		t.Parallel()

		_, err := loadGraphQL(src)
		if !errors.Is(err, errUndeclaredScalar) {
			t.Fatalf("loadGraphQL error = %v, want errUndeclaredScalar", err)
		}

		for _, want := range []string{jsonScalar, "SetValues.graphql"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error %q does not name %q", err, want)
			}
		}
	})
}

// inputType returns the type of input name on function fn, failing the test
// when either is missing.
func inputType(t *testing.T, funcs []graphqlFunction, fn, name string) string {
	t.Helper()

	for _, f := range funcs {
		if f.Name != fn {
			continue
		}

		for _, in := range f.Op.Inputs {
			if in.Name == name {
				return in.Type
			}
		}

		t.Fatalf("function %q has no input %q", fn, name)
	}

	t.Fatalf("no function %q", fn)

	return ""
}

func TestLoadMondaySchema(t *testing.T) {
	t.Parallel()

	schema, err := loadGraphQLSchema(filepath.Join(connectorsRoot, "monday", "monday", "v2", "spec", "schema.graphql"))
	if err != nil {
		t.Fatalf("loadGraphQLSchema: %v", err)
	}

	t.Run("columnValues type", func(t *testing.T) {
		t.Parallel()

		m, err := parseManifest(filepath.Join(connectorsRoot, "monday", "monday", "v2", "manifest.yaml"))
		if err != nil {
			t.Fatalf("parseManifest: %v", err)
		}

		src := m.Interfaces[0].Source

		funcs, err := loadGraphQL(graphqlSource{Schema: src.Schema, Operations: src.Operations, Scalars: src.Scalars})
		if err != nil {
			t.Fatalf("loadGraphQL: %v", err)
		}

		if got := inputType(t, funcs, "change-multiple-column-values", "columnValues"); got != "string" {
			t.Errorf("columnValues type = %q, want string", got)
		}
	})

	tests := []struct {
		root  string
		field string
	}{
		{"query", "items"},
		{"query", "users"},
		{"mutation", "change_multiple_column_values"},
		{"mutation", "create_update"},
	}

	for _, tt := range tests {
		t.Run(tt.field, func(t *testing.T) {
			t.Parallel()

			root := schema.Query
			if tt.root == "mutation" {
				root = schema.Mutation
			}

			if root == nil || root.Fields.ForName(tt.field) == nil {
				t.Errorf("%s root has no field %q", tt.root, tt.field)
			}
		})
	}
}
