# Salzburg Netz - grid electricity (POWER)

- [API description (PDF)](https://www.salzburgnetz.at/content/dam/salzburgnetz/dokumente/service/Programmierschnittstelle_Beschreibung_API.pdf)
- One POST to `<POWER_HOST>/api/v1/profile` returns a full day of load-profile values in 15-minute intervals (kWh per interval). The collector fetches the previous day once a day.
- Authentication is `POWER_TOKEN` (bearer); the account (`POWER_GPNR`) and metering point (`POWER_ZP`) are env vars you copy from the Salzburg Netz portal.
- Seeded metric: `electricity_consumption` (module `POWER`, kWh, poll_interval `86400`). Its `path` is the universal OBIS series id `1-1:1.9.0`; the per-deployment account and meter live in the environment, not the migration.
