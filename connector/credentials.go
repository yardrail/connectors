package connector

import (
	"errors"
	"log/slog"
)

// Errors returned by credential builders and auth editors.
var (
	// ErrUnknownInterface means the connector has no interface with the given ID.
	ErrUnknownInterface = errors.New("unknown interface")
	// ErrMissingCredential means a stored credential key is absent or empty.
	ErrMissingCredential = errors.New("missing credential")
	// ErrCredentialType means the connector was handed the wrong Credentials type.
	ErrCredentialType = errors.New("wrong credential type")
)

const redacted = "[redacted]"

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

// String hides the token.
func (BearerCredentials) String() string { return redacted }

// GoString hides the token.
func (BearerCredentials) GoString() string { return redacted }

// LogValue hides the token.
func (BearerCredentials) LogValue() slog.Value { return slog.StringValue(redacted) }

// APIKeyCredentials authenticates with an API key.
type APIKeyCredentials struct {
	Key string
}

func (APIKeyCredentials) credentialType() AuthMethod { return AuthMethodAPIKey }

// String hides the key.
func (APIKeyCredentials) String() string { return redacted }

// GoString hides the key.
func (APIKeyCredentials) GoString() string { return redacted }

// LogValue hides the key.
func (APIKeyCredentials) LogValue() slog.Value { return slog.StringValue(redacted) }

// BasicCredentials authenticates with username and password.
type BasicCredentials struct {
	Username string
	Password string
}

func (BasicCredentials) credentialType() AuthMethod { return AuthMethodBasic }

// String hides the username and password.
func (BasicCredentials) String() string { return redacted }

// GoString hides the username and password.
func (BasicCredentials) GoString() string { return redacted }

// LogValue hides the username and password.
func (BasicCredentials) LogValue() slog.Value { return slog.StringValue(redacted) }
