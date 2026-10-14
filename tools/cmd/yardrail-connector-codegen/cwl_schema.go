package main

import (
	"bytes"
	"fmt"
	"sort"
	"strings"
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

	methods := ctx.Methods
	sort.Slice(methods, func(i, j int) bool {
		return methods[i].Name < methods[j].Name
	})

	for _, m := range methods {
		className := goIdent(m.Name)
		opKey := normalizeMethodToOpKey(m.Name)
		op := ctx.Spec.Operations[opKey]

		writeClassEnum(&buf, connID, className)
		writeClassRecord(&buf, connID, className, op)
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

// normalizeMethodToOpKey converts a kebab method name to the same normalized
// form used as the key in Spec.Operations.
func normalizeMethodToOpKey(kebabName string) string {
	return strings.ToLower(strings.ReplaceAll(kebabName, "-", ""))
}
