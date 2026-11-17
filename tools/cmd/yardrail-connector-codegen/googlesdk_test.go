package main

import (
	"testing"

	"github.com/yardrail/connectors/tools/googleintrospect"
)

func TestGoogleSDKParamType(t *testing.T) {
	t.Parallel()

	tests := []struct {
		param googleintrospect.ToolParam
		want  string
	}{
		{googleintrospect.ToolParam{GoType: "string"}, "string"},
		{googleintrospect.ToolParam{GoType: "bool"}, "boolean"},
		{googleintrospect.ToolParam{GoType: "int64"}, "integer"},
		{googleintrospect.ToolParam{GoType: "float64"}, "number"},
		{googleintrospect.ToolParam{GoType: "[]string"}, "array"},
		{googleintrospect.ToolParam{IsStruct: true, GoType: "object"}, "object"},
	}

	for _, tt := range tests {
		if got := googleSDKParamType(tt.param); got != tt.want {
			t.Errorf("googleSDKParamType(%+v) = %q, want %q", tt.param, got, tt.want)
		}
	}
}

func TestGoogleSDKToolToOp(t *testing.T) {
	t.Parallel()

	tool := googleintrospect.ToolDef{
		Name: "spreadsheets-values-get",
		Desc: "Gets values from a spreadsheet.",
		Params: []googleintrospect.ToolParam{
			{JSONName: "spreadsheetId", GoType: "string", Required: true, Desc: "The spreadsheet ID."},
			{JSONName: "range", GoType: "string", Required: true, Desc: "The range to read."},
			{JSONName: "majorDimension", GoType: "string", Required: false, Desc: "Major dimension."},
			{JSONName: "valueRenderOption", GoType: "int64", Required: false, Desc: "Render option."},
		},
	}

	op := googleSDKToolToOp(tool)

	if op.Description != "Gets values from a spreadsheet." {
		t.Errorf("Description = %q", op.Description)
	}
	if len(op.Inputs) != 4 {
		t.Fatalf("len(Inputs) = %d, want 4", len(op.Inputs))
	}
	if op.Inputs[0].Type != "string" || !op.Inputs[0].Required {
		t.Errorf("Inputs[0] = %+v", op.Inputs[0])
	}
	if op.Inputs[3].Type != "integer" {
		t.Errorf("Inputs[3].Type = %q, want integer", op.Inputs[3].Type)
	}
}

func TestGoogleSDKToolToOpMedia(t *testing.T) {
	t.Parallel()

	tool := googleintrospect.ToolDef{
		Name:          "files-create",
		Desc:          "Creates a file.",
		SupportsMedia: true,
		Params: []googleintrospect.ToolParam{
			{JSONName: "file", GoType: "object", IsStruct: true, Required: true, Desc: "File metadata"},
		},
	}

	op := googleSDKToolToOp(tool)

	if len(op.Inputs) != 2 {
		t.Fatalf("len(Inputs) = %d, want 2", len(op.Inputs))
	}
	media := op.Inputs[1]
	if media.Name != "media" || media.Type != "File" || media.Required {
		t.Errorf("media input = %+v", media)
	}
}

func TestGoogleSDKToolToOpNoMedia(t *testing.T) {
	t.Parallel()

	tool := googleintrospect.ToolDef{
		Name:          "tasklists-delete",
		Desc:          "Deletes a task list.",
		SupportsMedia: false,
		Params: []googleintrospect.ToolParam{
			{JSONName: "tasklistid", GoType: "string", Required: true, Desc: "Task list ID."},
		},
	}

	op := googleSDKToolToOp(tool)

	if len(op.Inputs) != 1 {
		t.Fatalf("len(Inputs) = %d, want 1", len(op.Inputs))
	}
}
