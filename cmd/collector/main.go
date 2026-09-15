package main

import (
	"context"
	"database/sql"
	"log"
	"net/http"
	"os"
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
	cfg := config.Load()

	sqlDB, err := sql.Open("pgx", cfg.DSN)
	if err != nil {
		log.Fatalf("open database: %v", err)
	}
	defer sqlDB.Close()

	// With no arguments this is the collector. `collector mcp` instead serves
	// the stored metrics to an MCP client over stdio and exits when the client
	// closes the pipe: read-only, and never migrating, because the collecting
	// mode owns the schema. One binary so both modes share the same DSN.
	if len(os.Args) > 1 {
		if os.Args[1] != "mcp" {
			log.Fatalf("usage: %s [mcp]", os.Args[0])
		}
		// stdout carries the protocol, so nothing may be printed to it. The
		// standard logger writes to stderr, which MCP clients show as the log.
		if err := mcp.Serve(context.Background(), db.New(sqlDB), os.Stdin, os.Stdout); err != nil {
			log.Fatalf("serve: %v", err)
		}
		return
	}

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

	serveMCP(cfg, queries)

	log.Printf("collecting %d metrics across %d modules", len(configs), len(fetchers))
	collector.Run(ctx, queries, configs, fetchers)
	log.Println("shutting down")
}

// serveMCP starts the MCP HTTP transport alongside the pollers when MCP_ADDR is
// set, for clients that cannot spawn the stdio mode - a hosted assistant, or
// one on another machine. Unset, nothing listens and the deployment publishes
// no port, as before.
//
// The endpoint is plain HTTP: bind MCP_ADDR to loopback or to the host's
// Tailscale address and let that layer carry the encryption and device
// identity. The bearer token is what stops anyone already on that network.
func serveMCP(cfg config.Config, queries *db.Queries) {
	if cfg.MCPAddr == "" {
		return
	}
	if cfg.MCPToken == "" {
		log.Fatalf("MCP_ADDR is set but MCP_TOKEN is not; the endpoint would be unauthenticated")
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
}

// migrate applies the embedded goose migrations against the open database.
func migrate(sqlDB *sql.DB) error {
	goose.SetBaseFS(db.MigrationsFS)
	if err := goose.SetDialect("postgres"); err != nil {
		return err
	}
	return goose.Up(sqlDB, "migrations")
}
