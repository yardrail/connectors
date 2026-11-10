package main

import (
	"fmt"

	"github.com/yardrail/connectors/tools/googleintrospect"
)

// googleSDKToolDef wraps a ToolDef with the interface that owns it.
type googleSDKToolDef struct {
	InterfaceID string
	googleintrospect.ToolDef
}

// loadGoogleSDKInterfaces loads functions from every callable google_sdk
// interface into ctx.
func loadGoogleSDKInterfaces(ctx *genContext) error {
	for _, iface := range ctx.CallableIfaces {
		if iface.Protocol != "google_sdk" {
			continue
		}

		result, err := googleintrospect.Analyze(iface.Source.GoogleSDK)
		if err != nil {
			return fmt.Errorf("interface %q: google sdk: %w", iface.ID, err)
		}

		if ctx.GoogleSDKPkgPath != "" && ctx.GoogleSDKPkgPath != iface.Source.GoogleSDK {
			return fmt.Errorf("google_sdk interfaces must share one package")
		}
		ctx.GoogleSDKPkgPath = iface.Source.GoogleSDK
		ctx.GoogleSDKSvcType = result.RootServiceName
		ctx.GoogleSDKIfaces = append(ctx.GoogleSDKIfaces, iface)

		for _, tool := range result.Tools {
			ctx.GoogleSDKToolDefs = append(ctx.GoogleSDKToolDefs, googleSDKToolDef{
				InterfaceID: iface.ID,
				ToolDef:     tool,
			})

			ctx.Functions = append(ctx.Functions, connectorFunction{
				Name:        tool.Name,
				ClassName:   goIdent(tool.Name),
				MethodName:  tool.ServiceName + tool.MethodName,
				InterfaceID: iface.ID,
				Op:          googleSDKToolToOp(tool),
			})
		}
	}

	return nil
}

// googleSDKToolToOp converts a ToolDef into an OperationInfo for CWL schema
// generation and registration template rendering.
func googleSDKToolToOp(tool googleintrospect.ToolDef) *OperationInfo {
	var inputs []FieldInfo
	for _, p := range tool.Params {
		inputs = append(inputs, FieldInfo{
			Name:     p.JSONName,
			Type:     googleSDKParamType(p),
			Required: p.Required,
			Doc:      p.Desc,
		})
	}

	return &OperationInfo{
		OperationID: tool.Name,
		Description: tool.Desc,
		Inputs:      inputs,
	}
}

// googleSDKParamType maps a ToolParam's GoType to a FieldInfo type string.
func googleSDKParamType(p googleintrospect.ToolParam) string {
	if p.IsStruct {
		return "object"
	}
	if googleintrospect.IsIntegerType(p.GoType) {
		return "integer"
	}
	switch p.GoType {
	case "string":
		return "string"
	case "bool":
		return "boolean"
	case "float32", "float64":
		return "number"
	case "[]string":
		return "array"
	default:
		return "string"
	}
}
