package connector

// ProtocolType describes the wire protocol an interface uses.
type ProtocolType string

const (
	ProtocolREST    ProtocolType = "rest"
	ProtocolGraphQL ProtocolType = "graphql"
	ProtocolWebhook ProtocolType = "webhook"
	ProtocolMCP     ProtocolType = "mcp"
	ProtocolSMTP    ProtocolType = "smtp"
	ProtocolGRPC    ProtocolType = "grpc"

	ProtocolGoogleSDK ProtocolType = "google_sdk"
)

// AuthMethod describes how credentials are obtained for an interface.
type AuthMethod string

const (
	AuthMethodOAuth2User              AuthMethod = "oauth2_user"
	AuthMethodOAuth2ClientCredentials AuthMethod = "oauth2_client_credentials"
	AuthMethodAPIKey                  AuthMethod = "api_key"
	AuthMethodBasic                   AuthMethod = "basic"
	AuthMethodNone                    AuthMethod = "none"
)

// ConfigFieldType is the value type of a configuration field.
type ConfigFieldType string

const (
	ConfigFieldTypeString ConfigFieldType = "string"
	ConfigFieldTypeURL    ConfigFieldType = "url"
	ConfigFieldTypeNumber ConfigFieldType = "number"
	ConfigFieldTypeSelect ConfigFieldType = "select"
)

// ConfigField declares a single non-secret configuration parameter.
type ConfigField struct {
	Name        string
	Type        ConfigFieldType
	Label       string
	Description string
	Required    bool
	Options     []string
}

// Definition is the catalog metadata for a connector.
type Definition struct {
	ID          string
	Name        string
	Description string
	Interfaces  []Interface
}

// Interface is a single communication surface within a connector.
type Interface struct {
	ID           string
	Name         string
	Protocol     ProtocolType
	AuthMethod   AuthMethod
	DependsOn    []string
	ConfigFields []ConfigField
}
