package main

import (
	"bytes"
	"flag"
	"fmt"
	"go/ast"
	"go/format"
	"go/types"
	"log"
	"os"
	"strings"
	"text/template"
	"unicode"

	"golang.org/x/tools/go/packages"
)

type methodDoc struct {
	Desc      string            // first paragraph of the doc comment (before param lines)
	ParamDesc map[string]string // "- paramName: description" entries
}

type ToolParam struct {
	Name     string
	JSONName string
	GoType   string
	FullType string // fully qualified type for struct params (e.g. "target.Task")
	IsStruct bool
	Variadic bool // true if the param is ...T (variadic)
	Required bool
	Desc     string
}

type ToolDef struct {
	Name        string
	ServiceName string
	ServicePath string // dot-separated access path from root Service (e.g. "Projects.Locations")
	MethodName  string
	Desc        string
	Params      []ToolParam
	ReturnType  string
	HasReturn   bool
}

func main() {
	pkgPath := flag.String("pkg", "", "import path of the package to analyze")
	outFile := flag.String("out", "", "output file (default: stdout)")
	serverName := flag.String("name", "generated-mcp-server", "MCP server name")
	outPkg := flag.String("pkgname", "", "output package name (default: generate a standalone main)")
	flag.Parse()

	if *pkgPath == "" {
		log.Fatal("-pkg is required")
	}

	tools, pkgName, rootServiceName, err := analyze(*pkgPath)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Fprintf(os.Stderr, "found %d tools from package %s\n", len(tools), pkgName)
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

func analyze(pkgPath string) ([]ToolDef, string, string, error) {
	cfg := &packages.Config{
		Mode: packages.NeedTypes | packages.NeedName | packages.NeedSyntax | packages.NeedTypesInfo,
	}

	pkgs, err := packages.Load(cfg, pkgPath)
	if err != nil {
		return nil, "", "", fmt.Errorf("loading package: %w", err)
	}
	if len(pkgs) == 0 {
		return nil, "", "", fmt.Errorf("no packages found for %s", pkgPath)
	}
	pkg := pkgs[0]
	if len(pkg.Errors) > 0 {
		return nil, "", "", fmt.Errorf("package errors: %v", pkg.Errors)
	}

	scope := pkg.Types.Scope()

	// Find the root service type. Some packages use "APIService" when
	// "Service" collides with a data model type.
	rootServiceName := "Service"
	rootService := scope.Lookup("Service")
	if rootService != nil {
		// Check if this is actually the root service (has client *http.Client)
		// or a data model struct. If it's a data model, look for APIService.
		if !hasHTTPClient(rootService.Type()) {
			rootService = nil
		}
	}
	if rootService == nil {
		rootService = scope.Lookup("APIService")
		if rootService == nil {
			return nil, "", "", fmt.Errorf("no Service or APIService type found in package")
		}
		rootServiceName = "APIService"
	}
	rootServiceType := rootService.Type()

	// Build a map from service type name to its access path from the root Service.
	// e.g. "ProjectsLocationsService" -> "Projects.Locations"
	servicePaths := buildServicePaths(rootServiceType)

	// Build doc index from AST for method descriptions.
	docs := buildDocIndex(pkg.Syntax)

	var tools []ToolDef

	for _, name := range scope.Names() {
		obj := scope.Lookup(name)
		tn, ok := obj.(*types.TypeName)
		if !ok {
			continue
		}
		if !isResourceService(tn.Type(), rootServiceType) || isCallType(tn.Type()) {
			continue
		}

		svcPath, ok := servicePaths[name]
		if !ok {
			fmt.Fprintf(os.Stderr, "warning: no access path for %s, skipping\n", name)
			continue
		}

		mset := types.NewMethodSet(types.NewPointer(tn.Type()))

		for i := 0; i < mset.Len(); i++ {
			sel := mset.At(i)
			fn, ok := sel.Obj().(*types.Func)
			if !ok || !fn.Exported() {
				continue
			}
			sig := fn.Type().(*types.Signature)

			tool := extractTool(name, svcPath, fn.Name(), sig, docs)
			if tool != nil {
				tools = append(tools, *tool)
			}
		}
	}

	return tools, pkg.Name, rootServiceName, nil
}

// buildDocIndex walks the package's syntax trees and extracts doc comments
// from method declarations. The key is "ReceiverTypeName.MethodName".
func buildDocIndex(syntax []*ast.File) map[string]methodDoc {
	index := make(map[string]methodDoc)
	for _, file := range syntax {
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv == nil || fn.Doc == nil {
				continue
			}
			recvType := receiverTypeName(fn.Recv)
			if recvType == "" {
				continue
			}
			key := recvType + "." + fn.Name.Name
			index[key] = parseMethodDoc(fn.Doc.Text())
		}
	}
	return index
}

