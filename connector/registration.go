package connector

import "github.com/yardrail/yardrail/pkg/extensions"

// Registration bundles everything a connector exposes to the platform.
// Each generated connector produces one Registration. Yardrail collects
// them to build its catalog, resolver map, and CWL class registry.
type Registration struct {
	Definition Definition
	Resolve    Resolver
	CWLClasses []extensions.ConnectorClassEntry
}
