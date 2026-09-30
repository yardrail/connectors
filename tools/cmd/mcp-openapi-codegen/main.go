package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"go/ast"
	"go/format"
	"go/types"
	"log"
	"os"
	"regexp"
	"strings"
	"text/template"

	"golang.org/x/tools/go/packages"
)

type ToolParam struct {
	Name     string
	JSONName string
	GoType   string
	FullType string
	TypeConv string // conversion func: "parseUUID", "parseTime", or ""
	IsStruct bool
	IsPtr    bool // true if the Params struct field is a pointer
	Required bool
	Desc     string
}

// CallArg represents one argument in the method call, in order.
type CallArg struct {
	Expr     string // Go expression for this argument
	IsParams bool   // true if this is &params
}

type ToolDef struct {
	Name       string
	MethodName string
	Desc       string
	Params     []ToolParam
	CallArgs   []CallArg
	ParamsType string // non-empty if the method takes a *Params struct
	ReturnType string
	HasReturn  bool
}

type ControllerGroup struct {
	Name  string // e.g. "Bookings"
	Tools []ToolDef
}

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

	tagMap, err := parseSpecTags(*specFile)
	if err != nil {
		log.Fatalf("parsing spec: %v", err)
	}
	fmt.Fprintf(os.Stderr, "loaded %d operation->tag mappings from spec\n", len(tagMap))

	groups, pkgName, err := analyze(*pkgPath, tagMap)
	if err != nil {
		log.Fatal(err)
	}

	total := 0
	for _, g := range groups {
		total += len(g.Tools)
	}
	fmt.Fprintf(os.Stderr, "found %d tools in %d controllers from package %s\n", total, len(groups), pkgName)
	for _, g := range groups {
		fmt.Fprintf(os.Stderr, "  %s (%d tools)\n", g.Name, len(g.Tools))
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

// parseSpecTags reads an OpenAPI spec and returns a map from normalized
// operationId (lowercased, non-alnum stripped) to the list of tags.
func parseSpecTags(specPath string) (map[string][]string, error) {
	data, err := os.ReadFile(specPath)
	if err != nil {
		return nil, err
	}

	var spec struct {
		Paths map[string]map[string]struct {
			OperationID string   `json:"operationId"`
			Tags        []string `json:"tags"`
		} `json:"paths"`
	}
	if err := json.Unmarshal(data, &spec); err != nil {
		return nil, err
	}

	result := make(map[string][]string)
	for _, methods := range spec.Paths {
		for _, op := range methods {
			if op.OperationID == "" {
				continue
			}
			key := normalizeID(op.OperationID)
			result[key] = op.Tags
		}
	}
	return result, nil
}

var nonAlnum = regexp.MustCompile(`[^a-zA-Z0-9]`)

func normalizeID(s string) string {
	return strings.ToLower(nonAlnum.ReplaceAllString(s, ""))
}

// sanitizeTagName converts a tag like "Billing Settings" to "BillingSettings"
// for use as a Go identifier.
func sanitizeTagName(tag string) string {
	parts := strings.Fields(tag)
	var b strings.Builder
	for _, p := range parts {
		if len(p) > 0 {
			b.WriteString(strings.ToUpper(p[:1]))
			b.WriteString(p[1:])
		}
	}
	return b.String()
}

func analyze(pkgPath string, tagMap map[string][]string) ([]ControllerGroup, string, error) {
	cfg := &packages.Config{
		Mode: packages.NeedTypes | packages.NeedName | packages.NeedSyntax | packages.NeedTypesInfo,
	}

	pkgs, err := packages.Load(cfg, pkgPath)
	if err != nil {
		return nil, "", fmt.Errorf("loading package: %w", err)
	}
	if len(pkgs) == 0 {
		return nil, "", fmt.Errorf("no packages found for %s", pkgPath)
	}
	pkg := pkgs[0]
	if len(pkg.Errors) > 0 {
		return nil, "", fmt.Errorf("package errors: %v", pkg.Errors)
	}

	scope := pkg.Types.Scope()

	// Find ClientWithResponses type.
	cwrObj := scope.Lookup("ClientWithResponses")
	if cwrObj == nil {
		return nil, "", fmt.Errorf("no ClientWithResponses type found")
	}

	docs := buildDocIndex(pkg.Syntax)

	mset := types.NewMethodSet(types.NewPointer(cwrObj.Type()))

	// Collect tools grouped by controller.
	groupMap := make(map[string][]ToolDef)
	var groupOrder []string

	for i := 0; i < mset.Len(); i++ {
		sel := mset.At(i)
		fn, ok := sel.Obj().(*types.Func)
		if !ok || !fn.Exported() {
			continue
		}

		name := fn.Name()
		if !strings.HasSuffix(name, "WithResponse") {
			continue
		}
		if strings.HasSuffix(name, "WithBodyWithResponse") {
			continue
		}

		sig := fn.Type().(*types.Signature)
		tool := extractTool(name, sig, scope, docs)
		if tool == nil {
			continue
		}

		baseName := strings.TrimSuffix(name, "WithResponse")
		baseName = strings.TrimSuffix(baseName, "WithFormdataBody")
		baseName = strings.TrimSuffix(baseName, "WithBody")
		key := normalizeID(baseName)

		tags := tagMap[key]
		if len(tags) == 0 {
			fmt.Fprintf(os.Stderr, "warning: no tags for %s, skipping\n", name)
			continue
		}

		var groups []string
		for _, t := range tags {
			groups = append(groups, sanitizeTagName(t))
		}

		for _, grp := range groups {
			if _, exists := groupMap[grp]; !exists {
				groupOrder = append(groupOrder, grp)
			}
			groupMap[grp] = append(groupMap[grp], *tool)
		}
	}

	var groups []ControllerGroup
	for _, name := range groupOrder {
		groups = append(groups, ControllerGroup{
			Name:  name,
			Tools: groupMap[name],
		})
	}

	return groups, pkg.Name, nil
}

func extractTool(methodName string, sig *types.Signature, scope *types.Scope, docs map[string]string) *ToolDef {
	baseName := strings.TrimSuffix(methodName, "WithResponse")
	toolName := toKebab(baseName)

	desc := docs["ClientWithResponses."+methodName]
	if desc == "" {
		desc = baseName
	}

	// Check return type: should be (*SomeResponse, error)
	results := sig.Results()
	hasReturn := false
	returnType := ""
	if results.Len() >= 1 {
		first := results.At(0)
		if ptr, ok := first.Type().(*types.Pointer); ok {
			if named, ok := ptr.Elem().(*types.Named); ok {
				returnType = named.Obj().Name()
				hasReturn = true
			}
		}
	}

	var params []ToolParam
	var callArgs []CallArg
	paramsType := ""

	p := sig.Params()
	for i := 0; i < p.Len(); i++ {
		param := p.At(i)
		paramType := param.Type()
		typeStr := paramType.String()

		if strings.Contains(typeStr, "context.Context") {
			continue
		}
		if strings.Contains(typeStr, "RequestEditorFn") {
			continue
		}
		if strings.Contains(typeStr, "io.Reader") {
			return nil
		}

		paramName := param.Name()
		if paramName == "" {
			paramName = fmt.Sprintf("arg%d", i)
		}

		// Check if it's a *Params struct (query/header params)
		if ptr, ok := paramType.(*types.Pointer); ok {
			if named, ok := ptr.Elem().(*types.Named); ok {
				if st, ok := named.Underlying().(*types.Struct); ok {
					if strings.HasSuffix(named.Obj().Name(), "Params") {
						paramsType = named.Obj().Name()
						callArgs = append(callArgs, CallArg{Expr: "params"})
						for j := 0; j < st.NumFields(); j++ {
							f := st.Field(j)
							if !f.Exported() {
								continue
							}
							jsonType, goType := goTypeToJSON(f.Type())
							if jsonType == "" {
								continue
							}
							jsonName := jsonTagName(st.Tag(j))
							if jsonName == "" {
								jsonName = toLowerCamel(f.Name())
							}
							// Detect pointer fields, named types, and special conversions
							fullType := ""
							conv := typeConversion(f.Type())
							_, isPtr := f.Type().(*types.Pointer)
							if conv == "" {
								ft := f.Type()
								if pt, ok := ft.(*types.Pointer); ok {
									ft = pt.Elem()
								}
								if n, ok := typeObjName(ft); ok {
									if _, isBasic := ft.Underlying().(*types.Basic); isBasic && goType == "string" {
										fullType = "target." + n
									}
								}
							}
							params = append(params, ToolParam{
								Name:     f.Name(),
								JSONName: jsonName,
								GoType:   goType,
								FullType: fullType,
								TypeConv: conv,
								IsPtr:    isPtr,
								Required: false,
								Desc:     f.Name(),
							})
						}
						continue
					}
				}
			}
		}

		// Check if it's a JSON request body struct
		isStruct, fullType := isStructParam(paramType)
		if isStruct {
			params = append(params, ToolParam{
				Name:     paramName,
				JSONName: toLowerCamel(paramName),
				GoType:   "object",
				FullType: fullType,
				IsStruct: true,
				Required: true,
				Desc:     fmt.Sprintf("Request body (%s)", typeStr),
			})
			callArgs = append(callArgs, CallArg{Expr: paramName})
			continue
		}

		// Plain param (path param)
		jsonType, goType := goTypeToJSON(paramType)
		if jsonType == "" {
			continue
		}
		conv := typeConversion(paramType)
		fullType = ""
		if conv == "" {
			if name, ok := typeObjName(paramType); ok {
				if _, isBasic := paramType.Underlying().(*types.Basic); isBasic && goType == "string" {
					fullType = "target." + name
				}
			}
		}
		params = append(params, ToolParam{
			Name:     paramName,
			JSONName: toLowerCamel(paramName),
			GoType:   goType,
			FullType: fullType,
			TypeConv: conv,
			Required: true,
			Desc:     paramName,
		})
		expr := paramName
		switch {
		case conv != "":
			expr = fmt.Sprintf("%s(%s)", conv, paramName)
		case fullType != "":
			expr = fmt.Sprintf("%s(%s)", fullType, paramName)
		}
		callArgs = append(callArgs, CallArg{Expr: expr})
	}

	return &ToolDef{
		Name:       toolName,
		MethodName: methodName,
		Desc:       desc,
		Params:     params,
		CallArgs:   callArgs,
		ParamsType: paramsType,
		ReturnType: returnType,
		HasReturn:  hasReturn,
	}
}

func jsonTagName(tag string) string {
	// Extract name from `json:"name,omitempty"`
	for _, part := range strings.Split(tag, " ") {
		part = strings.Trim(part, "`")
		if strings.HasPrefix(part, `json:"`) {
			val := strings.TrimPrefix(part, `json:"`)
			val = strings.TrimSuffix(val, `"`)
			name := strings.Split(val, ",")[0]
			if name != "-" {
				return name
			}
		}
	}
	return ""
}

func buildDocIndex(syntax []*ast.File) map[string]string {
	index := make(map[string]string)
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
			text := strings.TrimSpace(fn.Doc.Text())
			// oapi-codegen comments are like "MethodName request returning *Response"
			if idx := strings.Index(text, " "); idx > 0 {
				text = text[idx+1:]
			}
			index[key] = text
		}
	}
	return index
}

