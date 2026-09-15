# Homepoll

Modular metrics collector for the home. Each external system is a self-contained module that polls its API on its own schedule; readings are stored in PostgreSQL and visualized via Grafana. Adding a system is a `configuration` row plus a fetcher - the core stays untouched. Shipped modules: HEATER (ETAtouch) and POWER (Salzburg Netz).

## Tech stack

1. GoLang 1.27.1
    1. `air` (developer experience, live reload)
    2. `goose` (migrations, embedded and applied on startup); hand-written `database/sql` for data access
    3. Standard library for everything else (HTTP, XML/JSON, scheduling, configuration)
    4. Minimal distroless Dockerfile for deployment
2. PostgreSQL v18 (storage)
3. Grafana (visualization, dashboard defined as code in `grafana/`)
4. Makefile for CLI scripts
5. Docker Compose for local development
6. GitHub Action for CI/CD

## Commands

- `make test` - run unit tests (integration tests skip via `-short`)
- `make test-all` - full suite: unit + DB-backed integration tests (needs `make up`)
- `make fmt` - gofmt + wrap lines at 100 (golines)
- `make build` / `make dev` - build once / live-reload with air
- `make docker` - build the container image (override tag with `IMAGE=name:tag`)
- `make up` / `make down` - run local dependencies (postgres + grafana) via Docker Compose
- `make migrate-create NAME=...` - create a new goose migration
- Fresh checkout: run `go mod tidy` once to write `go.sum`

## Layout

- `cmd/collector` - entry point: load config, self-migrate, start pollers. With the single argument `mcp` it instead runs the read-only MCP server over stdio and never migrates
- `internal/config` - environment-variable configuration
- `internal/eta` - HEATER module fetcher (ETAtouch REST client, stdlib `net/http` + `encoding/xml`)
- `internal/power` - POWER module fetcher (Salzburg Netz REST client, stdlib `net/http` + `encoding/json`)
- `internal/mock` - mock value generator used when `DRY_RUN=true`
- `internal/collector` - scheduler: groups metrics by interval, dispatches each to the fetcher for its module, batch-inserts
- `internal/db` - hand-written `database/sql` access + embedded goose migrations (schema + seed)
- `internal/mcp` - Model Context Protocol server: stdlib JSON-RPC 2.0 over stdio, `list_metrics` + `query_metric`
- `grafana/` - provisioned datasource and dashboards, defined as code

## Conventions and gotchas

- `internal/db` is hand-written `database/sql`: `db.go` holds the `Configuration` struct, `ListConfigurations`, and the variable-length batch `InsertMetrics`; `migrations.go` embeds the goose migrations. A schema change is a new migration plus a matching edit to the `Configuration` struct and its scan in `db.go`.
- Migrations are embedded and applied on startup; the metrics are seeded in the first migration.
- Fetching is dispatched per `configuration.module`. `main.go` builds a `map[string]collector.Fetch` containing only the modules that appear in the loaded config; a configured module with no registered fetcher is fatal. To add a system: add the enum value (migration) + config rows, write a fetcher implementing `collector.Fetch`, and add a case for it in `main.go`.
- A `collector.Fetch` returns `[]collector.Reading` (value + optional `RecordedAt`). Point-in-time sources (HEATER, MOCK) return one reading with a zero `RecordedAt` (defaults to now); historical sources (POWER) return many with explicit timestamps. The metric table has no surrogate key - its primary key is `(configuration_id, recorded_at)`, which the batch insert uses as the `ON CONFLICT DO NOTHING` target, so re-fetching a day is idempotent. `configuration.id` is an identity `integer`.
- POWER (Salzburg Netz) is fetched once a day and returns a full previous day of 15-minute samples in one POST. Account (`GPNR`) and metering point (`ZP`) are per-deployment identity, so they are env vars (`POWER_GPNR`, `POWER_ZP`) alongside `POWER_HOST` + `POWER_TOKEN` - not in the `configuration` row, whose `path` holds the universal OBIS series id. The POWER fetcher ignores `configuration.path`.
- `DRY_RUN=true` routes every module through the mock generator and fills the same tables, so the dashboard works with no external systems.
- The collector serves no HTTP and publishes no ports unless `MCP_ADDR` is set, which opts it into also serving MCP over HTTP. The stdio mode never listens; its stdout is the protocol channel, so nothing may be printed to it (logs go to stderr).
- The HTTP transport is the only network-reachable surface in the project: it requires a bearer token (`MCP_TOKEN`, compared with `subtle.ConstantTimeCompare`), refuses any request carrying an `Origin` header, and caps the body. It is plain HTTP on purpose - bind `MCP_ADDR` to loopback or the Tailscale address and let that carry TLS. Do not relax any of this.
- MCP is a mode of the collector binary, not a second one: both modes need the same config and DSN, so `cmd/collector/main.go` branches on `os.Args[1] == "mcp"` before migrating. The Docker image is unchanged and can serve MCP via `docker exec -i <container> /collector mcp`.
- The two transports share everything below `answer()` in `internal/mcp/mcp.go`; `http.go` holds only the framing and the trust boundary. A new method or tool is added once, in `dispatch`, and both transports get it.
- `internal/mcp` implements the protocol on `encoding/json` rather than an MCP SDK: a tools-only server is four methods (`initialize`, `ping`, `tools/list`, `tools/call`), which is smaller than the dependency. It is read-only; migrating stays with the collecting mode.
- Unit tests are idiomatic Go (`_test.go` colocated next to the code). DB-backed integration tests live in `tests/integration` and gate on `-short` (and skip when no database is reachable), so `make test` runs unit-only and `make test-all` runs everything.
- GitHub Actions in `.github/workflows` are pinned to a full 40-char commit SHA, not a tag - tags are mutable and a hijacked action runs with our deploy secrets. Keep the `# vN` comment after the SHA for readability; Dependabot bumps both. Never replace a SHA pin with a bare `@vN` tag.
