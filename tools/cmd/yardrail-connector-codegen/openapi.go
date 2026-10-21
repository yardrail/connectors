package main

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
)

// OperationInfo captures the typed input/output schema for one API operation.
type OperationInfo struct {
	OperationID string
	Description string
	Inputs      []FieldInfo
	Outputs     []FieldInfo
}

// FieldInfo describes one property in a request or response schema.
type FieldInfo struct {
	Name     string
	Type     string // "string", "integer", "number", "boolean", "array", "object"
	Required bool
	Doc      string
}

// parseOperations reads the full OpenAPI spec JSON and returns per-operation
// input/output schemas keyed by normalized operationId.
func parseOperations(specPath string) (map[string]*OperationInfo, error) {
	data, err := os.ReadFile(specPath)
	if err != nil {
		return nil, err
	}

	var raw rawOpenAPI
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parsing OpenAPI spec: %w", err)
	}

	resolver := newRefResolver(raw.Components.Schemas)
	ops := make(map[string]*OperationInfo)

	for _, methods := range raw.Paths {
		for _, op := range methods {
			if op.OperationID == "" {
				continue
			}

			info := &OperationInfo{
				OperationID: op.OperationID,
				Description: op.Summary,
			}
			if info.Description == "" {
				info.Description = op.Description
			}

			info.Inputs = collectInputs(op, resolver)
			info.Outputs = collectOutputs(op, resolver)

			key := normalizeOpID(op.OperationID)
			ops[key] = info
		}
	}

	return ops, nil
}

func collectInputs(op rawOperation, resolver *refResolver) []FieldInfo {
	var inputs []FieldInfo

	for _, p := range op.Parameters {
		if p.In == "header" {
			continue
		}
		typ := resolveType(p.Schema, resolver)
		inputs = append(inputs, FieldInfo{
			Name:     p.Name,
			Type:     typ,
			Required: p.Required,
			Doc:      p.Description,
		})
	}

	reqSchema := firstContentSchema(op.RequestBody.Content)
	if reqSchema != nil {
		requiredSet := toSet(reqSchema.Required)

		names := make([]string, 0, len(reqSchema.Properties))
		for name := range reqSchema.Properties {
			names = append(names, name)
		}

		sort.Strings(names)

		for _, name := range names {
			prop := reqSchema.Properties[name]
			typ := resolveType(prop, resolver)
			inputs = append(inputs, FieldInfo{
				Name:     name,
				Type:     typ,
				Required: requiredSet[name],
				Doc:      prop.Description,
			})
		}
	}

	return inputs
}

func collectOutputs(op rawOperation, resolver *refResolver) []FieldInfo {
	var schema *rawSchema
	for _, code := range []string{"200", "201"} {
		if resp, ok := op.Responses[code]; ok {
			schema = firstContentSchema(resp.Content)
			if schema != nil {
				break
			}
		}
	}
	if schema == nil {
		return nil
	}

	resolved := resolver.resolve(schema)
	if resolved.Properties == nil {
		return nil
	}

	requiredSet := toSet(resolved.Required)

	names := make([]string, 0, len(resolved.Properties))
	for name := range resolved.Properties {
		names = append(names, name)
	}

	sort.Strings(names)

	var outputs []FieldInfo

	for _, name := range names {
		prop := resolved.Properties[name]
		typ := resolveType(prop, resolver)
		outputs = append(outputs, FieldInfo{
			Name:     name,
			Type:     typ,
			Required: requiredSet[name],
			Doc:      prop.Description,
		})
	}

	return outputs
}

func resolveType(s rawSchema, resolver *refResolver) string {
	resolved := resolver.resolve(&s)

	switch resolved.Type {
	case "string":
		return "string"
	case "integer":
		return "integer"
	case "number":
		return "number"
	case "boolean":
		return "boolean"
	case "array":
		return "array"
	case "object":
		return "object"
	default:
		return "object"
	}
}

type refResolver struct {
	schemas map[string]rawSchema
}

func newRefResolver(schemas map[string]rawSchema) *refResolver {
	return &refResolver{schemas: schemas}
}

func (r *refResolver) resolve(s *rawSchema) *rawSchema {
	if s == nil {
		return &rawSchema{}
	}
	if s.Ref == "" {
		return s
	}
	name := refName(s.Ref)
	if resolved, ok := r.schemas[name]; ok {
		return &resolved
	}

	return &rawSchema{Type: "object"}
}

func refName(ref string) string {
	const prefix = "#/components/schemas/"
	if after, ok := strings.CutPrefix(ref, prefix); ok {
		return after
	}

	return ref
}

func firstContentSchema(content map[string]rawMediaType) *rawSchema {
	keys := make([]string, 0, len(content))
	for k := range content {
		keys = append(keys, k)
	}

	sort.Strings(keys)

	for _, k := range keys {
		mt := content[k]
		if mt.Schema.Type != "" || mt.Schema.Ref != "" || mt.Schema.Properties != nil {
			s := mt.Schema
			return &s
		}
	}

	return nil
}

func toSet(items []string) map[string]bool {
	m := make(map[string]bool, len(items))
	for _, item := range items {
		m[item] = true
	}

	return m
}

func normalizeOpID(id string) string {
	return strings.ToLower(strings.NewReplacer(
		"_", "",
		"-", "",
		".", "",
		" ", "",
	).Replace(id))
}

// --- Raw OpenAPI types for JSON unmarshalling ---

type rawOpenAPI struct {
	Paths      map[string]map[string]rawOperation `json:"paths"`
	Components struct {
		Schemas map[string]rawSchema `json:"schemas"`
	} `json:"components"`
}

type rawOperation struct {
	OperationID string                    `json:"operationId"`
	Summary     string                    `json:"summary"`
	Description string                    `json:"description"`
	Parameters  []rawParameter            `json:"parameters"`
	RequestBody rawRequestBody            `json:"requestBody"`
	Responses   map[string]rawResponse    `json:"responses"`
}

type rawParameter struct {
	Name        string    `json:"name"`
	In          string    `json:"in"`
	Required    bool      `json:"required"`
	Description string    `json:"description"`
	Schema      rawSchema `json:"schema"`
}

type rawRequestBody struct {
	Content map[string]rawMediaType `json:"content"`
}

type rawResponse struct {
	Content map[string]rawMediaType `json:"content"`
}

type rawMediaType struct {
	Schema rawSchema `json:"schema"`
}

type rawSchema struct {
	Ref         string               `json:"$ref"`
	Type        string               `json:"type"`
	Description string               `json:"description"`
	Properties  map[string]rawSchema `json:"properties"`
	Required    []string             `json:"required"`
	Items       *rawSchema           `json:"items"`
}
