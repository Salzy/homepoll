# ETA ePE 9kW (HEATER)

- [Documentation - ETAtouch RESTful Webservices](https://www.meineta.at/javax.faces.resource/downloads/ETA-RESTful-v1.2.pdf.xhtml?ln=default&v=0)
- Browse `http://<ETA_HOST>:8080/user/menu` to discover URIs for your installation.
- Values are read from `http://<ETA_HOST>:8080/user/var<path>`. Numeric metrics store the scaled raw value (element body / `scaleFactor`, the heater's true number); text metrics store the formatted `strValue`. The heater's `xxx` marker (value temporarily unavailable, for example boiler power while idle) is skipped, leaving a gap rather than a bogus reading. Values stay in the heater's own unit - notably `full_load_hours` is reported in seconds, and the dashboard converts to hours.
- The seeded metrics, with the gathered fields:

| name                  | path                       | type    | unit | poll_interval |
| --------------------- | -------------------------- | ------- | ---- | ------------- |
| `total_consumption`   | `/264/10891/0/0/12016`     | numeric | kg   | 900      |
| `full_load_hours`     | `/264/10891/0/0/12153`     | numeric | s    | 900      |
| `heating_cycles`      | `/264/10891/0/0/12017`     | numeric |      | 900      |
| `ignitions`           | `/264/10891/0/0/12018`     | numeric |      | 900      |
| `boiler_mode`         | `/264/10891/0/0/13414`     | text    |      | 60       |
| `boiler_temperature`  | `/264/10891/0/0/12161`     | numeric | C    | 60       |
| `boiler_pressure`     | `/264/10891/0/0/12180`     | numeric | bar  | 60       |
| `boiler_power`        | `/264/10891/14877/0/2287`  | numeric | kW   | 60       |
| `requested_power`     | `/264/10891/0/0/12077`     | numeric | kW   | 60       |
| `buffer_charge`       | `/264/10601/0/0/12528`     | numeric | %    | 300      |
| `exhaust_fan`         | `/264/10891/0/0/12165`     | numeric | rpm  | 60       |
| `buffer_load_cycles`  | `/264/10601/0/0/15044`     | numeric |      | 900      |
| `outside_temperature` | `/264/10601/0/0/12197`     | numeric | C    | 300      |

- `boiler_power` is the actual thermal output and reads `xxx` (a gap) while idle; `requested_power` is the controller's demanded output and is always numeric (0 when idle), so it graphs as a gap-free line.
