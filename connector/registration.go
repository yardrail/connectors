package connector

// Registration bundles everything a connector exposes to the platform.
// Each generated connector produces one Registration. Yardrail collects
// them to build its catalog, resolver map, and CWL class registry.
type Registration struct {
	Definition Definition
	Resolve    Resolver
	CWLClasses []CWLClassEntry
	// BuildCredentials turns the platform's stored credential keys into
	// the Credentials type the named interface authenticates with.
	BuildCredentials CredentialBuilder
}

// CredentialBuilder builds the Credentials for one interface from the
// key/value pairs the platform stores for an integration. It returns nil
// credentials for interfaces that need none.
type CredentialBuilder func(interfaceID string, stored map[string]string) (Credentials, error)
