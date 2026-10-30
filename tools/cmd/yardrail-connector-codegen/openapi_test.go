package main

import (
	"os"
	"path/filepath"
	"testing"
)

func writeTempJSON(t *testing.T, content string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "spec.json")

	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	return path
}

func TestParseOperations(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		spec        string
		wantOps     int
		checkOp     string
		wantInputs  int
		wantOutputs int
	}{
		{
			name: "POST with requestBody and 200 response",
			spec: `{"paths":{"/msg":{"post":{
				"operationId":"postMessage",
				"summary":"Post a message",
				"requestBody":{"content":{"application/json":{"schema":{
					"type":"object",
					"properties":{"channel":{"type":"string","description":"Channel ID"},"text":{"type":"string"}},
					"required":["channel"]
				}}}},
				"responses":{"200":{"content":{"application/json":{"schema":{
					"type":"object",
					"properties":{"ok":{"type":"boolean"},"ts":{"type":"string"}},
					"required":["ok","ts"]
				}}}}}
			}}}}`,
			wantOps:     1,
			checkOp:     "postmessage",
			wantInputs:  2,
			wantOutputs: 2,
		},
		{
			name: "GET with query params only",
			spec: `{"paths":{"/list":{"get":{
				"operationId":"listItems",
				"parameters":[
					{"name":"limit","in":"query","schema":{"type":"integer"},"required":false},
					{"name":"cursor","in":"query","schema":{"type":"string"}}
				],
				"responses":{"200":{"content":{"application/json":{"schema":{
					"type":"object",
					"properties":{"items":{"type":"array"},"total":{"type":"integer"}},
					"required":["items"]
				}}}}}
			}}}}`,
			wantOps:     1,
			checkOp:     "listitems",
			wantInputs:  2,
			wantOutputs: 2,
		},
		{
			name: "header params excluded from inputs",
			spec: `{"paths":{"/x":{"post":{
				"operationId":"doX",
				"parameters":[
					{"name":"token","in":"header","schema":{"type":"string"},"required":true},
					{"name":"id","in":"query","schema":{"type":"string"},"required":true}
				],
				"responses":{"200":{"content":{"application/json":{"schema":{
					"type":"object","properties":{"ok":{"type":"boolean"}}
				}}}}}
			}}}}`,
			wantOps:     1,
			checkOp:     "dox",
			wantInputs:  1,
			wantOutputs: 1,
		},
		{
			name: "response with $ref to simple type",
			spec: `{
				"paths":{"/x":{"get":{
					"operationId":"getX",
					"responses":{"200":{"content":{"application/json":{"schema":{
						"type":"object",
						"properties":{"id":{"$ref":"#/components/schemas/StringId"}},
						"required":["id"]
					}}}}}
				}}},
				"components":{"schemas":{"StringId":{"type":"string"}}}
			}`,
			wantOps:     1,
			checkOp:     "getx",
			wantInputs:  0,
			wantOutputs: 1,
		},
		{
			name: "response with $ref to complex object",
			spec: `{
				"paths":{"/x":{"get":{
					"operationId":"getX",
					"responses":{"200":{"content":{"application/json":{"schema":{
						"type":"object",
						"properties":{"msg":{"$ref":"#/components/schemas/Message"}}
					}}}}}
				}}},
				"components":{"schemas":{"Message":{"type":"object","properties":{"text":{"type":"string"}}}}}
			}`,
			wantOps:     1,
			checkOp:     "getx",
			wantOutputs: 1,
		},
		{
			name: "no 200 response",
			spec: `{"paths":{"/x":{"delete":{
				"operationId":"deleteX",
				"responses":{"204":{}}
			}}}}`,
			wantOps:     1,
			checkOp:     "deletex",
			wantOutputs: 0,
		},
		{
			name: "201 response used when no 200",
			spec: `{"paths":{"/x":{"post":{
				"operationId":"createX",
				"responses":{"201":{"content":{"application/json":{"schema":{
					"type":"object",
					"properties":{"id":{"type":"string"}},
					"required":["id"]
				}}}}}
			}}}}`,
			wantOps:     1,
			checkOp:     "createx",
			wantOutputs: 1,
		},
		{
			name: "empty response schema",
			spec: `{"paths":{"/x":{"get":{
				"operationId":"getX",
				"responses":{"200":{"content":{"application/json":{"schema":{"type":"object"}}}}}
			}}}}`,
			wantOps:     1,
			checkOp:     "getx",
			wantOutputs: 0,
		},
		{
			name: "multiple operations",
			spec: `{"paths":{
				"/a":{"get":{"operationId":"getA","responses":{}}},
				"/b":{"post":{"operationId":"postB","responses":{}}}
			}}`,
			wantOps: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			path := writeTempJSON(t, tt.spec)
			ops, err := parseOperations(path)
			if err != nil {
				t.Fatalf("parseOperations: %v", err)
			}

			if len(ops) != tt.wantOps {
				t.Fatalf("got %d ops, want %d", len(ops), tt.wantOps)
			}

			if tt.checkOp == "" {
				return
			}

			op, ok := ops[tt.checkOp]
			if !ok {
				t.Fatalf("operation %q not found; have %v", tt.checkOp, keys(ops))
			}

			if len(op.Inputs) != tt.wantInputs {
				t.Errorf("inputs: got %d, want %d; inputs=%v", len(op.Inputs), tt.wantInputs, fieldNames(op.Inputs))
			}

			if len(op.Outputs) != tt.wantOutputs {
				t.Errorf("outputs: got %d, want %d; outputs=%v", len(op.Outputs), tt.wantOutputs, fieldNames(op.Outputs))
			}
		})
	}
}

