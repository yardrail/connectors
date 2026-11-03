package main

import (
	"bytes"
	"fmt"
	"go/format"
	"strings"
	"text/template"

	"github.com/yardrail/connectors/tools/internal/introspect"
)

// genContext is the data passed to all templates.
type genContext struct {
	ConnectorID    string
	PkgName        string
	ClientPkgPath  string
	Manifest       *Manifest
	Spec           *SpecInfo
	Methods        []introspect.MethodInfo
	Functions      []connectorFunction
	CallableIfaces []ManifestInterface
	// RESTIfaces are the callable interfaces backed by the OpenAPI client.
	RESTIfaces []ManifestInterface
	// GraphQLIfaces and GraphQLFunctions are the callable GraphQL interfaces
	// and the operations they serve.
	GraphQLIfaces    []ManifestInterface
	GraphQLFunctions []graphqlFunction
	BaseURL          string
	AuthStrategy     map[string]authStrategy
	ConfigParams     map[string]string
}

// connectorFunction is one callable connector function, independent of the
// protocol that backs it. The CWL schema and class mapping render from these.
type connectorFunction struct {
	Name        string // kebab-case function name
	ClassName   string // CWL class name
	MethodName  string // Go method name for dispatch (REST only)
	InterfaceID string // interface that serves the function; empty when every callable interface does
	Op          *OperationInfo
}

type authStrategy struct {
	SecurityScheme SecurityScheme
	CredKey        string
	UsernameField  string
	PasswordField  string
}

// mergeSecuritySchemes combines spec-supplied and manifest-declared security
// schemes. A manifest scheme replaces a spec scheme of the same name.
func mergeSecuritySchemes(spec, manifest map[string]SecurityScheme) map[string]SecurityScheme {
	merged := make(map[string]SecurityScheme, len(spec)+len(manifest))
	for k, v := range spec {
		merged[k] = v
	}

	for k, v := range manifest {
		merged[k] = v
	}

	return merged
}

// deriveAuthStrategy maps each callable interface's security_scheme to concrete
// auth wiring based on the merged security schemes. A security_scheme that is
// set but not in schemes is an error.
func deriveAuthStrategy(manifest *Manifest, schemes map[string]SecurityScheme) (map[string]authStrategy, error) {
	strategies := make(map[string]authStrategy)

	for _, iface := range manifest.Interfaces {
		if !iface.IsCallable() || iface.SecurityScheme == "" {
			continue
		}

		scheme, ok := schemes[iface.SecurityScheme]
		if !ok {
			return nil, fmt.Errorf("interface %q: %w: %q", iface.ID, errUnresolvedSecurityScheme, iface.SecurityScheme)
		}

		credKey := mapCredField(iface.CredMapping, "access_token", "access_token")

		switch {
		case scheme.Type == "apiKey":
			credKey = mapCredField(iface.CredMapping, scheme.Name, scheme.Name)
		case scheme.Type == "http" && scheme.Scheme == "basic":
			credKey = "username"
		}

		usernameField := mapCredField(iface.CredMapping, "username", "username")
		passwordField := mapCredField(iface.CredMapping, "password", "password")

		strategies[iface.ID] = authStrategy{
			SecurityScheme: scheme,
			CredKey:        credKey,
			UsernameField:  usernameField,
			PasswordField:  passwordField,
		}
	}

	return strategies, nil
}

func mapCredField(mapping map[string]string, key, defaultVal string) string {
	if v, ok := mapping[key]; ok {
		return v
	}

	return defaultVal
}

func renderFile(tmplStr string, ctx *genContext) ([]byte, error) {
	tmpl, err := template.New("gen").Funcs(tmplFuncs).Parse(tmplStr)
	if err != nil {
		return nil, fmt.Errorf("parsing template: %w", err)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, ctx); err != nil {
		return nil, fmt.Errorf("executing template: %w", err)
	}

	formatted, err := format.Source(buf.Bytes())
	if err != nil {
		return nil, fmt.Errorf("formatting: %w\nraw output:\n%s", err, buf.String())
	}

	return formatted, nil
}

// goIdent converts a kebab/snake-case string to a PascalCase Go identifier.
// e.g. "bot-api" → "BotApi", "user_token" → "UserToken"
func goIdent(s string) string {
	r := strings.NewReplacer("-", " ", "_", " ")
	parts := strings.Fields(r.Replace(s))

	var b strings.Builder
	for _, p := range parts {
		if len(p) > 0 {
			b.WriteString(strings.ToUpper(p[:1]))
			b.WriteString(p[1:])
		}
	}

	return b.String()
}