func receiverTypeName(fields *ast.FieldList) string {
	if fields == nil || len(fields.List) == 0 {
		return ""
	}
	expr := fields.List[0].Type
	// Strip pointer: *TasklistsService -> TasklistsService
	if star, ok := expr.(*ast.StarExpr); ok {
		expr = star.X
	}
	if ident, ok := expr.(*ast.Ident); ok {
		return ident.Name
	}
	return ""
}

func parseMethodDoc(text string) methodDoc {
	md := methodDoc{ParamDesc: make(map[string]string)}
	lines := strings.Split(strings.TrimSpace(text), "\n")

	var descLines []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "- ") || strings.HasPrefix(trimmed, "  - ") {
			// Parameter doc line: "- paramName: description"
			paramLine := strings.TrimPrefix(trimmed, "- ")
			if idx := strings.Index(paramLine, ":"); idx > 0 {
				name := strings.TrimSpace(paramLine[:idx])
				desc := strings.TrimSpace(paramLine[idx+1:])
				md.ParamDesc[name] = desc
			}
		} else if len(md.ParamDesc) == 0 {
			// Part of the description (before any param lines)
			descLines = append(descLines, trimmed)
		}
	}

	desc := strings.Join(descLines, " ")
	// Strip the leading "MethodName: " prefix the generator adds.
	if idx := strings.Index(desc, ": "); idx > 0 && idx < 40 {
		desc = desc[idx+2:]
	}
	// Strip the "sets the optional parameter \"name\": " prefix on setter docs.
	if i := strings.Index(desc, "\": "); i > 0 {
		if j := strings.Index(desc, "\""); j >= 0 && j < i {
			desc = desc[i+3:]
		}
	}
	md.Desc = strings.TrimSpace(desc)
	return md
}

func hasHTTPClient(t types.Type) bool {
	st, ok := t.Underlying().(*types.Struct)
	if !ok {
		return false
	}
	for i := 0; i < st.NumFields(); i++ {
		if st.Field(i).Name() == "client" {
			return true
		}
	}
	return false
}

// buildServicePaths walks the root Service struct's fields recursively to
// build a map from service type name to its dot-separated access path.
// e.g. if Service has field Tasklists *TasklistsService, the map contains
// "TasklistsService" -> "Tasklists". For nested services like
// ProjectsLocationsService (accessed via svc.Projects.Locations), it produces
// "ProjectsLocationsService" -> "Projects.Locations".
func buildServicePaths(rootServiceType types.Type) map[string]string {
	paths := make(map[string]string)
	var walk func(t types.Type, prefix string)
	walk = func(t types.Type, prefix string) {
		st, ok := t.Underlying().(*types.Struct)
		if !ok {
			return
		}
		for i := 0; i < st.NumFields(); i++ {
			f := st.Field(i)
			if !f.Exported() {
				continue
			}
			pt, ok := f.Type().(*types.Pointer)
			if !ok {
				continue
			}
			named, ok := pt.Elem().(*types.Named)
			if !ok {
				continue
			}
			fieldPath := f.Name()
			if prefix != "" {
				fieldPath = prefix + "." + f.Name()
			}
			typeName := named.Obj().Name()
			paths[typeName] = fieldPath
			walk(named.Obj().Type(), fieldPath)
		}
	}
	walk(rootServiceType, "")
	return paths
}