func TestResolveType(t *testing.T) {
	t.Parallel()

	resolver := newRefResolver(map[string]rawSchema{
		"StringId": {Type: "string"},
		"Message":  {Type: "object", Properties: map[string]rawSchema{"text": {Type: "string"}}},
	}, nil)

	tests := []struct {
		name   string
		schema rawSchema
		want   string
	}{
		{name: "string", schema: rawSchema{Type: "string"}, want: "string"},
		{name: "integer", schema: rawSchema{Type: "integer"}, want: "integer"},
		{name: "number", schema: rawSchema{Type: "number"}, want: "number"},
		{name: "boolean", schema: rawSchema{Type: "boolean"}, want: "boolean"},
		{name: "array", schema: rawSchema{Type: "array"}, want: "array"},
		{name: "object", schema: rawSchema{Type: "object"}, want: "object"},
		{name: "empty defaults to object", schema: rawSchema{}, want: "object"},
		{name: "$ref to string", schema: rawSchema{Ref: "#/components/schemas/StringId"}, want: "string"},
		{name: "$ref to object", schema: rawSchema{Ref: "#/components/schemas/Message"}, want: "object"},
		{name: "$ref unknown", schema: rawSchema{Ref: "#/components/schemas/Missing"}, want: "object"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := resolveType(tt.schema, resolver)
			if got != tt.want {
				t.Errorf("resolveType = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestNormalizeOpID(t *testing.T) {
	t.Parallel()

	tests := []struct{ input, want string }{
		{"chat_postMessage", "chatpostmessage"},
		{"retrieve-a-database", "retrieveadatabase"},
		{"listItems", "listitems"},
		{"admin.apps.approve", "adminappsapprove"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			t.Parallel()

			got := normalizeOpID(tt.input)
			if got != tt.want {
				t.Errorf("normalizeOpID(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func keys(m map[string]*OperationInfo) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}

	return out
}

func fieldNames(fields []FieldInfo) []string {
	out := make([]string, len(fields))
	for i, f := range fields {
		out[i] = f.Name
	}

	return out
}
