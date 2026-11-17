package main

import (
	"bytes"
	"fmt"
	"go/format"
	"slices"
	"strings"
	"text/template"

	"github.com/yardrail/connectors/tools/googleintrospect"
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
	// GoogleSDKIfaces are the callable interfaces backed by a Google SDK package.
	GoogleSDKIfaces   []ManifestInterface
	GoogleSDKToolDefs []googleSDKToolDef
	GoogleSDKPkgPath  string
	GoogleSDKSvcType  string
	BaseURL           string
	AuthStrategy     map[string]authStrategy
	ConfigParams     map[string]string
	// CredBuilds is how each interface's Credentials are built from the
	// platform's stored credential keys, in manifest order.
	CredBuilds []credBuild
}

// credBuild is how BuildCredentials builds one interface's Credentials.
// Kind is "none", "bearer", "basic" or "apikey". Keys are the stored keys read:
// one for bearer and apikey, username then password for basic.
type credBuild struct {
	InterfaceID string
	Kind        string
	Keys        []string
}

// credentialBuilds derives each interface's credential build from its auth
// strategy. Interfaces with auth method none or webhook protocol build nothing;
// an interface without a security scheme is bearer from access_token.
func credentialBuilds(manifest *Manifest, strategies map[string]authStrategy) []credBuild {
	builds := make([]credBuild, 0, len(manifest.Interfaces))

	for _, iface := range manifest.Interfaces {
		b := credBuild{InterfaceID: iface.ID}

		strat, ok := strategies[iface.ID]

		switch {
		case iface.AuthMethod == "none" || iface.Protocol == "webhook":
			b.Kind = "none"
		case !ok:
			b.Kind, b.Keys = "bearer", []string{"access_token"}
		case authSchemeType(strat) == "basic":
			b.Kind, b.Keys = "basic", []string{strat.UsernameField, strat.PasswordField}
		case strings.HasPrefix(authSchemeType(strat), "apikey"):
			b.Kind, b.Keys = "apikey", []string{strat.CredKey}
		default:
			b.Kind, b.Keys = "bearer", []string{strat.CredKey}
		}

		builds = append(builds, b)
	}

	return builds
}