// isResourceService checks whether t is a resource service type by looking
// for an unexported field `s` whose type is *Service (pointer to the root
// Service type). This matches the structural pattern the Google API generator
// emits for all resource service types.
func isResourceService(t, rootServiceType types.Type) bool {
	st, ok := t.Underlying().(*types.Struct)
	if !ok {
		return false
	}
	for i := 0; i < st.NumFields(); i++ {
		f := st.Field(i)
		if f.Name() == "s" && !f.Exported() {
			if ptr, ok := f.Type().(*types.Pointer); ok {
				return types.Identical(ptr.Elem(), rootServiceType)
			}
		}
	}
	return false
}

// isCallType checks whether t is a Call type by looking for an unexported
// field `urlParams_` of type gensupport.URLParams. This matches the
// structural pattern the Google API generator emits for all Call types.
func isCallType(t types.Type) bool {
	st, ok := t.Underlying().(*types.Struct)
	if !ok {
		return false
	}
	for i := 0; i < st.NumFields(); i++ {
		f := st.Field(i)
		if f.Name() == "urlParams_" && !f.Exported() {
			return strings.HasSuffix(f.Type().String(), "gensupport.URLParams")
		}
	}
	return false
}

func extractTool(serviceName, svcPath, methodName string, sig *types.Signature, docs map[string]methodDoc) *ToolDef {
	results := sig.Results()
	if results.Len() != 1 {
		return nil
	}

	retType := results.At(0).Type()
	retPtr, ok := retType.(*types.Pointer)
	if !ok {
		return nil
	}
	retNamed, ok := retPtr.Elem().(*types.Named)
	if !ok {
		return nil
	}
	if !isCallType(retNamed.Obj().Type()) {
		return nil
	}

	// Build tool name from the access path: "Projects.Locations" -> "projects-locations"
	pathParts := strings.Split(svcPath, ".")
	var kebabParts []string
	for _, p := range pathParts {
		kebabParts = append(kebabParts, toKebab(p))
	}
	toolName := strings.Join(kebabParts, "-") + "_" + toKebab(methodName)

	// Look up doc comment for this method.
	docKey := serviceName + "." + methodName
	md := docs[docKey]
	desc := md.Desc
	if desc == "" {
		desc = fmt.Sprintf("%s.%s", svcPath, methodName)
	}

	var params []ToolParam
	unsupported := false

	p := sig.Params()
	for i := 0; i < p.Len(); i++ {
		param := p.At(i)
		paramType := param.Type()

		typeStr := paramType.String()
		if strings.Contains(typeStr, "context.Context") || strings.Contains(typeStr, "http") {
			continue
		}

		paramName := param.Name()
		if paramName == "" {
			paramName = fmt.Sprintf("arg%d", i)
		}

		// Try to find a doc description for this param.
		// The godoc uses the API name (e.g. "tasklist") not the Go param name (e.g. "tasklistid").
		paramDesc := md.ParamDesc[paramName]
		if paramDesc == "" {
			paramDesc = md.ParamDesc[strings.TrimSuffix(paramName, "id")]
		}

		isVariadic := sig.Variadic() && i == p.Len()-1

		isStruct, structType := isStructParam(paramType)
		if isStruct {
			if paramDesc == "" {
				paramDesc = fmt.Sprintf("JSON object (%s)", typeStr)
			}
			params = append(params, ToolParam{
				Name:     paramName,
				JSONName: toLowerCamel(paramName),
				GoType:   "object",
				FullType: structType,
				IsStruct: true,
				Required: true,
				Desc:     paramDesc,
			})
		} else {
			jsonType, goType := goTypeToJSON(paramType)
			if jsonType == "" {
				unsupported = true
				break
			}
			if paramDesc == "" {
				paramDesc = paramName
			}
			params = append(params, ToolParam{
				Name:     paramName,
				JSONName: toLowerCamel(paramName),
				GoType:   goType,
				Variadic: isVariadic,
				Required: true,
				Desc:     paramDesc,
			})
		}
	}

	if unsupported {
		return nil
	}

	callTypeName := retNamed.Obj().Name()
	callMethods := types.NewMethodSet(types.NewPointer(retNamed.Obj().Type()))
	for j := 0; j < callMethods.Len(); j++ {
		setter := callMethods.At(j)
		setterFn, ok := setter.Obj().(*types.Func)
		if !ok || !setterFn.Exported() {
			continue
		}

		setterName := setterFn.Name()
		if isCallBuiltinMethod(setterName) {
			continue
		}

		setterSig := setterFn.Type().(*types.Signature)
		if setterSig.Params().Len() != 1 {
			continue
		}
		setterResults := setterSig.Results()
		if setterResults.Len() != 1 {
			continue
		}
		if setterResults.At(0).Type().String() != retType.String() {
			continue
		}

		setterParam := setterSig.Params().At(0)
		jsonType, goType := goTypeToJSON(setterParam.Type())
		if jsonType == "" {
			continue
		}

		setterDoc := docs[callTypeName+"."+setterName]
		setterDesc := setterDoc.Desc
		if setterDesc == "" {
			setterDesc = fmt.Sprintf("Optional: %s", setterName)
		}

		params = append(params, ToolParam{
			Name:     setterName,
			JSONName: toLowerCamel(setterName),
			GoType:   goType,
			Variadic: setterSig.Variadic(),
			Required: false,
			Desc:     setterDesc,
		})
	}

	returnTypeName := ""
	hasReturn := false
	for j := 0; j < callMethods.Len(); j++ {
		m := callMethods.At(j)
		if m.Obj().Name() == "Do" {
			doSig := m.Obj().Type().(*types.Signature)
			if doSig.Results().Len() >= 1 {
				first := doSig.Results().At(0)
				if first.Type().String() != "error" {
					returnTypeName = first.Type().String()
					hasReturn = true
				}
			}
			break
		}
	}

	return &ToolDef{
		Name:        toolName,
		ServiceName: serviceName,
		ServicePath: svcPath,
		MethodName:  methodName,
		Desc:        desc,
		Params:      params,
		ReturnType:  returnTypeName,
		HasReturn:   hasReturn,
	}
}

