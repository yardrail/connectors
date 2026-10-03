package main

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Manifest is the top-level connector manifest structure.
type Manifest struct {
	Connector  ManifestConnector   `yaml:"connector"`
	Interfaces []ManifestInterface `yaml:"interfaces"`
}

// ManifestConnector identifies the connector.
type ManifestConnector struct {
	ID          string `yaml:"id"`
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
}

// ManifestInterface declares one communication surface within the connector.
type ManifestInterface struct {
	ID             string               `yaml:"id"`
	Name           string               `yaml:"name"`
	Protocol       string               `yaml:"protocol"`
	AuthMethod     string               `yaml:"auth_method"`
	AuthScope      string               `yaml:"auth_scope"`
	SecurityScheme string               `yaml:"security_scheme"`
	Capabilities   []string             `yaml:"capabilities"`
	DependsOn      []string             `yaml:"depends_on"`
	Callable       *bool                `yaml:"callable"`
	ConfigFields   []ManifestConfigField `yaml:"config_fields"`
	MCPGroups      []string             `yaml:"mcp_groups"`
}

// ManifestConfigField declares a non-secret configuration parameter.
type ManifestConfigField struct {
	Name        string   `yaml:"name"`
	Type        string   `yaml:"type"`
	Label       string   `yaml:"label"`
	Description string   `yaml:"description"`
	Required    bool     `yaml:"required"`
	Options     []string `yaml:"options"`
}

// IsCallable reports whether the interface dispatches function calls.
// Defaults to true when not explicitly set.
func (i *ManifestInterface) IsCallable() bool {
	return i.Callable == nil || *i.Callable
}

func parseManifest(path string) (*Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var m Manifest
	if err := yaml.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parsing manifest: %w", err)
	}

	if err := m.validate(); err != nil {
		return nil, err
	}

	return &m, nil
}

var validProtocols = map[string]bool{
	"rest": true, "graphql": true, "webhook": true,
	"mcp": true, "smtp": true, "grpc": true,
}

var validAuthMethods = map[string]bool{
	"oauth2_user": true, "oauth2_client_credentials": true,
	"api_key": true, "basic": true, "none": true,
}

var validAuthScopes = map[string]bool{"org": true, "user": true}

func (m *Manifest) validate() error {
	if m.Connector.ID == "" {
		return fmt.Errorf("connector.id is required")
	}
	if m.Connector.Name == "" {
		return fmt.Errorf("connector.name is required")
	}

	hasCallable := false

	for i, iface := range m.Interfaces {
		if iface.ID == "" {
			return fmt.Errorf("interfaces[%d].id is required", i)
		}
		if !validProtocols[iface.Protocol] {
			return fmt.Errorf("interfaces[%d].protocol %q is not valid", i, iface.Protocol)
		}
		if !validAuthMethods[iface.AuthMethod] {
			return fmt.Errorf("interfaces[%d].auth_method %q is not valid", i, iface.AuthMethod)
		}
		if !validAuthScopes[iface.AuthScope] {
			return fmt.Errorf("interfaces[%d].auth_scope %q is not valid", i, iface.AuthScope)
		}
		if iface.IsCallable() {
			hasCallable = true
		}
	}

	if !hasCallable {
		return fmt.Errorf("at least one interface must be callable")
	}

	return nil
}
