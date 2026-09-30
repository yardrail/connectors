package mcp

//go:generate go run github.com/yardrail/connectors/tools/cmd/mcp-openapi-codegen -pkg github.com/yardrail/connectors/stripe/stripe/v1/client -spec ../spec/openapi.json -name stripe -pkgname mcp -out tools_gen.go
