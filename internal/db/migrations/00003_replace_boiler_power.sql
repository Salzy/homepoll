-- +goose Up
-- The heat meter's power (Leistung) reads xxx even while heating, so
-- boiler_power never stored a value. Its energy counter works; the counter's
-- increase per hour is the boiler's average output.
UPDATE configuration
SET name = 'heat_energy',
  description = 'Heat delivered by the boiler, from its heat meter (kWh). Monotonically increasing counter; the increase per hour is the average output in kW.',
  unit = 'kWh',
  path = '/264/10891/14877/0/2273',
  poll_interval = 900
WHERE module = 'HEATER' AND name = 'boiler_power';

-- +goose Down
DELETE FROM metric
WHERE configuration_id = (SELECT id FROM configuration WHERE module = 'HEATER' AND name = 'heat_energy');

UPDATE configuration
SET name = 'boiler_power',
  description = 'Current boiler thermal power output. Reads xxx (a gap) while idle.',
  unit = 'kW',
  path = '/264/10891/14877/0/2287',
  poll_interval = 60
WHERE module = 'HEATER' AND name = 'heat_energy';
