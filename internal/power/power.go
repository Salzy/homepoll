// Package power fetches 15-minute grid consumption samples from the Salzburg
// Netz API.
package power

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	_ "time/tzdata" // embed the zoneinfo database so LoadLocation works in distroless

	"homepoll/internal/collector"
	"homepoll/internal/db"
)

var client = &http.Client{Timeout: 30 * time.Second}

// backfillDays is how many past days every fetch re-requests. Inserts are
// idempotent, so a failed or incomplete day is filled in by a later run.
// Verified against the Salzburg Netz API description (2026-10-07): a request
// may span up to three years, data updates once a day, and more than 90% of a
// day is available by 10-12h, so yesterday is often incomplete on first fetch.
const backfillDays = 7

// profileRecord is one 15-minute sample; only stored fields are decoded.
type profileRecord struct {
	Datum   string `json:"Datum"`   // dd.mm.yyyy
	Uhrzeit string `json:"Uhrzeit"` // HH:MM:SS, interval start
	UTC     string `json:"UTC"`     // offset in hours, for example "+2"
	Wert    string `json:"Wert"`    // value, comma decimal separator
}

// Fetcher returns a collector.Fetch for the last backfillDays days up to
// yesterday; the configuration is unused (one metering point per deployment).
func Fetcher(baseURL, token, gpnr, zp string) collector.Fetch {
	return func(ctx context.Context, _ db.Configuration) ([]collector.Reading, error) {
		from, to := window(time.Now())
		return Fetch(ctx, baseURL, token, gpnr, zp, from, to)
	}
}

// Fetch returns one reading per 15-minute interval from day from to day to,
// both inclusive ("yyyy-mm-dd").
func Fetch(
	ctx context.Context,
	baseURL, token, gpnr, zp, from, to string,
) ([]collector.Reading, error) {
	body, err := json.Marshal(map[string]string{
		"GPNR": gpnr, "ZP": zp, "AB": from, "BIS": to, "FORMAT": "json",
	})
	if err != nil {
		return nil, fmt.Errorf("power: marshal request: %w", err)
	}
	req, err := http.NewRequestWithContext(
		ctx, http.MethodPost, baseURL+"/api/v1/profile", bytes.NewReader(body),
	)
	if err != nil {
		return nil, fmt.Errorf("power: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("power: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("power: status %d", resp.StatusCode)
	}

	var records []profileRecord
	if err := json.NewDecoder(resp.Body).Decode(&records); err != nil {
		return nil, fmt.Errorf("power: decode response: %w", err)
	}
	return readings(records)
}

// readings converts decoded profile records into timestamped readings with a
// dot decimal separator.
func readings(records []profileRecord) ([]collector.Reading, error) {
	out := make([]collector.Reading, 0, len(records))
	for _, r := range records {
		at, err := recordTime(r.Datum, r.Uhrzeit, r.UTC)
		if err != nil {
			return nil, fmt.Errorf("power: %w", err)
		}
		out = append(out, collector.Reading{
			Value:      strings.ReplaceAll(r.Wert, ",", "."),
			RecordedAt: at,
		})
	}
	return out, nil
}

// recordTime builds a sample's instant from date, time and hour offset ("+2").
func recordTime(datum, uhrzeit, utc string) (time.Time, error) {
	// ponytail: Austria only ever uses whole-hour offsets (+1 / +2).
	offsetHours, err := strconv.Atoi(strings.TrimSpace(utc))
	if err != nil {
		return time.Time{}, fmt.Errorf("bad UTC offset %q: %w", utc, err)
	}
	loc := time.FixedZone("UTC"+utc, offsetHours*3600)
	return time.ParseInLocation("02.01.2006 15:04:05", datum+" "+uhrzeit, loc)
}

// window returns the first and last day to fetch in Austria as "yyyy-mm-dd":
// backfillDays days ago through yesterday.
func window(now time.Time) (from, to string) {
	// ponytail: tzdata is embedded, so LoadLocation with a constant name can't fail.
	loc, _ := time.LoadLocation("Europe/Vienna")
	today := now.In(loc)
	return today.AddDate(0, 0, -backfillDays).Format(time.DateOnly),
		today.AddDate(0, 0, -1).Format(time.DateOnly)
}
