package db

import "embed"

// MigrationsFS holds the goose SQL migrations, embedded so the binary can
// self-migrate on startup.
//
//go:embed migrations/*.sql
var MigrationsFS embed.FS
