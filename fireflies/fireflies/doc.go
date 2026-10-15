// Package fireflies holds the Fireflies GraphQL API contract
// (spec/schema.graphql) and the operations (operations/*.graphql) that
// yardrail-connector-codegen turns into the yardrail Fireflies connector, as
// declared in manifest.yaml. The meeting operation is named GetMeeting because
// Fireflies calls a meeting record a "transcript", and it does not select the
// full transcript text. Regenerate the yardrail code with
// `task generate:connector:fireflies` here or `task gen:connectors` in the
// yardrail repo.
package fireflies
