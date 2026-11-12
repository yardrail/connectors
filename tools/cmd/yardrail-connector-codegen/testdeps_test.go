package main

// Blank imports to keep go mod tidy from pruning client packages
// that are loaded dynamically by tests via go/packages.Load().
import (
	_ "github.com/yardrail/connectors/egnyte/egnyte/client"
	_ "github.com/yardrail/connectors/notion/notion/client"
	_ "github.com/yardrail/connectors/slack/slack/client"
	_ "google.golang.org/api/tasks/v1"
)
