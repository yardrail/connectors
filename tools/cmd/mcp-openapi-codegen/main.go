package main

import (
	"bytes"
	"flag"
	"fmt"
	"go/format"
	"log"
	"os"
	"strings"
	"text/template"

	"github.com/yardrail/connectors/tools/internal/introspect"
)

// Type aliases for backward compatibility with templates.
type ToolParam = introspect.MethodParam
type CallArg = introspect.CallArg
type ToolDef = introspect.MethodInfo
type ControllerGroup = introspect.MethodGroup

func main() {
	pkgPath := flag.String("pkg", "", "import path of the oapi-codegen package")
	specFile := flag.String("spec", "", "path to the OpenAPI spec (JSON/YAML) for tag-based grouping")
	outFile := flag.String("out", "", "output file (default: stdout)")
	serverName := flag.String("name", "generated-mcp-server", "MCP server name")
	outPkg := flag.String("pkgname", "", "output package name (default: main)")
	flag.Parse()

	if *pkgPath == "" {
		log.Fatal("-pkg is required")
	}
	if *specFile == "" {
		log.Fatal("-spec is required")
	}

	tagMap, err := introspect.ParseSpecTags(*specFile)
	if err != nil {
		log.Fatalf("parsing spec: %v", err)
	}
	fmt.Fprintf(os.Stderr, "loaded %d operation->tag mappings from spec\n", len(tagMap))

	groups, pkgName, err := introspect.LoadMethods(*pkgPath, tagMap)
	if err != nil {
		log.Fatal(err)
	}

	total := 0
	for _, g := range groups {
		total += len(g.Methods)
	}
	fmt.Fprintf(os.Stderr, "found %d tools in %d controllers from package %s\n", total, len(groups), pkgName)
	for _, g := range groups {
		fmt.Fprintf(os.Stderr, "  %s (%d tools)\n", g.Name, len(g.Methods))
	}

	tmplStr := libraryTemplate
	if *outPkg == "" {
		*outPkg = "main"
		tmplStr = standaloneTemplate
	}

	src, err := render(groups, *outPkg, *serverName, *pkgPath, tmplStr)
	if err != nil {
		log.Fatal(err)
	}

	if *outFile != "" {
		if err := os.WriteFile(*outFile, src, 0644); err != nil {
			log.Fatal(err)
		}
		fmt.Fprintf(os.Stderr, "wrote %s\n", *outFile)
	} else {
		os.Stdout.Write(src)
	}
}

func paramExtractExpr(p ToolParam) string {
	switch p.GoType {
	case "string":
		return fmt.Sprintf(`request.GetString("%s", "")`, p.JSONName)
	case "bool":
		return fmt.Sprintf(`request.GetBool("%s", false)`, p.JSONName)
	case "float64", "float32":
		return fmt.Sprintf(`request.GetFloat("%s", 0)`, p.JSONName)
	case "[]string":
		return fmt.Sprintf(`getStringSlice(request, "%s")`, p.JSONName)
	default:
		if introspect.IsIntegerType(p.GoType) {
			return fmt.Sprintf(`%s(request.GetInt("%s", 0))`, p.GoType, p.JSONName)
		}
		return fmt.Sprintf(`request.GetString("%s", "")`, p.JSONName)
	}
}

