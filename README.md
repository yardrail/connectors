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

| Path | Module | Tools |
|---|---|---|
| `officernd/officernd/v2/` | `github.com/yardrail/connectors/officernd/officernd/v2` | 161 |
| `hubspot/crm-contacts/v3/` | `github.com/yardrail/connectors/hubspot/crm-contacts/v3` | 13 |
| `canva/canva/v1/` | `github.com/yardrail/connectors/canva/canva/v1` | 61 |
| `egnyte/egnyte/v1/` | `github.com/yardrail/connectors/egnyte/egnyte/v1` | 178 |
| `mailchimp/marketing/v3/` | `github.com/yardrail/connectors/mailchimp/marketing/v3` | 298 |
| `notion/notion/v1/` | `github.com/yardrail/connectors/notion/notion/v1` | 9 |
| `slack/slack/v1/` | `github.com/yardrail/connectors/slack/slack/v1` | ~150 |
| `stripe/stripe/v1/` | `github.com/yardrail/connectors/stripe/stripe/v1` | 611 |
| `monday/monday/v2/` | `github.com/yardrail/connectors/monday/monday/v2` | — |

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

- `spec/` holds the API contract (OpenAPI JSON/YAML)
- `client/` holds the generated typed Go client (via oapi-codegen)
- `mcp/` is generated from the client package via `mcp-openapi-codegen`

```
officernd/officernd/v2/
├── go.mod
├── spec/
│   └── openapi.json
├── client/
│   ├── generate.go     # //go:generate oapi-codegen -config cfg.yaml ../spec/openapi.json
│   ├── cfg.yaml
│   └── client_gen.go
└── mcp/
    ├── generate.go     # //go:generate mcp-openapi-codegen ...
    └── tools_gen.go
```

## Spec preparation

Many upstream specs need fixups before oapi-codegen and mcp-openapi-codegen can process them. Common issues and their fixes:

| Issue | Fix | Affected |
|---|---|---|
| Swagger 2.0 instead of OpenAPI 3.x | Convert with `npx swagger2openapi --patch` | Slack, Mailchimp |
| OpenAPI 3.1 features (`type: [x, null]`, `const`) | Downconvert to 3.0 (`nullable: true`, `const` → single-value `enum`) | Notion |
| No tags on operations | Add tags derived from URL path prefix | Stripe |
| Response schema names collide with oapi-codegen response wrappers | Set `response-type-suffix: Resp` in `cfg.yaml` | Canva, Egnyte |
| Duplicate type names (e.g. `EventType` enum vs `Event.type` property) | Add `x-go-name` override to one schema | Egnyte, Stripe |
| Self-referencing type aliases (`type X = []X`) | Remove from generated output | Stripe |
| Missing `components/headers` referenced by `$ref` | Add stub definitions | Egnyte |
| Dotted or hyphenated tag names | Handled by `mcp-openapi-codegen` tag sanitization | Slack, Egnyte, Mailchimp |

## Codegen tools

All codegen commands live in `tools/`, which is its own Go module (`github.com/yardrail/connectors/tools`). This keeps codegen dependencies (template libraries, AST packages) out of service modules.

| Command | Purpose |
|---|---|
| `mcp-google-codegen` | Generate MCP server package from a Google Go SDK |
| `mcp-openapi-codegen` | Generate MCP server package from an oapi-codegen client |

Service packages invoke these via `//go:generate` directives in `generate.go` files.

`mcp-openapi-codegen` handles type conversions for `uuid.UUID`, `types.Date`, `time.Time`, named string types, `[]string`, and `map[string]interface{}` params. It sanitizes tag names containing dots, hyphens, and underscores, and deduplicates operations that appear under multiple tags.

Future protocol-specific generators (graphql, grpc, etc.) will be added as sibling commands under `tools/cmd/`.

## Taskfile

| Task | Description |
|---|---|
| `task build:tools` | Build codegen tool binaries |
| `task generate` | Run `go generate` across all service modules |
| `task build` | Build all modules |
| `task test` | Run tests across all modules |
| `task tidy` | Run `go mod tidy` across all modules |
| `task vet` | Run `go vet` across all modules |
| `task lint` | Run vet + build + test in sequence |
| `task list:modules` | List all Go modules in the repo |
| `task sync:workspace` | Regenerate `go.work` from all `go.mod` files |

## Adding a new service

1. Create the directory: `mkdir -p <company>/<product>/<version>/{spec,client,mcp}`
2. Initialize the module: `cd <company>/<product>/<version> && go mod init github.com/yardrail/connectors/<company>/<product>/<version>`
3. Copy in the API spec to `spec/` — apply any necessary fixups (see Spec preparation above)
4. Create `doc.go`, `client/generate.go`, `client/cfg.yaml`, `mcp/generate.go` (copy from an existing package of the same archetype)
5. Run `task sync:workspace` to add the module to `go.work`
6. Run `go generate ./client/` then `go mod tidy`
7. Run `go generate ./mcp/` then `go mod tidy`
8. Verify with `go build ./...`

## Go workspace

The root `go.work` file lists all modules for local development. This enables cross-module IDE support and `go build ./...` from the repo root. At scale, the `go.work` file itself may need to be generated from the directory tree.
