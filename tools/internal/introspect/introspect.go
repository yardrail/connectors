// Package introspect provides shared go/packages-based introspection of
// oapi-codegen ClientWithResponses types.
package introspect

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/types"
	"os"
	"regexp"
	"strings"

	"golang.org/x/tools/go/packages"
)

// MethodParam describes one parameter of a ClientWithResponses method.
type MethodParam struct {
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

// MethodInfo describes a single *WithResponse method on ClientWithResponses.
type MethodInfo struct {
	Name       string
	MethodName string
	Desc       string
	Params     []MethodParam
	CallArgs   []CallArg
	ParamsType string // non-empty if the method takes a *Params struct
	ReturnType string
	HasReturn  bool
}

// MethodGroup collects methods by OpenAPI tag.
type MethodGroup struct {
	Name    string // e.g. "Bookings"
	Methods []MethodInfo
}

// NonAlnum matches non-alphanumeric characters.
var NonAlnum = regexp.MustCompile(`[^a-zA-Z0-9]`)

// NormalizeID lowercases and strips non-alphanumeric characters.
func NormalizeID(s string) string {
	return strings.ToLower(NonAlnum.ReplaceAllString(s, ""))
}

// SanitizeTagName converts an OpenAPI tag into a PascalCase Go identifier.
func SanitizeTagName(tag string) string {
	r := strings.NewReplacer(".", " ", "-", " ", "_", " ")
	parts := strings.Fields(r.Replace(tag))
	var b strings.Builder
	for _, p := range parts {
		if len(p) > 0 {
			b.WriteString(strings.ToUpper(p[:1]))
			b.WriteString(p[1:])
		}
	}
	return b.String()
}

// ParseSpecTags reads an OpenAPI spec and returns a map from normalized
// operationId (lowercased, non-alnum stripped) to the list of tags.
func ParseSpecTags(specPath string) (map[string][]string, error) {
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
			key := NormalizeID(op.OperationID)
			result[key] = op.Tags
		}
	}
	return result, nil
}

// LoadMethods loads the given Go package, finds ClientWithResponses, and
// returns its *WithResponse methods grouped by OpenAPI tag.
func LoadMethods(pkgPath string, tagMap map[string][]string) ([]MethodGroup, string, error) {
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

	cwrObj := scope.Lookup("ClientWithResponses")
	if cwrObj == nil {
		return nil, "", fmt.Errorf("no ClientWithResponses type found")
	}

	docs := BuildDocIndex(pkg.Syntax)

	mset := types.NewMethodSet(types.NewPointer(cwrObj.Type()))

	groupMap := make(map[string][]MethodInfo)
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
		method := ExtractMethod(name, sig, scope, docs)
		if method == nil {
			continue
		}

		baseName := strings.TrimSuffix(name, "WithResponse")
		baseName = strings.TrimSuffix(baseName, "WithFormdataBody")
		baseName = strings.TrimSuffix(baseName, "WithBody")
		key := NormalizeID(baseName)

		tags := tagMap[key]
		if len(tags) == 0 {
			fmt.Fprintf(os.Stderr, "warning: no tags for %s, skipping\n", name)
			continue
		}

		bestTag := tags[0]
		for _, t := range tags[1:] {
			if len(t) > len(bestTag) {
				bestTag = t
			}
		}
		grp := SanitizeTagName(bestTag)
		if _, exists := groupMap[grp]; !exists {
			groupOrder = append(groupOrder, grp)
		}
		groupMap[grp] = append(groupMap[grp], *method)
	}

	var groups []MethodGroup
	for _, name := range groupOrder {
		groups = append(groups, MethodGroup{
			Name:    name,
			Methods: groupMap[name],
		})
	}

	return groups, pkg.Name, nil
}

