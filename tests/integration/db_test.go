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
