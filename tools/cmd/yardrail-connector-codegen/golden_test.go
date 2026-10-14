package main

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"testing"
)

var update = flag.Bool("update", false, "rewrite golden files under testdata/golden")

// connectorsRoot is the connectors repo root, relative to this package.
const connectorsRoot = "../../.."

// goldenFiles lists the artifacts run writes for a connector, as file names in
// testdata/golden/<id>/.
func goldenFiles(id string) []string {
	return []string{
		"handler_gen.go",
		"dispatch_gen.go",
		"catalog_" + id + "_gen.go",
		id + "-ext.yml",
		"connector_" + id + "_gen.go",
	}
}

// goldenConfig renders every artifact for a connector into dir, with the
// handler and dispatch files directly in dir.
func goldenConfig(dir, id, manifest, spec, pkg string) runConfig {
	return runConfig{
		ManifestPath:  filepath.Join(connectorsRoot, manifest),
		SpecPath:      filepath.Join(connectorsRoot, spec),
		PkgPath:       pkg,
		OutDir:        dir,
		CatalogOut:    filepath.Join(dir, "catalog_"+id+"_gen.go"),
		CWLSchemaOut:  filepath.Join(dir, id+"-ext.yml"),
		CWLMappingOut: filepath.Join(dir, "connector_"+id+"_gen.go"),
		PkgName:       id,
	}
}

func TestGoldenOpenAPI(t *testing.T) {
	t.Parallel()

	tests := []struct {
		id       string
		manifest string
		spec     string
		pkg      string
	}{
		{
			id:       "notion",
			manifest: "notion/notion/manifest.yaml",
			spec:     "notion/notion/spec/openapi.json",
			pkg:      "github.com/yardrail/connectors/notion/notion/client",
		},
		{
			id:       "slack",
			manifest: "slack/slack/manifest.yaml",
			spec:     "slack/slack/spec/openapi.json",
			pkg:      "github.com/yardrail/connectors/slack/slack/client",
		},
	}

	for _, tt := range tests {
		t.Run(tt.id, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			if err := run(goldenConfig(dir, tt.id, tt.manifest, tt.spec, tt.pkg)); err != nil {
				t.Fatalf("run: %v", err)
			}

			compareDirs(t, dir, filepath.Join("testdata", "golden", tt.id), goldenFiles(tt.id))
		})
	}
}

// compareDirs checks that each named file in gotDir matches wantDir byte for
// byte, or copies it over when -update is set.
func compareDirs(t *testing.T, gotDir, wantDir string, names []string) {
	t.Helper()

	for _, name := range names {
		got, err := os.ReadFile(filepath.Join(gotDir, name))
		if err != nil {
			t.Fatalf("reading generated %s: %v", name, err)
		}

		wantPath := filepath.Join(wantDir, name)

		if *update {
			if err := os.MkdirAll(wantDir, 0o755); err != nil {
				t.Fatal(err)
			}

			if err := os.WriteFile(wantPath, got, 0o644); err != nil {
				t.Fatal(err)
			}

			continue
		}

		want, err := os.ReadFile(wantPath)
		if err != nil {
			t.Fatalf("reading golden %s: %v", wantPath, err)
		}

		if !bytes.Equal(got, want) {
			t.Errorf("%s differs from %s (rerun with -update to accept)", name, wantPath)
		}
	}
}

// codegentestRoot is the module holding committed fixture connectors.
const codegentestRoot = "../../codegentest"

// fixtureConfig renders every artifact for a fixture manifest into dir.
func fixtureConfig(dir, id, manifest string) runConfig {
	return runConfig{
		ManifestPath:  manifest,
		OutDir:        dir,
		CatalogOut:    filepath.Join(dir, "catalog_"+id+"_gen.go"),
		CWLSchemaOut:  filepath.Join(dir, id+"-ext.yml"),
		CWLMappingOut: filepath.Join(dir, "connector_"+id+"_gen.go"),
		PkgName:       id,
	}
}

// checkFixture renders a fixture connector and compares handler and dispatch
// with the committed package in codegentest, and the catalog and CWL
// artifacts with testdata/golden/<id>/.
func checkFixture(t *testing.T, id, manifest string) {
	t.Helper()

	dir := t.TempDir()
	if err := run(fixtureConfig(dir, id, manifest)); err != nil {
		t.Fatalf("run: %v", err)
	}

	names := goldenFiles(id)
	compareDirs(t, dir, filepath.Join(codegentestRoot, id), names[:2])
	compareDirs(t, dir, filepath.Join("testdata", "golden", id), names[2:])
}

func TestGraphQLFixtureUpToDate(t *testing.T) {
	t.Parallel()

	checkFixture(t, "graphqlfixture", filepath.Join("testdata", "graphql", "fixture", "manifest.yaml"))
}

func TestMixedFixtureUpToDate(t *testing.T) {
	t.Parallel()

	checkFixture(t, "mixedfixture", filepath.Join("testdata", "mixed", "manifest.yaml"))
}
