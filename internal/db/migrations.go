package db

import "embed"

// MigrationsFS holds the goose migrations applied on startup.
//
//go:embed migrations/*.sql
var MigrationsFS embed.FS
