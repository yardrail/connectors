package main

import (
	"strings"
	"testing"

	"github.com/yardrail/connectors/tools/internal/introspect"
)

func TestRenderCWLSchema(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		connectorID  string
		methods      []introspect.MethodInfo
		operations   map[string]*OperationInfo
		wantContains []string
		wantAbsent   []string
	}{
		{
			name:        "single method",
			connectorID: "slack",
			methods:     []introspect.MethodInfo{{Name: "chat-post-message", MethodName: "ChatPostMessageWithResponse"}},
			operations: map[string]*OperationInfo{
				"chatpostmessage": {Description: "Posts a message.", Inputs: []FieldInfo{{Name: "text", Type: "string", Required: true}}},
			},
			wantContains: []string{
				`$base: "https://yardrail.com/cwl/slack#"`,
				`slack: "https://yardrail.com/cwl/slack#"`,
				"name: ChatPostMessage_class",
				"name: ChatPostMessage",
				"inVocab: false",
				`symbols: ["slack:ChatPostMessage"]`,
				"extends: cwl:Operation",
				"documentRoot: true",
				`type: "#ChatPostMessage_class"`,
			},
			wantAbsent: []string{
				"name: text",
			},
		},
		{
			name:        "multiple methods sorted",
			connectorID: "notion",
			methods: []introspect.MethodInfo{
				{Name: "search-by-title"},
				{Name: "create-page"},
			},
			operations: map[string]*OperationInfo{
				"searchbytitle": {Description: "Search pages."},
				"createpage":    {Description: "Create a page."},
			},
			wantContains: []string{
				"name: CreatePage",
				"name: SearchByTitle",
			},
		},
		{
			name:        "method with no operation info",
			connectorID: "test",
			methods:     []introspect.MethodInfo{{Name: "do-thing"}},
			operations:  map[string]*OperationInfo{},
			wantContains: []string{
				"name: DoThing",
				"inVocab: false",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ctx := &genContext{
				ConnectorID: tt.connectorID,
				Functions:   openAPIFunctions(tt.methods, tt.operations),
			}

			out, err := renderCWLSchema(ctx)
			if err != nil {
				t.Fatalf("renderCWLSchema: %v", err)
			}

			s := string(out)
			for _, want := range tt.wantContains {
				if !strings.Contains(s, want) {
					t.Errorf("output missing %q\n---output---\n%s", want, s)
				}
			}

			for _, absent := range tt.wantAbsent {
				if strings.Contains(s, absent) {
					t.Errorf("output should not contain %q\n---output---\n%s", absent, s)
				}
			}
		})
	}
}

func TestNormalizeMethodToOpKey(t *testing.T) {
	t.Parallel()

	tests := []struct{ input, want string }{
		{"chat-post-message", "chatpostmessage"},
		{"retrieve-a-database", "retrieveadatabase"},
		{"list", "list"},
		{"do-x", "dox"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			t.Parallel()

			got := normalizeMethodToOpKey(tt.input)
			if got != tt.want {
				t.Errorf("normalizeMethodToOpKey(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
