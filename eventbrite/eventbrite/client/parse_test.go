package client

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

// Eventbrite returns tx_time_limit as a number; the apib source declares a string.
func TestParseListEventsByOrganizationResponse_txTimeLimit(t *testing.T) {
	t.Parallel()

	rsp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": {"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"events":[{"id":"1","tx_time_limit":1200}]}`)),
	}

	parsed, err := ParseListEventsByOrganizationResponse(rsp)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	if parsed.JSON200 == nil {
		t.Fatal("JSON200 is nil")
	}
}