func receiverTypeName(fields *ast.FieldList) string {
	if fields == nil || len(fields.List) == 0 {
		return ""
	}
	expr := fields.List[0].Type
	if star, ok := expr.(*ast.StarExpr); ok {
		expr = star.X
	}
	if ident, ok := expr.(*ast.Ident); ok {
		return ident.Name
	}
	return ""
}

func typeObjName(t types.Type) (string, bool) {
	switch v := t.(type) {
	case *types.Named:
		return v.Obj().Name(), true
	case *types.Alias:
		return v.Obj().Name(), true
	}
	return "", false
}

func typeConversion(t types.Type) string {
	if pt, ok := t.(*types.Pointer); ok {
		return typeConversion(pt.Elem())
	}
	if isUUIDLike(t) {
		return "parseUUID"
	}
	if isDateLike(t) {
		return "parseDate"
	}
	if name, ok := typeObjName(t); ok {
		if name == "Time" && strings.HasSuffix(t.String(), "time.Time") {
			return "parseTime"
		}
	}
	return ""
}

func isUUIDLike(t types.Type) bool {
	if arr, ok := t.Underlying().(*types.Array); ok {
		if arr.Len() == 16 {
			if basic, ok := arr.Elem().(*types.Basic); ok && basic.Kind() == types.Byte {
				return true
			}
		}
	}
	return false
}