// authMethodConst maps a manifest auth_method value to the Go constant name.
func authMethodConst(s string) string {
	switch s {
	case "oauth2_user":
		return "AuthMethodOAuth2User"
	case "oauth2_client_credentials":
		return "AuthMethodOAuth2ClientCredentials"
	case "api_key":
		return "AuthMethodAPIKey"
	case "basic":
		return "AuthMethodBasic"
	case "none":
		return "AuthMethodNone"
	default:
		return "AuthMethod" + goIdent(s)
	}
}

// authScopeConst maps a manifest auth_scope value to the Go constant name.
func authScopeConst(s string) string {
	switch s {
	case "org":
		return "AuthScopeOrg"
	case "user":
		return "AuthScopeUser"
	default:
		return "AuthScope" + goIdent(s)
	}
}

// protocolConst maps a manifest protocol value to the Go constant name.
func protocolConst(s string) string {
	switch s {
	case "rest":
		return "ProtocolREST"
	case "graphql":
		return "ProtocolGraphQL"
	case "webhook":
		return "ProtocolWebhook"
	case "mcp":
		return "ProtocolMCP"
	case "smtp":
		return "ProtocolSMTP"
	case "grpc":
		return "ProtocolGRPC"
	default:
		return "Protocol" + goIdent(s)
	}
}

func escDesc(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", "")

	return s
}

// authSchemeType returns the effective auth type for template branching.
// "oauth2" and "http/bearer" both resolve to "bearer".
func authSchemeType(strat authStrategy) string {
	switch {
	case strat.SecurityScheme.Type == "oauth2":
		return "bearer"
	case strat.SecurityScheme.Type == "http" && strat.SecurityScheme.Scheme == "bearer":
		return "bearer"
	case strat.SecurityScheme.Type == "http" && strat.SecurityScheme.Scheme == "basic":
		return "basic"
	case strat.SecurityScheme.Type == "apiKey" && strat.SecurityScheme.In == "header":
		return "apikey_header"
	case strat.SecurityScheme.Type == "apiKey":
		return "apikey_query"
	default:
		return "bearer"
	}
}

// clientPkgName extracts the last path segment of a Go import path.
func clientPkgName(importPath string) string {
	idx := strings.LastIndex(importPath, "/")
	if idx < 0 {
		return importPath
	}

	return importPath[idx+1:]
}

var tmplFuncs = template.FuncMap{
	"goIdent":         goIdent,
	"authMethodConst": authMethodConst,
	"authScopeConst":  authScopeConst,
	"protocolConst":   protocolConst,
	"escDesc":         escDesc,
	"trimPrefix":      strings.TrimPrefix,
	"kebab":           introspect.ToKebab,
	"authSchemeType":  authSchemeType,
	"clientPkgName":   clientPkgName,
	"goString":        goStringLiteral,
	"fixTargetRef": func(clientPkgPath, expr string) string {
		pkg := clientPkgName(clientPkgPath)

		return strings.ReplaceAll(expr, "target.", pkg+".")
	},
	"needsUUID": func(methods []introspect.MethodInfo) bool {
		for _, m := range methods {
			for _, p := range m.Params {
				if p.TypeConv == "parseUUID" {
					return true
				}
			}

			for _, a := range m.CallArgs {
				if strings.Contains(a.Expr, "parseUUID") {
					return true
				}
			}
		}

		return false
	},
	"strategyFor": func(strategies map[string]authStrategy, id string) authStrategy {
		return strategies[id]
	},
	"configParam": func(configParams map[string]string, jsonName string) string {
		return configParams[jsonName]
	},
	"usesConfig": func(configParams map[string]string, m introspect.MethodInfo) bool {
		for _, p := range m.Params {
			if p.Required && !p.IsStruct && configParams[p.JSONName] != "" {
				return true
			}
		}

		return false
	},
	"opDescription": func(op *OperationInfo) string {
		if op == nil {
			return ""
		}

		return op.Description
	},
	"opInputs": func(op *OperationInfo) []FieldInfo {
		if op == nil {
			return nil
		}

		return op.Inputs
	},
	"opOutputs": func(op *OperationInfo) []FieldInfo {
		if op == nil {
			return nil
		}

		return op.Outputs
	},
	"configFieldTypeConst": func(s string) string {
		switch s {
		case "string":
			return "ConfigFieldTypeString"
		case "url":
			return "ConfigFieldTypeURL"
		case "number":
			return "ConfigFieldTypeNumber"
		case "select":
			return "ConfigFieldTypeSelect"
		default:
			return "ConfigFieldTypeString"
		}
	},
}

