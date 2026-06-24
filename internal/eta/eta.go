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

// Value is the decoded <value> element from the ETAtouch /user/var endpoint.
// Str is the heater-formatted display string (for example "22.7", "Heizen",
// "193h 6m", or the "xxx" sentinel for an unavailable reading). Num is the raw
// element body scaled by scaleFactor; NumOK is false when the value is
// unavailable - either the "xxx" sentinel or a non-numeric body.
type Value struct {
	Str   string
	Num   float64
	NumOK bool
}

// etaValue mirrors the <value> element attributes and body we care about. The
// element body holds the true number; strValue is only a localized display form.
type etaValue struct {
	StrValue    string `xml:"strValue,attr"`
	ScaleFactor string `xml:"scaleFactor,attr"`
	Raw         string `xml:",chardata"`
}

// etaResponse mirrors the <eta> root element of an ETAtouch variable response.
type etaResponse struct {
	Value etaValue `xml:"value"`
}

var client = &http.Client{Timeout: 10 * time.Second}

// Fetcher returns a collector.Fetch bound to the given ETA base URL (for
// example "http://192.168.1.142:8080"). The heater is a point-in-time source,
// so each call yields a single reading stamped with the current time. Numeric
// metrics store the scaled raw value; text metrics store the formatted string.
// An unavailable numeric reading (the "xxx" sentinel or a non-numeric body) is
// skipped so it leaves a gap rather than text in a numeric series.
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

// decode turns a raw <value> element into a Value, scaling the element body by
// scaleFactor (default 1). NumOK is left false when the value is unavailable:
// the heater signals this with strValue "xxx" (the element body may still hold a
// stale 0, so the sentinel is the authority), or with a non-numeric body.
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
