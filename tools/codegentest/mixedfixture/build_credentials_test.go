package mixedfixture

import (
	"errors"
	"strings"
	"testing"

	"github.com/yardrail/connectors/connector"
)

func TestBuildCredentials(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		interfaceID string
		stored      map[string]string
		want        connector.Credentials
		wantErr     error
		wantInErr   string
	}{
		{"rest bearer", "rest", map[string]string{"token": "k"}, connector.BearerCredentials{Token: "k"}, nil, ""},
		{"gql api key", "gql", map[string]string{"token": "k"}, connector.APIKeyCredentials{Key: "k"}, nil, ""},
		{"missing token", "rest", map[string]string{}, nil, connector.ErrMissingCredential, `"token"`},
		{"unknown interface", "nope", map[string]string{"token": "k"}, nil, connector.ErrUnknownInterface, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := BuildCredentials(tt.interfaceID, tt.stored)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("BuildCredentials error = %v, want %v", err, tt.wantErr)
			}

			if err != nil && !strings.Contains(err.Error(), tt.wantInErr) {
				t.Errorf("error %q does not contain %s", err, tt.wantInErr)
			}

			if got != tt.want {
				t.Errorf("BuildCredentials = %#v, want %#v", got, tt.want)
			}
		})
	}
}
