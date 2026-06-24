package main

import (
	"context"
	"database/sql"
	"log"
	"os/signal"
	"syscall"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"homepoll/internal/collector"
	"homepoll/internal/config"
	"homepoll/internal/db"
	"homepoll/internal/eta"
	"homepoll/internal/mock"
	"homepoll/internal/power"
)

func main() {
	cfg := config.Load()

	sqlDB, err := sql.Open("pgx", cfg.DSN)
	if err != nil {
		log.Fatalf("open database: %v", err)
	}
	defer sqlDB.Close()

	if err := migrate(sqlDB); err != nil {
		log.Fatalf("migrate: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	queries := db.New(sqlDB)
	configs, err := queries.ListConfigurations(ctx)
	if err != nil {
		log.Fatalf("list configurations: %v", err)
	}

	var mockFetch collector.Fetch
	if cfg.DryRun {
		log.Println(
			"DRY_RUN enabled: generating mock data for all modules, no external systems contacted",
		)
		mockFetch = mock.New().Fetcher()
	}

	// Build a fetcher only for the modules that actually appear in the
	// configuration. Migrations and their seeded config are embedded in this
	// binary, so a configured module with no fetcher is a build mistake - fail
	// fast rather than silently dropping its metrics. Add new systems as cases.
	fetchers := map[string]collector.Fetch{}
	for _, c := range configs {
		if _, ok := fetchers[c.Module]; ok {
			continue
		}
		switch {
		case cfg.DryRun:
			fetchers[c.Module] = mockFetch
		case c.Module == "HEATER":
			fetchers[c.Module] = eta.Fetcher(cfg.ETABaseURL)
		case c.Module == "POWER":
			if cfg.PowerToken == "" || cfg.PowerGPNR == "" || cfg.PowerZP == "" {
				log.Fatalf(
					"module POWER is configured but POWER_TOKEN, POWER_GPNR, or POWER_ZP is not set",
				)
			}
			fetchers[c.Module] = power.Fetcher(
				cfg.PowerBaseURL, cfg.PowerToken, cfg.PowerGPNR, cfg.PowerZP,
			)
		default:
			log.Fatalf("module %q is configured but no fetcher is registered for it", c.Module)
		}
	}

	log.Printf("collecting %d metrics across %d modules", len(configs), len(fetchers))
	collector.Run(ctx, queries, configs, fetchers)
	log.Println("shutting down")
}

// migrate applies the embedded goose migrations against the open database.
func migrate(sqlDB *sql.DB) error {
	goose.SetBaseFS(db.MigrationsFS)
	if err := goose.SetDialect("postgres"); err != nil {
		return err
	}
	return goose.Up(sqlDB, "migrations")
}
