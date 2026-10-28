package graphqlfixture

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// recorded is the request a test server received.
type recorded struct {
	authorization string
	apiVersion    string
	body          graphqlRequest
}

// newServer starts a server that records each request and replies with
// status and body.
func newServer(t *testing.T, status int, body string) (*httptest.Server, func() recorded) {
	t.Helper()

	var (
		mu  sync.Mutex
		got recorded
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("reading request: %v", err)
		}

		mu.Lock()
		got.authorization = r.Header.Get("Authorization")
		got.apiVersion = r.Header.Get("API-Version")

		if err := json.Unmarshal(raw, &got.body); err != nil {
			t.Errorf("decoding request %q: %v", raw, err)
		}
		mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)

		if _, err := io.WriteString(w, body); err != nil {
			t.Errorf("writing response: %v", err)
		}
	}))
	t.Cleanup(srv.Close)

	return srv, func() recorded {
		mu.Lock()
		defer mu.Unlock()

		return got
	}
}

// call resolves function on the api interface against srv and runs it.
func call(t *testing.T, srv *httptest.Server, function, inputs string) (any, error) {
	t.Helper()

	fn, err := Resolve("api", function, map[string]string{"token": "secret-token"}, map[string]string{"base_url": srv.URL}, nil)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	return fn(context.Background(), json.RawMessage(inputs))
}

func TestDispatchRequest(t *testing.T) {
	t.Parallel()

	srv, got := newServer(t, http.StatusOK, `{"data":{"widget":{"id":"7"}}}`)

	if _, err := call(t, srv, "get-widget", `{"widgetId":"7"}`); err != nil {
		t.Fatalf("call: %v", err)
	}

	req := got()

	tests := []struct {
		name string
		got  string
		want string
	}{
		{"query is the operation text", req.body.Query, queryGetWidget},
		{"variables are the step inputs", string(req.body.Variables), `{"widgetId":"7"}`},
		{"Authorization carries the mapped credential", req.authorization, "secret-token"},
		{"static header", req.apiVersion, "2026-01"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if tt.got != tt.want {
				t.Errorf("got %q, want %q", tt.got, tt.want)
			}
		})
	}
}

func TestDispatchResponse(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		status   int
		body     string
		wantData string
		wantErr  string
	}{
		{
			name:     "data only",
			status:   http.StatusOK,
			body:     `{"data":{"rename_widget":{"id":"7","name":"new"}}}`,
			wantData: `{"rename_widget":{"id":"7","name":"new"}}`,
		},
		{
			name:    "data and errors",
			status:  http.StatusOK,
			body:    `{"data":{"rename_widget":null},"errors":[{"message":"widget is locked"},{"message":"try later"}]}`,
			wantErr: "widget is locked; try later",
		},
		{
			name:    "HTTP error",
			status:  http.StatusUnauthorized,
			body:    `{"error_message":"bad token"}`,
			wantErr: "HTTP 401",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			srv, _ := newServer(t, tt.status, tt.body)

			out, err := call(t, srv, "rename-widget", `{"widgetId":"7","name":"new"}`)

			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error = %v, want one containing %q", err, tt.wantErr)
				}

				return
			}

			if err != nil {
				t.Fatalf("call: %v", err)
			}

			raw, ok := out.(json.RawMessage)
			if !ok {
				t.Fatalf("result is %T, want json.RawMessage", out)
			}

			if string(raw) != tt.wantData {
				t.Errorf("data = %s, want %s", raw, tt.wantData)
			}
		})
	}
}

func TestResolveErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		interfaceID string
		function    string
		wantErr     error
	}{
		{"unknown function", "api", "delete-widget", errUnknownFunction},
		{"unknown interface", "other", "get-widget", errInterfaceNotConfigured},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := Resolve(tt.interfaceID, tt.function, map[string]string{"token": "t"}, nil, nil)
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("Resolve error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}
