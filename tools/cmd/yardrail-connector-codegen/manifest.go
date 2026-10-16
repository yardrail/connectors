package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

var (
	errMissingSource            = errors.New("graphql interface requires source.operations and source.endpoint")
	errMissingRESTSource        = errors.New("rest interface source requires source.openapi and source.client")
	errUnresolvedSecurityScheme = errors.New("security_scheme not declared in manifest security_schemes or spec")
	errScalarsNotGraphQL        = errors.New("source.scalars is only valid on a graphql interface")
	errInvalidScalarType        = errors.New("source.scalars type must be string, integer, number, boolean, object or array")
	errDescriptionsNotGraphQL   = errors.New("source.descriptions is only valid on a graphql interface")
)

// Manifest is the top-level connector manifest structure.
type Manifest struct {
	Connector       ManifestConnector         `yaml:"connector"`
	SecuritySchemes map[string]SecurityScheme `yaml:"security_schemes"`
	Interfaces      []ManifestInterface       `yaml:"interfaces"`
}

// ManifestConnector identifies the connector.
type ManifestConnector struct {
	ID          string `yaml:"id"`
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
}

// ManifestInterface declares one communication surface within the connector.
type ManifestInterface struct {
	ID             string                `yaml:"id"`
	Name           string                `yaml:"name"`
	Protocol       string                `yaml:"protocol"`
	AuthMethod     string                `yaml:"auth_method"`
	AuthScope      string                `yaml:"auth_scope"`
	SecurityScheme string                `yaml:"security_scheme"`
	CredMapping    map[string]string     `yaml:"cred_mapping"`
	Headers        map[string]string     `yaml:"headers"`
	Capabilities   []string              `yaml:"capabilities"`
	DependsOn      []string              `yaml:"depends_on"`
	Callable       *bool                 `yaml:"callable"`
	ConfigParams   map[string]string     `yaml:"config_params"`
	ConfigFields   []ManifestConfigField `yaml:"config_fields"`
	MCPGroups      []string              `yaml:"mcp_groups"`
	Source         *ManifestSource       `yaml:"source"`
}

// ManifestSource declares where an interface's functions come from. GraphQL
// interfaces set Schema, Operations and Endpoint, Scalars to type the schema's
// custom scalars, and Descriptions to override operation descriptions, keyed
// by operation name; REST interfaces set OpenAPI and Client. File paths
// are relative to the manifest and resolved to absolute paths by parseManifest.
type ManifestSource struct {
	Schema       string            `yaml:"schema"`
	Operations   string            `yaml:"operations"`
	Endpoint     string            `yaml:"endpoint"`
	Scalars      map[string]string `yaml:"scalars"`
	Descriptions map[string]string `yaml:"descriptions"`
	OpenAPI      string            `yaml:"openapi"`
	Client       string            `yaml:"client"`
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

	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}

	m.resolveSourcePaths(filepath.Dir(absPath))

	return &m, nil
}

// resolveSourcePaths makes every source file path absolute, relative to dir.
func (m *Manifest) resolveSourcePaths(dir string) {
	for i := range m.Interfaces {
		src := m.Interfaces[i].Source
		if src == nil {
			continue
		}

		for _, p := range []*string{&src.Schema, &src.Operations, &src.OpenAPI} {
			if *p != "" && !filepath.IsAbs(*p) {
				*p = filepath.Join(dir, *p)
			}
		}
	}
}

// needsSpecFlags reports whether any callable OpenAPI-backed interface (any
// protocol but graphql) lacks a source block and so takes its spec and client
// from the -spec/-pkg flags.
func (m *Manifest) needsSpecFlags() bool {
	for _, iface := range m.Interfaces {
		if iface.IsCallable() && iface.Protocol != "graphql" && iface.Source == nil {
			return true
		}
	}

	return false
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

// validScalarTypes is the FieldInfo.Type vocabulary a declared custom scalar
// may map to.
var validScalarTypes = map[string]bool{
	"string": true, "integer": true, "number": true,
	"boolean": true, "object": true, "array": true,
}

func (m *Manifest) validate() error {
	if m.Connector.ID == "" {
		return fmt.Errorf("connector.id is required")
	}
	if m.Connector.Name == "" {
		return fmt.Errorf("connector.name is required")
	}

	hasCallable := false

	for i, iface := range m.Interfaces {
		if err := validateInterface(i, iface); err != nil {
			return err
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

func validateInterface(i int, iface ManifestInterface) error {
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

	return validateSource(iface)
}

// validateSource checks an interface's source block against its protocol.
func validateSource(iface ManifestInterface) error {
	src := iface.Source
	if iface.Protocol == "graphql" && (src == nil || src.Operations == "" || src.Endpoint == "") {
		return fmt.Errorf("interface %q: %w", iface.ID, errMissingSource)
	}
	if src == nil {
		return nil
	}
	if iface.Protocol == "rest" && (src.OpenAPI == "" || src.Client == "") {
		return fmt.Errorf("interface %q: %w", iface.ID, errMissingRESTSource)
	}

	if len(src.Descriptions) > 0 && iface.Protocol != "graphql" {
		return fmt.Errorf("interface %q: %w", iface.ID, errDescriptionsNotGraphQL)
	}

	return validateScalars(iface)
}

// validateScalars checks that only a graphql interface declares scalars and
// that each maps to a FieldInfo.Type name.
func validateScalars(iface ManifestInterface) error {
	if len(iface.Source.Scalars) > 0 && iface.Protocol != "graphql" {
		return fmt.Errorf("interface %q: %w", iface.ID, errScalarsNotGraphQL)
	}

	for name, typ := range iface.Source.Scalars {
		if !validScalarTypes[typ] {
			return fmt.Errorf("interface %q scalar %q type %q: %w", iface.ID, name, typ, errInvalidScalarType)
		}
	}

	return nil
}
