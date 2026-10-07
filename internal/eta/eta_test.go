package eta

import (
	"context"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"

	"homepoll/internal/db"
)

// valueXML wraps a <value> element in the real ETAtouch envelope, xmlns included.
func valueXML(attrs, body string) string {
	return `<eta version="1.0" xmlns="http://www.eta.co.at/rest/v1">` +
		`<value uri="/x" ` + attrs + `>` + body + `</value></eta>`
}

func TestFetch(t *testing.T) {
	const path = "/264/10891/0/0/12161"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/user/var"+path {
			t.Errorf("requested path = %q, want %q", r.URL.Path, "/user/var"+path)
		}
		w.Write(
			[]byte(valueXML(`strValue="25" scaleFactor="1" unit="°C" decPlaces="0"`, "25.4141")),
		)
	}))
	defer srv.Close()

	got, err := Fetch(context.Background(), srv.URL, path)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if !got.NumOK || math.Abs(got.Num-25.4141) > 1e-9 {
		t.Errorf("Fetch = %+v, want Num=25.4141 (scaled body, not strValue 25)", got)
	}
}

func TestFetchStatusError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	if _, err := Fetch(context.Background(), srv.URL, "/x"); err == nil {
		t.Error("Fetch: expected error on 500 status, got nil")
	}
}

// TestDecode uses real heater wire formats: number = body / scaleFactor.
func TestDecode(t *testing.T) {
	cases := []struct {
		name      string
		v         etaValue
		wantStr   string
		wantNum   float64
		wantNumOK bool
	}{
		{
			"scaled counter",
			etaValue{StrValue: "557", ScaleFactor: "10", Raw: "5573"},
			"557",
			557.3,
			true,
		},
		{
			"comma decimal",
			etaValue{StrValue: "2,06", ScaleFactor: "100", Raw: "206"},
			"2,06",
			2.06,
			true,
		},
		{
			"float body",
			etaValue{StrValue: "25", ScaleFactor: "1", Raw: "25.4141"},
			"25",
			25.4141,
			true,
		},
		{
			"duration in seconds",
			etaValue{StrValue: "193h 6m", ScaleFactor: "1", Raw: "695218"},
			"193h 6m",
			695218,
			true,
		},
		{"missing scaleFactor defaults to 1", etaValue{StrValue: "5", Raw: "5"}, "5", 5, true},
		{
			"xxx sentinel is unavailable",
			etaValue{StrValue: "xxx", ScaleFactor: "1000", Raw: "0"},
			"xxx",
			0,
			false,
		},
		{
			"non-numeric body",
			etaValue{StrValue: "Heizen", ScaleFactor: "1", Raw: "Heizen"},
			"Heizen",
			0,
			false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := decode(c.v)
			if got.Str != c.wantStr || got.NumOK != c.wantNumOK {
				t.Fatalf(
					"decode(%+v) = %+v, want Str=%q NumOK=%v",
					c.v,
					got,
					c.wantStr,
					c.wantNumOK,
				)
			}
			if c.wantNumOK && math.Abs(got.Num-c.wantNum) > 1e-9 {
				t.Errorf("Num = %v, want %v", got.Num, c.wantNum)
			}
		})
	}
}

// TestFetcher: numeric stores the scaled number, text stores strValue, "xxx" is skipped.
func TestFetcher(t *testing.T) {
	responses := map[string]string{
		"/user/var/num": valueXML(`strValue="557" scaleFactor="10"`, "5573"),
		"/user/var/txt": valueXML(`strValue="Heizen" scaleFactor="1"`, "1802"),
		"/user/var/xxx": valueXML(`strValue="xxx" scaleFactor="1000"`, "0"),
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, ok := responses[r.URL.Path]
		if !ok {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		w.Write([]byte(body))
	}))
	defer srv.Close()
	fetch := Fetcher(srv.URL)

	cases := []struct {
		name    string
		cfg     db.Configuration
		wantLen int
		wantVal string
	}{
		{"numeric", db.Configuration{Name: "n", Path: "/num", Type: "numeric"}, 1, "557.3"},
		{"text", db.Configuration{Name: "t", Path: "/txt", Type: "text"}, 1, "Heizen"},
		{
			"unavailable numeric is skipped",
			db.Configuration{Name: "x", Path: "/xxx", Type: "numeric"},
			0,
			"",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := fetch(context.Background(), c.cfg)
			if err != nil {
				t.Fatalf("fetch: %v", err)
			}
			if len(got) != c.wantLen {
				t.Fatalf("got %d readings, want %d", len(got), c.wantLen)
			}
			if c.wantLen == 1 && got[0].Value != c.wantVal {
				t.Errorf("value = %q, want %q", got[0].Value, c.wantVal)
			}
		})
	}
}
