package mcp

//go:generate go run github.com/yardrail/connectors/tools/cmd/mcp-openapi-codegen -pkg github.com/yardrail/connectors/slack/slack/v1/client -spec ../spec/openapi.json -name slack -pkgname mcp -out tools_gen.go