var tmplFuncs = template.FuncMap{
	"trimResponse": func(s string) string {
		return strings.TrimSuffix(s, "WithResponse")
	},
	"toKebab": func(s string) string {
		return introspect.ToKebab(s)
	},
	"escDesc": func(s string) string {
		s = strings.ReplaceAll(s, `\`, `\\`)
		s = strings.ReplaceAll(s, `"`, `\"`)
		return s
	},
	"requiredArgs": func(params []ToolParam) string {
		var args []string
		for _, p := range params {
			if p.Required {
				args = append(args, p.JSONName)
			}
		}
		return strings.Join(args, ", ")
	},
	"paramWithType": func(p ToolParam) string {
		if p.IsStruct {
			return ""
		}
		return paramExtractExpr(p)
	},
	"optionalPtrExpr": func(p ToolParam) string {
		base := paramExtractExpr(p)
		if p.TypeConv != "" {
			inner := fmt.Sprintf("%s(request.GetString(\"%s\", \"\"))", p.TypeConv, p.JSONName)
			if p.IsPtr {
				return fmt.Sprintf("ptrVal(%s)", inner)
			}
			return inner
		}
		if !p.IsPtr {
			if p.FullType != "" {
				return fmt.Sprintf("%s(request.GetString(\"%s\", \"\"))", p.FullType, p.JSONName)
			}
			return base
		}
		if p.FullType != "" {
			return fmt.Sprintf("ptrVal(%s(request.GetString(\"%s\", \"\")))", p.FullType, p.JSONName)
		}
		switch p.GoType {
		case "float32":
			return fmt.Sprintf("ptrVal(float32(request.GetFloat(\"%s\", 0)))", p.JSONName)
		default:
			return fmt.Sprintf("ptrVal(%s)", base)
		}
	},
	"mcpPropFunc": func(p ToolParam) string {
		if p.IsStruct {
			return "mcp.WithObject"
		}
		switch p.GoType {
		case "string":
			return "mcp.WithString"
		case "bool":
			return "mcp.WithBoolean"
		case "float64", "float32":
			return "mcp.WithNumber"
		case "[]string":
			return "mcp.WithArray"
		default:
			if introspect.IsIntegerType(p.GoType) {
				return "mcp.WithInteger"
			}
			return "mcp.WithString"
		}
	},
	"extraPropOpts": func(p ToolParam) string {
		if p.GoType == "[]string" {
			return ", mcp.WithStringItems()"
		}
		return ""
	},
}

func render(groups []ControllerGroup, pkgName, serverName, pkgPath, tmplStr string) ([]byte, error) {
	tmpl := template.Must(template.New("server").Funcs(tmplFuncs).Parse(tmplStr))

	needsJSON := false
	hasOptional := false
	needsUUID := false
	needsTime := false
	needsDate := false
	hasSliceParams := false
	for _, g := range groups {
		for _, t := range g.Methods {
			if t.HasReturn {
				needsJSON = true
			}
			for _, p := range t.Params {
				if p.IsStruct {
					needsJSON = true
				}
				if !p.Required {
					hasOptional = true
				}
				switch p.TypeConv {
				case "parseUUID":
					needsUUID = true
				case "parseTime":
					needsTime = true
				case "parseDate":
					needsDate = true
				}
				if p.GoType == "[]string" {
					hasSliceParams = true
				}
			}
			for _, a := range t.CallArgs {
				if strings.Contains(a.Expr, "parseUUID") {
					needsUUID = true
				}
				if strings.Contains(a.Expr, "parseTime") {
					needsTime = true
				}
				if strings.Contains(a.Expr, "parseDate") {
					needsDate = true
				}
			}
		}
	}

	var buf bytes.Buffer
	err := tmpl.Execute(&buf, struct {
		PkgPath        string
		PkgName        string
		ServerName     string
		Controllers    []ControllerGroup
		NeedsJSON      bool
		HasOptional    bool
		NeedsUUID      bool
		NeedsTime      bool
		NeedsDate      bool
		HasSliceParams bool
	}{
		PkgPath:        pkgPath,
		PkgName:        pkgName,
		ServerName:     serverName,
		Controllers:    groups,
		NeedsJSON:      needsJSON,
		HasOptional:    hasOptional,
		NeedsUUID:      needsUUID,
		NeedsTime:      needsTime,
		NeedsDate:      needsDate,
		HasSliceParams: hasSliceParams,
	})
	if err != nil {
		return nil, fmt.Errorf("template: %w", err)
	}

	formatted, err := format.Source(buf.Bytes())
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: gofmt failed: %v\nraw output:\n%s\n", err, buf.String())
		return buf.Bytes(), nil
	}
	return formatted, nil
}

const convHelpers = `
{{- if .HasOptional}}
func ptrVal[T any](v T) *T {
	return &v
}
{{- end}}
{{- if .NeedsUUID}}

func parseUUID(s string) uuid.UUID {
	id, _ := uuid.Parse(s)
	return id
}
{{- end}}
{{- if .NeedsTime}}

func parseTime(s string) time.Time {
	t, _ := time.Parse(time.RFC3339, s)
	return t
}
{{- end}}
{{- if .NeedsDate}}

func parseDate(s string) openapi_types.Date {
	t, _ := time.Parse("2006-01-02", s)
	return openapi_types.Date{Time: t}
}
{{- end}}
{{- if .HasSliceParams}}

func getStringSlice(request mcp.CallToolRequest, key string) []string {
	args := request.GetArguments()
	if args == nil {
		return nil
	}
	v, ok := args[key]
	if !ok {
		return nil
	}
	slice, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(slice))
	for _, item := range slice {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}
	return out
}
{{- end}}
`

