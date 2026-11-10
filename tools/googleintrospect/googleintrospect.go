// Package googleintrospect provides go/packages-based introspection of
// Google API client packages (the service/Call/resource-service pattern the
// Google API generator emits). It is intentionally not under internal/ so the
// separate google/tools module can import it.
package googleintrospect

import (
	"fmt"
	"go/ast"
	"go/types"
	"os"
	"strings"

	"github.com/yardrail/connectors/tools/internal/introspect"
	"golang.org/x/tools/go/packages"
)

// Re-exported shared utilities for cross-module consumers (the google/tools
// module cannot import tools/internal/).
var (
	ToKebab       = introspect.ToKebab
	ToLowerCamel  = introspect.ToLowerCamel
	IsIntegerType = introspect.IsIntegerType
)

// MethodDoc holds a parsed method doc comment.
type MethodDoc struct {
	Desc      string            // first paragraph of the doc comment (before param lines)
	ParamDesc map[string]string // "- paramName: description" entries
}

// ToolParam describes one parameter of a generated tool.
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

// ToolDef describes a generated tool derived from a Google API method.
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

// AnalyzeResult wraps the result of Analyze.
type AnalyzeResult struct {
	Tools           []ToolDef
	PkgName         string
	RootServiceName string
}

// Analyze loads the given Google API client package and extracts tool
// definitions from its resource services.
func Analyze(pkgPath string) (*AnalyzeResult, error) {
	cfg := &packages.Config{
		Mode: packages.NeedTypes | packages.NeedName | packages.NeedSyntax | packages.NeedTypesInfo,
	}

	pkgs, err := packages.Load(cfg, pkgPath)
	if err != nil {
		return nil, fmt.Errorf("loading package: %w", err)
	}
	if len(pkgs) == 0 {
		return nil, fmt.Errorf("no packages found for %s", pkgPath)
	}
	pkg := pkgs[0]
	if len(pkg.Errors) > 0 {
		return nil, fmt.Errorf("package errors: %v", pkg.Errors)
	}

	scope := pkg.Types.Scope()

	// Find the root service type. Some packages use "APIService" when
	// "Service" collides with a data model type.
	rootServiceName := "Service"
	rootService := scope.Lookup("Service")
	if rootService != nil {
		// Check if this is actually the root service (has client *http.Client)
		// or a data model struct. If it's a data model, look for APIService.
		if !HasHTTPClient(rootService.Type()) {
			rootService = nil
		}
	}
	if rootService == nil {
		rootService = scope.Lookup("APIService")
		if rootService == nil {
			return nil, fmt.Errorf("no Service or APIService type found in package")
		}
		rootServiceName = "APIService"
	}
	rootServiceType := rootService.Type()

	// Build a map from service type name to its access path from the root Service.
	// e.g. "ProjectsLocationsService" -> "Projects.Locations"
	servicePaths := BuildServicePaths(rootServiceType)

	// Build doc index from AST for method descriptions.
	docs := BuildDocIndex(pkg.Syntax)

	var tools []ToolDef

	for _, name := range scope.Names() {
		obj := scope.Lookup(name)
		tn, ok := obj.(*types.TypeName)
		if !ok {
			continue
		}
		if !IsResourceService(tn.Type(), rootServiceType) || IsCallType(tn.Type()) {
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

			tool := ExtractTool(name, svcPath, fn.Name(), sig, docs)
			if tool != nil {
				tools = append(tools, *tool)
			}
		}
	}

	return &AnalyzeResult{
		Tools:           tools,
		PkgName:         pkg.Name,
		RootServiceName: rootServiceName,
	}, nil
}

// BuildDocIndex walks the package's syntax trees and extracts doc comments
// from method declarations. The key is "ReceiverTypeName.MethodName".
func BuildDocIndex(syntax []*ast.File) map[string]MethodDoc {
	index := make(map[string]MethodDoc)
	for _, file := range syntax {
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv == nil || fn.Doc == nil {
				continue
			}
			recvType := introspect.ReceiverTypeName(fn.Recv)
			if recvType == "" {
				continue
			}
			key := recvType + "." + fn.Name.Name
			index[key] = ParseMethodDoc(fn.Doc.Text())
		}
	}
	return index
}

