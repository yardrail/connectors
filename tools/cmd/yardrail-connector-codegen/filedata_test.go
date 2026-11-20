package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/yardrail/connectors/tools/internal/introspect"
)

const fileDataSpec = "testdata/filedata/openapi.json"

// renderFileDataFixture renders the dispatch and registration files for one
// method of the file data fixture spec.
func renderFileDataFixture(t *testing.T, method introspect.MethodInfo) (dispatch, registration string) {
	t.Helper()

	ops, err := parseOperations(filepath.FromSlash(fileDataSpec))
	if err != nil {
		t.Fatalf("parseOperations: %v", err)
	}

	methods := []introspect.MethodInfo{method}
	ctx := &genContext{
		ConnectorID:   "filedata",
		PkgName:       "filedata",
		ClientPkgPath: "example.com/filedata/client",
		Manifest:      &Manifest{Connector: ManifestConnector{ID: "filedata", Name: "File data"}},
		Spec:          &SpecInfo{Operations: ops},
		Methods:       methods,
		Functions:     openAPIFunctions(methods, ops),
	}

	d, err := renderFile(dispatchTemplate, ctx)
	if err != nil {
		t.Fatalf("rendering dispatch: %v", err)
	}

	r, err := renderFile(registrationTemplate, ctx)
	if err != nil {
		t.Fatalf("rendering registration: %v", err)
	}

	return string(d), string(r)
}

func TestBinaryResponse(t *testing.T) {
	t.Parallel()

	dispatch, registration := renderFileDataFixture(t, introspect.MethodInfo{
		Name:       "get-file-content",
		MethodName: "GetFileContentWithResponse",
		Params:     []introspect.MethodParam{{Name: "id", JSONName: "id", GoType: "string", Required: true}},
		CallArgs:   []introspect.CallArg{{Expr: "id"}},
	})

	if !strings.Contains(dispatch, "return connector.BinaryResult{Data: resp.Body, ContentType: respContentType}, nil") {
		t.Errorf("dispatch does not return connector.BinaryResult:\n%s", dispatch)
	}

	if !strings.Contains(registration, `{Name: "file", Type: "File", Required: true`) {
		t.Errorf("registration output is not file: File:\n%s", registration)
	}
}

func TestByteFieldFileInput(t *testing.T) {
	t.Parallel()

	dispatch, registration := renderFileDataFixture(t, introspect.MethodInfo{
		Name:       "upload-file",
		MethodName: "UploadFileWithResponse",
		Params: []introspect.MethodParam{{
			Name: "body", JSONName: "body", FullType: "target.UploadFileJSONRequestBody", IsStruct: true, Required: true,
		}},
		CallArgs: []introspect.CallArg{{Expr: "body"}},
	})

	if !strings.Contains(registration, `{Name: "file_data", Type: "File"`) {
		t.Errorf("registration input file_data is not File:\n%s", registration)
	}

	for _, want := range []string{
		`inlineFileFields("filedata.upload-file", inputs, "file_data")`,
		"json.Unmarshal(bodyInputs, &body)",
		"`json:\"path\"`",
		"os.Open(path)",
	} {
		if !strings.Contains(dispatch, want) {
			t.Errorf("dispatch does not contain %s:\n%s", want, dispatch)
		}
	}
}
