package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/parser"
	"github.com/vektah/gqlparser/v2/validator"

	"github.com/yardrail/connectors/tools/internal/introspect"
)

var (
	errOperationCount     = errors.New("operation file must hold exactly one named operation")
	errOperationName      = errors.New("operation file name must equal the operation name")
	errInvalidOperation   = errors.New("operation does not validate against the schema")
	errUndeclaredScalar   = errors.New("custom scalar not declared in the manifest's source.scalars")
	errUnknownDescription = errors.New("source.descriptions names no operation")
)

// builtinScalars and builtinDirectives are the GraphQL built-ins that vendor
// introspection dumps redeclare. gqlparser supplies them through its prelude
// and rejects a second declaration, so they are dropped from the vendor schema.
var (
	builtinScalars    = map[string]bool{"Int": true, "Float": true, "String": true, "Boolean": true, "ID": true}
	builtinDirectives = map[string]bool{
		"include": true, "skip": true, "deprecated": true,
		"specifiedBy": true, "oneOf": true, "defer": true,
	}
)

// graphqlSource locates a GraphQL interface's schema (optional) and its
// directory of operation files, maps custom scalar names to FieldInfo.Type
// names, and maps operation names to descriptions that replace the schema's.
type graphqlSource struct {
	Schema       string
	Operations   string
	Scalars      map[string]string
	Descriptions map[string]string
}

// graphqlFunction is one connector function backed by a named operation.
type graphqlFunction struct {
	InterfaceID string // set by the caller; loadGraphQL leaves it empty
	Name        string // kebab-case function name
	ClassName   string // CWL class name
	Query       string // operation file text, sent as the request's query
	Op          *OperationInfo
}

// loadGraphQL reads every *.graphql file in src.Operations and returns one
// function per file, sorted by name. When src.Schema is set, every operation
// is validated against it and inputs and outputs are typed from it. An
// operation named in src.Descriptions takes that description; a name that
// matches no operation fails with errUnknownDescription.
func loadGraphQL(src graphqlSource) ([]graphqlFunction, error) {
	var schema *ast.Schema

	if src.Schema != "" {
		var err error

		schema, err = loadGraphQLSchema(src.Schema)
		if err != nil {
			return nil, err
		}
	}

	paths, err := filepath.Glob(filepath.Join(src.Operations, "*.graphql"))
	if err != nil {
		return nil, fmt.Errorf("listing operations: %w", err)
	}

	sort.Strings(paths)

	funcs := make([]graphqlFunction, 0, len(paths))

	for _, path := range paths {
		fn, err := loadOperationFile(path, schema, src.Scalars)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}

		if desc, ok := src.Descriptions[fn.Op.OperationID]; ok {
			fn.Op.Description = desc
		}

		funcs = append(funcs, fn)
	}

	err = checkDescriptions(src.Descriptions, funcs)
	if err != nil {
		return nil, err
	}

	return funcs, nil
}

// checkDescriptions fails with errUnknownDescription for the first key, in
// sorted order, that names none of funcs' operations.
func checkDescriptions(descriptions map[string]string, funcs []graphqlFunction) error {
	ops := make(map[string]bool, len(funcs))
	for _, fn := range funcs {
		ops[fn.Op.OperationID] = true
	}

	names := make([]string, 0, len(descriptions))
	for name := range descriptions {
		names = append(names, name)
	}

	sort.Strings(names)

	for _, name := range names {
		if !ops[name] {
			return fmt.Errorf("%w: %s", errUnknownDescription, name)
		}
	}

	return nil
}

// loadGraphQLSchema parses a schema file, drops its redeclared built-ins and
// validates it together with gqlparser's prelude. The file is not modified.
func loadGraphQLSchema(path string) (*ast.Schema, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	doc, err := parser.ParseSchema(&ast.Source{Name: path, Input: string(data)})
	if err != nil {
		return nil, fmt.Errorf("parsing schema %s: %w", path, err)
	}

	stripBuiltins(doc)

	prelude, err := parser.ParseSchema(validator.Prelude)
	if err != nil {
		return nil, fmt.Errorf("parsing prelude: %w", err)
	}

	prelude.Merge(doc)

	schema, err := validator.ValidateSchemaDocument(prelude)
	if err != nil {
		return nil, fmt.Errorf("validating schema %s: %w", path, err)
	}

	return schema, nil
}

// stripBuiltins removes scalar and directive declarations whose names are
// GraphQL built-ins. Other definitions with those names are kept.
func stripBuiltins(doc *ast.SchemaDocument) {
	defs := doc.Definitions[:0]
	for _, def := range doc.Definitions {
		if def.Kind == ast.Scalar && builtinScalars[def.Name] {
			continue
		}

		defs = append(defs, def)
	}

	doc.Definitions = defs

	dirs := doc.Directives[:0]
	for _, dir := range doc.Directives {
		if builtinDirectives[dir.Name] {
			continue
		}

		dirs = append(dirs, dir)
	}

	doc.Directives = dirs
}

