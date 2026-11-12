package main

// Blank imports to keep go mod tidy from pruning client packages
// that are loaded dynamically by tests via go/packages.Load().
import (
	_ "github.com/yardrail/connectors/canva/canva/client"
	_ "github.com/yardrail/connectors/egnyte/egnyte/client"
	_ "github.com/yardrail/connectors/eventbrite/eventbrite/client"
	_ "github.com/yardrail/connectors/hubspot/crm-contacts/client"
	_ "github.com/yardrail/connectors/mailchimp/marketing/client"
	_ "github.com/yardrail/connectors/microsoft/outlook/client"
	_ "github.com/yardrail/connectors/notion/notion/client"
	_ "github.com/yardrail/connectors/officernd/officernd/client"
	_ "github.com/yardrail/connectors/quickbooks/account/client"
	_ "github.com/yardrail/connectors/slack/slack/client"
	_ "github.com/yardrail/connectors/stripe/stripe/client"
	_ "google.golang.org/api/tasks/v1"
)