// ---- Templates ----

const handlerTemplate = `// Code generated by yardrail-connector-codegen. DO NOT EDIT.

package {{.PkgName}}

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"{{.ClientPkgPath}}"
)

// InterfaceClient wraps an authenticated oapi-codegen client for one interface.
type InterfaceClient struct {
	cl *{{clientPkgName .ClientPkgPath}}.ClientWithResponses
}

` + restDispatchSwitchTemplate + `
// Connector holds an authenticated client for each configured interface.
type Connector struct {
	clients map[string]*InterfaceClient
}

// NewConnector constructs a Connector from per-interface credentials and config.
// When httpClient is non-nil it is used for all outbound HTTP calls.
func NewConnector(creds, config map[string]map[string]string, httpClient *http.Client) (*Connector, error) {
` + openAPIClientsTemplate + `	return &Connector{clients: clients}, nil
}

` + openAPIAccessorsTemplate + authEditorsTemplate

// authEditorsTemplate renders one request editor per callable interface that
// applies its auth scheme and static headers. Every handler template uses it.
const authEditorsTemplate = `{{range .CallableIfaces}}{{$strat := strategyFor $.AuthStrategy .ID}}
func authEditor{{goIdent .ID}}(creds map[string]string) func(context.Context, *http.Request) error {
	return func(_ context.Context, req *http.Request) error {
{{- if eq (authSchemeType $strat) "bearer"}}
		if token := creds["{{$strat.CredKey}}"]; token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
{{- else if eq (authSchemeType $strat) "basic"}}
		req.SetBasicAuth(creds["{{$strat.UsernameField}}"], creds["{{$strat.PasswordField}}"])
{{- else if eq (authSchemeType $strat) "apikey_header"}}
		if key := creds["{{$strat.CredKey}}"]; key != "" {
			req.Header.Set("{{$strat.SecurityScheme.Name}}", key)
		}
{{- else if eq (authSchemeType $strat) "apikey_query"}}
		if key := creds["{{$strat.CredKey}}"]; key != "" {
			q := req.URL.Query()
			q.Set("{{$strat.SecurityScheme.Name}}", key)
			req.URL.RawQuery = q.Encode()
		}
{{- end}}
{{- range $k, $v := .Headers}}
		req.Header.Set("{{$k}}", "{{$v}}")
{{- end}}

		return nil
	}
}
{{end}}

`

const dispatchTemplate = `// Code generated by yardrail-connector-codegen. DO NOT EDIT.

// Package {{.PkgName}} is a generated connector for {{.Manifest.Connector.Name}}.
package {{.PkgName}}

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
{{- if needsUUID .Methods}}

	"github.com/google/uuid"
{{- end}}

	"{{.ClientPkgPath}}"
)

` + openAPIUUIDTemplate + `
var (
	errInterfaceNotConfigured = errors.New("{{.ConnectorID}}: interface not configured")
	errUnknownFunction        = errors.New("{{.ConnectorID}}: unknown function")
)

` + resolveTemplate + openAPIMethodsTemplate

// openAPIClientsTemplate builds the clients map in NewConnector, one
// oapi-codegen client per interface in .RESTIfaces.
const openAPIClientsTemplate = `	clients := make(map[string]*InterfaceClient, {{len .RESTIfaces}})
{{range .RESTIfaces}}
	if ifaceCreds, ok := creds["{{.ID}}"]; ok {
		baseURL := resolveBaseURL(config["{{.ID}}"])
		authFn := authEditor{{goIdent .ID}}(ifaceCreds)

		opts := []{{clientPkgName $.ClientPkgPath}}.ClientOption{ {{clientPkgName $.ClientPkgPath}}.WithRequestEditorFn(authFn)}
		if httpClient != nil {
			opts = append(opts, {{clientPkgName $.ClientPkgPath}}.WithHTTPClient(httpClient))
		}

		cl, err := {{clientPkgName $.ClientPkgPath}}.NewClientWithResponses(baseURL, opts...)
		if err != nil {
			return nil, fmt.Errorf("creating {{.ID}} client: %w", err)
		}

		clients["{{.ID}}"] = &InterfaceClient{cl: cl}
	}
{{end}}
`