func isCallBuiltinMethod(name string) bool {
	builtins := map[string]bool{
		"Do": true, "Pages": true, "Context": true, "Fields": true,
		"Header": true, "IfNoneMatch": true, "Media": true,
		"ProgressUpdater": true, "ResumableMedia": true,
		"RequestId": true,
	}
	return builtins[name]
}

func isStructParam(t types.Type) (bool, string) {
	pt, ok := t.(*types.Pointer)
	if !ok {
		return false, ""
	}
	named, ok := pt.Elem().(*types.Named)
	if !ok {
		return false, ""
	}
	if _, ok := named.Underlying().(*types.Struct); ok {
		return true, "target." + named.Obj().Name()
	}
	return false, ""
}

func goTypeToJSON(t types.Type) (jsonType string, goType string) {
	switch u := t.Underlying().(type) {
	case *types.Basic:
		switch {
		case u.Info()&types.IsString != 0:
			return "string", "string"
		case u.Info()&types.IsInteger != 0:
			return "integer", t.String()
		case u.Info()&types.IsFloat != 0:
			return "number", t.String()
		case u.Info()&types.IsBoolean != 0:
			return "boolean", "bool"
		}
	case *types.Pointer:
		return goTypeToJSON(u.Elem())
	case *types.Slice:
		if elem, ok := u.Elem().(*types.Basic); ok && elem.Info()&types.IsString != 0 {
			return "array", "[]string"
		}
	case *types.Interface:
		// Skip interface types like io.Reader — can't represent in MCP
	}
	return "", t.String()
}

func toKebab(s string) string {
	var result []rune
	for i, r := range s {
		if unicode.IsUpper(r) && i > 0 {
			result = append(result, '-')
		}
		result = append(result, unicode.ToLower(r))
	}
	return string(result)
}

func toLowerCamel(s string) string {
	if len(s) == 0 {
		return s
	}
	return strings.ToLower(s[:1]) + s[1:]
}

func isIntegerType(goType string) bool {
	switch goType {
	case "int", "int8", "int16", "int32", "int64",
		"uint", "uint8", "uint16", "uint32", "uint64":
		return true
	}
	return false
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
		if isIntegerType(p.GoType) {
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
			if isIntegerType(p.GoType) {
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
