# QuickBooks Online Accounting API Connector

Generated from the [QuickBooks Online Accounting API Postman collection](https://www.postman.com/intuit-developer/quickbooks-online-accounting-api/).

## Conversion Pipeline

1. Convert Postman collection to OpenAPI: `postman2openapi spec/postman_collection.json -f json > spec/openapi.json`
2. Fix OpenAPI spec (see below)
3. Generate client: `go generate ./client/...`
4. Generate MCP tools: `go generate ./mcp/...`

## Manual Fixes Required After Conversion

The Postman collection has quirks that produce an invalid or unusable OpenAPI spec. These fixes were applied to `spec/openapi.json` after conversion:

### 1. Duplicate `companyid` path parameter

The `/v3/company/{companyid}/companyinfo/{companyid}` path uses `companyid` twice. OpenAPI requires unique parameter names per path. Fix: rename the second to `{companyInfoId}`.

### 2. `text/plain` request bodies

Several endpoints (query, upload, vendorcredit create) have raw SQL-like bodies (`Select * from Account...`) that postman2openapi maps to `text/plain` content type. oapi-codegen generates broken Go for text/plain body interfaces. Fix: change `text/plain` to `application/json` with a `{"query": "string"}` schema.

### 3. Invalid characters in tag names

The `ChangeOrderNew!` folder name produces an invalid Go identifier. Fix: strip non-alphanumeric characters from tag names.

### 4. Path-level parameters

postman2openapi emits path-level `parameters` arrays. The mcp-openapi-codegen tool expects parameters at the operation level only. Fix: push path-level parameters down into each operation.
