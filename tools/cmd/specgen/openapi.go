package main

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"
)

func buildOpenAPI(spec *CorrelatedSpec) (*openapi3.T, error) {
	doc := &openapi3.T{
		OpenAPI: "3.1.0",
		Info: &openapi3.Info{
			Title:       "QuickBooks Online Accounting API",
			Description: "Generated from Intuit SDK XSD definitions and developer documentation.",
			Version:     "1.0.0",
		},
		Servers: openapi3.Servers{
			{URL: "https://quickbooks.api.intuit.com"},
			{URL: "https://sandbox-quickbooks.api.intuit.com"},
		},
		Components: &openapi3.Components{
			Schemas:         make(openapi3.Schemas),
			SecuritySchemes: make(openapi3.SecuritySchemes),
			Parameters:      make(openapi3.ParametersMap),
		},
		Paths:    openapi3.NewPaths(),
		Security: openapi3.SecurityRequirements{{"OAuth2": {}}},
	}

	addSecurityScheme(doc)
	addCommonParameters(doc)
	addTags(doc, spec)

	// Enum schemas from XSD — sorted for deterministic output
	enumNames := make([]string, 0, len(spec.Enums))
	for name := range spec.Enums {
		enumNames = append(enumNames, name)
	}
	sort.Strings(enumNames)
	for _, name := range enumNames {
		enum := spec.Enums[name]
		vals := make([]any, len(enum.Values))
		for i, v := range enum.Values {
			vals[i] = v
		}
		s := &openapi3.Schema{
			Type: &openapi3.Types{"string"},
			Enum: vals,
		}
		if enum.Doc != "" {
			s.Description = enum.Doc
		}
		doc.Components.Schemas[name] = &openapi3.SchemaRef{Value: s}
	}

	knownSchemas := buildKnownSchemas(spec)

	// XSD base type schemas — sorted for deterministic output
	if spec.XSDTypes != nil {
		xsdTypeNames := make([]string, 0, len(spec.XSDTypes))
		for name := range spec.XSDTypes {
			xsdTypeNames = append(xsdTypeNames, name)
		}
		sort.Strings(xsdTypeNames)
		for _, name := range xsdTypeNames {
			ct := spec.XSDTypes[name]
			if _, isEntity := knownSchemas[strings.ToLower(name)]; !isEntity {
				continue
			}
			doc.Components.Schemas[name] = &openapi3.SchemaRef{
				Value: buildXSDSchema(ct, knownSchemas, spec.Enums),
			}
		}
	}

	// Shared model schemas — sorted for deterministic output
	sharedKeys := make([]string, 0, len(spec.SharedTypes))
	for key := range spec.SharedTypes {
		sharedKeys = append(sharedKeys, key)
	}
	sort.Strings(sharedKeys)
	for _, key := range sharedKeys {
		si := spec.SharedTypes[key]
		doc.Components.Schemas[si.DisplayName] = &openapi3.SchemaRef{
			Value: buildModelSchema(si.Model, knownSchemas, spec.Enums, spec.RefRewrites),
		}
	}

	// Per-entity schemas + envelope schemas
	for _, entity := range spec.Entities {
		addEntitySchemas(doc, entity, knownSchemas, spec.Enums, spec.RefRewrites)
	}

	// Shared envelope schemas
	addSharedEnvelopes(doc)

	// Paths from entity operations
	for _, entity := range spec.Entities {
		addEntityPaths(doc, entity)
	}

	// Emit placeholder schemas for any unresolved $ref targets
	resolveUnresolvedRefs(doc)

	return doc, nil
}

func resolveUnresolvedRefs(doc *openapi3.T) {
	data, err := json.Marshal(doc)
	if err != nil {
		return
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return
	}
	refs := findAllRefs(raw)
	for _, ref := range refs {
		const prefix = "#/components/schemas/"
		if !strings.HasPrefix(ref, prefix) {
			continue
		}
		name := strings.TrimPrefix(ref, prefix)
		if doc.Components.Schemas[name] != nil {
			continue
		}
		doc.Components.Schemas[name] = &openapi3.SchemaRef{
			Value: &openapi3.Schema{
				Type: &openapi3.Types{"object"},
			},
		}
	}
}

func findAllRefs(v any) []string {
	var refs []string
	switch val := v.(type) {
	case map[string]any:
		if ref, ok := val["$ref"].(string); ok {
			refs = append(refs, ref)
		}
		for _, child := range val {
			refs = append(refs, findAllRefs(child)...)
		}
	case []any:
		for _, child := range val {
			refs = append(refs, findAllRefs(child)...)
		}
	}
	return refs
}

