package main

import (
	"bytes"
	"fmt"
	"sort"
	"strings"

	"github.com/yardrail/connectors/tools/internal/introspect"
)

func renderCWLSchema(ctx *genContext) ([]byte, error) {
	connID := ctx.ConnectorID
	nsURI := "https://yardrail.com/cwl/" + connID + "#"

	var buf bytes.Buffer
	fmt.Fprintf(&buf, "$base: %q\n", nsURI)
	fmt.Fprintf(&buf, "$namespaces:\n")
	fmt.Fprintf(&buf, "  %s: %q\n", connID, nsURI)
	fmt.Fprintf(&buf, "  cwl: \"https://w3id.org/cwl/cwl#\"\n")
	fmt.Fprintf(&buf, "\n$graph:\n")

	funcs := append([]connectorFunction(nil), ctx.Functions...)
	sort.Slice(funcs, func(i, j int) bool {
		return funcs[i].Name < funcs[j].Name
	})

	for _, fn := range funcs {
		writeClassEnum(&buf, connID, fn.ClassName)
		writeClassRecord(&buf, connID, fn.ClassName, fn.Op)
	}

	return buf.Bytes(), nil
}

func writeClassEnum(buf *bytes.Buffer, connID, className string) {
	fmt.Fprintf(buf, "\n  - name: %s_class\n", className)
	fmt.Fprintf(buf, "    type: enum\n")
	fmt.Fprintf(buf, "    inVocab: false\n")
	fmt.Fprintf(buf, "    symbols: [\"%s:%s\"]\n", connID, className)
}

func writeClassRecord(buf *bytes.Buffer, connID, className string, op *OperationInfo) {
	doc := ""
	if op != nil {
		doc = op.Description
	}

	fmt.Fprintf(buf, "\n  - name: %s\n", className)
	fmt.Fprintf(buf, "    type: record\n")
	fmt.Fprintf(buf, "    inVocab: false\n")
	fmt.Fprintf(buf, "    extends: cwl:Process\n")
	fmt.Fprintf(buf, "    documentRoot: true\n")
	if doc != "" {
		fmt.Fprintf(buf, "    doc: %q\n", doc)
	}
	fmt.Fprintf(buf, "    specialize:\n")
	fmt.Fprintf(buf, "      - specializeFrom: cwl:InputParameter\n")
	fmt.Fprintf(buf, "        specializeTo: cwl:OperationInputParameter\n")
	fmt.Fprintf(buf, "      - specializeFrom: cwl:OutputParameter\n")
	fmt.Fprintf(buf, "        specializeTo: cwl:OperationOutputParameter\n")
	fmt.Fprintf(buf, "    fields:\n")
	fmt.Fprintf(buf, "      - name: class\n")
	fmt.Fprintf(buf, "        type: \"#%s_class\"\n", className)
	fmt.Fprintf(buf, "        jsonldPredicate:\n")
	fmt.Fprintf(buf, "          _id: \"@type\"\n")
	fmt.Fprintf(buf, "          _type: \"@vocab\"\n")
}

func openAPIToCWLType(t string) string {
	switch t {
	case "string":
		return "string"
	case "integer":
		return "int"
	case "number":
		return "float"
	case "boolean":
		return "boolean"
	default:
		return "Any"
	}
}

// openAPIFunctions pairs each oapi-codegen method with its spec operation.
func openAPIFunctions(methods []introspect.MethodInfo, ops map[string]*OperationInfo) []connectorFunction {
	funcs := make([]connectorFunction, 0, len(methods))
	for _, m := range methods {
		funcs = append(funcs, connectorFunction{
			Name:      m.Name,
			ClassName: goIdent(m.Name),
			Op:        ops[normalizeMethodToOpKey(m.Name)],
		})
	}

	return funcs
}

// normalizeMethodToOpKey converts a kebab method name to the same normalized
// form used as the key in SpecInfo.Operations.
func normalizeMethodToOpKey(kebabName string) string {
	return strings.ToLower(strings.ReplaceAll(kebabName, "-", ""))
}
