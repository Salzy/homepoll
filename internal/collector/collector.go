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

// Reading is one value to store. A zero RecordedAt means now.
type Reading struct {
	Value      string
	RecordedAt time.Time
}

// Fetch retrieves the readings for one configured metric.
type Fetch func(context.Context, db.Configuration) ([]Reading, error)

// Run polls each interval group with its module's Fetch until ctx is cancelled.
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

// collect fetches every metric in the group and inserts all readings at once.
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

// row maps a reading to the numeric or text column per the configured type.
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

// parseNumeric parses a number, accepting a comma decimal separator (POWER).
func parseNumeric(s string) (float64, error) {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, ",", ".")
	return strconv.ParseFloat(s, 64)
}
