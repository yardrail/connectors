# Replace anyOf: [{type: array, ...}, {type: string, enum: [""]}] with the array
# branch + nullable. Stripe uses the empty-string enum branch to mean "unset", but
# oapi-codegen emits self-referencing type aliases for the array branch of these unions.
walk(
  if type == "object" and .anyOf and (.anyOf | length) == 2
    and (.anyOf | map(select(.type == "array")) | length) == 1
    and (.anyOf | map(select(.type == "string" and .enum == [""])) | length) == 1
  then (.anyOf[] | select(.type == "array")) + {nullable: true}
  else .
  end
)
