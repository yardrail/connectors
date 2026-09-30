# connectors

Go packages for integrating [yardrail](https://github.com/yardrail/yardrail) with external services. Each service produces a typed Go client and/or an MCP server package. Yardrail imports these packages directly.

This repo also holds the API specs and codegen tools used to produce those packages.

## Repository structure

```
connectors/
├── go.work                         # Go workspace — lists all modules
├── Taskfile.yml                    # Orchestrate codegen and validation across modules
├── tools/                          # Codegen utilities (separate Go module)
│   └── cmd/
│       ├── mcp-google-codegen/     # Generate MCP server from a Google Go SDK package
│       └── mcp-openapi-codegen/    # Generate MCP server from an oapi-codegen client
└── <company>/
    └── <product>/
        └── <version>/              # Each version is its own Go module
            ├── go.mod
            ├── spec/               # API contract (OpenAPI, protobuf, graphql, etc.)
            ├── client/             # Generated typed Go client
            └── mcp/                # Generated MCP server package
```

### Naming convention

Services are nested `company/product/version`:

| Path                      | Module                                                  |
| ------------------------- | ------------------------------------------------------- |
| `google/sheets/v4/`       | `github.com/yardrail/connectors/google/sheets/v4`       |
| `atlassian/jira/v3/`      | `github.com/yardrail/connectors/atlassian/jira/v3`      |
| `slack/slack/v1/`         | `github.com/yardrail/connectors/slack/slack/v1`         |
| `officernd/officernd/v2/` | `github.com/yardrail/connectors/officernd/officernd/v2` |

Single-product companies repeat the name (`slack/slack/`, `officernd/officernd/`). This keeps the structure consistent and avoids restructuring if the company later exposes additional products.

Each API version is a discrete Go module. Importing `google/sheets/v4` does not pull in `google/sheets/v3` dependencies.

## Service archetypes

### SDK-backed

For services with an official Go SDK (e.g. Google Sheets via `google.golang.org/api/sheets/v4`):

- No `spec/` or `client/` directories
- `go.mod` depends on the official SDK, acting as a version pin
- Root package re-exports key types from the SDK
- `mcp/` is generated from the SDK package via `mcp-google-codegen`

```
google/sheets/v4/
├── go.mod              # depends on google.golang.org/api/sheets/v4
├── doc.go              # re-exports SDK types
└── mcp/
    ├── generate.go     # //go:generate mcp-google-codegen ...
    └── tools_gen.go
```

### Spec-generated

For services without a usable Go SDK — everything is generated from the API spec:

- `spec/` holds the API contract (OpenAPI JSON/YAML, protobuf, graphql schema)
- `client/` holds the generated typed Go client (via oapi-codegen or future protocol-specific generators)
- `mcp/` is generated from the client package via `mcp-openapi-codegen`

```
officernd/officernd/v2/
├── go.mod
├── spec/
│   └── openapi.json
├── client/
│   ├── generate.go     # //go:generate oapi-codegen -config cfg.yaml ../../spec/openapi.json
│   ├── cfg.yaml
│   └── client_gen.go
└── mcp/
    ├── generate.go     # //go:generate mcp-openapi-codegen ...
    └── tools_gen.go
```

### Hybrid

For services with an official SDK but where MCP is generated from the spec (because no custom MCP generator exists for that SDK):

- No `client/` directory — consumers use the SDK directly, pinned in `go.mod`
- `spec/` holds the API contract
- `mcp/` is generated directly from the spec via `mcp-openapi-codegen`, which handles internal client generation as a build step

```
somecompany/someproduct/v1/
├── go.mod              # depends on the official SDK
├── doc.go
├── spec/
│   └── openapi.json
└── mcp/
    ├── generate.go     # //go:generate mcp-openapi-codegen -spec ../spec/openapi.json ...
    └── tools_gen.go
```

## Codegen tools

All codegen commands live in `tools/`, which is its own Go module (`github.com/yardrail/connectors/tools`). This keeps codegen dependencies (template libraries, AST packages) out of service modules.

| Command               | Purpose                                                 |
| --------------------- | ------------------------------------------------------- |
| `mcp-google-codegen`  | Generate MCP server package from a Google Go SDK        |
| `mcp-openapi-codegen` | Generate MCP server package from an oapi-codegen client |

Service packages invoke these via `//go:generate` directives in `generate.go` files.

Future protocol-specific generators (graphql, grpc, etc.) will be added as sibling commands under `tools/cmd/`.

## Adding a new service

1. Create the directory: `mkdir -p <company>/<product>/<version>`
2. Initialize the module: `cd <company>/<product>/<version> && go mod init github.com/yardrail/connectors/<company>/<product>/<version>`
3. Add `generate.go` files with the appropriate `//go:generate` directives (copy from an existing package of the same archetype)
4. Copy in the API spec if applicable
5. Run `go generate ./...` to produce the generated code
6. Run `task sync:workspace` to update the Go workspace

## Go workspace

The root `go.work` file lists all modules for local development. This enables cross-module IDE support and `go build ./...` from the repo root. At scale, the `go.work` file itself may need to be generated from the directory tree.
