package collector

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"homepoll/internal/db"
)

func TestRow(t *testing.T) {
	numeric := db.Configuration{ID: 1, Name: "boiler_temperature", Type: "numeric"}
	if got := row(numeric, Reading{Value: " 22.7 "}, time.Now()); got.ValueNum.Float64 != 22.7 ||
		got.ValueText.Valid {
		t.Errorf("row(numeric, 22.7) = %+v, want ValueNum 22.7 and no text", got)
	}
	// A numeric metric that reads non-numeric falls back to text, not a dropped row.
	if got := row(numeric, Reading{Value: "Heizen"}, time.Now()); got.ValueNum.Valid ||
		got.ValueText.String != "Heizen" {
		t.Errorf("row(numeric, Heizen) = %+v, want text fallback", got)
	}
}

func TestCollectDispatchesByModuleAndSkipsUnknown(t *testing.T) {
	configs := []db.Configuration{
		{ID: 1, Module: "HEATER", Name: "boiler_temperature", Type: "numeric"},
		{ID: 2, Module: "SOLAR", Name: "panel_output", Type: "numeric"},
		{ID: 3, Module: "UNKNOWN", Name: "ignored", Type: "numeric"},
	}
	var heaterCalls, solarCalls int
	fetchers := map[string]Fetch{
		"HEATER": func(_ context.Context, _ db.Configuration) ([]Reading, error) {
			heaterCalls++
			return []Reading{{Value: "42"}}, nil
		},
		"SOLAR": func(_ context.Context, _ db.Configuration) ([]Reading, error) {
			solarCalls++
			return []Reading{{Value: "7"}}, nil
		},
	}

	fake := &fakeDBTX{}
	collect(context.Background(), db.New(fake), configs, fetchers)

	if heaterCalls != 1 || solarCalls != 1 {
		t.Errorf("fetch calls: heater=%d solar=%d, want 1 and 1", heaterCalls, solarCalls)
	}
	// UNKNOWN has no fetcher: one query, 2 rows * 4 params.
	if fake.execCount != 1 {
		t.Errorf("ExecContext called %d times, want 1", fake.execCount)
	}
	if len(fake.lastArgs) != 8 {
		t.Errorf("inserted %d params, want 8 (2 rows)", len(fake.lastArgs))
	}
}

func TestCollectFlattensMultipleReadings(t *testing.T) {
	configs := []db.Configuration{
		{ID: 1, Module: "POWER", Name: "electricity_consumption", Type: "numeric"},
	}
	start := time.Date(2026, 6, 21, 0, 0, 0, 0, time.UTC)
	fetchers := map[string]Fetch{
		"POWER": func(_ context.Context, _ db.Configuration) ([]Reading, error) {
			return []Reading{
				{Value: "0.1", RecordedAt: start},
				{Value: "0.2", RecordedAt: start.Add(15 * time.Minute)},
				{Value: "0.3", RecordedAt: start.Add(30 * time.Minute)},
			}, nil
		},
	}

	fake := &fakeDBTX{}
	collect(context.Background(), db.New(fake), configs, fetchers)

	// One config returning three readings inserts three rows in one query.
	if fake.execCount != 1 {
		t.Errorf("ExecContext called %d times, want 1", fake.execCount)
	}
	if len(fake.lastArgs) != 12 {
		t.Errorf("inserted %d params, want 12 (3 rows * 4)", len(fake.lastArgs))
	}
}

// fakeDBTX records ExecContext calls, the only method collect uses.
type fakeDBTX struct {
	execCount int
	lastArgs  []any
}

func (f *fakeDBTX) ExecContext(_ context.Context, _ string, args ...any) (sql.Result, error) {
	f.execCount++
	f.lastArgs = args
	return fakeResult{}, nil
}

func (f *fakeDBTX) QueryContext(context.Context, string, ...any) (*sql.Rows, error) {
	return nil, nil
}

type fakeResult struct{}

func (fakeResult) LastInsertId() (int64, error) { return 0, nil }
func (fakeResult) RowsAffected() (int64, error) { return 0, nil }
