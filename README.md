# Homepoll

Lightweight, modular metrics collector for the home. Each external system is a self-contained module that polls its API on its own schedule; readings are stored in PostgreSQL and visualized with Grafana. Adding a system is a `configuration` row plus a fetcher - the core stays untouched.

Two external systems are supported: [ETA pellet heater](docs/eta-heater.md) and [Salzburg Netz grid electricity](docs/salzburg-netz-power.md).

```
+--------------+                +--------------+
|  ETA Heater  |  HTTP/XML ---->|              |
|  (REST API)  |                |              |   SQL    +--------------+
+--------------+                |  Collector   |--------->|  PostgreSQL  |
                                |   (GoLang)   |          +------+-------+
+--------------+                |              |                 | SQL
| Salzburg Netz| HTTPS/JSON --->|              |                 v
|  (REST API)  |                |              |          +--------------+
+--------------+                +--------------+          |   Grafana    |
                                                          |  port 3000   |
                                                          +--------------+
```

## General idea

- Polls each configured source's API on a recurring basis and stores the metrics in PostgreSQL.
- Metrics are visualized with Grafana dashboards, defined as code (JSON).
- Sources expose many metrics, polled at different rates .
- Designed to extend to further systems/modules (solar, water, ...) by adding `configuration` rows plus a fetcher.
- The set of metrics is configurable via the `configuration` table, not hard-coded.
- A mock module (`DRY_RUN=true`) fills the database so the dashboard can be tested without connected external systems.

## Quick start

```bash
go mod tidy            # Resolves dependencies, writes go.sum
make up                # PostgreSQL + Grafana in Docker
DRY_RUN=true make dev  # Run the application with mock data
```

- Grafana: http://localhost:3000 (user: `admin` / password: `admin` or `env.GRAFANA_PASSWORD`).

## Environment Variables (`.env`)

| Variable            | Required | Default         | Description                            |
| ------------------- | -------- | --------------- | -------------------------------------- |
| `DRY_RUN`           | No       | `false`         | Generate mock data, no heater needed   |
| `POSTGRES_HOST`     | No       | `localhost`     | PostgreSQL host                        |
| `POSTGRES_PORT`     | No       | `5432`          | PostgreSQL port                        |
| `POSTGRES_USER`     | No       | `homepoll`      | PostgreSQL user                        |
| `POSTGRES_PASSWORD` | No       | `homepoll`      | PostgreSQL password                    |
| `POSTGRES_DB`       | No       | `homepoll`      | PostgreSQL database                    |
| `GRAFANA_PASSWORD`  | No       | `admin`         | Grafana admin password                 |
| `ETA_HOST`          | No       | `192.168.1.142` | IP address of your ETA heater          |
| `ETA_PORT`          | No       | `8080`          | Port of your ETA heater                |
| `POWER_HOST`        | No       | `https://api.salzburgnetz.at` | Salzburg Netz API base URL   |
| `POWER_TOKEN`       | If POWER | -               | Salzburg Netz API bearer token         |
| `POWER_GPNR`        | If POWER | -               | Customer number (8 digits, from the portal) |
| `POWER_ZP`          | If POWER | -               | Metering point (`AT00...`, from the portal) |
| `MCP_ADDR`          | No       | -               | Serve MCP over HTTP on this address, e.g. `127.0.0.1:8080`. Empty means stdio only, nothing listens |
| `MCP_TOKEN`         | If `MCP_ADDR` | -          | Bearer token the HTTP MCP endpoint requires        |

### How metrics are stored

- A `configuration` row defines each metric: the `module` it belongs to, its `name`, the API response object `path` to read, the `type` (numeric or text), and the `poll_interval` in seconds. The table is seeded by the first migration.
- The collector groups metrics by poll interval, polls each on its schedule, and writes readings to a `metric` table.
- Migrations (goose) are embedded in the binary and applied automatically on startup; the full schema lives in `internal/db/migrations`.

## External systems

Each shipped module has its own documentation - doc link, seeded metrics, and fetch specifics:

- [ETA ePE 9kW (HEATER)](docs/eta-heater.md)
- [Salzburg Netz - grid electricity (POWER)](docs/salzburg-netz-power.md)

More will be added here as new modules ship.

## Grafana

Visualization is delegated entirely to [Grafana](https://grafana.com). No need to reinvent the wheel when prior art already exists.

The repository includes two dashboards, provisioned as code under `grafana/`:

1. **Homepoll - Heater**, covering pellet consumption, full-load runtime, ignitions, boiler mode and power, and daily cost.
2. **Homepoll - Power**, covering daily and 15-minute electricity consumption and daily cost.


## MCP server

The same binary can expose the stored metrics over the [Model Context Protocol](https://modelcontextprotocol.io), so an assistant can answer "how warm is the boiler?" or "how much power did we use yesterday?" from the database Grafana reads. It is read-only and never migrates - point it at a database the collector has already created.

Two tools:

| Tool           | Purpose                                                                          |
| -------------- | -------------------------------------------------------------------------------- |
| `list_metrics` | Every configured metric with its module, description, unit, type and poll interval |
| `query_metric` | Readings of one metric, newest first; optional `module`, `from`/`to` (RFC 3339) and `limit` (default 1, max 500) |

### Over stdio (local)

`collector mcp` speaks JSON-RPC on stdin/stdout and the client owns the process, so nothing listens on a port. This is the right choice when the assistant runs on the same machine as the database.

```bash
go build -o "$HOME/.local/bin/homepoll" ./cmd/collector
claude mcp add homepoll -s local -- "$HOME/.local/bin/homepoll" mcp
```

The process does **not** read `.env` or inherit your shell environment, so pass any non-default `POSTGRES_*` value with `-e`.

### Over HTTP (remote)

Setting `MCP_ADDR` makes the running collector also serve MCP at `POST /mcp`, for assistants that cannot spawn a local process. Every request must carry `Authorization: Bearer $MCP_TOKEN`; requests with an `Origin` header are refused outright, which is the DNS-rebinding defence the transport calls for.

```bash
MCP_ADDR=127.0.0.1:8080 MCP_TOKEN=$(openssl rand -hex 32) make dev
claude mcp add --transport http homepoll http://127.0.0.1:8080/mcp \
  --header "Authorization: Bearer $MCP_TOKEN"
```

The endpoint is plain HTTP and deliberately so: bind `MCP_ADDR` to loopback or to the host's Tailscale address and let that layer carry the encryption and device identity. Do not bind it to `0.0.0.0` on an untrusted network without a TLS reverse proxy in front.

## Make targets

`make dev | build | docker | test | test-all | fmt | tidy | migrate-create | up | down`

`make test` runs unit tests only; `make test-all` adds the DB-backed integration tests (needs `make up`). `make migrate-create NAME=...` scaffolds a new goose migration.

## CI/CD

`.github/workflows/ci.yml` tests every push/PR and deploys `main` to the shared host over SSH.

Set these in repo settings: secrets `DEPLOY_HOST`, `DEPLOY_USER`, `DEPLOY_SSH_KEY`, `POSTGRES_PASSWORD`, `GRAFANA_PASSWORD`, `POWER_TOKEN`, `POWER_GPNR`, `POWER_ZP`, `MCP_TOKEN` (only if you set the `MCP_ADDR` variable); var `GRAFANA_PATH` (host dir Grafana mounts as `GRAFANA_PATH/{provisioning,dashboards}`). Other env vars from the table above can be set as `vars.*` to override their defaults.

## License

Copyright (C) 2026 Philipp Brandauer

Licensed under the GNU General Public License v3.0 - see [LICENSE](LICENSE).
