package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
)

func main() {
	var cfg runConfig

	flag.StringVar(&cfg.ManifestPath, "manifest", "", "path to connector manifest YAML (required)")
	flag.StringVar(&cfg.SpecPath, "spec", "", "path to OpenAPI spec JSON (required when a rest interface has no source block)")
	flag.StringVar(&cfg.PkgPath, "pkg", "", "import path of oapi-codegen client package (required when a rest interface has no source block)")
	flag.StringVar(&cfg.OutDir, "out", "", "output dir for handler_gen.go (required)")
	flag.StringVar(&cfg.CatalogOut, "catalog", "", "output path for catalog_<id>_gen.go (optional)")
	flag.StringVar(&cfg.CWLSchemaOut, "cwl-schema", "", "output path for CWL extension schema YAML (optional)")
	flag.StringVar(&cfg.CWLMappingOut, "cwl-mapping", "", "output path for Go CWL class mapping file (optional)")
	flag.StringVar(&cfg.PkgName, "pkgname", "", "Go package name for generated handler files (default: connector ID)")
	flag.Parse()

	if err := run(cfg); err != nil {
		if errors.Is(err, errMissingFlags) {
			flag.Usage()
		}

		fmt.Fprintf(os.Stderr, "yardrail-connector-codegen: %v\n", err)
		os.Exit(1)
	}
}
