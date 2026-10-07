# Homepoll

Modular home metrics collector (Go 1.27.1, PostgreSQL 18, Grafana). Usage, modules, env vars and make targets: see `README.md`.

## Stack

- Go standard library for HTTP, XML/JSON, scheduling, config; hand-written `database/sql`; `goose` migrations; `air` for live reload.
- Distroless Docker image, Docker Compose for local dependencies, GitHub Actions for CI/CD.

## Layout

| Path                                              | Role                                                                           |
| ------------------------------------------------- | ------------------------------------------------------------------------------ |
| `cmd/collector`                                   | Entry point: config, migrate, build fetchers, serve MCP if `MCP_ADDR`, run     |
| `internal/config`                                 | Environment variables                                                          |
| `internal/collector`                              | Groups metrics by interval, dispatches to the module's `Fetch`, batch-inserts  |
| `internal/eta`, `internal/power`, `internal/mock` | HEATER, POWER, `DRY_RUN` fetchers                                              |
| `internal/db`                                     | `database/sql` queries (`db.go`) + embedded migrations                         |
| `internal/mcp`                                    | JSON-RPC 2.0 MCP server; `mcp.go` protocol and tools, `http.go` HTTP transport |
| `tests/integration`                               | DB-backed tests, skipped under `-short` or without a database                  |
| `grafana/`                                        | Provisioned datasource + dashboards                                            |

## Conventions and gotchas

- Schema change = new migration + matching edit to `Configuration` and its scan in `db.go`.
- New module = enum value + `configuration` rows (migration) + `collector.Fetch` + case in `main.go`. A configured module without a fetcher is fatal.
- `Fetch` returns `[]Reading`. Zero `RecordedAt` = now (HEATER, MOCK); POWER sets explicit timestamps.
- `metric` has no surrogate key: primary key `(configuration_id, recorded_at)` is the `ON CONFLICT DO NOTHING` target, so re-fetching is idempotent.
- POWER identity (`POWER_GPNR`, `POWER_ZP`) lives in env vars, not `configuration`; the POWER fetcher ignores `path`.
- MCP runs inside the collector, read-only, no SDK. Add methods/tools in `dispatch` (`mcp.go`).
- HTTP MCP (`MCP_ADDR`) is the only network surface: bearer token (`subtle.ConstantTimeCompare`), `Origin` header refused, body capped, plain HTTP behind loopback/Tailscale. Do not relax any of this.
- Tests: unit tests colocated (`_test.go`); `make test` = unit only, `make test-all` = plus integration.
- GitHub Actions are pinned to full commit SHAs with a `# vN` comment. Never replace with a bare tag.
