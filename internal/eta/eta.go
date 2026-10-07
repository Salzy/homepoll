package eta

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"homepoll/internal/collector"
	"homepoll/internal/db"
)

// Value is a decoded <value> element: Str is the display string, Num the scaled
// body. NumOK is false when unavailable ("xxx" or a non-numeric body).
type Value struct {
	Str   string
	Num   float64
	NumOK bool
}

// etaValue mirrors <value>; the body holds the true number, strValue is display only.
type etaValue struct {
	StrValue    string `xml:"strValue,attr"`
	ScaleFactor string `xml:"scaleFactor,attr"`
	Raw         string `xml:",chardata"`
}

// etaResponse mirrors the <eta> root element.
type etaResponse struct {
	Value etaValue `xml:"value"`
}

var client = &http.Client{Timeout: 10 * time.Second}

// Fetcher returns a collector.Fetch for the heater at baseURL, yielding one
// current reading. An unavailable numeric value is skipped, leaving a gap.
func Fetcher(baseURL string) collector.Fetch {
	return func(ctx context.Context, cfg db.Configuration) ([]collector.Reading, error) {
		v, err := Fetch(ctx, baseURL, cfg.Path)
		if err != nil {
			return nil, err
		}
		if cfg.Type == "numeric" {
			if !v.NumOK {
				log.Printf("%s: heater returned no numeric value (%q), skipping", cfg.Name, v.Str)
				return nil, nil
			}
			return []collector.Reading{{Value: strconv.FormatFloat(v.Num, 'f', -1, 64)}}, nil
		}
		return []collector.Reading{{Value: v.Str}}, nil
	}
}

// Fetch reads a single variable from the ETAtouch REST API and decodes it.
func Fetch(ctx context.Context, baseURL, path string) (Value, error) {
	target := baseURL + "/user/var" + path
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return Value{}, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return Value{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Value{}, fmt.Errorf("eta %s: status %d", path, resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return Value{}, err
	}
	var r etaResponse
	if err := xml.Unmarshal(body, &r); err != nil {
		return Value{}, fmt.Errorf("eta %s: parse: %w", path, err)
	}
	return decode(r.Value), nil
}

// decode scales the body by scaleFactor (default 1). strValue "xxx" wins over
// the body, which may hold a stale 0.
func decode(v etaValue) Value {
	out := Value{Str: strings.TrimSpace(v.StrValue)}
	if out.Str == "xxx" {
		return out
	}
	n, err := strconv.ParseFloat(strings.TrimSpace(v.Raw), 64)
	if err != nil {
		return out
	}
	scale := 1.0
	if s, err := strconv.ParseFloat(strings.TrimSpace(v.ScaleFactor), 64); err == nil && s != 0 {
		scale = s
	}
	out.Num = n / scale
	out.NumOK = true
	return out
}
