package mcp

//go:generate go run github.com/yardrail/connectors/tools/cmd/mcp-openapi-codegen -pkg github.com/yardrail/connectors/quickbooks/account/client -spec ../spec/openapi.json -name quickbooks-account -pkgname mcp -out tools_gen.go
