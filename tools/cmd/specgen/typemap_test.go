package main

import "testing"

func TestMapIntuitType(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input string
		want  OpenAPIType
	}{
		{"String", OpenAPIType{Type: "string"}},
		{"string", OpenAPIType{Type: "string"}},
		{"Decimal", OpenAPIType{Type: "number"}},
		{"BigDecimal", OpenAPIType{Type: "number"}},
		{"Integer", OpenAPIType{Type: "integer"}},
		{"Positive Integer", OpenAPIType{Type: "integer"}},
		{"Numeric Id", OpenAPIType{Type: "integer"}},
		{"Integer as String", OpenAPIType{Type: "string"}},
		{"Boolean", OpenAPIType{Type: "boolean"}},
		{"Date", OpenAPIType{Type: "string", Format: "date"}},
		{"DateTime", OpenAPIType{Type: "string", Format: "date-time"}},
		{"EmailAddress", OpenAPIType{Type: "string", Format: "email"}},
		{"WebSiteAddress", OpenAPIType{Type: "string", Format: "uri"}},
		{"TelephoneNumber", OpenAPIType{Type: "string"}},
		{"UnknownThing", OpenAPIType{Type: "string"}},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			t.Parallel()
			got := mapIntuitType(tt.input, nil, nil)
			if got != tt.want {
				t.Errorf("mapIntuitType(%q) = %+v, want %+v", tt.input, got, tt.want)
			}
		})
	}
}

func TestMapIntuitType_EnumRef(t *testing.T) {
	t.Parallel()

	enums := map[string]*XSDEnum{
		"PrintStatusEnum": {Name: "PrintStatusEnum", Values: []string{"NotSet", "NeedToPrint"}},
	}
	got := mapIntuitType("PrintStatusEnum", nil, enums)
	want := OpenAPIType{Ref: "#/components/schemas/PrintStatusEnum"}
	if got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestMapIntuitType_SchemaRef(t *testing.T) {
	t.Parallel()

	schemas := map[string]bool{"referencetype": true}
	got := mapIntuitType("ReferenceType", schemas, nil)
	want := OpenAPIType{Ref: "#/components/schemas/ReferenceType"}
	if got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
}