func addTags(doc *openapi3.T, spec *CorrelatedSpec) {
	tagNames := make(map[string]bool)
	for _, e := range spec.Entities {
		tagNames[e.Name] = true
	}
	sorted := make([]string, 0, len(tagNames))
	for name := range tagNames {
		sorted = append(sorted, name)
	}
	sort.Strings(sorted)
	for _, name := range sorted {
		doc.Tags = append(doc.Tags, &openapi3.Tag{Name: name})
	}
}

func addSecurityScheme(doc *openapi3.T) {
	doc.Components.SecuritySchemes["OAuth2"] = &openapi3.SecuritySchemeRef{
		Value: &openapi3.SecurityScheme{
			Type: "oauth2",
			Flows: &openapi3.OAuthFlows{
				AuthorizationCode: &openapi3.OAuthFlow{
					AuthorizationURL: "https://appcenter.intuit.com/connect/oauth2",
					TokenURL:         "https://oauth.platform.intuit.com/oauth2/v1/tokens/bearer",
					Scopes: map[string]string{
						"com.intuit.quickbooks.accounting": "Accounting scope",
					},
				},
			},
		},
	}
}

func addCommonParameters(doc *openapi3.T) {
	doc.Components.Parameters["realmId"] = &openapi3.ParameterRef{
		Value: &openapi3.Parameter{
			Name:        "realmId",
			In:          "path",
			Required:    true,
			Description: "The QuickBooks company ID (Realm ID).",
			Schema: &openapi3.SchemaRef{Value: &openapi3.Schema{
				Type: &openapi3.Types{"string"},
			}},
		},
	}
	doc.Components.Parameters["minorversion"] = &openapi3.ParameterRef{
		Value: &openapi3.Parameter{
			Name:        "minorversion",
			In:          "query",
			Description: "Minor version of the QBO API.",
			Schema: &openapi3.SchemaRef{Value: &openapi3.Schema{
				Type: &openapi3.Types{"integer"},
			}},
		},
	}
}

func buildKnownSchemas(spec *CorrelatedSpec) map[string]bool {
	known := make(map[string]bool)
	for k, si := range spec.SharedTypes {
		known[k] = true
		known[strings.ToLower(si.DisplayName)] = true
	}
	if spec.XSDTypes != nil {
		for k := range spec.XSDTypes {
			known[strings.ToLower(k)] = true
		}
	}
	for _, e := range spec.Entities {
		known[strings.ToLower(e.Name)] = true
	}
	return known
}

func buildXSDSchema(ct *XSDComplexType, knownSchemas map[string]bool, enums map[string]*XSDEnum) *openapi3.Schema {
	props := make(openapi3.Schemas)
	for _, p := range ct.Properties {
		oaType := mapIntuitType(p.TypeName, knownSchemas, enums)
		var propRef *openapi3.SchemaRef

		if oaType.Ref != "" {
			if p.MaxOccurs == -1 {
				propRef = &openapi3.SchemaRef{Value: &openapi3.Schema{
					Type:  &openapi3.Types{"array"},
					Items: &openapi3.SchemaRef{Ref: oaType.Ref},
				}}
			} else {
				propRef = &openapi3.SchemaRef{Ref: oaType.Ref}
			}
		} else {
			s := &openapi3.Schema{
				Type:   &openapi3.Types{oaType.Type},
				Format: oaType.Format,
			}
			if p.Doc != "" {
				s.Description = strings.TrimSpace(p.Doc)
			}
			if p.MaxOccurs == -1 {
				propRef = &openapi3.SchemaRef{Value: &openapi3.Schema{
					Type:  &openapi3.Types{"array"},
					Items: &openapi3.SchemaRef{Value: s},
				}}
			} else {
				propRef = &openapi3.SchemaRef{Value: s}
			}
		}

		props[p.Name] = propRef
	}

	if ct.BaseName != "" {
		s := &openapi3.Schema{
			AllOf: openapi3.SchemaRefs{
				{Ref: "#/components/schemas/" + ct.BaseName},
				{Value: &openapi3.Schema{
					Type:       &openapi3.Types{"object"},
					Properties: props,
				}},
			},
		}
		if ct.Doc != "" {
			s.Description = ct.Doc
		}
		return s
	}

	s := &openapi3.Schema{
		Type:       &openapi3.Types{"object"},
		Properties: props,
	}
	if ct.Doc != "" {
		s.Description = ct.Doc
	}
	return s
}

