package main

import "testing"

func TestEscDesc(t *testing.T) {
	t.Parallel()

	escDesc := tmplFuncs["escDesc"].(func(string) string)

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"plain text", "hello world", "hello world"},
		{"double quotes", `say "hello"`, `say \"hello\"`},
		{"backslash", `path\to\file`, `path\\to\\file`},
		{"newline", "line one\nline two", `line one\nline two`},
		{"carriage return", "line one\r\nline two", `line one\r\nline two`},
		{"tab", "col1\tcol2", `col1\tcol2`},
		{"mixed", "say \"hi\"\nand\\bye", `say \"hi\"\nand\\bye`},
		{"empty", "", ""},
		{
			"multiline description",
			"Create Account\n\nTakes a body of the `application/json` content type.",
			`Create Account\n\nTakes a body of the ` + "`" + `application/json` + "`" + ` content type.`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := escDesc(tt.input)
			if got != tt.want {
				t.Errorf("escDesc(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestParamExtractExpr(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		param ToolParam
		want  string
	}{
		{
			"string param",
			ToolParam{GoType: "string", JSONName: "name"},
			`request.GetString("name", "")`,
		},
		{
			"bool param",
			ToolParam{GoType: "bool", JSONName: "active"},
			`request.GetBool("active", false)`,
		},
		{
			"float64 param",
			ToolParam{GoType: "float64", JSONName: "amount"},
			`request.GetFloat("amount", 0)`,
		},
		{
			"int param",
			ToolParam{GoType: "int", JSONName: "count"},
			`int(request.GetInt("count", 0))`,
		},
		{
			"int32 param",
			ToolParam{GoType: "int32", JSONName: "limit"},
			`int32(request.GetInt("limit", 0))`,
		},
		{
			"string slice param",
			ToolParam{GoType: "[]string", JSONName: "tags"},
			`getStringSlice(request, "tags")`,
		},
		{
			"unknown type defaults to string",
			ToolParam{GoType: "CustomType", JSONName: "custom"},
			`request.GetString("custom", "")`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := paramExtractExpr(tt.param)
			if got != tt.want {
				t.Errorf("paramExtractExpr(%+v) = %q, want %q", tt.param, got, tt.want)
			}
		})
	}
}

func TestRender_ValidGo(t *testing.T) {
	t.Parallel()

	groups := []ControllerGroup{{
		Name: "TestGroup",
		Methods: []ToolDef{{
			Name:       "test-tool",
			MethodName: "TestToolWithResponse",
			Desc:       "A test tool\nwith newlines\nand \"quotes\"",
			Params: []ToolParam{
				{Name: "Id", JSONName: "id", GoType: "string", Required: true, Desc: "The ID"},
			},
			CallArgs:   []CallArg{{Expr: "id"}},
			ParamsType: "",
			ReturnType: "TestToolResp",
			HasReturn:  true,
		}},
	}}

	src, err := render(groups, "testpkg", "test-server", "example.com/client", libraryTemplate)
	if err != nil {
		t.Fatalf("render: %v", err)
	}

	// If gofmt succeeded, the output is valid Go
	if len(src) == 0 {
		t.Fatal("empty output")
	}

	output := string(src)
	if !contains(output, "package testpkg") {
		t.Error("missing package declaration")
	}
	if !contains(output, `A test tool\nwith newlines\nand \"quotes\"`) {
		t.Error("description not properly escaped in output")
	}
}

func contains(s, substr string) bool {
	return len(s) > 0 && len(substr) > 0 && stringContains(s, substr)
}

func stringContains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
