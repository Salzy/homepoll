package integration

import (
	"context"
	"database/sql"
	"math"
	"os"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"homepoll/internal/db"
)

const defaultDSN = "postgres://homepoll:homepoll@localhost:5432/homepoll?sslmode=disable"

// openTestDB connects to the integration Postgres (TEST_DATABASE_URL, or the
// docker-compose default) and applies the migrations. It skips under -short, and
// also when no database is reachable, so `make test` stays green without a DB.
func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping integration test in short mode (-short)")
	}
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		dsn = defaultDSN
	}
	sqlDB, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Skipf("no database: %v", err)
	}
	if err := sqlDB.Ping(); err != nil {
		sqlDB.Close()
		t.Skipf("database not reachable (%s): %v", dsn, err)
	}
	goose.SetBaseFS(db.MigrationsFS)
	if err := goose.SetDialect("postgres"); err != nil {
		t.Fatalf("goose dialect: %v", err)
	}
	if err := goose.Up(sqlDB, "migrations"); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return sqlDB
}

// TestListConfigurations runs ListConfigurations against the real schema. It is
// only faked in the unit tests, yet runs on every startup - this guards the
// hand-written struct and row scan in db.go against schema drift (for example a
// column added in a migration without a matching edit to Configuration).
func TestListConfigurations(t *testing.T) {
	sqlDB := openTestDB(t)
	defer sqlDB.Close()

	configs, err := db.New(sqlDB).ListConfigurations(context.Background())
	if err != nil {
		t.Fatalf("ListConfigurations: %v", err)
	}
	if len(configs) == 0 {
		t.Fatal("ListConfigurations returned no rows; the migration seed is missing")
	}

	byName := make(map[string]db.Configuration, len(configs))
	for _, c := range configs {
		byName[c.Name] = c
	}

	// Representative seeded metrics across both modules and both value types,
	// so the assertion exercises module-enum, numeric, and text scanning.
	// description and unit exist only to describe a metric to an MCP client;
	// nothing in the collector reads them, so this is what keeps their scan honest.
	if c := byName["boiler_temperature"]; c.Unit.String != "C" || c.Description.String == "" {
		t.Errorf(
			"boiler_temperature = {unit:%q description:%q}, want unit C and a non-empty description",
			c.Unit.String,
			c.Description.String,
		)
	}

	want := []struct {
		name, module, typ, path string
	}{
		{"boiler_temperature", "HEATER", "numeric", "/264/10891/0/0/12161"},
		{"boiler_mode", "HEATER", "text", "/264/10891/0/0/13414"},
		{"requested_power", "HEATER", "numeric", "/264/10891/0/0/12077"},
		{"electricity_consumption", "POWER", "numeric", "1-1:1.9.0"},
	}
	for _, w := range want {
		c, ok := byName[w.name]
		if !ok {
			t.Errorf("seeded metric %q is missing", w.name)
			continue
		}
		if c.Module != w.module || c.Type != w.typ || c.Path != w.path {
			t.Errorf(
				"%q = {module:%q type:%q path:%q}, want {module:%q type:%q path:%q}",
				w.name, c.Module, c.Type, c.Path, w.module, w.typ, w.path,
			)
		}
	}
}

