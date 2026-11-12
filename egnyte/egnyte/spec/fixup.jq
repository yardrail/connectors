# Add tags to operations that lack them.
# /v1/fs/... → "Fs", /pubapi/.../perms/... → "Perms"
.paths |= with_entries(
  .key as $path |
  .value |= with_entries(
    if .value | type == "object" and has("operationId") and ((.tags // []) | length) == 0 then
      .value.tags = [
        if ($path | startswith("/v1/fs")) then "Fs"
        elif ($path | contains("/perms")) then "Perms"
        else ($path | split("/") | .[2] // "Unknown")
        end
      ]
    else .
    end
  )
)
