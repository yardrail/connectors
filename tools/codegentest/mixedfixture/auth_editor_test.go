package mixedfixture

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/yardrail/connectors/connector"
)

func TestAuthEditorRejects(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		creds    connector.Credentials
		wantErr  error
		wantAuth string
	}{
		{"wrong type", connector.APIKeyCredentials{Key: "k"}, connector.ErrCredentialType, ""},
		{"empty token", connector.BearerCredentials{Token: ""}, connector.ErrMissingCredential, ""},
		{"bearer token", connector.BearerCredentials{Token: "k"}, nil, "Bearer k"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			edit, err := authEditorRest(tt.creds)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("authEditorRest error = %v, want %v", err, tt.wantErr)
			}

			if err != nil {
				return
			}

			req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://example.test", nil)
			if err := edit(context.Background(), req); err != nil {
				t.Fatalf("edit: %v", err)
			}

			if got := req.Header.Get("Authorization"); got != tt.wantAuth {
				t.Errorf("Authorization = %q, want %q", got, tt.wantAuth)
			}
		})
	}
}
