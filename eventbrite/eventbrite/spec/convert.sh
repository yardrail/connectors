#!/usr/bin/env bash
#
# Converts the Eventbrite API Blueprint to OpenAPI 3.0.
#
# Prerequisites:
#   npx (ships with Node.js)
#   python3
#
# Usage:
#   cd eventbrite/eventbrite/spec
#   ./convert.sh
#
set -euo pipefail
cd "$(dirname "$0")"

APIB="eventbrite-api-v3-public.apib"
SWAGGER2="swagger2.json"
OPENAPI="openapi.json"

echo "==> Converting API Blueprint to Swagger 2.0"
npx --yes api-spec-converter@2 --from api_blueprint --to swagger_2 "$APIB" > "$SWAGGER2"

echo "==> Fixing Swagger 2.0 spec"
python3 - "$SWAGGER2" <<'PYEOF'
import json, re, sys

with open(sys.argv[1]) as f:
    spec = json.load(f)

# 1. Deduplicate body parameters (blueprint produces multiples per operation)
for path, path_item in spec.get("paths", {}).items():
    for method, op in path_item.items():
        if not isinstance(op, dict):
            continue
        params = op.get("parameters", [])
        seen_body = False
        new_params = []
        for p in params:
            if isinstance(p, dict) and p.get("in") == "body":
                if seen_body:
                    continue
                seen_body = True
            new_params.append(p)
        op["parameters"] = new_params

# 2. Normalise angle-bracket path params to curly braces
new_paths = {}
for path, methods in spec.get("paths", {}).items():
    new_paths[re.sub(r"<(\w+)>", r"{\1}", path)] = methods
spec["paths"] = new_paths

with open(sys.argv[1], "w") as f:
    json.dump(spec, f, indent=2)
PYEOF

echo "==> Converting Swagger 2.0 to OpenAPI 3.0"
npx --yes swagger2openapi@7 --patch "$SWAGGER2" -o "$OPENAPI"
rm "$SWAGGER2"

echo "==> Fixing OpenAPI 3.0 spec"
python3 - "$OPENAPI" <<'PYEOF'
import json, re, sys

with open(sys.argv[1]) as f:
    spec = json.load(f)

# 1. Add missing path parameter declarations
for path, path_item in spec.get("paths", {}).items():
    path_params = re.findall(r"\{(\w+)\}", path)
    if not path_params:
        continue
    for method, op in path_item.items():
        if method in ("summary", "description", "servers", "parameters") or not isinstance(op, dict):
            continue
        params = op.get("parameters", [])
        declared = {p.get("name") for p in params if isinstance(p, dict) and p.get("in") == "path"}
        for name in path_params:
            if name not in declared:
                params.append({"name": name, "in": "path", "required": True, "schema": {"type": "string"}})
        if params:
            op["parameters"] = params

# 2. Path param IDs should be strings (they're URL path segments)
for path, path_item in spec.get("paths", {}).items():
    for method, op in path_item.items():
        if not isinstance(op, dict):
            continue
        for param in op.get("parameters", []):
            if isinstance(param, dict) and param.get("in") == "path":
                schema = param.get("schema", {})
                if schema.get("type") == "number":
                    schema["type"] = "string"

# 3. Number-typed query params -> integer
for path, path_item in spec.get("paths", {}).items():
    for method, op in path_item.items():
        if not isinstance(op, dict):
            continue
        for param in op.get("parameters", []):
            if isinstance(param, dict) and param.get("in") == "query":
                schema = param.get("schema", {})
                if schema.get("type") == "number":
                    schema["type"] = "integer"

# 4. Remove bogus requestBody from GET operations
for path, path_item in spec.get("paths", {}).items():
    if "get" in path_item and "requestBody" in path_item["get"]:
        del path_item["get"]["requestBody"]

with open(sys.argv[1], "w") as f:
    json.dump(spec, f, indent=2)
PYEOF

echo "==> Done: $OPENAPI"
