package connector

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/yardrail/connectors/connector"
)

// TestFileUploadSendsFileBytes checks that a File input for file_data is sent
// as the file's base64 contents, alongside the other body fields.
func TestFileUploadSendsFileBytes(t *testing.T) {
	t.Parallel()

	content := []byte("\x89PNG fake image")

	var sent map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&sent); err != nil {
			t.Errorf("decoding request body: %v", err)
		}

		_, _ = w.Write([]byte(`{"id": 1}`))
	}))
	defer srv.Close()

	path := filepath.Join(t.TempDir(), "logo.png")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}

	call, err := Resolve("api-key", "post-file-manager-files",
		connector.BasicCredentials{Username: "u", Password: "p"},
		map[string]string{"base_url": srv.URL}, nil)
	if err != nil {
		t.Fatal(err)
	}

	inputs, _ := json.Marshal(map[string]any{
		"name":      "logo.png",
		"file_data": map[string]string{"path": path, "mimeType": "image/png"},
	})

	if _, err := call(context.Background(), inputs); err != nil {
		t.Fatalf("call: %v", err)
	}

	if want := base64.StdEncoding.EncodeToString(content); sent["file_data"] != want {
		t.Errorf("file_data = %v, want %q", sent["file_data"], want)
	}

	if sent["name"] != "logo.png" {
		t.Errorf("name = %v, want logo.png", sent["name"])
	}
}