// ParseMethodDoc parses a method doc comment into a MethodDoc.
func ParseMethodDoc(text string) MethodDoc {
	md := MethodDoc{ParamDesc: make(map[string]string)}
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

// HasHTTPClient reports whether t has an unexported field named "client",
// matching the root Service struct pattern.
func HasHTTPClient(t types.Type) bool {
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

// BuildServicePaths walks the root Service struct's fields recursively to
// build a map from service type name to its dot-separated access path.
// e.g. if Service has field Tasklists *TasklistsService, the map contains
// "TasklistsService" -> "Tasklists". For nested services like
// ProjectsLocationsService (accessed via svc.Projects.Locations), it produces
// "ProjectsLocationsService" -> "Projects.Locations".
func BuildServicePaths(rootServiceType types.Type) map[string]string {
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

// IsResourceService checks whether t is a resource service type by looking
// for an unexported field `s` whose type is *Service (pointer to the root
// Service type). This matches the structural pattern the Google API generator
// emits for all resource service types.
func IsResourceService(t, rootServiceType types.Type) bool {
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

// IsCallType checks whether t is a Call type by looking for an unexported
// field `urlParams_` of type gensupport.URLParams. This matches the
// structural pattern the Google API generator emits for all Call types.
func IsCallType(t types.Type) bool {
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

// ExtractTool builds a ToolDef from a resource service method signature.
func ExtractTool(serviceName, svcPath, methodName string, sig *types.Signature, docs map[string]MethodDoc) *ToolDef {
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
	if !IsCallType(retNamed.Obj().Type()) {
		return nil
	}

	// Build tool name from the access path: "Projects.Locations" -> "projects-locations"
	pathParts := strings.Split(svcPath, ".")
	var kebabParts []string
	for _, p := range pathParts {
		kebabParts = append(kebabParts, ToKebab(p))
	}
	toolName := strings.Join(kebabParts, "-") + "_" + ToKebab(methodName)

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

		isStruct, structType := IsStructParam(paramType)
		if isStruct {
			if paramDesc == "" {
				paramDesc = fmt.Sprintf("JSON object (%s)", typeStr)
			}
			params = append(params, ToolParam{
				Name:     paramName,
				JSONName: ToLowerCamel(paramName),
				GoType:   "object",
				FullType: structType,
				IsStruct: true,
				Required: true,
				Desc:     paramDesc,
			})
		} else {
			jsonType, goType := GoTypeToJSON(paramType)
			if jsonType == "" {
				unsupported = true
				break
			}
			if paramDesc == "" {
				paramDesc = paramName
			}
			params = append(params, ToolParam{
				Name:     paramName,
				JSONName: ToLowerCamel(paramName),
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
		if IsCallBuiltinMethod(setterName) {
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
		jsonType, goType := GoTypeToJSON(setterParam.Type())
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
			JSONName: ToLowerCamel(setterName),
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

// IsCallBuiltinMethod reports whether a Call method is a built-in (not a
// setter for an optional parameter).
func IsCallBuiltinMethod(name string) bool {
	builtins := map[string]bool{
		"Do": true, "Pages": true, "Context": true, "Fields": true,
		"Header": true, "IfNoneMatch": true, "Media": true,
		"ProgressUpdater": true, "ResumableMedia": true,
		"RequestId": true,
	}
	return builtins[name]
}

// IsStructParam reports whether t is a pointer to a named struct (request
// body), returning the fully qualified target type name.
func IsStructParam(t types.Type) (bool, string) {
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

// GoTypeToJSON maps a Go type to its JSON schema type and Go type string.
// This is the Google-specific variant: it does not handle UUID, Date, Time,
// maps, or struct types.
func GoTypeToJSON(t types.Type) (jsonType string, goType string) {
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
		return GoTypeToJSON(u.Elem())
	case *types.Slice:
		if elem, ok := u.Elem().(*types.Basic); ok && elem.Info()&types.IsString != 0 {
			return "array", "[]string"
		}
	case *types.Interface:
		// Skip interface types like io.Reader — can't represent in MCP
	}
	return "", t.String()
}
