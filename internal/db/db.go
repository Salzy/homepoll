// Package db is the hand-written data access layer: a thin wrapper over
// database/sql for the two statements this service runs - list configurations
// and batch-insert metrics - plus the embedded goose migrations (migrations.go).
package db

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// DBTX is the subset of *sql.DB and *sql.Tx the queries call, so a Queries can
// run against either. We need both:
//   - *sql.DB (the pool): production and normal reads.
//   - *sql.Tx (a transaction): the integration tests run inside one and roll it
//     back, exercising the real SQL while leaving no rows behind.
//
// They are distinct concrete types with no shared interface in the standard
// library, so we declare this one to write code generic over both. (A fake also
// satisfies it, which lets the collector be unit-tested with no database.)
type DBTX interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

// Queries runs the service's SQL against a database handle or transaction.
type Queries struct {
	db DBTX
}

// New returns a Queries bound to the given database handle or transaction.
//
// Parameters:
//   - db: the *sql.DB pool or a *sql.Tx to run statements against.
//
// Returns the Queries wrapper.
func New(db DBTX) *Queries {
	return &Queries{db: db}
}

// Configuration is one row of the configuration table: the definition of a
// single metric to collect.
type Configuration struct {
	ID           int32
	Module       string
	Name         string
	Path         string
	Type         string
	PollInterval int32
}

const listConfigurations = `SELECT id, module, name, path, type, poll_interval FROM configuration`

// ListConfigurations returns every configured metric.
//
// Parameters:
//   - ctx: cancellation/deadline for the query.
//
// Returns the configuration rows, or an error if the query or a row scan fails.
func (q *Queries) ListConfigurations(ctx context.Context) ([]Configuration, error) {
	rows, err := q.db.QueryContext(ctx, listConfigurations)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []Configuration
	for rows.Next() {
		var c Configuration
		if err := rows.Scan(
			&c.ID,
			&c.Module,
			&c.Name,
			&c.Path,
			&c.Type,
			&c.PollInterval,
		); err != nil {
			return nil, err
		}
		items = append(items, c)
	}
	return items, rows.Err()
}

// InsertMetricParams is one metric reading to insert.
type InsertMetricParams struct {
	RecordedAt      time.Time
	ValueNum        sql.NullFloat64
	ValueText       sql.NullString
	ConfigurationID int32
}

// InsertMetrics writes many readings in a single multi-row INSERT, so a whole
// batch (for example a day of POWER samples) costs one round trip.
//
// Parameters:
//   - ctx: cancellation/deadline for the statement.
//   - args: the readings to insert; a nil/empty slice is a no-op.
//
// Returns an error if the insert fails.
func (q *Queries) InsertMetrics(ctx context.Context, args []InsertMetricParams) error {
	if len(args) == 0 {
		return nil
	}
	params := make([]any, 0, len(args)*4)
	for _, a := range args {
		params = append(params, a.RecordedAt, a.ValueNum, a.ValueText, a.ConfigurationID)
	}
	_, err := q.db.ExecContext(ctx, buildInsertMetrics(len(args)), params...)
	return err
}

// buildInsertMetrics builds the multi-row INSERT statement with positional
// placeholders for the given number of rows. ON CONFLICT DO NOTHING makes
// re-fetching the same data idempotent (the POWER module re-reads the previous
// day on every restart); it relies on the primary key over
// (configuration_id, recorded_at).
func buildInsertMetrics(rows int) string {
	var b strings.Builder
	b.WriteString(
		"INSERT INTO metric (recorded_at, value_num, value_text, configuration_id) VALUES ",
	)
	for i := range rows {
		if i > 0 {
			b.WriteByte(',')
		}
		n := i * 4
		fmt.Fprintf(&b, "($%d,$%d,$%d,$%d)", n+1, n+2, n+3, n+4)
	}
	b.WriteString(" ON CONFLICT (configuration_id, recorded_at) DO NOTHING")
	return b.String()
}
