package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/yardrail/connectors/tools/internal/introspect"
)

// SpecInfo holds the subset of an OpenAPI spec relevant to code generation.
type SpecInfo struct {
	Servers         []ServerInfo
	SecuritySchemes map[string]SecurityScheme
	TagMap          map[string][]string
}

// ServerInfo describes one entry from the spec's servers array.
type ServerInfo struct {
	URL       string
	Variables map[string]ServerVariable
}

// ServerVariable captures a templated variable from a server URL.
type ServerVariable struct {
	Default     string
	Description string
	Enum        []string
}

// SecurityScheme captures what we need from an OpenAPI securityScheme.
type SecurityScheme struct {
	Type   string // "oauth2", "apiKey", "http"
	Scheme string // "bearer", "basic" (for type=http)
	In     string // "header", "query" (for type=apiKey)
	Name   string // header/query param name (for type=apiKey)
}

func parseSpec(path string) (*SpecInfo, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var raw struct {
		Servers []struct {
			URL       string `json:"url"`
			Variables map[string]struct {
				Default     string   `json:"default"`
				Description string   `json:"description"`
				Enum        []string `json:"enum"`
			} `json:"variables"`
		} `json:"servers"`
		Components struct {
			SecuritySchemes map[string]struct {
				Type   string `json:"type"`
				Scheme string `json:"scheme"`
				In     string `json:"in"`
				Name   string `json:"name"`
			} `json:"securitySchemes"`
		} `json:"components"`
	}

	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parsing spec: %w", err)
	}

	info := &SpecInfo{
		SecuritySchemes: make(map[string]SecurityScheme),
	}

	for _, s := range raw.Servers {
		si := ServerInfo{URL: s.URL, Variables: make(map[string]ServerVariable)}
		for k, v := range s.Variables {
			si.Variables[k] = ServerVariable{
				Default:     v.Default,
				Description: v.Description,
				Enum:        v.Enum,
			}
		}
		info.Servers = append(info.Servers, si)
	}

	for k, v := range raw.Components.SecuritySchemes {
		info.SecuritySchemes[k] = SecurityScheme{
			Type:   v.Type,
			Scheme: v.Scheme,
			In:     v.In,
			Name:   v.Name,
		}
	}

	info.TagMap, err = introspect.ParseSpecTags(path)
	if err != nil {
		return nil, err
	}

	return info, nil
}

// resolveBaseURLTemplate substitutes server URL variables with their defaults.
func resolveBaseURLTemplate(server ServerInfo) string {
	url := server.URL
	for name, v := range server.Variables {
		url = strings.ReplaceAll(url, "{"+name+"}", v.Default)
	}
	return url
}
