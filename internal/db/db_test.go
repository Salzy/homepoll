package db

import (
	"context"
	"testing"
)

func TestBuildInsertMetrics(t *testing.T) {
	got := buildInsertMetrics(2)
	want := "INSERT INTO metric (recorded_at, value_num, value_text, configuration_id) VALUES " +
		"($1,$2,$3,$4),($5,$6,$7,$8) ON CONFLICT (configuration_id, recorded_at) DO NOTHING"
	if got != want {
		t.Errorf("buildInsertMetrics(2):\n got %q\nwant %q", got, want)
	}
}

func TestInsertMetricsEmptyIsNoop(t *testing.T) {
	// A nil DBTX is safe only because of the empty-slice guard.
	if err := New(nil).InsertMetrics(context.Background(), nil); err != nil {
		t.Errorf("InsertMetrics(nil) = %v, want nil", err)
	}
}
