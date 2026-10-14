package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/yardrail/connectors/tools/internal/introspect"
)

var (
	errMissingFlags           = errors.New("-manifest and -out are required, and -spec and -pkg when a rest interface has no source block")
	errMultipleOpenAPISources = errors.New("REST interfaces must share one OpenAPI spec and client")
	errDuplicateFunction      = errors.New("function name produced by two interfaces")
)

// runConfig holds the codegen inputs and output paths, one field per flag.
type runConfig struct {
	ManifestPath  string
	SpecPath      string
	PkgPath       string
	OutDir        string
	CatalogOut    string
	CWLSchemaOut  string
	CWLMappingOut string
	PkgName       string
}

// handlerTemplates is the handler and dispatch template pair for one protocol.
type handlerTemplates struct {
	handler  string
	dispatch string
}

func run(cfg runConfig) error {
	if cfg.ManifestPath == "" || cfg.OutDir == "" {
		return errMissingFlags
	}

	manifest, err := parseManifest(cfg.ManifestPath)
	if err != nil {
		return fmt.Errorf("manifest: %w", err)
	}

	if manifest.needsSpecFlags() && (cfg.SpecPath == "" || cfg.PkgPath == "") {
		return errMissingFlags
	}

	ctx := newContext(cfg, manifest)

	tmpls, specSchemes, err := loadFunctions(cfg, ctx)
	if err != nil {
		return err
	}

	ctx.AuthStrategy, err = deriveAuthStrategy(manifest, mergeSecuritySchemes(specSchemes, manifest.SecuritySchemes))
	if err != nil {
		return err
	}

	if err := writeOutputs(cfg, ctx, tmpls); err != nil {
		return err
	}

	fmt.Fprintf(os.Stderr, "generated %d functions for connector %q\n", len(ctx.Functions), manifest.Connector.ID)

	return nil
}

// newContext fills the protocol-independent parts of the template context.
func newContext(cfg runConfig, manifest *Manifest) *genContext {
	pkgName := cfg.PkgName
	if pkgName == "" {
		pkgName = manifest.Connector.ID
	}

	var callableIfaces []ManifestInterface
	for _, iface := range manifest.Interfaces {
		if iface.IsCallable() {
			callableIfaces = append(callableIfaces, iface)
		}
	}

	configParams := make(map[string]string)
	for _, iface := range callableIfaces {
		for k, v := range iface.ConfigParams {
			configParams[k] = v
		}
	}

	return &genContext{
		ConnectorID:    manifest.Connector.ID,
		PkgName:        pkgName,
		Manifest:       manifest,
		CallableIfaces: callableIfaces,
		ConfigParams:   configParams,
	}
}

// loadFunctions fills ctx with the connector's functions from each callable
// interface's source and returns the templates to render them with and any
// security schemes the OpenAPI spec declares.
func loadFunctions(cfg runConfig, ctx *genContext) (handlerTemplates, map[string]SecurityScheme, error) {
	specSchemes, err := loadOpenAPIInterfaces(cfg, ctx)
	if err != nil {
		return handlerTemplates{}, nil, err
	}

	if err := loadGraphQLInterfaces(ctx); err != nil {
		return handlerTemplates{}, nil, err
	}

	if err := checkFunctionNames(ctx); err != nil {
		return handlerTemplates{}, nil, err
	}

	sort.Slice(ctx.Functions, func(i, j int) bool {
		return ctx.Functions[i].Name < ctx.Functions[j].Name
	})

	switch {
	case len(ctx.GraphQLIfaces) == 0:
		return handlerTemplates{handlerTemplate, dispatchTemplate}, specSchemes, nil
	case len(ctx.RESTIfaces) == 0:
		return handlerTemplates{graphqlHandlerTemplate, graphqlDispatchTemplate}, specSchemes, nil
	default:
		return handlerTemplates{mixedHandlerTemplate, mixedDispatchTemplate}, specSchemes, nil
	}
}

// openAPISource is the spec and oapi-codegen client package behind REST
// interfaces.
type openAPISource struct {
	spec string
	pkg  string
}

// restSource returns an OpenAPI-backed interface's source block, or the
// -spec/-pkg flags when it has none.
func restSource(cfg runConfig, iface ManifestInterface) openAPISource {
	if iface.Source != nil {
		return openAPISource{spec: iface.Source.OpenAPI, pkg: iface.Source.Client}
	}

	return openAPISource{spec: cfg.SpecPath, pkg: cfg.PkgPath}
}

