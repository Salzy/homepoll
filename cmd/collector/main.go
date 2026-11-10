// Command collector migrates the database, then polls every configured metric
// on its interval and optionally serves the read-only MCP endpoint.
package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"homepoll/internal/collector"
	"homepoll/internal/config"
	"homepoll/internal/db"
	"homepoll/internal/eta"
	"homepoll/internal/mcp"
	"homepoll/internal/mock"
	"homepoll/internal/power"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

// run wires up the collector and blocks until SIGINT or SIGTERM. Returning an
// error instead of exiting lets the deferred cleanup run first.
func run() error {
	cfg := config.Load()

	sqlDB, err := sql.Open("pgx", cfg.DSN)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer sqlDB.Close()

	if err := migrate(sqlDB); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	queries := db.New(sqlDB)
	configs, err := queries.ListConfigurations(ctx)
	if err != nil {
		return fmt.Errorf("list configurations: %w", err)
	}

	fetchers, err := buildFetchers(cfg, configs)
	if err != nil {
		return err
	}

	if err := serveMCP(cfg, queries); err != nil {
		return err
	}

	log.Printf("collecting %d metrics across %d modules", len(configs), len(fetchers))
	collector.Run(ctx, queries, configs, fetchers)
	log.Println("shutting down")
	return nil
}

// buildFetchers returns one fetcher per configured module. The configuration is
// embedded, so a module without a fetcher is a build mistake: fail fast.
func buildFetchers(
	cfg config.Config,
	configs []db.Configuration,
) (map[string]collector.Fetch, error) {
	var mockFetch collector.Fetch
	if cfg.DryRun {
		log.Println(
			"DRY_RUN enabled: generating mock data for all modules, no external systems contacted",
		)
		mockFetch = mock.New().Fetcher()
	}

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
				return nil, errors.New(
					"module POWER is configured but POWER_TOKEN, POWER_GPNR, or POWER_ZP is not set",
				)
			}
			fetchers[c.Module] = power.Fetcher(
				cfg.PowerBaseURL, cfg.PowerToken, cfg.PowerGPNR, cfg.PowerZP,
			)
		default:
			return nil, fmt.Errorf(
				"module %q is configured but no fetcher is registered for it", c.Module,
			)
		}
	}
	return fetchers, nil
}

// serveMCP serves MCP over plain HTTP when MCP_ADDR is set. Bind it to loopback
// or Tailscale for transport security; the bearer token guards that network.
// It returns an error when MCP_ADDR is set without MCP_TOKEN.
func serveMCP(cfg config.Config, queries *db.Queries) error {
	if cfg.MCPAddr == "" {
		return nil
	}
	if cfg.MCPToken == "" {
		return errors.New(
			"MCP_ADDR is set but MCP_TOKEN is not; the endpoint would be unauthenticated",
		)
	}
	srv := &http.Server{
		Addr:              cfg.MCPAddr,
		Handler:           mcp.Handler(queries, cfg.MCPToken),
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		log.Printf("serving MCP over HTTP on %s%s", cfg.MCPAddr, mcp.Path)
		log.Fatalf("mcp http: %v", srv.ListenAndServe())
	}()
	return nil
}

// migrate applies the embedded goose migrations against the open database.
func migrate(sqlDB *sql.DB) error {
	goose.SetBaseFS(db.MigrationsFS)
	if err := goose.SetDialect("postgres"); err != nil {
		return err
	}
	return goose.Up(sqlDB, "migrations")
}