// TestBatchInsertManyRows exercises the hand-written variable-length INSERT in
// db.go with more than one row (the POWER module inserts a full day at once).
// The unit test only checks the built SQL string and the dedup test inserts a
// single row, so this is the only place the multi-row statement runs against
// real Postgres.
func TestBatchInsertManyRows(t *testing.T) {
	sqlDB := openTestDB(t)
	defer sqlDB.Close()

	ctx := context.Background()
	tx, err := sqlDB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback()

	var configID int32
	if err := tx.QueryRowContext(ctx, "SELECT id FROM configuration LIMIT 1").Scan(&configID); err != nil {
		t.Fatalf("need at least one seeded configuration: %v", err)
	}

	q := db.New(tx)
	base := time.Date(2099, 2, 1, 0, 0, 0, 0, time.UTC)
	num := func(at time.Time, v float64) db.InsertMetricParams {
		return db.InsertMetricParams{
			RecordedAt:      at,
			ValueNum:        sql.NullFloat64{Float64: v, Valid: true},
			ConfigurationID: configID,
		}
	}
	rows := []db.InsertMetricParams{
		num(base, 0.1),
		num(base.Add(15*time.Minute), 0.2),
		num(base.Add(30*time.Minute), 0.3),
	}
	if err := q.InsertMetrics(ctx, rows); err != nil {
		t.Fatalf("batch insert: %v", err)
	}

	var count int
	var sum float64
	err = tx.QueryRowContext(
		ctx,
		"SELECT count(*), coalesce(sum(value_num), 0) FROM metric WHERE configuration_id = $1 AND recorded_at BETWEEN $2 AND $3",
		configID,
		base,
		base.Add(30*time.Minute),
	).Scan(&count, &sum)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if count != 3 {
		t.Errorf("inserted rows = %d, want 3", count)
	}
	if math.Abs(sum-0.6) > 1e-9 {
		t.Errorf("sum(value_num) = %v, want 0.6", sum)
	}
}

// TestMetricDedup verifies that the (configuration_id, recorded_at) primary key
// plus ON CONFLICT DO NOTHING make re-inserting the same instant idempotent -
// the scenario when the POWER module re-fetches the previous day on restart.
// The whole test runs in a transaction that is rolled back, so it leaves no
// rows behind even when run against the live dev database.
func TestMetricDedup(t *testing.T) {
	sqlDB := openTestDB(t)
	defer sqlDB.Close()

	ctx := context.Background()
	tx, err := sqlDB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback()

	var configID int32
	if err := tx.QueryRowContext(ctx, "SELECT id FROM configuration LIMIT 1").Scan(&configID); err != nil {
		t.Fatalf("need at least one seeded configuration: %v", err)
	}

	q := db.New(tx)
	at := time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC)
	row := func(v float64) db.InsertMetricParams {
		return db.InsertMetricParams{
			RecordedAt:      at,
			ValueNum:        sql.NullFloat64{Float64: v, Valid: true},
			ConfigurationID: configID,
		}
	}

	// Insert a reading, then re-fetch the same instant with a different value.
	if err := q.InsertMetrics(ctx, []db.InsertMetricParams{row(1.0)}); err != nil {
		t.Fatalf("first insert: %v", err)
	}
	if err := q.InsertMetrics(ctx, []db.InsertMetricParams{row(2.0)}); err != nil {
		t.Fatalf("re-insert: %v", err)
	}

	var count int
	var stored float64
	err = tx.QueryRowContext(
		ctx,
		"SELECT count(*), max(value_num) FROM metric WHERE configuration_id = $1 AND recorded_at = $2",
		configID,
		at,
	).Scan(&count, &stored)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if count != 1 {
		t.Errorf("got %d rows for the same (config, instant), want 1 - dedup failed", count)
	}
	if stored != 1.0 {
		t.Errorf(
			"stored value = %v, want 1.0 - ON CONFLICT DO NOTHING must keep the original",
			stored,
		)
	}
}

