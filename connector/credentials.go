package connector

// Credentials is the authentication material for one interface.
// Sealed by the unexported method — only the three concrete types
// in this package implement it.
type Credentials interface {
	credentialType() AuthMethod
}

// BearerCredentials authenticates with a bearer token.
type BearerCredentials struct {
	Token string
}

func (BearerCredentials) credentialType() AuthMethod { return AuthMethodOAuth2User }

// APIKeyCredentials authenticates with an API key.
type APIKeyCredentials struct {
	Key string
}

func (APIKeyCredentials) credentialType() AuthMethod { return AuthMethodAPIKey }

// BasicCredentials authenticates with username and password.
type BasicCredentials struct {
	Username string
	Password string
}

func (BasicCredentials) credentialType() AuthMethod { return AuthMethodBasic }