// ExtractMethod builds a MethodInfo from a *WithResponse method signature.
func ExtractMethod(methodName string, sig *types.Signature, scope *types.Scope, docs map[string]string) *MethodInfo {
	baseName := strings.TrimSuffix(methodName, "WithResponse")
	toolName := ToKebab(baseName)

	desc := docs["ClientWithResponses."+methodName]
	if desc == "" {
		desc = baseName
	}

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

	var params []MethodParam
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
							jsonType, goType := GoTypeToJSON(f.Type())
							if jsonType == "" || goType == "object" {
								continue
							}
							jsonName := JSONTagName(st.Tag(j))
							if jsonName == "" {
								jsonName = ToLowerCamel(f.Name())
							}
							fullType := ""
							conv := TypeConversion(f.Type())
							_, isPtr := f.Type().(*types.Pointer)
							if conv == "" {
								ft := f.Type()
								if pt, ok := ft.(*types.Pointer); ok {
									ft = pt.Elem()
								}
								if n, ok := TypeObjName(ft); ok {
									if _, isBasic := ft.Underlying().(*types.Basic); isBasic && goType == "string" {
										fullType = "target." + n
									}
								}
							}
							params = append(params, MethodParam{
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

		isStruct, fullType := IsStructParam(paramType)
		if isStruct {
			params = append(params, MethodParam{
				Name:     paramName,
				JSONName: ToLowerCamel(paramName),
				GoType:   "object",
				FullType: fullType,
				IsStruct: true,
				Required: true,
				Desc:     fmt.Sprintf("Request body (%s)", typeStr),
			})
			callArgs = append(callArgs, CallArg{Expr: paramName})
			continue
		}

		jsonType, goType := GoTypeToJSON(paramType)
		if jsonType == "" {
			continue
		}
		conv := TypeConversion(paramType)
		fullType = ""
		if conv == "" {
			if name, ok := TypeObjName(paramType); ok {
				if _, isBasic := paramType.Underlying().(*types.Basic); isBasic && goType == "string" {
					fullType = "target." + name
				}
			}
		}
		params = append(params, MethodParam{
			Name:     paramName,
			JSONName: ToLowerCamel(paramName),
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

	return &MethodInfo{
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

// JSONTagName extracts the JSON field name from a struct tag.
func JSONTagName(tag string) string {
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

// BuildDocIndex builds a map from "TypeName.MethodName" to doc comment text.
func BuildDocIndex(syntax []*ast.File) map[string]string {
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

// TypeObjName returns the type name for Named and Alias types.
func TypeObjName(t types.Type) (string, bool) {
	switch v := t.(type) {
	case *types.Named:
		return v.Obj().Name(), true
	case *types.Alias:
		return v.Obj().Name(), true
	}
	return "", false
}

// TypeConversion returns the conversion function name for special types.
func TypeConversion(t types.Type) string {
	if pt, ok := t.(*types.Pointer); ok {
		return TypeConversion(pt.Elem())
	}
	if IsUUIDLike(t) {
		return "parseUUID"
	}
	if IsDateLike(t) {
		return "parseDate"
	}
	if name, ok := TypeObjName(t); ok {
		if name == "Time" && strings.HasSuffix(t.String(), "time.Time") {
			return "parseTime"
		}
	}
	return ""
}

// IsUUIDLike reports whether t is a [16]byte array (UUID representation).
func IsUUIDLike(t types.Type) bool {
	if arr, ok := t.Underlying().(*types.Array); ok {
		if arr.Len() == 16 {
			if basic, ok := arr.Elem().(*types.Basic); ok && basic.Kind() == types.Byte {
				return true
			}
		}
	}
	return false
}

// IsDateLike reports whether t looks like an openapi_types.Date.
func IsDateLike(t types.Type) bool {
	s := t.String()
	return strings.HasSuffix(s, "types.Date") || strings.HasSuffix(s, "openapi_types.Date")
}

// IsStructParam reports whether t is a struct or map parameter (request body).
func IsStructParam(t types.Type) (bool, string) {
	isComplexUnderlying := func(u types.Type) bool {
		switch u.(type) {
		case *types.Struct, *types.Map:
			return true
		}
		return false
	}
	if isComplexUnderlying(t.Underlying()) {
		if name, ok := TypeObjName(t); ok {
			return true, "target." + name
		}
	}
	if pt, ok := t.(*types.Pointer); ok {
		if isComplexUnderlying(pt.Elem().Underlying()) {
			if name, ok := TypeObjName(pt.Elem()); ok {
				return true, "target." + name
			}
		}
	}
	return false, ""
}

// GoTypeToJSON maps a Go type to its JSON schema type and Go type string.
func GoTypeToJSON(t types.Type) (jsonType string, goType string) {
	if name, ok := TypeObjName(t); ok {
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
		return GoTypeToJSON(u.Elem())
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

// ToKebab converts a PascalCase string to kebab-case.
func ToKebab(s string) string {
	var result []rune
	for i, r := range s {
		if r >= 'A' && r <= 'Z' && i > 0 {
			result = append(result, '-')
		}
		result = append(result, rune(strings.ToLower(string(r))[0]))
	}
	return string(result)
}

// ToLowerCamel converts a PascalCase string to lowerCamelCase.
func ToLowerCamel(s string) string {
	if len(s) == 0 {
		return s
	}
	return strings.ToLower(s[:1]) + s[1:]
}

// IsIntegerType reports whether a Go type string is an integer type.
func IsIntegerType(goType string) bool {
	switch goType {
	case "int", "int8", "int16", "int32", "int64",
		"uint", "uint8", "uint16", "uint32", "uint64":
		return true
	}
	return false
}
