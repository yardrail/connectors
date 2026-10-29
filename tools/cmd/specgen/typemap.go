package main

import "strings"

// OpenAPIType represents a mapped Intuit type in OpenAPI terms.
type OpenAPIType struct {
	Type   string // "string", "number", "integer", "boolean"
	Format string // "date", "date-time", "email", "uri", ""
	Ref    string // "#/components/schemas/...", or ""
}

var scalarTypes = map[string]OpenAPIType{
	"string":               {Type: "string"},
	"decimal":              {Type: "number"},
	"bigdecimal":           {Type: "number"},
	"integer":              {Type: "integer"},
	"positive integer":     {Type: "integer"},
	"numeric id":           {Type: "integer"},
	"integer as string":    {Type: "string"},
	"percentage as string": {Type: "string"},
	"boolean":              {Type: "boolean"},
	"date":                 {Type: "string", Format: "date"},
	"datetime":             {Type: "string", Format: "date-time"},
	"emailaddress":         {Type: "string", Format: "email"},
	"websiteaddress":       {Type: "string", Format: "uri"},
	"telephonenumber":      {Type: "string"},
	"idtype":               {Type: "string"},
}

func mapIntuitType(
	intuitType string,
	knownSchemas map[string]bool,
	knownEnums map[string]*XSDEnum,
) OpenAPIType {
	lower := strings.ToLower(intuitType)

	if oa, ok := scalarTypes[lower]; ok {
		return oa
	}

	if _, ok := knownEnums[intuitType]; ok {
		return OpenAPIType{Ref: "#/components/schemas/" + intuitType}
	}

	if knownSchemas[lower] {
		return OpenAPIType{Ref: "#/components/schemas/" + intuitType}
	}

	return OpenAPIType{Type: "string"}
}
