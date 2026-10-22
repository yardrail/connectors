#!/usr/bin/env python3
"""Fix Eventbrite spec issues from the apib->swagger2->openapi3 conversion pipeline.

Reads swagger2.json, applies fixes, writes openapi3-ready swagger2-fixed.json.
Then reads the final openapi.json (after swagger2openapi) and patches it too.

Usage (called by Taskfile):
  python3 fixup.py swagger2 swagger2.json swagger2-fixed.json
  python3 fixup.py openapi3 openapi.json
"""
import json, re, sys


def fix_swagger2(inpath, outpath):
    with open(inpath) as f:
        spec = json.load(f)

    for path_item in spec.get("paths", {}).values():
        for op in path_item.values():
            if not isinstance(op, dict):
                continue
            # Deduplicate body parameters (blueprint produces multiples per operation)
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

    # Normalise angle-bracket path params to curly braces
    spec["paths"] = {
        re.sub(r"<(\w+)>", r"{\1}", path): methods
        for path, methods in spec["paths"].items()
    }

    with open(outpath, "w") as f:
        json.dump(spec, f, indent=2)


def fix_openapi3(path):
    with open(path) as f:
        spec = json.load(f)

    # Add bearer token security scheme (not in the apib source)
    spec.setdefault("components", {}).setdefault("securitySchemes", {})["bearer"] = {
        "type": "http",
        "scheme": "bearer",
    }

    for url, path_item in spec.get("paths", {}).items():
        path_params = re.findall(r"\{(\w+)\}", url)

        # Remove bogus requestBody from GET operations
        if "get" in path_item and "requestBody" in path_item["get"]:
            del path_item["get"]["requestBody"]

        for op in path_item.values():
            if not isinstance(op, dict):
                continue
            params = op.get("parameters", [])

            # Add missing path parameter declarations
            declared = {p["name"] for p in params if isinstance(p, dict) and p.get("in") == "path"}
            for name in path_params:
                if name not in declared:
                    params.append({"name": name, "in": "path", "required": True, "schema": {"type": "string"}})
            if params:
                op["parameters"] = params

            # Fix param types
            for param in params:
                if not isinstance(param, dict):
                    continue
                schema = param.get("schema", {})
                if schema.get("type") == "number":
                    schema["type"] = "string" if param.get("in") == "path" else "integer"

    # Wire up $ref for list endpoints whose items are untyped ({}) but have
    # a matching component schema.  The apib→swagger2→openapi3 pipeline loses
    # array item types; this table restores them for the endpoints we use.
    list_item_refs = {
        "attendees": "#/components/schemas/Attendee",
        "orders": "#/components/schemas/Order",
        "events": "#/components/schemas/Event",
        "ticket_classes": "#/components/schemas/Ticket_Class",
        "venues": "#/components/schemas/Venue_Response",
        "discounts": "#/components/schemas/Discount",
        "teams": "#/components/schemas/Event_Team_Response",
        "organizations": "#/components/schemas/Organization",
        "categories": "#/components/schemas/Category",
        "subcategories": "#/components/schemas/Subcategory",
        "formats": "#/components/schemas/Format",
        "roles": "#/components/schemas/Role",
    }
    for path_item in spec.get("paths", {}).values():
        for op in path_item.values():
            if not isinstance(op, dict):
                continue
            resp = op.get("responses", {}).get("200", {})
            schema = resp.get("content", {}).get("application/json", {}).get("schema")
            if not schema:
                continue
            props = schema.get("properties", {})
            for prop_name, prop_val in props.items():
                if prop_val.get("type") == "array" and prop_val.get("items") == {}:
                    ref = list_item_refs.get(prop_name)
                    if ref:
                        prop_val["items"] = {"$ref": ref}
            # Replace inline pagination with component ref
            if "pagination" in props and "$ref" not in props["pagination"]:
                props["pagination"] = {"$ref": "#/components/schemas/Pagination"}

    # Add query parameters the conversion dropped, for the endpoints we use.
    # Descriptions come from the apib source.
    extra_query_params = {
        ("/organizations/{organization_id}/events/", "get"): [
            {"name": "status", "in": "query", "required": False, "schema": {"type": "string"},
             "description": "Filter Organization's Events by status. Specify multiple status values as a comma delimited string."},
            {"name": "order_by", "in": "query", "required": False,
             "schema": {"type": "string", "enum": ["start_asc", "start_desc", "created_asc", "created_desc", "name_asc", "name_desc"]},
             "description": "Sort order for the list of Events."},
            {"name": "page_size", "in": "query", "required": False, "schema": {"type": "integer"},
             "description": "Number of records to display on each page of the list."},
        ],
    }
    for (url, method), extra in extra_query_params.items():
        op = spec.get("paths", {}).get(url, {}).get(method)
        if op is None:
            continue
        params = op.setdefault("parameters", [])
        declared = {p["name"] for p in params if isinstance(p, dict)}
        params.extend(p for p in extra if p["name"] not in declared)

    # Drop property names containing whitespace: apib description lines the
    # conversion turned into keys (e.g. "fee_name: payment_fee"). They are not
    # API fields and produce struct tags go vet rejects.
    # Also drop empty-object allOf members ({"type": "object", "properties": {}})
    # the conversion appends to $refs; oapi-codegen cannot merge them with a
    # string $ref such as datetime.
    def drop_bogus_props(node):
        if isinstance(node, dict):
            if isinstance(node.get("allOf"), list):
                node["allOf"] = [m for m in node["allOf"] if m != {"type": "object", "properties": {}}]
            props = node.get("properties")
            if isinstance(props, dict):
                bogus = [k for k in props if re.search(r"\s", k)]
                for k in bogus:
                    del props[k]
                if bogus and isinstance(node.get("required"), list):
                    node["required"] = [r for r in node["required"] if r not in bogus]
            for v in node.values():
                drop_bogus_props(v)
        elif isinstance(node, list):
            for v in node:
                drop_bogus_props(v)

    drop_bogus_props(spec)

    with open(path, "w") as f:
        json.dump(spec, f, indent=2)


if __name__ == "__main__":
    cmd = sys.argv[1]
    if cmd == "swagger2":
        fix_swagger2(sys.argv[2], sys.argv[3])
    elif cmd == "openapi3":
        fix_openapi3(sys.argv[2])
    else:
        sys.exit(f"unknown command: {cmd}")
