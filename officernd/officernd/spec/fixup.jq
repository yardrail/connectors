# Strip "Controller" and "_api/v2" from operationIds.
.paths |= with_entries(
  .value |= with_entries(
    if .value | type == "object" and has("operationId") then
      .value.operationId |= (gsub("Controller(?=_)"; "") | gsub("_api/v2$"; ""))
    else . end
  )
)