// loadOperationFile parses one operation file, checks it holds a single
// operation named after the file, and validates and types it when schema is
// non-nil.
func loadOperationFile(path string, schema *ast.Schema, scalars map[string]string) (graphqlFunction, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return graphqlFunction{}, err
	}

	text := string(data)

	doc, err := parser.ParseQuery(&ast.Source{Name: path, Input: text})
	if err != nil {
		return graphqlFunction{}, fmt.Errorf("parsing: %w", err)
	}

	if len(doc.Operations) != 1 || doc.Operations[0].Name == "" {
		return graphqlFunction{}, fmt.Errorf("%w, found %d", errOperationCount, len(doc.Operations))
	}

	op := doc.Operations[0]

	if base := strings.TrimSuffix(filepath.Base(path), ".graphql"); base != op.Name {
		return graphqlFunction{}, fmt.Errorf("%w: file %q holds %q", errOperationName, base, op.Name)
	}

	if schema != nil {
		if errs := validator.ValidateWithRules(schema, doc, nil); len(errs) > 0 {
			return graphqlFunction{}, fmt.Errorf("%w: %s", errInvalidOperation, errs.Error())
		}
	}

	info, err := operationInfo(op, schema, scalars)
	if err != nil {
		return graphqlFunction{}, err
	}

	name := introspect.ToKebab(op.Name)

	return graphqlFunction{
		Name:      name,
		ClassName: goIdent(name),
		Query:     text,
		Op:        info,
	}, nil
}

// operationInfo builds inputs from the operation's variables and outputs from
// the fields selected under its first root field.
func operationInfo(op *ast.OperationDefinition, schema *ast.Schema, scalars map[string]string) (*OperationInfo, error) {
	info := &OperationInfo{OperationID: op.Name}

	for _, v := range op.VariableDefinitions {
		typ, err := graphqlFieldType(v.Type, schema, scalars)
		if err != nil {
			return nil, err
		}

		info.Inputs = append(info.Inputs, FieldInfo{
			Name:     v.Variable,
			Type:     typ,
			Required: v.Type.NonNull && v.DefaultValue == nil,
		})
	}

	root := firstField(op.SelectionSet)
	if root == nil {
		return info, nil
	}

	if root.Definition != nil {
		info.Description = root.Definition.Description
	}

	outputs, err := operationOutputs(root, schema, scalars)
	if err != nil {
		return nil, err
	}

	info.Outputs = outputs

	return info, nil
}

// operationOutputs returns one output per field selected under root. Each is
// typed when schema is non-nil; fields nested deeper are not inspected.
func operationOutputs(root *ast.Field, schema *ast.Schema, scalars map[string]string) ([]FieldInfo, error) {
	var outputs []FieldInfo

	for _, sel := range root.SelectionSet {
		f, ok := sel.(*ast.Field)
		if !ok {
			continue
		}

		var typ string

		if schema != nil && f.Definition != nil {
			var err error

			typ, err = graphqlFieldType(f.Definition.Type, schema, scalars)
			if err != nil {
				return nil, err
			}
		}

		outputs = append(outputs, FieldInfo{Name: f.Alias, Type: typ})
	}

	return outputs, nil
}

func firstField(set ast.SelectionSet) *ast.Field {
	for _, sel := range set {
		if f, ok := sel.(*ast.Field); ok {
			return f
		}
	}

	return nil
}

// graphqlFieldType maps a GraphQL type to the FieldInfo.Type names used for
// OpenAPI. A custom scalar maps to its type in scalars and fails with
// errUndeclaredScalar when scalars does not declare it. Named types that
// cannot be looked up without a schema map to "" (CWL Any).
func graphqlFieldType(t *ast.Type, schema *ast.Schema, scalars map[string]string) (string, error) {
	if t.Elem != nil {
		return "array", nil
	}

	switch t.NamedType {
	case "Int":
		return "integer", nil
	case "Float":
		return "number", nil
	case "Boolean":
		return "boolean", nil
	case "String", "ID":
		return "string", nil
	}

	if schema == nil {
		return "", nil
	}

	def := schema.Types[t.NamedType]
	if def == nil {
		return "", nil
	}

	switch def.Kind {
	case ast.Enum:
		return "string", nil
	case ast.Object, ast.InputObject, ast.Interface, ast.Union:
		return "object", nil
	default: // ast.Scalar; built-in scalars returned above
		return declaredScalarType(def.Name, scalars)
	}
}

// declaredScalarType returns the type scalars declares for a custom scalar.
func declaredScalarType(name string, scalars map[string]string) (string, error) {
	typ, ok := scalars[name]
	if !ok {
		return "", fmt.Errorf("%w: %s", errUndeclaredScalar, name)
	}

	return typ, nil
}
