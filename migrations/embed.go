// Package migrations contains the Goose migrations for the Kubernetes migration job.
package migrations

import "embed"

// Files contains the same SQL migrations used by the local Goose CLI.
//
//go:embed *.sql
var Files embed.FS
