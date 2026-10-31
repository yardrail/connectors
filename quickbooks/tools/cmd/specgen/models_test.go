package main

import (
	"os"
	"path/filepath"
	"testing"
)

func writeTempJSON(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.json")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestParseModels_BasicProperty(t *testing.T) {
	t.Parallel()
	data := `{"models":{"qbo":{
		"accountrequest":{
			"properties":{
				"Name":{"type":"String","requiredFlag":"Required","description":"Account name"},
				"Active":{"type":"Boolean","readOnly":true}
			},
			"Required":["Name"]
		}
	}},"codes":{"qbo":{}}}`

	models, _, err := parseCodesModels(writeTempJSON(t, data))
	if err != nil {
		t.Fatal(err)
	}
	m := models["accountrequest"]
	if m == nil {
		t.Fatal("model not found")
	}
	if len(m.Required) != 1 || m.Required[0] != "Name" {
		t.Errorf("Required = %v, want [Name]", m.Required)
	}
	name := m.Properties["Name"]
	if name.Type != "String" || name.RequiredFlag != "Required" {
		t.Errorf("Name: type=%q flag=%q", name.Type, name.RequiredFlag)
	}
	active := m.Properties["Active"]
	if !active.ReadOnly {
		t.Error("Active should be readOnly")
	}
}

func TestParseModels_ArraySuffix(t *testing.T) {
	t.Parallel()
	data := `{"models":{"qbo":{
		"invoicerequest":{
			"properties":{
				"Line [0..n]":{"type":"SalesItemLine","$ref":[{"salesitemline":"SalesItemLine"}]}
			}
		}
	}},"codes":{"qbo":{}}}`

	models, _, err := parseCodesModels(writeTempJSON(t, data))
	if err != nil {
		t.Fatal(err)
	}
	m := models["invoicerequest"]
	line := m.Properties["Line"]
	if line == nil {
		t.Fatal("Line property not found (suffix not stripped?)")
	}
	if !line.IsArray {
		t.Error("Line should be IsArray")
	}
	if len(line.Refs) != 1 || line.Refs[0].DisplayName != "SalesItemLine" {
		t.Errorf("Refs = %+v", line.Refs)
	}
}

func TestParseModels_MultipleRefs(t *testing.T) {
	t.Parallel()
	data := `{"models":{"qbo":{
		"invoicerequest":{
			"properties":{
				"Line [0..n]":{
					"type":"Invoice line object",
					"$ref":[
						{"salesitemline":"SalesItemLine"},
						{"groupline":"GroupLine"},
						{"descriptiononlyline":"DescriptionOnlyLine"}
					]
				}
			}
		}
	}},"codes":{"qbo":{}}}`

	models, _, err := parseCodesModels(writeTempJSON(t, data))
	if err != nil {
		t.Fatal(err)
	}
	line := models["invoicerequest"].Properties["Line"]
	if len(line.Refs) != 3 {
		t.Fatalf("got %d refs, want 3", len(line.Refs))
	}
}

func TestParseModels_Codes(t *testing.T) {
	t.Parallel()
	data := `{"models":{"qbo":{}},"codes":{"qbo":{
		"Invoice":{
			"Create0-request":"{\"Line\":[]}",
			"Create0-response":"{\"Invoice\":{},\"time\":\"2024-01-01\"}",
			"Query0-request":"select * from Invoice"
		}
	}}}`

	_, codes, err := parseCodesModels(writeTempJSON(t, data))
	if err != nil {
		t.Fatal(err)
	}
	inv := codes["Invoice"]
	if inv == nil {
		t.Fatal("Invoice codes not found")
	}
	if len(inv.Operations) != 3 {
		t.Errorf("got %d ops, want 3", len(inv.Operations))
	}
}

func TestStripHTML(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input string
		want  string
	}{
		{
			`<span class="literal">TotalAmt</span> is read-only`,
			"`TotalAmt` is read-only",
		},
		{
			"Line one<br/>Line two<br>Line three",
			"Line one Line two Line three",
		},
		{"Plain text", "Plain text"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			t.Parallel()
			got := stripHTML(tt.input)
			if got != tt.want {
				t.Errorf("stripHTML(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