func canonicalRef(displayName string, rewrites map[string]string) string {
	if rewritten, ok := rewrites[displayName]; ok {
		return rewritten
	}
	return displayName
}

func buildModelSchema(model *Model, knownSchemas map[string]bool, enums map[string]*XSDEnum, rewrites map[string]string) *openapi3.Schema {
	schema := &openapi3.Schema{
		Type:       &openapi3.Types{"object"},
		Properties: make(openapi3.Schemas),
	}

	propNames := make([]string, 0, len(model.Properties))
	for name := range model.Properties {
		propNames = append(propNames, name)
	}
	sort.Strings(propNames)
	for _, name := range propNames {
		prop := model.Properties[name]
		schema.Properties[name] = buildPropertyRef(prop, knownSchemas, enums, rewrites)
	}

	if len(model.Required) > 0 {
		schema.Required = model.Required
	}

	return schema
}

func buildPropertyRef(prop *ModelProperty, knownSchemas map[string]bool, enums map[string]*XSDEnum, rewrites map[string]string) *openapi3.SchemaRef {
	// Polymorphic $ref (multiple distinct refs -> oneOf)
	if len(prop.Refs) > 1 {
		seen := make(map[string]bool)
		var refs openapi3.SchemaRefs
		for _, ref := range prop.Refs {
			refPath := "#/components/schemas/" + canonicalRef(ref.DisplayName, rewrites)
			if seen[refPath] {
				continue
			}
			seen[refPath] = true
			refs = append(refs, &openapi3.SchemaRef{Ref: refPath})
		}
		// Dedup may collapse to a single ref
		if len(refs) == 1 {
			if prop.IsArray {
				return &openapi3.SchemaRef{Value: &openapi3.Schema{
					Type:  &openapi3.Types{"array"},
					Items: refs[0],
				}}
			}
			return refs[0]
		}
		s := &openapi3.Schema{OneOf: refs}
		if prop.IsArray {
			return &openapi3.SchemaRef{Value: &openapi3.Schema{
				Type:  &openapi3.Types{"array"},
				Items: &openapi3.SchemaRef{Value: s},
			}}
		}
		return &openapi3.SchemaRef{Value: s}
	}

	// Single $ref
	if len(prop.Refs) == 1 {
		ref := &openapi3.SchemaRef{
			Ref: "#/components/schemas/" + canonicalRef(prop.Refs[0].DisplayName, rewrites),
		}
		if prop.IsArray {
			return &openapi3.SchemaRef{Value: &openapi3.Schema{
				Type:  &openapi3.Types{"array"},
				Items: ref,
			}}
		}
		return ref
	}

	// Scalar type
	oaType := mapIntuitType(prop.Type, knownSchemas, enums)

	if oaType.Ref != "" {
		if prop.IsArray {
			return &openapi3.SchemaRef{Value: &openapi3.Schema{
				Type:  &openapi3.Types{"array"},
				Items: &openapi3.SchemaRef{Ref: oaType.Ref},
			}}
		}
		return &openapi3.SchemaRef{Ref: oaType.Ref}
	}

	s := &openapi3.Schema{
		Type:   &openapi3.Types{oaType.Type},
		Format: oaType.Format,
	}
	if prop.ReadOnly {
		s.ReadOnly = true
	}
	if prop.Description != "" {
		s.Description = prop.Description
	}

	if prop.IsArray {
		return &openapi3.SchemaRef{Value: &openapi3.Schema{
			Type:  &openapi3.Types{"array"},
			Items: &openapi3.SchemaRef{Value: s},
		}}
	}

	return &openapi3.SchemaRef{Value: s}
}

func addEntitySchemas(doc *openapi3.T, entity *CorrelatedEntity, knownSchemas map[string]bool, enums map[string]*XSDEnum, rewrites map[string]string) {
	// Request schema (for create)
	if entity.RequestModel != nil {
		doc.Components.Schemas[entity.Name+"CreateRequest"] = &openapi3.SchemaRef{
			Value: buildModelSchema(entity.RequestModel, knownSchemas, enums, rewrites),
		}
	}

	// Response schema (full entity)
	if entity.ResponseModel != nil {
		doc.Components.Schemas[entity.Name] = &openapi3.SchemaRef{
			Value: buildModelSchema(entity.ResponseModel, knownSchemas, enums, rewrites),
		}
	}

	// Response envelope
	if entity.ResponseModel != nil && entity.Category == CategoryCRUD {
		doc.Components.Schemas[entity.Name+"Envelope"] = &openapi3.SchemaRef{
			Value: &openapi3.Schema{
				Type: &openapi3.Types{"object"},
				Properties: openapi3.Schemas{
					entity.Name: {Ref: "#/components/schemas/" + entity.Name},
					"time":      {Value: &openapi3.Schema{Type: &openapi3.Types{"string"}, Format: "date-time"}},
				},
			},
		}
	}
}

