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
	errOperationCount   = errors.New("operation file must hold exactly one named operation")
	errOperationName    = errors.New("operation file name must equal the operation name")
	errInvalidOperation = errors.New("operation does not validate against the schema")
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
// directory of operation files.
type graphqlSource struct {
	Schema     string
	Operations string
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
// is validated against it and inputs and outputs are typed from it.
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
		fn, err := loadOperationFile(path, schema)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}

		funcs = append(funcs, fn)
	}

	return funcs, nil
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
// operation named after the file, and validates it when schema is non-nil.
func loadOperationFile(path string, schema *ast.Schema) (graphqlFunction, error) {
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

	name := introspect.ToKebab(op.Name)

	return graphqlFunction{
		Name:      name,
		ClassName: goIdent(name),
		Query:     text,
		Op:        operationInfo(op, schema),
	}, nil
}

// operationInfo builds inputs from the operation's variables and outputs from
// the fields selected under its first root field.
func operationInfo(op *ast.OperationDefinition, schema *ast.Schema) *OperationInfo {
	info := &OperationInfo{OperationID: op.Name}

	for _, v := range op.VariableDefinitions {
		info.Inputs = append(info.Inputs, FieldInfo{
			Name:     v.Variable,
			Type:     graphqlFieldType(v.Type, schema),
			Required: v.Type.NonNull && v.DefaultValue == nil,
		})
	}

	root := firstField(op.SelectionSet)
	if root == nil {
		return info
	}

	if root.Definition != nil {
		info.Description = root.Definition.Description
	}

	for _, sel := range root.SelectionSet {
		f, ok := sel.(*ast.Field)
		if !ok {
			continue
		}

		var typ string
		if schema != nil && f.Definition != nil {
			typ = graphqlFieldType(f.Definition.Type, schema)
		}

		info.Outputs = append(info.Outputs, FieldInfo{Name: f.Alias, Type: typ})
	}

	return info
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
// OpenAPI. Custom scalars, and named types that cannot be looked up without a
// schema, map to "" (CWL Any).
func graphqlFieldType(t *ast.Type, schema *ast.Schema) string {
	if t.Elem != nil {
		return "array"
	}

	switch t.NamedType {
	case "Int":
		return "integer"
	case "Float":
		return "number"
	case "Boolean":
		return "boolean"
	case "String", "ID":
		return "string"
	}

	if schema == nil {
		return ""
	}

	def := schema.Types[t.NamedType]
	if def == nil {
		return ""
	}

	switch def.Kind {
	case ast.Enum:
		return "string"
	case ast.Object, ast.InputObject, ast.Interface, ast.Union:
		return "object"
	default:
		return ""
	}
}
