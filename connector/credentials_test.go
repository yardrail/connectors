package connector

import (
	"bytes"
	"fmt"
	"log/slog"
	"strings"
	"testing"
)

func TestCredentialsRedacted(t *testing.T) {
	const secret = "s3cr3t-value"
	cases := map[string]Credentials{
		"bearer": BearerCredentials{Token: secret},
		"apikey": APIKeyCredentials{Key: secret},
		"basic":  BasicCredentials{Username: secret, Password: secret},
	}
	for name, creds := range cases {
		t.Run(name, func(t *testing.T) {
			var outs []string
			for _, verb := range []string{"%v", "%+v", "%#v", "%s"} {
				outs = append(outs, fmt.Sprintf(verb, creds))
			}
			var buf bytes.Buffer
			slog.New(slog.NewTextHandler(&buf, nil)).Info("msg", "creds", creds)
			outs = append(outs, buf.String())
			for _, out := range outs {
				if !strings.Contains(out, "[redacted]") || strings.Contains(out, secret) {
					t.Errorf("output %q leaks or lacks redaction", out)
				}
			}
		})
	}
}
