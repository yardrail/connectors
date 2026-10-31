package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
)

type config struct {
	ServiceDefPath  string
	FinancePath     string
	BaseTypesPath   string
	CodesModelsPath string
	OutPath         string
}

var errMissingFlags = errors.New("required flags missing")

func main() {
	var cfg config
	flag.StringVar(&cfg.ServiceDefPath, "service-def", "", "path to IntuitRestServiceDef.xsd")
	flag.StringVar(&cfg.FinancePath, "finance", "", "path to Finance.xsd")
	flag.StringVar(&cfg.BaseTypesPath, "base-types", "", "path to IntuitBaseTypes.xsd")
	flag.StringVar(&cfg.CodesModelsPath, "codes-models", "", "path to CodesModelsJsonObjects_v2.json")
	flag.StringVar(&cfg.OutPath, "out", "", "output path for OpenAPI JSON")
	flag.Parse()

	if err := run(cfg); err != nil {
		if errors.Is(err, errMissingFlags) {
			flag.Usage()
		}
		fmt.Fprintf(os.Stderr, "specgen: %v\n", err)
		os.Exit(1)
	}
}

func run(cfg config) error {
	if cfg.ServiceDefPath == "" || cfg.FinancePath == "" ||
		cfg.BaseTypesPath == "" || cfg.CodesModelsPath == "" || cfg.OutPath == "" {
		return errMissingFlags
	}

	entities, err := parseServiceDef(cfg.ServiceDefPath)
	if err != nil {
		return fmt.Errorf("parse service def: %w", err)
	}

	xsdTypes, enums, err := parseFinanceXSD(cfg.FinancePath, cfg.BaseTypesPath)
	if err != nil {
		return fmt.Errorf("parse finance XSD: %w", err)
	}

	models, codes, err := parseCodesModels(cfg.CodesModelsPath)
	if err != nil {
		return fmt.Errorf("parse codes/models: %w", err)
	}

	spec := correlate(entities, xsdTypes, enums, models, codes)

	doc, err := buildOpenAPI(spec)
	if err != nil {
		return fmt.Errorf("build openapi: %w", err)
	}

	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal JSON: %w", err)
	}

	// Roundtrip through any to normalize map key ordering
	var normalized any
	if err := json.Unmarshal(data, &normalized); err != nil {
		return fmt.Errorf("normalize JSON: %w", err)
	}
	data, err = json.MarshalIndent(normalized, "", "  ")
	if err != nil {
		return fmt.Errorf("re-marshal JSON: %w", err)
	}
	data = append(data, '\n')

	return os.WriteFile(cfg.OutPath, data, 0o644)
}
