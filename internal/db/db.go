// Package db is the hand-written database/sql access layer plus the embedded
// goose migrations.
package db

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// DBTX is satisfied by *sql.DB, *sql.Tx (integration tests roll back) and test
// fakes.
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

// Configuration is one row of the configuration table: one metric to collect.
type Configuration struct {
	Description  sql.NullString
	Unit         sql.NullString
	Module       string
	Name         string
	Path         string
	Type         string
	ID           int32
	PollInterval int32
}

const listConfigurations = `SELECT id, module, name, description, unit, path, type, poll_interval
FROM configuration
ORDER BY module, name`

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
			&c.Description,
			&c.Unit,
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

// InsertMetrics writes all readings in one multi-row INSERT.
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

// buildInsertMetrics builds the INSERT for the given row count. ON CONFLICT DO
// NOTHING on the primary key makes re-fetching idempotent.
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

// Reading is one stored value; ValueNum or ValueText is set per configuration.type.
type Reading struct {
	RecordedAt time.Time
	ValueNum   sql.NullFloat64
	ValueText  sql.NullString
}

// ListReadingsParams selects one metric's readings; a NULL From/To is unbounded.
type ListReadingsParams struct {
	From            sql.NullTime
	To              sql.NullTime
	ConfigurationID int32
	Limit           int32
}

// Newest first, so LIMIT 1 is the current value. Served by the primary key.
const listReadings = `SELECT recorded_at, value_num, value_text FROM metric
WHERE configuration_id = $1
  AND ($2::timestamptz IS NULL OR recorded_at >= $2)
  AND ($3::timestamptz IS NULL OR recorded_at <= $3)
ORDER BY recorded_at DESC
LIMIT $4`

// ListReadings returns the stored readings of a single metric, newest first.
//
// Parameters:
//   - ctx: cancellation/deadline for the query.
//   - arg: the metric, optional time window, and maximum number of rows.
//
// Returns the readings, or an error if the query or a row scan fails.
func (q *Queries) ListReadings(ctx context.Context, arg ListReadingsParams) ([]Reading, error) {
	rows, err := q.db.QueryContext(
		ctx, listReadings, arg.ConfigurationID, arg.From, arg.To, arg.Limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []Reading
	for rows.Next() {
		var r Reading
		if err := rows.Scan(&r.RecordedAt, &r.ValueNum, &r.ValueText); err != nil {
			return nil, err
		}
		items = append(items, r)
	}
	return items, rows.Err()
}
