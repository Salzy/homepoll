package mock

import (
	"strconv"
	"testing"

	"homepoll/internal/db"
)

func TestCounterMonotonic(t *testing.T) {
	m := New()
	cfg := db.Configuration{Name: "total_consumption", Type: "numeric"}
	prev := -1.0
	for i := 0; i < 50; i++ {
		v, err := m.Value(cfg)
		if err != nil {
			t.Fatalf("Value: %v", err)
		}
		f, err := strconv.ParseFloat(v, 64)
		if err != nil {
			t.Fatalf("ParseFloat(%q): %v", v, err)
		}
		if f < prev {
			t.Fatalf("counter decreased: %v < %v", f, prev)
		}
		prev = f
	}
}

func TestGaugeInRange(t *testing.T) {
	m := New()
	cfg := db.Configuration{Name: "boiler_temperature", Type: "numeric"}
	for i := 0; i < 50; i++ {
		v, _ := m.Value(cfg)
		f, err := strconv.ParseFloat(v, 64)
		if err != nil {
			t.Fatalf("ParseFloat(%q): %v", v, err)
		}
		if f < 60 || f > 80 {
			t.Fatalf("boiler_temperature out of range: %v", f)
		}
	}
}