const handlerFragment = `{{range .Controllers}}{{range .Methods}}
func (h *Handler) handle{{.MethodName}}(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	client, err := h.resolve(ctx)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
{{range .Params}}{{if .Required}}{{if .IsStruct}}
	var {{.JSONName}} target.{{slice .FullType 7}}
	if argData, ok := request.GetArguments()["{{.JSONName}}"]; ok {
		b, _ := json.Marshal(argData)
		json.Unmarshal(b, &{{.JSONName}})
	}
{{- else}}
	{{.JSONName}} := {{paramWithType .}}
{{- end}}{{end}}{{end}}
{{- if .ParamsType}}
	params := &target.{{.ParamsType}}{}
	{{- range .Params}}{{if not .Required}}
	if args := request.GetArguments(); args != nil {
		if _, ok := args["{{.JSONName}}"]; ok {
			params.{{.Name}} = {{optionalPtrExpr .}}
		}
	}
	{{- end}}{{end}}
{{- end}}

	resp, err := client.{{.MethodName}}(ctx, {{range .CallArgs}}{{.Expr}}, {{end}})
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
{{if .HasReturn}}
	data, _ := json.MarshalIndent(resp, "", "  ")
	return mcp.NewToolResultText(string(data)), nil
{{- else}}
	_ = resp
	return mcp.NewToolResultText("success"), nil
{{- end}}
}
{{end}}{{end}}`

const toolRegistration = `{{range .Methods}}
	s.AddTool(
		mcp.NewTool("{{.Name}}",
			mcp.WithDescription("{{escDesc .Desc}}"),
{{- range .Params}}
			{{mcpPropFunc .}}("{{.JSONName}}"{{if .Required}}, mcp.Required(){{end}}, mcp.Description("{{escDesc .Desc}}"){{extraPropOpts .}}),
{{- end}}
		),
		h.handle{{.MethodName}},
	)
{{end}}`

const mountAllFunc = `
// Mounter can mount an MCPServer at a path. Implement this with your
// HTTP server's app type.
type Mounter interface {
	Mount(path string, s *server.MCPServer)
}

// MountAll creates one MCP server per tag group and mounts each at
// {prefix}/{tag-name}.
func (h *Handler) MountAll(m Mounter, prefix string) {
{{- range .Controllers}}
	{
		s := server.NewMCPServer("{{$.ServerName}}-{{.Name}}", "0.1.0")
		h.Register{{.Name}}(s)
		m.Mount(prefix + "/{{.Name | toKebab}}", s)
	}
{{- end}}
}
`

const handlerType = `
type ServiceResolver func(ctx context.Context) (*target.ClientWithResponses, error)

type Handler struct {
	resolve ServiceResolver
}

func NewHandler(resolve ServiceResolver) *Handler {
	return &Handler{resolve: resolve}
}
{{range .Controllers}}
// Register{{.Name}} adds {{.Name}} tools to the given MCP server.
func (h *Handler) Register{{.Name}}(s *server.MCPServer) {
` + toolRegistration + `
}
{{end}}
// RegisterAll adds all tools to the given MCP server.
func (h *Handler) RegisterAll(s *server.MCPServer) {
{{- range .Controllers}}
	h.Register{{.Name}}(s)
{{- end}}
}
`

var standaloneTemplate = `// Code generated by mcp-openapi-codegen. DO NOT EDIT.
package main

import (
	"context"
{{- if .NeedsJSON}}
	"encoding/json"
{{- end}}
	"log"
	"os"
{{- if .NeedsTime}}
	"time"
{{- end}}

{{- if .NeedsUUID}}
	"github.com/google/uuid"
{{- end}}
{{- if .Controllers}}
	"github.com/mark3labs/mcp-go/mcp"
{{- end}}
{{- if .NeedsDate}}
	openapi_types "github.com/oapi-codegen/runtime/types"
{{- end}}
	"github.com/mark3labs/mcp-go/server"
	target "{{.PkgPath}}"
)
` + handlerType + convHelpers + `
func main() {
	client, err := target.NewClientWithResponses(os.Getenv("API_BASE_URL"))
	if err != nil {
		log.Fatal(err)
	}

	s := server.NewMCPServer("{{.ServerName}}", "0.1.0")
	NewHandler(func(_ context.Context) (*target.ClientWithResponses, error) {
		return client, nil
	}).Register(s)

	stdio := server.NewStdioServer(s)
	if err := stdio.Listen(context.Background(), os.Stdin, os.Stdout); err != nil {
		log.Fatal(err)
	}
}
` + handlerFragment

var libraryTemplate = `// Code generated by mcp-openapi-codegen. DO NOT EDIT.
package {{.PkgName}}

import (
	"context"
{{- if .NeedsJSON}}
	"encoding/json"
{{- end}}
{{- if .NeedsTime}}
	"time"
{{- end}}

{{- if .NeedsUUID}}
	"github.com/google/uuid"
{{- end}}
{{- if .Controllers}}
	"github.com/mark3labs/mcp-go/mcp"
{{- end}}
{{- if .NeedsDate}}
	openapi_types "github.com/oapi-codegen/runtime/types"
{{- end}}
	"github.com/mark3labs/mcp-go/server"
	target "{{.PkgPath}}"
)
` + handlerType + mountAllFunc + convHelpers + handlerFragment