func addSharedEnvelopes(doc *openapi3.T) {
	// QueryResponse envelope
	doc.Components.Schemas["QueryResponseEnvelope"] = &openapi3.SchemaRef{
		Value: &openapi3.Schema{
			Type: &openapi3.Types{"object"},
			Properties: openapi3.Schemas{
				"QueryResponse": {Value: &openapi3.Schema{
					Type: &openapi3.Types{"object"},
					Properties: openapi3.Schemas{
						"startPosition": {Value: &openapi3.Schema{Type: &openapi3.Types{"integer"}}},
						"maxResults":    {Value: &openapi3.Schema{Type: &openapi3.Types{"integer"}}},
						"totalCount":    {Value: &openapi3.Schema{Type: &openapi3.Types{"integer"}}},
					},
				}},
				"time": {Value: &openapi3.Schema{Type: &openapi3.Types{"string"}, Format: "date-time"}},
			},
		},
	}

	// Report envelope
	doc.Components.Schemas["ReportEnvelope"] = &openapi3.SchemaRef{
		Value: &openapi3.Schema{
			Type: &openapi3.Types{"object"},
			Properties: openapi3.Schemas{
				"Header":  {Value: &openapi3.Schema{Type: &openapi3.Types{"object"}}},
				"Rows":    {Value: &openapi3.Schema{Type: &openapi3.Types{"object"}}},
				"Columns": {Value: &openapi3.Schema{Type: &openapi3.Types{"object"}}},
			},
		},
	}

	// Fault envelope
	doc.Components.Schemas["FaultEnvelope"] = &openapi3.SchemaRef{
		Value: &openapi3.Schema{
			Type: &openapi3.Types{"object"},
			Properties: openapi3.Schemas{
				"Fault": {Value: &openapi3.Schema{
					Type: &openapi3.Types{"object"},
					Properties: openapi3.Schemas{
						"type": {Value: &openapi3.Schema{Type: &openapi3.Types{"string"}}},
						"Error": {Value: &openapi3.Schema{
							Type: &openapi3.Types{"array"},
							Items: &openapi3.SchemaRef{Value: &openapi3.Schema{
								Type: &openapi3.Types{"object"},
								Properties: openapi3.Schemas{
									"Message": {Value: &openapi3.Schema{Type: &openapi3.Types{"string"}}},
									"Detail":  {Value: &openapi3.Schema{Type: &openapi3.Types{"string"}}},
									"code":    {Value: &openapi3.Schema{Type: &openapi3.Types{"string"}}},
								},
							}},
						}},
					},
				}},
				"time": {Value: &openapi3.Schema{Type: &openapi3.Types{"string"}, Format: "date-time"}},
			},
		},
	}
}

