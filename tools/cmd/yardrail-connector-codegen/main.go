package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"

	"github.com/yardrail/connectors/tools/internal/introspect"
)

func main() {
	manifestPath := flag.String("manifest", "", "path to connector manifest YAML (required)")
	specPath := flag.String("spec", "", "path to OpenAPI spec JSON (required)")
	pkgPath := flag.String("pkg", "", "import path of oapi-codegen client package (required)")
	outDir := flag.String("out", "", "output dir for handler_gen.go (required)")
	catalogOut := flag.String("catalog", "", "output path for catalog_<id>_gen.go (optional)")
	pkgName := flag.String("pkgname", "", "Go package name for generated handler files (default: connector ID)")
	flag.Parse()

	if *manifestPath == "" || *specPath == "" || *pkgPath == "" || *outDir == "" {
		flag.Usage()
		os.Exit(1)
	}

	manifest, err := parseManifest(*manifestPath)
	if err != nil {
		log.Fatalf("manifest: %v", err)
	}

	spec, err := parseSpec(*specPath)
	if err != nil {
		log.Fatalf("spec: %v", err)
	}

	groups, _, err := introspect.LoadMethods(*pkgPath, spec.TagMap)
	if err != nil {
		log.Fatalf("introspect: %v", err)
	}

	var methods []introspect.MethodInfo
	for _, g := range groups {
		methods = append(methods, g.Methods...)
	}
	sort.Slice(methods, func(i, j int) bool {
		return methods[i].Name < methods[j].Name
	})

	if *pkgName == "" {
		*pkgName = manifest.Connector.ID
	}

	var callableIfaces []ManifestInterface
	for _, iface := range manifest.Interfaces {
		if iface.IsCallable() {
			callableIfaces = append(callableIfaces, iface)
		}
	}

	baseURL := ""
	if len(spec.Servers) > 0 {
		baseURL = resolveBaseURLTemplate(spec.Servers[0])
	}

	ctx := &genContext{
		ConnectorID:    manifest.Connector.ID,
		PkgName:        *pkgName,
		ClientPkgPath:  *pkgPath,
		Manifest:        manifest,
		Spec:            spec,
		Methods:         methods,
		CallableIfaces:  callableIfaces,
		BaseURL:         baseURL,
		AuthStrategy:    deriveAuthStrategy(manifest, spec),
	}

	if err := os.MkdirAll(*outDir, 0o755); err != nil {
		log.Fatal(err)
	}

	handlerSrc, renderErr := renderFile(handlerTemplate, ctx)
	if renderErr != nil {
		log.Fatalf("rendering handler_gen.go: %v", renderErr)
	}

	handlerPath := filepath.Join(*outDir, "handler_gen.go")

	if writeErr := os.WriteFile(handlerPath, handlerSrc, 0o644); writeErr != nil {
		log.Fatal(writeErr)
	}

	fmt.Fprintf(os.Stderr, "wrote %s\n", handlerPath)

	if *catalogOut != "" {
		src, renderErr := renderFile(catalogTemplate, ctx)
		if renderErr != nil {
			log.Fatalf("rendering catalog: %v", renderErr)
		}
		if writeErr := os.WriteFile(*catalogOut, src, 0o644); writeErr != nil {
			log.Fatal(writeErr)
		}
		fmt.Fprintf(os.Stderr, "wrote %s\n", *catalogOut)
	}

	fmt.Fprintf(os.Stderr, "generated %d functions for connector %q\n", len(methods), manifest.Connector.ID)
}
