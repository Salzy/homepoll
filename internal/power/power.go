// Package power fetches a day of 15-minute grid consumption samples from the
// Salzburg Netz API.
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

// profileRecord is one 15-minute sample; only stored fields are decoded.
type profileRecord struct {
	Datum   string `json:"Datum"`   // dd.mm.yyyy
	Uhrzeit string `json:"Uhrzeit"` // HH:MM:SS, interval start
	UTC     string `json:"UTC"`     // offset in hours, for example "+2"
	Wert    string `json:"Wert"`    // value, comma decimal separator
}

// Fetcher returns a collector.Fetch for the previous day; the configuration is
// unused (one metering point per deployment).
func Fetcher(baseURL, token, gpnr, zp string) collector.Fetch {
	return func(ctx context.Context, _ db.Configuration) ([]collector.Reading, error) {
		return Fetch(ctx, baseURL, token, gpnr, zp, yesterday(time.Now()))
	}
}

// Fetch returns one reading per 15-minute interval of day ("yyyy-mm-dd").
func Fetch(
	ctx context.Context,
	baseURL, token, gpnr, zp, day string,
) ([]collector.Reading, error) {
	body, err := json.Marshal(map[string]string{
		"GPNR": gpnr, "ZP": zp, "AB": day, "BIS": day, "FORMAT": "json",
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

// readings converts decoded profile records into timestamped readings.
func readings(records []profileRecord) ([]collector.Reading, error) {
	out := make([]collector.Reading, 0, len(records))
	for _, r := range records {
		at, err := recordTime(r.Datum, r.Uhrzeit, r.UTC)
		if err != nil {
			return nil, fmt.Errorf("power: %w", err)
		}
		out = append(out, collector.Reading{Value: r.Wert, RecordedAt: at})
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

// yesterday returns the previous day in Austria as "yyyy-mm-dd".
func yesterday(now time.Time) string {
	// ponytail: tzdata is embedded, so LoadLocation with a constant name can't fail.
	loc, _ := time.LoadLocation("Europe/Vienna")
	return now.In(loc).AddDate(0, 0, -1).Format("2006-01-02")
}
