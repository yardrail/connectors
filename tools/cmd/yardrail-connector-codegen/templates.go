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
	CallableIfaces []ManifestInterface
	BaseURL        string
	AuthStrategy   map[string]authStrategy
}

type authStrategy struct {
	SecurityScheme SecurityScheme
	CredKey        string
	UsernameField  string
	PasswordField  string
}

// deriveAuthStrategy maps each callable interface's security_scheme to concrete
// auth wiring based on the OpenAPI securitySchemes.
func deriveAuthStrategy(manifest *Manifest, spec *SpecInfo) map[string]authStrategy {
	strategies := make(map[string]authStrategy)

	for _, iface := range manifest.Interfaces {
		if !iface.IsCallable() || iface.SecurityScheme == "" {
			continue
		}

		scheme, ok := spec.SecuritySchemes[iface.SecurityScheme]
		if !ok {
			continue
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

	return strategies
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
	"clientPkgName": clientPkgName,
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
	"fmt"
	"net/http"

	"{{.ClientPkgPath}}"
)

// Connector wraps authenticated oapi-codegen clients per interface.
// Use Client() for typed access to the full API surface.
type Connector struct {
	clients map[string]*{{clientPkgName .ClientPkgPath}}.ClientWithResponses
}

// NewConnector constructs a Connector from per-interface credentials and config.
func NewConnector(creds, config map[string]map[string]string) (*Connector, error) {
	clients := make(map[string]*{{clientPkgName .ClientPkgPath}}.ClientWithResponses, {{len .CallableIfaces}})
{{range .CallableIfaces}}
	if ifaceCreds, ok := creds["{{.ID}}"]; ok {
		baseURL := resolveBaseURL(config["{{.ID}}"])
		authFn := authEditor{{goIdent .ID}}(ifaceCreds)

		c, err := {{clientPkgName $.ClientPkgPath}}.NewClientWithResponses(baseURL, {{clientPkgName $.ClientPkgPath}}.WithRequestEditorFn(authFn))
		if err != nil {
			return nil, fmt.Errorf("creating {{.ID}} client: %w", err)
		}

		clients["{{.ID}}"] = c
	}
{{end}}
	return &Connector{clients: clients}, nil
}

// Client returns the typed oapi-codegen client for the given interface.
// Returns nil if the interface was not configured with credentials.
func (c *Connector) Client(interfaceID string) *{{clientPkgName .ClientPkgPath}}.ClientWithResponses {
	return c.clients[interfaceID]
}

func resolveBaseURL(config map[string]string) string {
	if u, ok := config["base_url"]; ok && u != "" {
		return u
	}

	return "{{.BaseURL}}"
}

{{range .CallableIfaces}}{{$strat := strategyFor $.AuthStrategy .ID}}
func authEditor{{goIdent .ID}}(creds map[string]string) func(context.Context, *http.Request) error {
	return func(_ context.Context, req *http.Request) error {
{{- if eq (authSchemeType $strat) "bearer"}}
		if token := creds["access_token"]; token != "" {
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
{{- if needsUUID .Methods}}

	"github.com/google/uuid"
{{- end}}

	"{{.ClientPkgPath}}"
)

{{- if needsUUID .Methods}}

func parseUUID(s string) uuid.UUID {
	id, _ := uuid.Parse(s)

	return id
}
{{- end}}

var (
	errInterfaceNotConfigured = errors.New("{{.ConnectorID}}: interface not configured")
	errUnknownFunction        = errors.New("{{.ConnectorID}}: unknown function")
)

// Dispatcher deserializes JSON inputs into typed args, calls a typed client
// method, and returns the typed response. This is the serialization boundary
// for Temporal/CWL — typed callers use Client() directly instead.
type Dispatcher func(
	ctx context.Context,
	cl *{{clientPkgName .ClientPkgPath}}.ClientWithResponses,
	inputs json.RawMessage,
) (any, error)

// Dispatchers maps function names to their typed dispatch functions.
var Dispatchers = map[string]Dispatcher{
{{range .Methods}}	"{{.Name}}": dispatch{{.MethodName}},
{{end}}}

// Resolve constructs a Connector and returns a ready-to-call function for the
// given interface and function name. The returned function deserializes JSON
// inputs and calls the typed client method.
func Resolve(
	interfaceID, function string,
	creds, config map[string]string,
) (func(context.Context, json.RawMessage) (any, error), error) {
	perIfaceCreds := map[string]map[string]string{interfaceID: creds}
	perIfaceConfig := map[string]map[string]string{interfaceID: config}

	c, err := NewConnector(perIfaceCreds, perIfaceConfig)
	if err != nil {
		return nil, err
	}

	cl := c.Client(interfaceID)
	if cl == nil {
		return nil, fmt.Errorf("%w: %q", errInterfaceNotConfigured, interfaceID)
	}

	d, ok := Dispatchers[function]
	if !ok {
		return nil, fmt.Errorf("%w: %q", errUnknownFunction, function)
	}

	return func(ctx context.Context, inputs json.RawMessage) (any, error) {
		return d(ctx, cl, inputs)
	}, nil
}

{{range .Methods}}{{$m := .}}
{{- /* Determine what args this method needs */ -}}
{{- $hasBody := false -}}
{{- $hasPathParams := false -}}
{{- $bodyJSONName := "" -}}
{{- $bodyType := "" -}}
{{- range .Params -}}
  {{- if .Required -}}
    {{- if .IsStruct -}}
      {{- $hasBody = true -}}
      {{- $bodyJSONName = .JSONName -}}
      {{- $bodyType = (trimPrefix .FullType "target.") -}}
    {{- else -}}
      {{- $hasPathParams = true -}}
    {{- end -}}
  {{- end -}}
{{- end}}

func dispatch{{.MethodName}}(ctx context.Context, cl *{{clientPkgName $.ClientPkgPath}}.ClientWithResponses, inputs json.RawMessage) (any, error) {
{{- if $hasPathParams}}
	var pathArgs map[string]json.RawMessage

	pathErr := json.Unmarshal(inputs, &pathArgs)
	if pathErr != nil {
		return nil, fmt.Errorf("{{$.ConnectorID}}.{{$m.Name}}: %w", pathErr)
	}
{{range .Params}}{{if and .Required (not .IsStruct)}}
	var {{.JSONName}} {{.GoType}}

	if raw, ok := pathArgs["{{.JSONName}}"]; ok {
		paramErr := json.Unmarshal(raw, &{{.JSONName}})
		if paramErr != nil {
			return nil, fmt.Errorf("{{$.ConnectorID}}.{{$m.Name}}: param {{.JSONName}}: %w", paramErr)
		}
	}
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
	return cl.{{$m.MethodName}}(ctx, {{range .CallArgs}}{{if .IsParams}}&params{{else if eq .Expr "params"}}&params{{else if eq .Expr "body"}}body{{else}}{{fixTargetRef $.ClientPkgPath .Expr}}{{end}}, {{end}})
}
{{end}}
`

const catalogTemplate = `// Code generated by yardrail-connector-codegen. DO NOT EDIT.

package connector

func {{goIdent .ConnectorID}}Connector() Connector {
	return Connector{
		ID:           "{{.Manifest.Connector.ID}}",
		Name:         "{{.Manifest.Connector.Name}}",
		Description:  "{{escDesc .Manifest.Connector.Description}}",
		AdminEnabled: true,
		Interfaces: []ConnectorInterface{
{{range .Manifest.Interfaces}}			{
				ID:           "{{.ID}}",
				Name:         "{{.Name}}",
				Protocol:     {{protocolConst .Protocol}},
				AuthMethod:   {{authMethodConst .AuthMethod}},
				AuthScope:    {{authScopeConst .AuthScope}},
				Capabilities: []Capability{ {{range .Capabilities}}"{{.}}", {{end}} },
				DependsOn:    {{if .DependsOn}}[]string{ {{range .DependsOn}}"{{.}}", {{end}} }{{else}}nil{{end}},
				ConfigFields: {{if .ConfigFields}}[]ConfigField{
{{range .ConfigFields}}					{
						Name:        "{{.Name}}",
						Type:        {{configFieldTypeConst .Type}},
						Label:       "{{.Label}}",
						Description: "{{escDesc .Description}}",
						Required:    {{.Required}},
					},
{{end}}				}{{else}}nil{{end}},
			},
{{end}}		},
	}
}
`