// loadOpenAPIInterfaces loads the methods behind every callable interface that
// is not GraphQL. Those interfaces share one generated client type, so they
// must all resolve to the same spec and client package.
func loadOpenAPIInterfaces(cfg runConfig, ctx *genContext) (map[string]SecurityScheme, error) {
	var src openAPISource

	for _, iface := range ctx.CallableIfaces {
		if iface.Protocol == "graphql" {
			continue
		}

		ifaceSrc := restSource(cfg, iface)
		if len(ctx.RESTIfaces) > 0 && ifaceSrc != src {
			return nil, fmt.Errorf("interfaces %q and %q: %w", ctx.RESTIfaces[0].ID, iface.ID, errMultipleOpenAPISources)
		}

		src = ifaceSrc
		ctx.RESTIfaces = append(ctx.RESTIfaces, iface)
	}

	if len(ctx.RESTIfaces) == 0 {
		return nil, nil
	}

	spec, err := parseSpec(src.spec)
	if err != nil {
		return nil, fmt.Errorf("spec: %w", err)
	}

	methods, err := loadOpenAPIMethods(src.pkg, spec)
	if err != nil {
		return nil, err
	}

	ctx.ClientPkgPath = src.pkg
	ctx.Spec = spec
	ctx.Methods = methods
	ctx.Functions = append(ctx.Functions, openAPIFunctions(methods, spec.Operations)...)

	if len(spec.Servers) > 0 {
		ctx.BaseURL = resolveBaseURLTemplate(spec.Servers[0])
	}

	return spec.SecuritySchemes, nil
}

// checkFunctionNames fails when two interfaces that do not share a source
// produce the same function name. OpenAPI functions belong to every REST
// interface and carry an empty InterfaceID.
func checkFunctionNames(ctx *genContext) error {
	restOwner := ""
	if len(ctx.RESTIfaces) > 0 {
		restOwner = ctx.RESTIfaces[0].ID
	}

	owners := make(map[string]string, len(ctx.Functions))

	for _, fn := range ctx.Functions {
		owner := fn.InterfaceID
		if owner == "" {
			owner = restOwner
		}

		if prev, ok := owners[fn.Name]; ok {
			return fmt.Errorf("%w: %q in interfaces %q and %q", errDuplicateFunction, fn.Name, prev, owner)
		}

		owners[fn.Name] = owner
	}

	return nil
}

// loadGraphQLInterfaces loads the operations of every callable GraphQL
// interface into ctx.
func loadGraphQLInterfaces(ctx *genContext) error {
	for _, iface := range ctx.CallableIfaces {
		if iface.Protocol != "graphql" {
			continue
		}

		funcs, err := loadGraphQL(graphqlSource{Schema: iface.Source.Schema, Operations: iface.Source.Operations})
		if err != nil {
			return fmt.Errorf("interface %q: %w", iface.ID, err)
		}

		ctx.GraphQLIfaces = append(ctx.GraphQLIfaces, iface)

		for _, fn := range funcs {
			fn.InterfaceID = iface.ID
			ctx.GraphQLFunctions = append(ctx.GraphQLFunctions, fn)
			ctx.Functions = append(ctx.Functions, connectorFunction{
				Name:        fn.Name,
				ClassName:   fn.ClassName,
				InterfaceID: iface.ID,
				Op:          fn.Op,
			})
		}
	}

	return nil
}

func loadOpenAPIMethods(pkgPath string, spec *SpecInfo) ([]introspect.MethodInfo, error) {
	groups, _, err := introspect.LoadMethods(pkgPath, spec.TagMap)
	if err != nil {
		return nil, fmt.Errorf("introspect: %w", err)
	}

	var methods []introspect.MethodInfo
	for _, g := range groups {
		methods = append(methods, g.Methods...)
	}

	sort.Slice(methods, func(i, j int) bool {
		return methods[i].Name < methods[j].Name
	})

	return methods, nil
}

// output is one generated artifact: where it goes and how it renders.
type output struct {
	path   string
	render func(*genContext) ([]byte, error)
}

func templateRenderer(tmpl string) func(*genContext) ([]byte, error) {
	return func(ctx *genContext) ([]byte, error) {
		return renderFile(tmpl, ctx)
	}
}

func writeOutputs(cfg runConfig, ctx *genContext, tmpls handlerTemplates) error {
	if err := os.MkdirAll(cfg.OutDir, 0o755); err != nil {
		return fmt.Errorf("creating output dir: %w", err)
	}

	outputs := []output{
		{filepath.Join(cfg.OutDir, "handler_gen.go"), templateRenderer(tmpls.handler)},
		{filepath.Join(cfg.OutDir, "dispatch_gen.go"), templateRenderer(tmpls.dispatch)},
		{cfg.CatalogOut, templateRenderer(catalogTemplate)},
		{cfg.CWLSchemaOut, renderCWLSchema},
		{cfg.CWLMappingOut, templateRenderer(cwlMappingTemplate)},
	}

	for _, o := range outputs {
		if o.path == "" {
			continue
		}

		src, err := o.render(ctx)
		if err != nil {
			return fmt.Errorf("rendering %s: %w", filepath.Base(o.path), err)
		}

		if err := os.WriteFile(o.path, src, 0o644); err != nil {
			return fmt.Errorf("writing %s: %w", o.path, err)
		}

		fmt.Fprintf(os.Stderr, "wrote %s\n", o.path)
	}

	return nil
}
