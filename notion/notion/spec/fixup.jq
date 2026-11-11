# Add tags to operations that lack them, derived from the URL path resource.
.paths |= with_entries(
  .key as $path |
  .value |= with_entries(
    if .value | type == "object" and has("operationId") and ((.tags // []) | length) == 0 then
      .value.tags = [
        ($path | split("/") | .[2] | gsub("\\{[^}]+\\}"; "") | gsub("_"; " ")
         | split(" ") | map(select(length > 0))
         | map((.[:1] | ascii_upcase) + .[1:])
         | join(" "))
      ]
    else .
    end
  )
)