func isDateLike(t types.Type) bool {
	s := t.String()
	return strings.HasSuffix(s, "types.Date") || strings.HasSuffix(s, "openapi_types.Date")
}

func isStructParam(t types.Type) (bool, string) {
	isComplexUnderlying := func(u types.Type) bool {
		switch u.(type) {
		case *types.Struct, *types.Map:
			return true
		}
		return false
	}
	if isComplexUnderlying(t.Underlying()) {
		if name, ok := typeObjName(t); ok {
			return true, "target." + name
		}
	}
	if pt, ok := t.(*types.Pointer); ok {
		if isComplexUnderlying(pt.Elem().Underlying()) {
			if name, ok := typeObjName(pt.Elem()); ok {
				return true, "target." + name
			}
		}
	}
	return false, ""
}

func goTypeToJSON(t types.Type) (jsonType string, goType string) {
	if name, ok := typeObjName(t); ok {
		switch name {
		case "UUID":
			return "string", "string"
		case "Date", "Time":
			return "string", "string"
		}
	}
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
		return "object", "object"
	case *types.Map:
		return "object", "object"
	case *types.Struct:
		return "object", "object"
	}
	fmt.Fprintf(os.Stderr, "warning: treating unrecognized type %s as string\n", t.String())
	return "string", "string"
}

func toKebab(s string) string {
	var result []rune
	for i, r := range s {
		if r >= 'A' && r <= 'Z' && i > 0 {
			result = append(result, '-')
		}
		result = append(result, rune(strings.ToLower(string(r))[0]))
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
	case "float64", "float32":
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
	"trimResponse": func(s string) string {
		return strings.TrimSuffix(s, "WithResponse")
	},
	"toKebab": func(s string) string {
		return toKebab(s)
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

func render(groups []ControllerGroup, pkgName, serverName, pkgPath, tmplStr string) ([]byte, error) {
	tmpl := template.Must(template.New("server").Funcs(tmplFuncs).Parse(tmplStr))

	needsJSON := false
	hasOptional := false
	needsUUID := false
	needsTime := false
	needsDate := false
	hasSliceParams := false
	for _, g := range groups {
		for _, t := range g.Tools {
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
		PkgPath     string
		PkgName     string
		ServerName  string
		Controllers []ControllerGroup
		NeedsJSON   bool
		HasOptional bool
		NeedsUUID   bool
		NeedsTime   bool
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

const handlerFragment = `{{range .Controllers}}{{range .Tools}}
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

const toolRegistration = `{{range .Tools}}
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
