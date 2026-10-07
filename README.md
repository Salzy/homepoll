# Homepoll

Modular metrics collector for the home: each module polls an external API on its own schedule, readings land in PostgreSQL, Grafana visualizes them.

```
ETA heater    (HTTP/XML)   --->  Collector (Go)  --->  PostgreSQL  --->  Grafana :3000
Salzburg Netz (HTTPS/JSON) --->        |
                                       +---> MCP over HTTP (read-only, optional)
```

## Quick start

```bash
go mod tidy            # writes go.sum
make up                # PostgreSQL + Grafana
DRY_RUN=true make dev  # mock data, no external systems
```

Grafana: http://localhost:3000 (`admin` / `GRAFANA_PASSWORD`, default `admin`).

## How it works

- One `configuration` row per metric: `module`, `name`, `path`, `type` (`numeric` / `text`), `poll_interval` (seconds). Seeded in `internal/db/migrations/00001_init.sql`.
- The collector groups metrics by interval and batch-inserts readings into `metric`.
- Migrations (goose) are embedded and applied on startup.
- New system = enum value + `configuration` rows (migration) + fetcher + a case in `cmd/collector/main.go`.

## Modules

| Module   | System                   | Fetch                                                                | Docs                                                                                                                                    |
| -------- | ------------------------ | -------------------------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------- |
| `HEATER` | ETA ePE 9kW (ETAtouch)   | `GET /user/var<path>`, one value per call                            | [ETAtouch REST API](https://www.meineta.at/javax.faces.resource/downloads/ETA-RESTful-v1.2.pdf.xhtml?ln=default&v=0)                    |
| `POWER`  | Salzburg Netz grid meter | `POST /api/v1/profile`, previous day of 15-minute samples once a day | [API description](https://www.salzburgnetz.at/content/dam/salzburgnetz/dokumente/service/Programmierschnittstelle_Beschreibung_API.pdf) |
| `MOCK`   | -                        | `DRY_RUN=true` replaces every module with random values              | -                                                                                                                                       |

**HEATER**

- Find paths for your installation at `http://<ETA_HOST>:8080/user/menu`.
- Numeric metrics store element body / `scaleFactor`; text metrics store `strValue`.
- `xxx` (value unavailable) is skipped and leaves a gap.
- Values keep the heater's unit (`full_load_hours` is seconds; the dashboard converts).

**POWER**

- `path` is the OBIS series id `1-1:1.9.0`; account and meter come from `POWER_GPNR` / `POWER_ZP` (Salzburg Netz portal).
- Re-fetching a day is idempotent (`ON CONFLICT DO NOTHING`).

| Metric                                              | Module | Unit | Interval (s) |
| --------------------------------------------------- | ------ | ---- | ------------ |
| `total_consumption`                                 | HEATER | kg   | 900          |
| `full_load_hours`                                   | HEATER | s    | 900          |
| `heating_cycles`, `ignitions`, `buffer_load_cycles` | HEATER | -    | 900          |
| `buffer_charge`                                     | HEATER | %    | 300          |
| `outside_temperature`                               | HEATER | C    | 300          |
| `boiler_mode` (text)                                | HEATER | -    | 60           |
| `boiler_temperature`                                | HEATER | C    | 60           |
| `boiler_pressure`                                   | HEATER | bar  | 60           |
| `boiler_power` (gap while idle)                     | HEATER | kW   | 60           |
| `requested_power` (0 while idle)                    | HEATER | kW   | 60           |
| `exhaust_fan`                                       | HEATER | rpm  | 60           |
| `electricity_consumption`                           | POWER  | kWh  | 86400        |

## Configuration (`.env`)

| Variable                                                  | Default                                                     | Notes                                                         |
| --------------------------------------------------------- | ----------------------------------------------------------- | ------------------------------------------------------------- |
| `DRY_RUN`                                                 | `false`                                                     | Mock data for every module                                    |
| `POSTGRES_HOST` / `_PORT` / `_USER` / `_PASSWORD` / `_DB` | `localhost` / `5432` / `homepoll` / `homepoll` / `homepoll` |                                                               |
| `POSTGRES_SSLMODE`                                        | `disable`                                                   |                                                               |
| `GRAFANA_PASSWORD`                                        | `admin`                                                     |                                                               |
| `ETA_HOST` / `ETA_PORT`                                   | `192.168.1.142` / `8080`                                    |                                                               |
| `POWER_HOST`                                              | `https://api.salzburgnetz.at`                               |                                                               |
| `POWER_TOKEN`, `POWER_GPNR`, `POWER_ZP`                   | -                                                           | Required for POWER                                            |
| `MCP_ADDR`                                                | -                                                           | Serve MCP over HTTP, e.g. `127.0.0.1:8080`; empty disables it |
| `MCP_TOKEN`                                               | -                                                           | Required with `MCP_ADDR`                                      |

## Grafana

Dashboards as code in `grafana/`: **Heater** (pellets, runtime, ignitions, mode, power, cost) and **Power** (daily and 15-minute consumption, cost).

## MCP server

Read-only [MCP](https://modelcontextprotocol.io) access to the stored metrics, served by the running collector at `POST /mcp` when `MCP_ADDR` is set.

| Tool           | Purpose                                                                                                         |
| -------------- | --------------------------------------------------------------------------------------------------------------- |
| `list_metrics` | All metrics with module, description, unit, type, interval                                                      |
| `query_metric` | Readings of one metric, newest first; optional `module`, `from` / `to` (RFC 3339), `limit` (default 1, max 500) |

Bearer token required, requests with an `Origin` header refused. Plain HTTP: bind to loopback or Tailscale, never `0.0.0.0` on an untrusted network.

```bash
MCP_ADDR=127.0.0.1:8080 MCP_TOKEN=$(openssl rand -hex 32) make dev
claude mcp add --transport http homepoll http://127.0.0.1:8080/mcp \
  --header "Authorization: Bearer $MCP_TOKEN"
```

## Development

| Command                        | Does                                                                             |
| ------------------------------ | -------------------------------------------------------------------------------- |
| `make dev` / `make build`      | Live reload (air, loads `.env`) / build once                                     |
| `make test` / `make test-all`  | Unit tests / plus DB integration tests (needs `make up`, or `TEST_DATABASE_URL`) |
| `make fmt`                     | gofmt + wrap at 100 columns                                                      |
| `make migrate-create NAME=...` | New goose migration                                                              |
| `make up` / `make down`        | Local PostgreSQL + Grafana                                                       |
| `make docker`                  | Container image (`IMAGE=name:tag`)                                               |

## CI/CD

`.github/workflows/ci.yml`: test every push and PR; on `main`, build the `linux/arm/v7` image to GHCR and deploy over SSH via Tailscale.

- Secrets: `DEPLOY_HOST`, `DEPLOY_USER`, `DEPLOY_SSH_KEY`, `TS_OAUTH_CLIENT_ID`, `TS_OAUTH_SECRET`, `POSTGRES_PASSWORD`, `GRAFANA_PASSWORD`, `POWER_TOKEN`, `POWER_GPNR`, `POWER_ZP`, `MCP_TOKEN` (only with `MCP_ADDR`).
- Variables: `GRAFANA_PATH` (host dir with `provisioning/` and `dashboards/`); optional overrides `POSTGRES_HOST`, `POSTGRES_PORT`, `POSTGRES_USER`, `POSTGRES_DB`, `ETA_HOST`, `ETA_PORT`, `POWER_HOST`, `MCP_ADDR`.

## License

Copyright (C) 2026 Philipp Brandauer. GPL v3, see [LICENSE](LICENSE).
