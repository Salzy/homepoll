package power

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestRecordTime(t *testing.T) {
	got, err := recordTime("21.06.2026", "00:15:00", "+2")
	if err != nil {
		t.Fatalf("recordTime: %v", err)
	}
	// 00:15 at +02:00 is 22:15 on the previous day in UTC.
	want := time.Date(2026, 6, 20, 22, 15, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("recordTime = %v, want %v", got.UTC(), want)
	}
}

func TestFetch(t *testing.T) {
	const responseBody = `[
	  {"Datum":"21.06.2026","Uhrzeit":"00:00:00","UTC":"+2","Wert":"0,086","Einheit":"kWh"},
	  {"Datum":"21.06.2026","Uhrzeit":"00:15:00","UTC":"+2","Wert":"0,080","Einheit":"kWh"}
	]`
	var gotAuth, gotMethod, gotPath string
	var gotBody map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth, gotMethod, gotPath = r.Header.Get("Authorization"), r.Method, r.URL.Path
		raw, _ := io.ReadAll(r.Body)
		json.Unmarshal(raw, &gotBody)
		w.WriteHeader(http.StatusCreated) // API returns 201, not 200
		w.Write([]byte(responseBody))
	}))
	defer srv.Close()

	readings, err := Fetch(context.Background(), srv.URL, "tok", "10745740", "AT00ZP", "2026-06-21")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}

	if gotMethod != http.MethodPost {
		t.Errorf("method = %q, want POST", gotMethod)
	}
	if gotPath != "/api/v1/profile" {
		t.Errorf("path = %q, want /api/v1/profile", gotPath)
	}
	if gotAuth != "Bearer tok" {
		t.Errorf("Authorization = %q", gotAuth)
	}
	want := map[string]string{
		"GPNR": "10745740", "ZP": "AT00ZP", "AB": "2026-06-21", "BIS": "2026-06-21", "FORMAT": "json",
	}
	for k, v := range want {
		if gotBody[k] != v {
			t.Errorf("request body[%q] = %q, want %q", k, gotBody[k], v)
		}
	}

	if len(readings) != 2 {
		t.Fatalf("got %d readings, want 2", len(readings))
	}
	if readings[0].Value != "0,086" {
		t.Errorf(
			"readings[0].Value = %q, want %q (comma decimal preserved)",
			readings[0].Value,
			"0,086",
		)
	}
	if u := readings[1].RecordedAt.UTC(); !u.Equal(time.Date(2026, 6, 20, 22, 15, 0, 0, time.UTC)) {
		t.Errorf("readings[1].RecordedAt = %v, want 2026-06-20T22:15:00Z", u)
	}
}

func TestFetchStatusError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	_, err := Fetch(context.Background(), srv.URL, "tok", "10745740", "AT00ZP", "2026-06-21")
	if err == nil {
		t.Error("Fetch: expected error on 401 status, got nil")
	}
}
