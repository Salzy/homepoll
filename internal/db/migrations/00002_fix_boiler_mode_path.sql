-- +goose Up
-- boiler_mode read the operating mode setting (Betriebsart, always "Dauernd"),
-- not the boiler state (Kessel-Zustand detailliert). Its stored values are
-- wrong and are dropped.
UPDATE configuration
SET description = 'Boiler state (Heizen, Bereit, Zünden, Entaschen, etc.).',
  path = '/264/10891/0/0/12000'
WHERE module = 'HEATER' AND name = 'boiler_mode';

DELETE FROM metric
WHERE configuration_id = (SELECT id FROM configuration WHERE module = 'HEATER' AND name = 'boiler_mode')
  AND value_text = 'Dauernd';

-- +goose Down
UPDATE configuration
SET description = 'Boiler operating mode (Heizen, Entaschen, Bereit, etc.).',
  path = '/264/10891/0/0/13414'
WHERE module = 'HEATER' AND name = 'boiler_mode';