// TestListReadings exercises the hand-written read query behind the MCP
// query_metric tool: newest-first ordering, the optional window bounds, the
// limit, and the scan of both value columns. It runs in a transaction that is
// rolled back, so it leaves no rows behind.
func TestListReadings(t *testing.T) {
	sqlDB := openTestDB(t)
	defer sqlDB.Close()

	ctx := context.Background()
	tx, err := sqlDB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback()

	q := db.New(tx)
	configs, err := q.ListConfigurations(ctx)
	if err != nil {
		t.Fatalf("ListConfigurations: %v", err)
	}
	byName := make(map[string]db.Configuration, len(configs))
	for _, c := range configs {
		byName[c.Name] = c
	}
	numeric, ok := byName["boiler_temperature"]
	if !ok {
		t.Fatal("seeded metric boiler_temperature is missing")
	}
	text, ok := byName["boiler_mode"]
	if !ok {
		t.Fatal("seeded metric boiler_mode is missing")
	}

	// Far enough in the future that these rows are the newest in the table even
	// when the test runs against the live development database.
	base := time.Date(2099, 3, 1, 0, 0, 0, 0, time.UTC)
	at := func(i int) time.Time { return base.Add(time.Duration(i) * 15 * time.Minute) }
	rows := make([]db.InsertMetricParams, 0, 5)
	for i := range 4 {
		rows = append(rows, db.InsertMetricParams{
			RecordedAt:      at(i),
			ValueNum:        sql.NullFloat64{Float64: float64(i), Valid: true},
			ConfigurationID: numeric.ID,
		})
	}
	rows = append(rows, db.InsertMetricParams{
		RecordedAt:      base,
		ValueText:       sql.NullString{String: "Heizen", Valid: true},
		ConfigurationID: text.ID,
	})
	if err := q.InsertMetrics(ctx, rows); err != nil {
		t.Fatalf("seed readings: %v", err)
	}

	t.Run("should return the newest readings first, honouring the limit", func(t *testing.T) {
		got, err := q.ListReadings(ctx, db.ListReadingsParams{
			ConfigurationID: numeric.ID,
			Limit:           2,
		})
		if err != nil {
			t.Fatalf("ListReadings: %v", err)
		}
		if len(got) != 2 {
			t.Fatalf("got %d readings, want 2", len(got))
		}
		if !got[0].RecordedAt.Equal(at(3)) || !got[1].RecordedAt.Equal(at(2)) {
			t.Errorf("readings = %v, %v, want %v, %v then descending",
				got[0].RecordedAt, got[1].RecordedAt, at(3), at(2))
		}
		if got[0].ValueNum.Float64 != 3 {
			t.Errorf("newest value = %v, want 3", got[0].ValueNum.Float64)
		}
	})

	t.Run("should keep both window bounds inclusive", func(t *testing.T) {
		got, err := q.ListReadings(ctx, db.ListReadingsParams{
			ConfigurationID: numeric.ID,
			From:            sql.NullTime{Time: at(1), Valid: true},
			To:              sql.NullTime{Time: at(2), Valid: true},
			Limit:           100,
		})
		if err != nil {
			t.Fatalf("ListReadings: %v", err)
		}
		if len(got) != 2 {
			t.Fatalf("got %d readings in [at(1), at(2)], want 2 (bounds are inclusive)", len(got))
		}
	})

	t.Run("should leave the other side open when only one bound is set", func(t *testing.T) {
		got, err := q.ListReadings(ctx, db.ListReadingsParams{
			ConfigurationID: numeric.ID,
			From:            sql.NullTime{Time: at(2), Valid: true},
			Limit:           100,
		})
		if err != nil {
			t.Fatalf("ListReadings: %v", err)
		}
		if len(got) != 2 {
			t.Fatalf("got %d readings from at(2) onwards, want 2", len(got))
		}
	})

	t.Run("should scan a text metric into value_text", func(t *testing.T) {
		got, err := q.ListReadings(ctx, db.ListReadingsParams{
			ConfigurationID: text.ID,
			From:            sql.NullTime{Time: base, Valid: true},
			Limit:           1,
		})
		if err != nil {
			t.Fatalf("ListReadings: %v", err)
		}
		if len(got) != 1 {
			t.Fatalf("got %d readings, want 1", len(got))
		}
		if got[0].ValueNum.Valid || got[0].ValueText.String != "Heizen" {
			t.Errorf("reading = %+v, want value_text Heizen and a NULL value_num", got[0])
		}
	})
}
