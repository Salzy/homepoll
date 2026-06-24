package mock

import (
	"context"
	"math/rand"
	"strconv"
	"sync"

	"homepoll/internal/collector"
	"homepoll/internal/db"
)

// Mock generates plausible heater values so the dashboard can be exercised
// without a real heater (DRY_RUN=true).
type Mock struct {
	mu       sync.Mutex
	counters map[string]float64
}

// New creates a Mock with zeroed counter state.
func New() *Mock {
	return &Mock{counters: map[string]float64{}}
}

// Fetcher adapts Value to the collector's fetch signature, returning a single
// current reading per call.
func (m *Mock) Fetcher() collector.Fetch {
	return func(_ context.Context, cfg db.Configuration) ([]collector.Reading, error) {
		value, err := m.Value(cfg)
		if err != nil {
			return nil, err
		}
		return []collector.Reading{{Value: value}}, nil
	}
}

// Value returns a generated value for the given configuration. Monotonic
// counters increase on every call; gauges vary within a realistic range.
// ponytail: counters reset on restart; seed from the database if continuity matters.
func (m *Mock) Value(cfg db.Configuration) (string, error) {
	switch cfg.Name {
	case "boiler_mode":
		modes := []string{"Heizen", "Bereit", "Entaschen", "Zuenden"}
		return modes[rand.Intn(len(modes))], nil
	case "total_consumption":
		return m.tick(cfg.Name, 0.5+rand.Float64()*1.5), nil
	case "full_load_hours":
		return m.tick(cfg.Name, rand.Float64()*0.25), nil
	case "heating_cycles", "ignitions", "buffer_load_cycles":
		return m.tick(cfg.Name, float64(rand.Intn(2))), nil
	case "boiler_temperature":
		return gauge(60, 80), nil
	case "boiler_pressure":
		return gauge(1.2, 1.8), nil
	case "boiler_power":
		return gauge(0, 9), nil
	case "buffer_charge":
		return gauge(0, 100), nil
	case "exhaust_fan":
		return gauge(0, 1400), nil
	case "outside_temperature":
		return gauge(-5, 25), nil
	default:
		return gauge(0, 100), nil
	}
}

// tick increases the named counter by delta and returns the new total.
func (m *Mock) tick(name string, delta float64) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.counters[name] += delta
	return strconv.FormatFloat(m.counters[name], 'f', 2, 64)
}

// gauge returns a random value within [low, high].
func gauge(low, high float64) string {
	return strconv.FormatFloat(low+rand.Float64()*(high-low), 'f', 2, 64)
}
