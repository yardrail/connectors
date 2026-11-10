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

	"github.com/yardrail/connectors/tools/googleintrospect"
)

type ToolParam = googleintrospect.ToolParam
type ToolDef = googleintrospect.ToolDef

func main() {
	pkgPath := flag.String("pkg", "", "import path of the package to analyze")
	outFile := flag.String("out", "", "output file (default: stdout)")
	serverName := flag.String("name", "generated-mcp-server", "MCP server name")
	outPkg := flag.String("pkgname", "", "output package name (default: generate a standalone main)")
	flag.Parse()

	if *pkgPath == "" {
		log.Fatal("-pkg is required")
	}

	result, err := googleintrospect.Analyze(*pkgPath)
	if err != nil {
		log.Fatal(err)
	}
	tools := result.Tools
	rootServiceName := result.RootServiceName

	fmt.Fprintf(os.Stderr, "found %d tools from package %s\n", len(tools), result.PkgName)
	for _, t := range tools {
		fmt.Fprintf(os.Stderr, "  %s (%d params)\n", t.Name, len(t.Params))
	}

	tmplStr := standaloneTemplate
	if *outPkg != "" {
		tmplStr = libraryTemplate
	} else {
		*outPkg = "main"
	}

	src, err := render(tools, *outPkg, *serverName, *pkgPath, rootServiceName, tmplStr)
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
	case "float64":
		return fmt.Sprintf(`request.GetFloat("%s", 0)`, p.JSONName)
	case "[]string":
		return fmt.Sprintf(`getStringSlice(request, "%s")`, p.JSONName)
	default:
		if googleintrospect.IsIntegerType(p.GoType) {
			return fmt.Sprintf(`%s(request.GetInt("%s", 0))`, p.GoType, p.JSONName)
		}
		return fmt.Sprintf(`request.GetString("%s", "")`, p.JSONName)
	}
}

