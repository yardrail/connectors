package main

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"
)

// Model represents a parsed model from the models.qbo section.
type Model struct {
	Key        string
	Properties map[string]*ModelProperty
	Required   []string
}

// ModelProperty represents a single property within a model.
type ModelProperty struct {
	Name         string
	Type         string
	Description  string
	RequiredFlag string
	ReadOnly     bool
	Filterable   bool
	Sortable     bool
	Refs         []ModelRef
	IsArray      bool
}

// ModelRef is a single entry from a property's $ref array.
type ModelRef struct {
	Key         string // lowercase key
	DisplayName string // PascalCase display name
}

// EntityCodes holds parsed example payloads for one entity.
type EntityCodes struct {
	EntityName string
	Operations map[string]string // "Create0-request" -> JSON string
}

// --- raw JSON structures for unmarshaling ---

type codesModelsFile struct {
	Models struct {
		QBO map[string]json.RawMessage `json:"qbo"`
	} `json:"models"`
	Codes struct {
		QBO map[string]json.RawMessage `json:"qbo"`
	} `json:"codes"`
}

type rawModel struct {
	Properties map[string]rawProperty `json:"properties"`
	Required   []string               `json:"Required"`
}

type rawProperty struct {
	Type         string              `json:"type"`
	Description  string              `json:"description"`
	RequiredFlag string              `json:"requiredFlag"`
	ReadOnly     bool                `json:"readOnly"`
	Filterable   bool                `json:"filterable"`
	Sortable     bool                `json:"sortable"`
	Ref          []map[string]string `json:"$ref"`
}

var arrayPattern = regexp.MustCompile(`\s*\[0\.\.n\]$`)

func parseCodesModels(path string) (map[string]*Model, map[string]*EntityCodes, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, fmt.Errorf("read %s: %w", path, err)
	}

	var file codesModelsFile
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, nil, fmt.Errorf("unmarshal %s: %w", path, err)
	}

	models := make(map[string]*Model)
	for key, raw := range file.Models.QBO {
		var rm rawModel
		if err := json.Unmarshal(raw, &rm); err != nil {
			continue
		}
		m := &Model{
			Key:        key,
			Properties: make(map[string]*ModelProperty),
			Required:   rm.Required,
		}
		for propName, rp := range rm.Properties {
			mp := &ModelProperty{
				Type:         rp.Type,
				Description:  stripHTML(rp.Description),
				RequiredFlag: rp.RequiredFlag,
				ReadOnly:     rp.ReadOnly,
				Filterable:   rp.Filterable,
				Sortable:     rp.Sortable,
			}
			cleanName := arrayPattern.ReplaceAllString(propName, "")
			if cleanName != propName {
				mp.IsArray = true
			}
			mp.Name = cleanName

			for _, refMap := range rp.Ref {
				for k, v := range refMap {
					mp.Refs = append(mp.Refs, ModelRef{Key: k, DisplayName: v})
				}
			}

			m.Properties[cleanName] = mp
		}
		// Clean Required array: strip [0..n] suffixes
		for i, r := range m.Required {
			m.Required[i] = arrayPattern.ReplaceAllString(r, "")
		}

		models[key] = m
	}

	codes := make(map[string]*EntityCodes)
	for key, raw := range file.Codes.QBO {
		var ops map[string]string
		if err := json.Unmarshal(raw, &ops); err != nil {
			continue
		}
		codes[key] = &EntityCodes{
			EntityName: key,
			Operations: ops,
		}
	}

	return models, codes, nil
}

var htmlTagPattern = regexp.MustCompile(`<[^>]*>`)
var literalSpanPattern = regexp.MustCompile(
	`<span\s+class="literal">([^<]*)</span>`,
)

func stripHTML(s string) string {
	s = literalSpanPattern.ReplaceAllString(s, "`$1`")
	s = strings.ReplaceAll(s, "<br/>", " ")
	s = strings.ReplaceAll(s, "<br>", " ")
	s = strings.ReplaceAll(s, "<br />", " ")
	s = htmlTagPattern.ReplaceAllString(s, "")
	s = strings.Join(strings.Fields(s), " ")
	return strings.TrimSpace(s)
}
