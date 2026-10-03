package mcp

//go:generate go run github.com/yardrail/connectors/tools/cmd/mcp-openapi-codegen -pkg github.com/yardrail/connectors/notion/notion/v1/client -spec ../spec/openapi.json -name notion -pkgname mcp -out tools_gen.go