func addEntityPaths(doc *openapi3.T, entity *CorrelatedEntity) {
	for _, op := range entity.Operations {
		pathItem := doc.Paths.Value(op.Path)
		if pathItem == nil {
			pathItem = &openapi3.PathItem{}
		}

		operation := &openapi3.Operation{
			Tags:        []string{entity.Name},
			OperationID: buildOperationID(entity.Name, op.Type),
			Summary:     buildSummary(entity.Name, op.Type),
			Parameters: openapi3.Parameters{
				{Ref: "#/components/parameters/realmId"},
				{Ref: "#/components/parameters/minorversion"},
			},
			Responses: openapi3.NewResponses(),
		}

		// Response
		envelopeName := entity.Name + "Envelope"
		switch {
		case op.Type == OpQuery:
			envelopeName = "QueryResponseEnvelope"
		case entity.Category == CategoryReport:
			envelopeName = "ReportEnvelope"
		case op.Type == OpPDF:
			operation.Responses.Set("200", &openapi3.ResponseRef{
				Value: &openapi3.Response{
					Description: ptr("PDF document"),
					Content: openapi3.Content{
						"application/pdf": &openapi3.MediaType{
							Schema: &openapi3.SchemaRef{Value: &openapi3.Schema{
								Type:   &openapi3.Types{"string"},
								Format: "binary",
							}},
						},
					},
				},
			})
			goto setPath
		}

		operation.Responses.Set("200", &openapi3.ResponseRef{
			Value: &openapi3.Response{
				Description: ptr("Successful response"),
				Content: openapi3.NewContentWithJSONSchemaRef(
					&openapi3.SchemaRef{Ref: "#/components/schemas/" + envelopeName},
				),
			},
		})

		// Request body for mutating POST operations
		if op.Method == "POST" {
			schemaName := entity.Name
			if entity.RequestModel != nil && (op.Type == OpCreate || op.Type == OpUpdate) {
				if op.Type == OpCreate {
					schemaName = entity.Name + "CreateRequest"
				}
			}
			if op.Type == OpDelete || op.Type == OpVoid {
				// Delete/Void just need Id + SyncToken, use a minimal inline schema
				operation.RequestBody = &openapi3.RequestBodyRef{
					Value: openapi3.NewRequestBody().
						WithRequired(true).
						WithJSONSchemaRef(&openapi3.SchemaRef{Value: &openapi3.Schema{
							Type: &openapi3.Types{"object"},
							Properties: openapi3.Schemas{
								"Id":        {Value: &openapi3.Schema{Type: &openapi3.Types{"string"}}},
								"SyncToken": {Value: &openapi3.Schema{Type: &openapi3.Types{"string"}}},
							},
							Required: []string{"Id", "SyncToken"},
						}}),
					}
			} else {
				operation.RequestBody = &openapi3.RequestBodyRef{
					Value: openapi3.NewRequestBody().
						WithRequired(true).
						WithJSONSchemaRef(&openapi3.SchemaRef{
							Ref: "#/components/schemas/" + schemaName,
						}),
				}
			}
		}

	setPath:
		// {id} path parameter
		if strings.Contains(op.Path, "{id}") {
			operation.Parameters = append(operation.Parameters, &openapi3.ParameterRef{
				Value: &openapi3.Parameter{
					Name:     "id",
					In:       "path",
					Required: true,
					Schema: &openapi3.SchemaRef{Value: &openapi3.Schema{
						Type: &openapi3.Types{"string"},
					}},
				},
			})
		}

		// Query params for delete/void — sorted for deterministic output
		qpKeys := make([]string, 0, len(op.QueryParams))
		for k := range op.QueryParams {
			qpKeys = append(qpKeys, k)
		}
		sort.Strings(qpKeys)
		for _, k := range qpKeys {
			v := op.QueryParams[k]
			operation.Parameters = append(operation.Parameters, &openapi3.ParameterRef{
				Value: &openapi3.Parameter{
					Name:     k,
					In:       "query",
					Required: true,
					Schema: &openapi3.SchemaRef{Value: &openapi3.Schema{
						Type: &openapi3.Types{"string"},
						Enum: []any{v},
					}},
				},
			})
		}

		// Query-specific parameter
		if op.Type == OpQuery {
			operation.Parameters = append(operation.Parameters, &openapi3.ParameterRef{
				Value: &openapi3.Parameter{
					Name:        "query",
					In:          "query",
					Required:    true,
					Description: fmt.Sprintf("SQL-like query string, e.g. SELECT * FROM %s", entity.Name),
					Schema: &openapi3.SchemaRef{Value: &openapi3.Schema{
						Type: &openapi3.Types{"string"},
					}},
				},
			})
		}

		switch op.Method {
		case "GET":
			if pathItem.Get == nil {
				pathItem.Get = operation
			}
		case "POST":
			if pathItem.Post == nil {
				pathItem.Post = operation
			}
		}

		doc.Paths.Set(op.Path, pathItem)
	}
}

func buildOperationID(entity string, op OperationType) string {
	switch op {
	case OpCreate:
		return "create" + entity
	case OpRead:
		return "read" + entity
	case OpUpdate:
		return "update" + entity
	case OpDelete:
		return "delete" + entity
	case OpVoid:
		return "void" + entity
	case OpQuery:
		return "query" + entity
	case OpPDF:
		return "get" + entity + "PDF"
	case OpSend:
		return "send" + entity
	default:
		return strings.ToLower(entity)
	}
}

func buildSummary(entity string, op OperationType) string {
	switch op {
	case OpCreate:
		return "Create " + entity
	case OpRead:
		return "Read " + entity
	case OpUpdate:
		return "Update " + entity
	case OpDelete:
		return "Delete " + entity
	case OpVoid:
		return "Void " + entity
	case OpQuery:
		return "Query " + entity
	case OpPDF:
		return "Get " + entity + " as PDF"
	case OpSend:
		return "Send " + entity
	default:
		return entity
	}
}

func ptr(s string) *string { return &s }
