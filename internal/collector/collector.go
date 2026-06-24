package collector

import (
	"context"
	"database/sql"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"

	"homepoll/internal/db"
)

// Reading is one value to store for a metric. RecordedAt is the instant the
// value applies to; the zero value means "now" (used by point-in-time sources
// like the heater). Historical sources (such as POWER) set it explicitly.
type Reading struct {
	Value      string
	RecordedAt time.Time
}

// Fetch retrieves the readings for a configured metric. Point-in-time sources
// return a single reading; sources that return a batch of timestamped samples
// (for example a daily power profile) return many.
type Fetch func(context.Context, db.Configuration) ([]Reading, error)

// Run groups the configurations by poll interval, starts one poller per
// distinct interval, and blocks until ctx is cancelled. Each metric is fetched
// by the Fetch registered for its module in fetchers.
// ponytail: one ticker per distinct interval. Fine for a handful of intervals.
func Run(
	ctx context.Context,
	q *db.Queries,
	configs []db.Configuration,
	fetchers map[string]Fetch,
) {
	groups := map[int32][]db.Configuration{}
	for _, cfg := range configs {
		groups[cfg.PollInterval] = append(groups[cfg.PollInterval], cfg)
	}

	var wg sync.WaitGroup
	for interval, group := range groups {
		wg.Add(1)
		go func(interval int32, group []db.Configuration) {
			defer wg.Done()
			poll(ctx, q, group, time.Duration(interval)*time.Second, fetchers)
		}(interval, group)
	}
	wg.Wait()
}

// poll collects the group immediately, then once per interval until cancelled.
func poll(
	ctx context.Context,
	q *db.Queries,
	group []db.Configuration,
	interval time.Duration,
	fetchers map[string]Fetch,
) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	collect(ctx, q, group, fetchers)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			collect(ctx, q, group, fetchers)
		}
	}
}

// collect fetches every metric in the group, dispatching to the fetcher for its
// module, and writes all readings in a single batched insert.
func collect(
	ctx context.Context,
	q *db.Queries,
	group []db.Configuration,
	fetchers map[string]Fetch,
) {
	now := time.Now()
	rows := make([]db.InsertMetricParams, 0, len(group))
	for _, cfg := range group {
		fetch, ok := fetchers[cfg.Module]
		if !ok {
			log.Printf("%s: no fetcher for module %q, skipping", cfg.Name, cfg.Module)
			continue
		}
		readings, err := fetch(ctx, cfg)
		if err != nil {
			log.Printf("%s: fetch: %v", cfg.Name, err)
			continue
		}
		for _, r := range readings {
			rows = append(rows, row(cfg, r, now))
		}
	}
	if err := q.InsertMetrics(ctx, rows); err != nil {
		log.Printf("insert %d metrics: %v", len(rows), err)
	}
}

// row builds an insert row from a reading, choosing the numeric or text column
// based on the configured type. A zero RecordedAt defaults to now.
func row(cfg db.Configuration, r Reading, now time.Time) db.InsertMetricParams {
	at := r.RecordedAt
	if at.IsZero() {
		at = now
	}
	arg := db.InsertMetricParams{ConfigurationID: cfg.ID, RecordedAt: at}
	if cfg.Type == "numeric" {
		if f, err := parseNumeric(r.Value); err == nil {
			arg.ValueNum = sql.NullFloat64{Float64: f, Valid: true}
			return arg
		}
		log.Printf("%s: %q not numeric, storing as text", cfg.Name, r.Value)
	}
	arg.ValueText = sql.NullString{String: r.Value, Valid: true}
	return arg
}

// parseNumeric parses a reading's string value, tolerating a comma decimal
// separator (used by the POWER API; ETA numeric values arrive dot-formatted).
func parseNumeric(s string) (float64, error) {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, ",", ".")
	return strconv.ParseFloat(s, 64)
}