var tmplFuncs = template.FuncMap{
	"escDesc": func(s string) string {
		s = strings.ReplaceAll(s, `\`, `\\`)
		s = strings.ReplaceAll(s, `"`, `\"`)
		return s
	},
	"exportName": func(s string) string { return s },
	"requiredArgs": func(params []ToolParam) string {
		var args []string
		for _, p := range params {
			if p.Required {
				if p.IsStruct {
					args = append(args, "&"+p.JSONName)
				} else if p.Variadic {
					args = append(args, p.JSONName+"...")
				} else {
					args = append(args, p.JSONName)
				}
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
	"hasCheck": func(p ToolParam) string {
		if p.GoType == "string" {
			return fmt.Sprintf(`if v := request.GetString("%s", ""); v != "" {`, p.JSONName)
		}
		if p.GoType == "[]string" {
			return fmt.Sprintf(`if args := request.GetArguments(); args != nil { if _, ok := args["%s"]; ok {`, p.JSONName)
		}
		return fmt.Sprintf(`if args := request.GetArguments(); args != nil { if _, ok := args["%s"]; ok {`, p.JSONName)
	},
	"hasCheckClose": func(p ToolParam) string {
		if p.GoType == "string" {
			return "}"
		}
		return "}}"
	},
	"optionalVal": func(p ToolParam) string {
		expr := paramExtractExpr(p)
		if p.Variadic {
			return expr + "..."
		}
		return expr
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
		case "[]string":
			return "mcp.WithArray"
		case "float64":
			return "mcp.WithNumber"
		default:
			if googleintrospect.IsIntegerType(p.GoType) {
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

func render(tools []ToolDef, pkgName, serverName, pkgPath, rootServiceName, tmplStr string) ([]byte, error) {
	tmpl := template.Must(template.New("server").Funcs(tmplFuncs).Parse(tmplStr))

	needsJSON := false
	hasSliceParams := false
	for _, t := range tools {
		if t.HasReturn {
			needsJSON = true
		}
		for _, p := range t.Params {
			if p.IsStruct {
				needsJSON = true
			}
			if p.GoType == "[]string" {
				hasSliceParams = true
			}
		}
	}

	var buf bytes.Buffer
	err := tmpl.Execute(&buf, struct {
		PkgPath        string
		PkgName        string
		ServerName     string
		ServiceType    string
		Tools          []ToolDef
		HasTools       bool
		NeedsJSON      bool
		HasSliceParams bool
	}{
		PkgPath:        pkgPath,
		PkgName:        pkgName,
		ServerName:     serverName,
		ServiceType:    rootServiceName,
		Tools:          tools,
		HasTools:       len(tools) > 0,
		NeedsJSON:      needsJSON,
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

const handlerFragment = `{{range .Tools}}
func (h *Handler) handle{{.ServiceName}}{{.MethodName}}(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	svc, err := h.resolve(ctx)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
{{range .Params}}{{if .Required}}{{if .IsStruct}}
	var {{.JSONName}} {{.FullType}}
	if argData, ok := request.GetArguments()["{{.JSONName}}"]; ok {
		b, _ := json.Marshal(argData)
		json.Unmarshal(b, &{{.JSONName}})
	}
{{- else}}
	{{.JSONName}} := {{paramWithType .}}
{{- end}}{{end}}{{end}}

	call := svc.{{.ServicePath}}.{{.MethodName}}({{requiredArgs .Params}})
{{range .Params}}{{if not .Required}}
	{{hasCheck .}}
		call = call.{{.Name}}({{optionalVal .}})
	{{hasCheckClose .}}
{{- end}}{{end}}
{{if .HasReturn}}
	result, err := call.Do()
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	data, _ := json.MarshalIndent(result, "", "  ")
	return mcp.NewToolResultText(string(data)), nil
{{- else}}
	if err := call.Do(); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return mcp.NewToolResultText("success"), nil
{{- end}}
}
{{end}}`

const toolRegistration = `{{range .Tools}}
	s.AddTool(
		mcp.NewTool("{{.Name}}",
			mcp.WithDescription("{{escDesc .Desc}}"),
{{- range .Params}}
			{{mcpPropFunc .}}("{{.JSONName}}"{{if .Required}}, mcp.Required(){{end}}, mcp.Description("{{escDesc .Desc}}"){{extraPropOpts .}}),
{{- end}}
		),
		h.handle{{.ServiceName}}{{.MethodName}},
	)
{{end}}`

const sliceHelper = `
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

const handlerType = `
// ServiceResolver returns a target API service for the given context.
// The context carries client identity (e.g. via auth.APIKey).
type ServiceResolver func(ctx context.Context) (*target.{{.ServiceType}}, error)

// Handler provides MCP tool handlers backed by a ServiceResolver.
type Handler struct {
	resolve ServiceResolver
}

// NewHandler creates a new Handler with the given resolver.
func NewHandler(resolve ServiceResolver) *Handler {
	return &Handler{resolve: resolve}
}

// Register adds all tools to the given MCP server.
func (h *Handler) Register(s *server.MCPServer) {
` + toolRegistration + `
}
`

var standaloneTemplate = `// Code generated by mcp-codegen. DO NOT EDIT.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	target "{{.PkgPath}}"
	"golang.org/x/oauth2"
	"google.golang.org/api/option"
)
` + handlerType + sliceHelper + `
func main() {
	token := os.Getenv("OAUTH_TOKEN")
	if token == "" {
		log.Fatal("OAUTH_TOKEN not set")
	}
	ts := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: token})
	svc, err := target.NewService(context.Background(), option.WithTokenSource(ts))
	if err != nil {
		log.Fatal(err)
	}

	s := server.NewMCPServer("{{.ServerName}}", "0.1.0")
	NewHandler(func(_ context.Context) (*target.{{.ServiceType}}, error) {
		return svc, nil
	}).Register(s)

	stdio := server.NewStdioServer(s)
	if err := stdio.Listen(context.Background(), os.Stdin, os.Stdout); err != nil {
		log.Fatal(err)
	}
}
` + handlerFragment

var libraryTemplate = `// Code generated by mcp-codegen. DO NOT EDIT.
package {{.PkgName}}

import (
	"context"
{{- if .NeedsJSON}}
	"encoding/json"
{{- end}}

{{- if .HasTools}}
	"github.com/mark3labs/mcp-go/mcp"
{{- end}}
	"github.com/mark3labs/mcp-go/server"
	target "{{.PkgPath}}"
)
` + handlerType + sliceHelper + handlerFragment