// openAPIAccessorsTemplate renders the typed client accessor and base URL
// resolution for OpenAPI interfaces.
const openAPIAccessorsTemplate = `// Client returns the interface client for the given interface ID.
// Returns nil if the interface was not configured with credentials.
func (c *Connector) Client(interfaceID string) *InterfaceClient {
	return c.clients[interfaceID]
}

func resolveBaseURL(config map[string]string) string {
	if u, ok := config["base_url"]; ok && u != "" {
		return u
	}

	return "{{.BaseURL}}"
}

`

// openAPIAccessorsBaseURLTemplate renders only resolveBaseURL (used by mixed
// template which defines its own Client accessor).
const openAPIAccessorsBaseURLTemplate = `func resolveBaseURL(config map[string]string) string {
	if u, ok := config["base_url"]; ok && u != "" {
		return u
	}

	return "{{.BaseURL}}"
}

`

// openAPIUUIDTemplate renders parseUUID when a method takes a UUID.
const openAPIUUIDTemplate = `{{- if needsUUID .Methods}}

func parseUUID(s string) uuid.UUID {
	id, _ := uuid.Parse(s)

	return id
}
{{- end}}
`

// openAPIMethodsTemplate renders one dispatch method per method on InterfaceClient.
const openAPIMethodsTemplate = `{{range .Methods}}{{$m := .}}
{{- /* Determine what args this method needs */ -}}
{{- $hasBody := false -}}
{{- $hasInputPathParams := false -}}
{{- $bodyJSONName := "" -}}
{{- $bodyType := "" -}}
{{- range .Params -}}
  {{- if .Required -}}
    {{- if .IsStruct -}}
      {{- $hasBody = true -}}
      {{- $bodyJSONName = .JSONName -}}
      {{- $bodyType = (trimPrefix .FullType "target.") -}}
    {{- else -}}
      {{- if eq (configParam $.ConfigParams .JSONName) "" -}}
        {{- $hasInputPathParams = true -}}
      {{- end -}}
    {{- end -}}
  {{- end -}}
{{- end}}

func (ic *InterfaceClient) dispatch{{.MethodName}}(ctx context.Context, {{if usesConfig $.ConfigParams .}}config{{else}}_{{end}} map[string]string, inputs json.RawMessage) (any, error) {
{{- range .Params}}{{if and .Required (not .IsStruct)}}
{{- $cp := configParam $.ConfigParams .JSONName}}
{{- if ne $cp ""}}
	{{.JSONName}} := config["{{$cp}}"]
{{- end}}
{{- end}}{{end}}
{{- if $hasInputPathParams}}
	var pathArgs map[string]json.RawMessage

	pathErr := json.Unmarshal(inputs, &pathArgs)
	if pathErr != nil {
		return nil, fmt.Errorf("{{$.ConnectorID}}.{{$m.Name}}: %w", pathErr)
	}
{{range .Params}}{{if and .Required (not .IsStruct)}}
{{- if eq (configParam $.ConfigParams .JSONName) ""}}
	var {{.JSONName}} {{.GoType}}

	if raw, ok := pathArgs["{{.JSONName}}"]; ok {
		paramErr := json.Unmarshal(raw, &{{.JSONName}})
		if paramErr != nil {
			return nil, fmt.Errorf("{{$.ConnectorID}}.{{$m.Name}}: param {{.JSONName}}: %w", paramErr)
		}
	}
{{- end}}
{{end}}{{end}}
{{- end}}
{{- if $hasBody}}
	var body {{clientPkgName $.ClientPkgPath}}.{{$bodyType}}

	bodyErr := json.Unmarshal(inputs, &body)
	if bodyErr != nil {
		return nil, fmt.Errorf("{{$.ConnectorID}}.{{$m.Name}}: %w", bodyErr)
	}
{{end}}
{{- if .ParamsType}}
	var params {{clientPkgName $.ClientPkgPath}}.{{.ParamsType}}

	paramsErr := json.Unmarshal(inputs, &params)
	if paramsErr != nil {
		return nil, fmt.Errorf("{{$.ConnectorID}}.{{$m.Name}}: params: %w", paramsErr)
	}
{{end}}
	resp, callErr := ic.cl.{{$m.MethodName}}(ctx, {{range .CallArgs}}{{if .IsParams}}&params{{else if eq .Expr "params"}}&params{{else if eq .Expr "body"}}body{{else}}{{fixTargetRef $.ClientPkgPath .Expr}}{{end}}, {{end}})
	if callErr != nil {
		return nil, callErr
	}

	if resp == nil {
		return nil, fmt.Errorf("{{$.ConnectorID}}.{{$m.Name}}: nil response")
	}

	if resp.HTTPResponse != nil && resp.HTTPResponse.StatusCode >= 400 {
		return nil, fmt.Errorf("{{$.ConnectorID}}.{{$m.Name}}: HTTP %d: %s", resp.HTTPResponse.StatusCode, string(resp.Body))
	}

	return json.RawMessage(resp.Body), nil
}
{{end}}
`

