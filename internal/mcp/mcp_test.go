package mcp

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"homepoll/internal/db"
)

// configuration builds a db.Configuration for the resolver tests.
func configuration(id int32, module, name string) db.Configuration {
	return db.Configuration{ID: id, Module: module, Name: name, Type: "numeric"}
}

func TestServe(t *testing.T) {
	// Every request here is answered without touching the database, so a nil
	// handle is safe and the protocol can be exercised with no Postgres.
	in := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26"}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":3,"method":"resources/list"}`,
	}, "\n")

	var out strings.Builder
	if err := Serve(context.Background(), db.New(nil), strings.NewReader(in), &out); err != nil {
		t.Fatalf("Serve: %v", err)
	}

	dec := json.NewDecoder(strings.NewReader(out.String()))
	var got []response
	for dec.More() {
		var r response
		if err := dec.Decode(&r); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		got = append(got, r)
	}

	// Three responses, not four: the notification carries no id and so must be
	// answered with silence.
	if len(got) != 3 {
		t.Fatalf("got %d responses, want 3 (the notification must not be answered)", len(got))
	}

	t.Run("should echo the protocol version the client asked for", func(t *testing.T) {
		result, _ := got[0].Result.(map[string]any)
		if v := result["protocolVersion"]; v != "2025-03-26" {
			t.Errorf("protocolVersion = %v, want 2025-03-26", v)
		}
		if _, ok := result["capabilities"].(map[string]any)["tools"]; !ok {
			t.Error("initialize did not advertise the tools capability")
		}
	})

	t.Run("should advertise both tools with a valid schema", func(t *testing.T) {
		result, _ := got[1].Result.(map[string]any)
		raw, err := json.Marshal(result["tools"])
		if err != nil {
			t.Fatalf("marshal tools: %v", err)
		}
		var tools []struct {
			Name        string         `json:"name"`
			Description string         `json:"description"`
			InputSchema map[string]any `json:"inputSchema"`
		}
		if err := json.Unmarshal(raw, &tools); err != nil {
			t.Fatalf("tools/list is not valid JSON Schema: %v", err)
		}
		if len(tools) != 2 {
			t.Fatalf("got %d tools, want 2", len(tools))
		}
		for _, tool := range tools {
			if tool.Name == "" || tool.Description == "" || tool.InputSchema["type"] != "object" {
				t.Errorf("tool %+v is missing a name, description or object schema", tool)
			}
		}
	})

	t.Run("should reject an unknown method with the method-not-found code", func(t *testing.T) {
		if got[2].Error == nil {
			t.Fatal("resources/list was accepted, want an error")
		}
		if got[2].Error.Code != codeMethodNotFound {
			t.Errorf("code = %d, want %d", got[2].Error.Code, codeMethodNotFound)
		}
	})
}

func TestFindConfiguration(t *testing.T) {
	configs := []db.Configuration{
		configuration(1, "HEATER", "boiler_temperature"),
		configuration(2, "HEATER", "outside_temperature"),
		configuration(3, "POWER", "outside_temperature"),
	}

	t.Run("should resolve a name that is unique across modules", func(t *testing.T) {
		got, err := findConfiguration(configs, "boiler_temperature", "")
		if err != nil {
			t.Fatalf("findConfiguration: %v", err)
		}
		if got.ID != 1 {
			t.Errorf("ID = %d, want 1", got.ID)
		}
	})

	t.Run("should match the name case-insensitively", func(t *testing.T) {
		if _, err := findConfiguration(configs, "Boiler_Temperature", "heater"); err != nil {
			t.Errorf("findConfiguration: %v", err)
		}
	})

	t.Run("should refuse an ambiguous name and name the modules", func(t *testing.T) {
		_, err := findConfiguration(configs, "outside_temperature", "")
		if err == nil {
			t.Fatal("an ambiguous name resolved, want an error")
		}
		for _, want := range []string{"HEATER", "POWER"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error %q does not mention module %s", err, want)
			}
		}
	})

	t.Run("should resolve an ambiguous name once a module is given", func(t *testing.T) {
		got, err := findConfiguration(configs, "outside_temperature", "POWER")
		if err != nil {
			t.Fatalf("findConfiguration: %v", err)
		}
		if got.ID != 3 {
			t.Errorf("ID = %d, want 3", got.ID)
		}
	})

	t.Run("should point an unknown name at list_metrics", func(t *testing.T) {
		_, err := findConfiguration(configs, "solar_yield", "")
		if err == nil {
			t.Fatal("an unknown name resolved, want an error")
		}
		if !strings.Contains(err.Error(), "list_metrics") {
			t.Errorf("error %q does not tell the model how to recover", err)
		}
	})
}

func TestClampLimit(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   int32
		want int32
	}{
		{"should default a missing limit to the current value", 0, defaultLimit},
		{"should default a negative limit", -5, defaultLimit},
		{"should keep a limit inside the range", 42, 42},
		{"should cap an over-large limit", maxLimit + 1, maxLimit},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := clampLimit(tc.in); got != tc.want {
				t.Errorf("clampLimit(%d) = %d, want %d", tc.in, got, tc.want)
			}
		})
	}
}

func TestParseBound(t *testing.T) {
	t.Run("should leave an empty bound unset so the window stays open", func(t *testing.T) {
		got, err := parseBound("", "from")
		if err != nil {
			t.Fatalf("parseBound: %v", err)
		}
		if got.Valid {
			t.Errorf("empty bound = %v, want unset", got)
		}
	})

	t.Run("should parse an RFC 3339 bound", func(t *testing.T) {
		got, err := parseBound("2026-09-14T00:00:00Z", "from")
		if err != nil {
			t.Fatalf("parseBound: %v", err)
		}
		want := sql.NullTime{Time: time.Date(2026, 9, 14, 0, 0, 0, 0, time.UTC), Valid: true}
		if !got.Time.Equal(want.Time) || !got.Valid {
			t.Errorf("parseBound = %v, want %v", got, want)
		}
	})

	t.Run("should reject a bound that is not RFC 3339", func(t *testing.T) {
		if _, err := parseBound("yesterday", "from"); err == nil {
			t.Error("parseBound accepted \"yesterday\", want an error")
		}
	})
}