// secretKey is the stored credential key a pasted secret goes under.
func secretKey(connectorID string) string {
	if connectorID == "slack" {
		return "bot_token"
	}

	return "token"
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

// platformCredKeys are the stored credential keys the platform writes: token
// and bot_token for pasted secrets, access_token for OAuth.
var platformCredKeys = map[string]bool{"token": true, "bot_token": true, "access_token": true}

// deriveAuthStrategy maps each callable interface's security_scheme to concrete
// auth wiring based on the merged security schemes. A security_scheme that is
// set but not in schemes is an error, as is a credential mapping the platform
// cannot satisfy or a static Authorization header.
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

		for h := range iface.Headers {
			if strings.EqualFold(h, "Authorization") {
				return nil, fmt.Errorf("interface %q: %w", iface.ID, errStaticAuthHeader)
			}
		}

		var targets []string

		switch {
		case scheme.Type == "apiKey":
			targets = []string{scheme.Name}
		case scheme.Type == "http" && scheme.Scheme == "basic":
			targets = []string{"username", "password"}
		default:
			targets = []string{"access_token"}
		}

		for target := range iface.CredMapping {
			if !slices.Contains(targets, target) {
				return nil, fmt.Errorf("interface %q: %w: %q (want one of %q)", iface.ID, errInvalidCredTarget, target, targets)
			}
		}

		sources := make([]string, len(targets))
		for i, target := range targets {
			sources[i] = mapCredField(iface.CredMapping, target, target)
			if !platformCredKeys[sources[i]] {
				return nil, fmt.Errorf("interface %q: %w: %q", iface.ID, errUnstoredCredKey, sources[i])
			}
		}

		strat := authStrategy{SecurityScheme: scheme, CredKey: sources[0]}
		if len(sources) == 2 {
			strat.UsernameField, strat.PasswordField = sources[0], sources[1]
		}

		strategies[iface.ID] = strat
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
	case "google_sdk":
		return "ProtocolGoogleSDK"
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
				if p.GoType == "uuid.UUID" && p.FullType == "" {
					return true
				}
			}
		}

		return false
	},
	"needsStreamImports": func(methods []introspect.MethodInfo) bool {
		for _, m := range methods {
			if m.IsStreamBody {
				return true
			}
		}

		return false
	},
	"secretKey": secretKey,
	"needsStoredCredential": func(builds []credBuild) bool {
		for _, b := range builds {
			if b.Kind != "none" {
				return true
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
	"pathArgRef": func(configParams map[string]string, params []introspect.MethodParam, expr string) string {
		for _, p := range params {
			if p.Required && !p.IsStruct && configParams[p.JSONName] == "" {
				expr = strings.ReplaceAll(expr, p.JSONName, "pathArgs."+goIdent(p.JSONName))
			}
		}

		return expr
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
	"hasRequiredParams": func(params []googleintrospect.ToolParam) bool {
		for _, p := range params {
			if p.Required {
				return true
			}
		}

		return false
	},
	"hasOptionalParams": func(params []googleintrospect.ToolParam) bool {
		for _, p := range params {
			if !p.Required {
				return true
			}
		}

		return false
	},
	"googleSDKParamGoType": func(p googleintrospect.ToolParam) string {
		if p.IsStruct {
			return p.FullType
		}

		return p.GoType
	},
	"googleSDKRequiredArgs": func(params []googleintrospect.ToolParam) string {
		var args []string
		for _, p := range params {
			if !p.Required {
				continue
			}

			switch {
			case p.IsStruct:
				args = append(args, "&args."+goIdent(p.Name))
			case p.Variadic:
				args = append(args, "args."+goIdent(p.Name)+"...")
			default:
				args = append(args, "args."+goIdent(p.Name))
			}
		}

		return strings.Join(args, ", ")
	},
	"isIntegerGoType": func(goType string) bool {
		return googleintrospect.IsIntegerType(goType)
	},
	"isFloatGoType": func(goType string) bool {
		switch goType {
		case "float32", "float64":
			return true
		}

		return false
	},
	"hasGoogleSDKStructParams": func(tools []googleSDKToolDef) bool {
		for _, t := range tools {
			for _, p := range t.Params {
				if p.IsStruct {
					return true
				}
			}
		}

		return false
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

	"github.com/yardrail/connectors/connector"

	"{{.ClientPkgPath}}"
)

// InterfaceClient wraps an authenticated oapi-codegen client for one interface.
type InterfaceClient struct {
	cl *{{clientPkgName .ClientPkgPath}}.ClientWithResponses
}

` + functionEnumTemplate + restDispatchSwitchTemplate + `
// Connector holds an authenticated client for one interface.
type Connector struct {
	client *InterfaceClient
}

// Client returns the interface client.
func (c *Connector) Client() *InterfaceClient {
	return c.client
}

// NewConnector constructs a Connector for the given interface.
func NewConnector(interfaceID string, creds connector.Credentials, config map[string]string, httpClient *http.Client) (*Connector, error) {
	baseURL := resolveBaseURL(config)

	var authFn func(context.Context, *http.Request) error
	switch interfaceID {
{{range .RESTIfaces}}	case "{{.ID}}":
		authFn = authEditor{{goIdent .ID}}(creds)
{{end}}	default:
		return nil, fmt.Errorf("%w: %q", errInterfaceNotConfigured, interfaceID)
	}

	opts := []{{clientPkgName .ClientPkgPath}}.ClientOption{ {{clientPkgName .ClientPkgPath}}.WithRequestEditorFn(authFn)}
	if httpClient != nil {
		opts = append(opts, {{clientPkgName .ClientPkgPath}}.WithHTTPClient(httpClient))
	}

	cl, err := {{clientPkgName .ClientPkgPath}}.NewClientWithResponses(baseURL, opts...)
	if err != nil {
		return nil, fmt.Errorf("creating %s client: %w", interfaceID, err)
	}

	return &Connector{client: &InterfaceClient{cl: cl}}, nil
}

func resolveBaseURL(config map[string]string) string {
	if u, ok := config["base_url"]; ok && u != "" {
		return u
	}

	return "{{.BaseURL}}"
}

` + authEditorsTemplate

// authEditorsTemplate renders one request editor per callable interface that
// applies its auth scheme and static headers. Every handler template uses it.
const authEditorsTemplate = `{{range .CallableIfaces}}{{$strat := strategyFor $.AuthStrategy .ID}}
func authEditor{{goIdent .ID}}(creds connector.Credentials) func(context.Context, *http.Request) error {
{{- if eq (authSchemeType $strat) "bearer"}}
	bc := creds.(connector.BearerCredentials)
	return func(_ context.Context, req *http.Request) error {
		if bc.Token != "" {
			req.Header.Set("Authorization", "Bearer "+bc.Token)
		}
{{- else if eq (authSchemeType $strat) "basic"}}
	bc := creds.(connector.BasicCredentials)
	return func(_ context.Context, req *http.Request) error {
		req.SetBasicAuth(bc.Username, bc.Password)
{{- else if eq (authSchemeType $strat) "apikey_header"}}
	ac := creds.(connector.APIKeyCredentials)
	return func(_ context.Context, req *http.Request) error {
		if ac.Key != "" {
			req.Header.Set("{{$strat.SecurityScheme.Name}}", ac.Key)
		}
{{- else if eq (authSchemeType $strat) "apikey_query"}}
	ac := creds.(connector.APIKeyCredentials)
	return func(_ context.Context, req *http.Request) error {
		if ac.Key != "" {
			q := req.URL.Query()
			q.Set("{{$strat.SecurityScheme.Name}}", ac.Key)
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

` + buildCredentialsTemplate

// buildCredentialsTemplate renders BuildCredentials, which turns the
// platform's stored credential keys into each interface's Credentials.
// Every handler template uses it.
const buildCredentialsTemplate = `// BuildCredentials builds the Credentials the named interface authenticates
// with from the platform's stored credential keys. It returns nil credentials
// for interfaces that need none.
func BuildCredentials(interfaceID string, stored map[string]string) (connector.Credentials, error) {
	switch interfaceID {
{{- range .CredBuilds}}
	case "{{.InterfaceID}}":
{{- if eq .Kind "none"}}
		return nil, nil
{{- else if eq .Kind "basic"}}
		username, err := storedCredential(stored, interfaceID, "{{index .Keys 0}}")
		if err != nil {
			return nil, err
		}

		password, err := storedCredential(stored, interfaceID, "{{index .Keys 1}}")
		if err != nil {
			return nil, err
		}

		return connector.BasicCredentials{Username: username, Password: password}, nil
{{- else}}
		secret, err := storedCredential(stored, interfaceID, "{{index .Keys 0}}")
		if err != nil {
			return nil, err
		}
{{if eq .Kind "apikey"}}
		return connector.APIKeyCredentials{Key: secret}, nil
{{- else}}
		return connector.BearerCredentials{Token: secret}, nil
{{- end}}
{{- end}}
{{- end}}
	default:
		return nil, fmt.Errorf("{{.ConnectorID}}: %w: %q", connector.ErrUnknownInterface, interfaceID)
	}
}
{{- if needsStoredCredential .CredBuilds}}

// storedCredential returns the stored value under key, or an error naming the
// key when it is missing or empty.
func storedCredential(stored map[string]string, interfaceID, key string) (string, error) {
	if v := stored[key]; v != "" {
		return v, nil
	}

	return "", fmt.Errorf("{{.ConnectorID}}: interface %q: %w %q", interfaceID, connector.ErrMissingCredential, key)
}
{{- end}}
`

const dispatchTemplate = `// Code generated by yardrail-connector-codegen. DO NOT EDIT.

// Package {{.PkgName}} is a generated connector for {{.Manifest.Connector.Name}}.
package {{.PkgName}}

import (
{{- if needsStreamImports .Methods}}
	"bytes"
{{- end}}
	"context"
{{- if needsStreamImports .Methods}}
	"encoding/base64"
{{- end}}
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
{{- if needsUUID .Methods}}

	"github.com/google/uuid"
{{- end}}

	"github.com/yardrail/connectors/connector"

	"{{.ClientPkgPath}}"
)

var (
	errInterfaceNotConfigured = errors.New("{{.ConnectorID}}: interface not configured")
	errUnknownFunction        = errors.New("{{.ConnectorID}}: unknown function")
)
{{- if needsStreamImports .Methods}}

const maxStreamBodySize = 50 << 20 // 50 MiB
{{- end}}

` + resolveTemplate + openAPIMethodsTemplate

// openAPIAccessorsBaseURLTemplate renders only resolveBaseURL (used by mixed
// template which defines its own Client accessor).
const openAPIAccessorsBaseURLTemplate = `func resolveBaseURL(config map[string]string) string {
	if u, ok := config["base_url"]; ok && u != "" {
		return u
	}

	return "{{.BaseURL}}"
}

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
	var pathArgs struct {
{{- range .Params}}{{if and .Required (not .IsStruct)}}
{{- if eq (configParam $.ConfigParams .JSONName) ""}}
		{{goIdent .JSONName}} {{if .FullType}}{{fixTargetRef $.ClientPkgPath .FullType}}{{else}}{{.GoType}}{{end}} ` + "`" + `json:"{{.JSONName}}"` + "`" + `
{{- end}}
{{- end}}{{end}}
	}

	if err := json.Unmarshal(inputs, &pathArgs); err != nil {
		return nil, fmt.Errorf("{{$.ConnectorID}}.{{$m.Name}}: %w", err)
	}
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
{{- if $m.IsStreamBody}}
	var streamInput struct {
		Data        string ` + "`" + `json:"data"` + "`" + `
		ContentType string ` + "`" + `json:"content_type"` + "`" + `
	}

	if err := json.Unmarshal(inputs, &streamInput); err != nil {
		return nil, fmt.Errorf("{{$.ConnectorID}}.{{$m.Name}}: stream input: %w", err)
	}

	bodyBytes, decErr := base64.StdEncoding.DecodeString(streamInput.Data)
	if decErr != nil {
		return nil, fmt.Errorf("{{$.ConnectorID}}.{{$m.Name}}: decoding data: %w", decErr)
	}

	if len(bodyBytes) > maxStreamBodySize {
		return nil, fmt.Errorf("{{$.ConnectorID}}.{{$m.Name}}: data exceeds %d byte limit", maxStreamBodySize)
	}

	bodyReader := bytes.NewReader(bodyBytes)
	contentType := streamInput.ContentType
	if contentType == "" {
		contentType = "application/octet-stream"
	}
{{end}}
	resp, callErr := ic.cl.{{$m.MethodName}}(ctx, {{range .CallArgs}}{{if .IsParams}}&params{{else if eq .Expr "params"}}&params{{else if .IsStreamContentType}}contentType{{else if .IsStreamBody}}bodyReader{{else if and $hasBody (eq .Expr "body")}}body{{else}}{{pathArgRef $.ConfigParams $m.Params (fixTargetRef $.ClientPkgPath .Expr)}}{{end}}, {{end}})
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

// functionEnumTemplate renders the Function type, constants, and ParseFunction.
const functionEnumTemplate = `// Function identifies a callable operation on this connector.
type Function string

const (
{{range .Functions}}	Fn{{goIdent .Name}} Function = "{{.Name}}"
{{end}})

// ParseFunction validates a function name and returns the typed Function value.
func ParseFunction(s string) (Function, error) {
	switch Function(s) {
{{range .Functions}}	case Fn{{goIdent .Name}}:
		return Fn{{goIdent .Name}}, nil
{{end}}	default:
		return "", fmt.Errorf("%w: %q", errUnknownFunction, s)
	}
}
`

// restDispatchSwitchTemplate renders the Dispatch method as a switch on Function.
const restDispatchSwitchTemplate = `// Dispatch calls the named function's typed client method.
func (ic *InterfaceClient) Dispatch(ctx context.Context, fn Function, config map[string]string, inputs json.RawMessage) (any, error) {
	switch fn {
{{range .Functions}}	case Fn{{goIdent .Name}}:
		return ic.dispatch{{.MethodName}}(ctx, config, inputs)
{{end}}	default:
		return nil, fmt.Errorf("%w: %q", errUnknownFunction, string(fn))
	}
}
`

// resolveTemplate renders the Resolve function (shared across all protocol templates).
const resolveTemplate = `// Resolve creates a ready-to-call dispatch function for the given interface.
func Resolve(
	interfaceID, function string,
	creds connector.Credentials,
	config map[string]string,
	httpClient *http.Client,
) (func(context.Context, json.RawMessage) (any, error), error) {
	fn, err := ParseFunction(function)
	if err != nil {
		return nil, err
	}

	c, err := NewConnector(interfaceID, creds, config, httpClient)
	if err != nil {
		return nil, err
	}

	return func(ctx context.Context, inputs json.RawMessage) (any, error) {
		return c.Client().Dispatch(ctx, fn, config, inputs)
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
{{- if .IsCallable}}
					SecretKey:    "{{secretKey $.ConnectorID}}",
{{- end}}
				},
{{end}}			},
		},
		Resolve:          Resolve,
		BuildCredentials: BuildCredentials,
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