// restDispatchSwitchTemplate renders the Dispatch method as a switch on function name.
const restDispatchSwitchTemplate = `// Dispatch calls the named function's typed client method.
func (ic *InterfaceClient) Dispatch(ctx context.Context, function string, config map[string]string, inputs json.RawMessage) (any, error) {
	switch function {
{{range .Functions}}	case "{{.Name}}":
		return ic.dispatch{{.MethodName}}(ctx, config, inputs)
{{end}}	default:
		return nil, fmt.Errorf("%w: %q", errUnknownFunction, function)
	}
}
`

// resolveTemplate renders the Resolve function (shared across all protocol templates).
const resolveTemplate = `// Resolve creates a ready-to-call dispatch function for the given interface.
func Resolve(
	interfaceID, function string,
	creds, config map[string]string,
	httpClient *http.Client,
) (func(context.Context, json.RawMessage) (any, error), error) {
	c, err := NewConnector(
		map[string]map[string]string{interfaceID: creds},
		map[string]map[string]string{interfaceID: config},
		httpClient,
	)
	if err != nil {
		return nil, err
	}

	cl := c.Client(interfaceID)
	if cl == nil {
		return nil, fmt.Errorf("%w: %q", errInterfaceNotConfigured, interfaceID)
	}

	return func(ctx context.Context, inputs json.RawMessage) (any, error) {
		return cl.Dispatch(ctx, function, config, inputs)
	}, nil
}
`

const registrationTemplate = `// Code generated by yardrail-connector-codegen. DO NOT EDIT.

package {{.PkgName}}

import "github.com/yardrail/connectors/connector"

// Register returns the bundled registration for this connector.
func Register() connector.Registration {
	return connector.Registration{
		Definition: connector.Definition{
			ID:          "{{.Manifest.Connector.ID}}",
			Name:        "{{.Manifest.Connector.Name}}",
			Description: "{{escDesc .Manifest.Connector.Description}}",
			Interfaces: []connector.Interface{
{{range .Manifest.Interfaces}}				{
					ID:           "{{.ID}}",
					Name:         "{{.Name}}",
					Protocol:     connector.{{protocolConst .Protocol}},
					AuthMethod:   connector.{{authMethodConst .AuthMethod}},
					DependsOn:    {{if .DependsOn}}[]string{ {{range .DependsOn}}"{{.}}", {{end}} }{{else}}nil{{end}},
					ConfigFields: {{if .ConfigFields}}[]connector.ConfigField{
{{range .ConfigFields}}						{
							Name:        "{{.Name}}",
							Type:        connector.{{configFieldTypeConst .Type}},
							Label:       "{{.Label}}",
							Description: "{{escDesc .Description}}",
							Required:    {{.Required}},
						},
{{end}}					}{{else}}nil{{end}},
				},
{{end}}			},
		},
		Resolve: Resolve,
		CWLClasses: []connector.CWLClassEntry{
{{- range .Functions}}
			{ClassIRI: "https://yardrail.com/cwl/{{$.ConnectorID}}#{{.ClassName}}", Info: connector.CWLClassInfo{
				ConnectorID: "{{$.ConnectorID}}",
				Function:    "{{.Name}}",
				ClassName:   "{{.ClassName}}",
				Description: "{{escDesc (opDescription .Op)}}",
				Inputs: []connector.CWLFieldInfo{
{{- range opInputs .Op}}
					{Name: "{{.Name}}", Type: "{{.Type}}", Required: {{.Required}}, Description: "{{escDesc .Doc}}"},
{{- end}}
				},
				Outputs: []connector.CWLFieldInfo{
{{- range opOutputs .Op}}
					{Name: "{{.Name}}", Type: "{{.Type}}", Required: {{.Required}}, Description: "{{escDesc .Doc}}"},
{{- end}}
				},
			}},
{{- end}}
		},
	}
}

// Ensure Resolve matches the connector.Resolver type at compile time.
var _ connector.Resolver = Resolve
`
