package mixedfixture

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// hit is what a test server saw on its last request.
type hit struct {
	path          string
	authorization string
	notionVersion string
	query         string
}

// newServer starts a server that records each request and replies with body.
func newServer(t *testing.T, body string) (*httptest.Server, func() hit) {
	t.Helper()

	var (
		mu   sync.Mutex
		last hit
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("reading request: %v", err)
		}

		var req graphqlRequest
		if len(raw) > 0 {
			// Only GraphQL requests decode into a query; REST bodies leave it empty.
			_ = json.Unmarshal(raw, &req)
		}

		mu.Lock()
		last = hit{
			path:          r.URL.Path,
			authorization: r.Header.Get("Authorization"),
			notionVersion: r.Header.Get("Notion-Version"),
			query:         req.Query,
		}
		mu.Unlock()

		w.Header().Set("Content-Type", "application/json")

		if _, err := io.WriteString(w, body); err != nil {
			t.Errorf("writing response: %v", err)
		}
	}))
	t.Cleanup(srv.Close)

	return srv, func() hit {
		mu.Lock()
		defer mu.Unlock()

		return last
	}
}

func TestResolveGraphQLInterface(t *testing.T) {
	t.Parallel()

	srv, last := newServer(t, `{"data":{"widget":{"id":"7"}}}`)

	fn, err := Resolve("gql", "get-widget", map[string]string{"token": "gql-token"}, map[string]string{"base_url": srv.URL})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	out, err := fn(context.Background(), json.RawMessage(`{"widgetId":"7"}`))
	if err != nil {
		t.Fatalf("call: %v", err)
	}

	if got, want := string(out.(json.RawMessage)), `{"widget":{"id":"7"}}`; got != want {
		t.Errorf("data = %s, want %s", got, want)
	}

	h := last()
	if h.query != queryGetWidget {
		t.Errorf("query = %q, want the GetWidget operation", h.query)
	}

	if h.authorization != "gql-token" {
		t.Errorf("Authorization = %q, want raw gql-token", h.authorization)
	}
}

func TestResolveRESTInterface(t *testing.T) {
	t.Parallel()

	srv, last := newServer(t, `{"object":"database","id":"db1"}`)

	fn, err := Resolve("rest", "retrieve-a-database", map[string]string{"token": "rest-token"}, map[string]string{"base_url": srv.URL})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	if _, err := fn(context.Background(), json.RawMessage(`{"databaseId":"db1"}`)); err != nil {
		t.Fatalf("call: %v", err)
	}

	h := last()

	tests := []struct {
		name string
		got  string
		want string
	}{
		{"path", h.path, "/v1/databases/db1"},
		{"bearer auth", h.authorization, "Bearer rest-token"},
		{"static header", h.notionVersion, "2022-06-28"},
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

func TestResolveErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		interfaceID string
		function    string
		wantErr     error
	}{
		{"GraphQL function on REST interface", "rest", "get-widget", errUnknownFunction},
		{"REST function on GraphQL interface", "gql", "retrieve-a-database", errUnknownFunction},
		{"unknown interface", "other", "get-widget", errInterfaceNotConfigured},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := Resolve(tt.interfaceID, tt.function, map[string]string{"token": "t"}, nil)
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("Resolve error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}
