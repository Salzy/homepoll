-- +goose Up
CREATE TYPE module_type AS ENUM (
  'HEATER',
  'MOCK',
  'POWER'
);

CREATE TABLE configuration (
  id integer GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  module module_type NOT NULL,
  name varchar(128) NOT NULL,
  description text,
  unit varchar(32),
  path varchar(256) NOT NULL,
  type varchar(32) NOT NULL,
  poll_interval integer NOT NULL,
  UNIQUE (module, name)
);

-- A reading is identified by its metric and instant, so there is no surrogate
-- key. The composite primary key doubles as the dedupe target for the batched
-- ON CONFLICT DO NOTHING insert (keeping the daily POWER re-fetch idempotent)
-- and as the index for "latest value" lookups.
CREATE TABLE metric (
  recorded_at timestamptz NOT NULL DEFAULT NOW(),
  value_num double precision,
  value_text varchar(512),
  configuration_id integer NOT NULL REFERENCES configuration (id),
  PRIMARY KEY (configuration_id, recorded_at)
);

-- HEATER paths are the installation-specific CAN-bus addresses from /user/menu.
-- The POWER path is the universal OBIS series id; that account (GPNR) and
-- metering point (ZP) are per-deployment and come from POWER_GPNR / POWER_ZP.
INSERT INTO configuration (module, name, description, unit, path, type, poll_interval)
VALUES
  ('HEATER', 'total_consumption', 'Total pellet consumption (kg). Monotonically increasing counter.', 'kg', '/264/10891/0/0/12016', 'numeric',900),
  ('HEATER', 'full_load_hours', 'Total full load operating time, stored in seconds (heater reports seconds; dashboard converts to hours). Monotonically increasing counter.', 's', '/264/10891/0/0/12153', 'numeric',900),
  ('HEATER', 'heating_cycles', 'Heating cycle counter. Monotonically increasing.', NULL, '/264/10891/0/0/12017', 'numeric',900),
  ('HEATER', 'ignitions', 'Ignition counter. Monotonically increasing.', NULL, '/264/10891/0/0/12018', 'numeric',900),
  ('HEATER', 'boiler_mode', 'Boiler operating mode (Heizen, Entaschen, Bereit, etc.).', NULL, '/264/10891/0/0/13414', 'text',60),
  ('HEATER', 'boiler_temperature', 'Current boiler temperature (C).', 'C', '/264/10891/0/0/12161', 'numeric',60),
  ('HEATER', 'boiler_pressure', 'Boiler water pressure (bar).', 'bar', '/264/10891/0/0/12180', 'numeric',60),
  ('HEATER', 'boiler_power', 'Current boiler thermal power output. Reads xxx (a gap) while idle.', 'kW', '/264/10891/14877/0/2287', 'numeric',60),
  ('HEATER', 'requested_power', 'Requested (demanded) boiler thermal power. Always numeric, 0 when idle.', 'kW', '/264/10891/0/0/12077', 'numeric',60),
  ('HEATER', 'buffer_charge', 'Buffer tank charge level.', '%', '/264/10601/0/0/12528', 'numeric',300),
  ('HEATER', 'exhaust_fan', 'Exhaust fan speed/state.', 'rpm', '/264/10891/0/0/12165', 'numeric',60),
  ('HEATER', 'buffer_load_cycles', 'Buffer tank load cycle counter.', NULL, '/264/10601/0/0/15044', 'numeric',900),
  ('HEATER', 'outside_temperature', 'Outside temperature.', 'C', '/264/10601/0/0/12197', 'numeric',300),
  ('POWER', 'electricity_consumption', 'Grid electricity consumption per 15-minute interval (OBIS 1-1:1.9.0).', 'kWh', '1-1:1.9.0', 'numeric',86400);

-- +goose Down
DROP TABLE IF EXISTS metric;

DROP TABLE IF EXISTS configuration;

DROP TYPE IF EXISTS module_type;
