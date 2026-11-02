package connector

import (
	"context"
	"encoding/json"
	"net/http"
)

// Resolver creates a dispatch function for a specific connector
// interface and function. The returned function accepts JSON-encoded
// inputs and returns the result.
type Resolver func(
	interfaceID, function string,
	creds, config map[string]string,
	httpClient *http.Client,
) (func(context.Context, json.RawMessage) (any, error), error)
