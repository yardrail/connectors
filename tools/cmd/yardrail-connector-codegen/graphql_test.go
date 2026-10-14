package main

import (
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const graphqlTestdata = "testdata/graphql"

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
		wantDesc    string
		wantInputs  []FieldInfo
		wantOutputs []FieldInfo
	}{
		{
			name:     "with schema",
			schema:   filepath.Join(graphqlTestdata, "basic", "schema.graphql"),
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
				{Name: "data", Type: ""},
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

			funcs, err := loadGraphQL(graphqlSource{Schema: tt.schema, Operations: ops})
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

func TestLoadMondaySchema(t *testing.T) {
	t.Parallel()

	schema, err := loadGraphQLSchema(filepath.Join(connectorsRoot, "monday", "monday", "v2", "spec", "schema.graphql"))
	if err != nil {
		t.Fatalf("loadGraphQLSchema: %v", err)
	}

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
