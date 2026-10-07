// Package mcp is a read-only, tools-only Model Context Protocol server for the
// collected metrics. Two transports share everything below answer(): stdio
// (Serve) and Streamable HTTP (Handler, http.go).
package mcp

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"homepoll/internal/db"
)

// query_metric limits; the default of 1 returns the current value.
const (
	defaultLimit = 1
	maxLimit     = 500
)

// defaultProtocolVersion is used only when the client names no version.
const defaultProtocolVersion = "2025-06-18"

// JSON-RPC 2.0 error codes this server produces.
const (
	codeMethodNotFound = -32601
	codeInternalError  = -32603
)

// errMethodNotFound marks an unknown method or tool (codeMethodNotFound).
var errMethodNotFound = errors.New("method not found")

// request is an incoming JSON-RPC message; no ID means a notification (no reply).
type request struct {
	ID     json.RawMessage `json:"id"`
	Params json.RawMessage `json:"params"`
	Method string          `json:"method"`
}

// response is an outgoing JSON-RPC message; exactly one of Result and Error is set.
type response struct {
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
	JSONRPC string          `json:"jsonrpc"`
}

// rpcError is the JSON-RPC error object.
type rpcError struct {
	Message string `json:"message"`
	Code    int    `json:"code"`
}

// Serve answers JSON-RPC requests from in on out until in is closed.
//
// Parameters:
//   - ctx: cancellation/deadline passed to every database query.
//   - q: the read-only data access used by the tools.
//   - in: the request stream (os.Stdin in production).
//   - out: the response stream (os.Stdout in production - never log to it).
//
// Returns nil on a clean end of input, or the first transport error.
func Serve(ctx context.Context, q *db.Queries, in io.Reader, out io.Writer) error {
	dec := json.NewDecoder(in)
	enc := json.NewEncoder(out)
	for {
		var req request
		if err := dec.Decode(&req); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return fmt.Errorf("decode request: %w", err)
		}
		if len(req.ID) == 0 {
			continue // notification
		}
		if err := enc.Encode(answer(ctx, q, req)); err != nil {
			return fmt.Errorf("encode response: %w", err)
		}
	}
}

// answer runs one request and wraps the outcome as a JSON-RPC response.
func answer(ctx context.Context, q *db.Queries, req request) response {
	resp := response{JSONRPC: "2.0", ID: req.ID}
	result, err := dispatch(ctx, q, req)
	if err != nil {
		resp.Error = &rpcError{Code: errorCode(err), Message: err.Error()}
	} else {
		resp.Result = result
	}
	return resp
}

// errorCode maps an internal error to the JSON-RPC code that describes it.
func errorCode(err error) int {
	if errors.Is(err, errMethodNotFound) {
		return codeMethodNotFound
	}
	return codeInternalError
}

// dispatch routes one request to its handler and returns the JSON-RPC result.
func dispatch(ctx context.Context, q *db.Queries, req request) (any, error) {
	switch req.Method {
	case "initialize":
		return initialize(req.Params), nil
	case "ping":
		return struct{}{}, nil
	case "tools/list":
		return map[string]any{"tools": toolsJSON}, nil
	case "tools/call":
		return callTool(ctx, q, req.Params)
	default:
		return nil, fmt.Errorf("%w: %q", errMethodNotFound, req.Method)
	}
}

// initialize answers the handshake, echoing the client's protocol version.
// ponytail: echo the client's version; revisit if a tool needs a newer revision.
func initialize(params json.RawMessage) any {
	var p struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	_ = json.Unmarshal(params, &p)
	if p.ProtocolVersion == "" {
		p.ProtocolVersion = defaultProtocolVersion
	}
	return map[string]any{
		"protocolVersion": p.ProtocolVersion,
		"capabilities":    map[string]any{"tools": map[string]any{}},
		"serverInfo":      map[string]any{"name": "homepoll", "version": "0.1.0"},
	}
}

// toolsJSON is the tools/list payload; its descriptions are the model's only docs.
var toolsJSON = json.RawMessage(`[
  {
    "name": "list_metrics",
    "description": "List every metric homepoll collects, with its module, name, description, unit, value type and poll interval. Call this first to find the exact metric name to pass to query_metric.",
    "inputSchema": {"type": "object", "properties": {}}
  },
  {
    "name": "query_metric",
    "description": "Read stored readings of one metric, newest first. With no time window it returns the current value; pass from/to and a larger limit to read history.",
    "inputSchema": {
      "type": "object",
      "properties": {
        "name": {"type": "string", "description": "Metric name as reported by list_metrics, for example boiler_temperature."},
        "module": {"type": "string", "description": "Module the metric belongs to, for example HEATER or POWER. Only needed when the same name exists in more than one module."},
        "from": {"type": "string", "description": "Optional inclusive start of the time window, RFC 3339, for example 2026-09-14T00:00:00Z."},
        "to": {"type": "string", "description": "Optional inclusive end of the time window, RFC 3339."},
        "limit": {"type": "integer", "description": "Maximum number of readings, newest first. Defaults to 1 (the current value); capped at 500."}
      },
      "required": ["name"]
    }
  }
]`)

// callTool runs one tool. Bad arguments come back as isError content so the
// model can correct itself; only a malformed call is a protocol error.
func callTool(ctx context.Context, q *db.Queries, params json.RawMessage) (any, error) {
	var p struct {
		Arguments json.RawMessage `json:"arguments"`
		Name      string          `json:"name"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("tools/call: %w", err)
	}

	var (
		payload any
		err     error
	)
	switch p.Name {
	case "list_metrics":
		payload, err = listMetrics(ctx, q)
	case "query_metric":
		payload, err = queryMetric(ctx, q, p.Arguments)
	default:
		return nil, fmt.Errorf("%w: tool %q", errMethodNotFound, p.Name)
	}
	if err != nil {
		return textResult(err.Error(), true), nil
	}

	body, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode %s result: %w", p.Name, err)
	}
	return textResult(string(body), false), nil
}

// textResult wraps text as the single content block of a tools/call result.
func textResult(text string, isError bool) map[string]any {
	return map[string]any{
		"content": []map[string]any{{"type": "text", "text": text}},
		"isError": isError,
	}
}

// metric is one entry of the list_metrics result.
type metric struct {
	Module          string `json:"module"`
	Name            string `json:"name"`
	Description     string `json:"description,omitempty"`
	Unit            string `json:"unit,omitempty"`
	Type            string `json:"type"`
	PollIntervalSec int32  `json:"poll_interval_seconds"`
}

// listMetrics describes every configured metric, ordered by the query.
func listMetrics(ctx context.Context, q *db.Queries) (any, error) {
	configs, err := q.ListConfigurations(ctx)
	if err != nil {
		return nil, fmt.Errorf("list configurations: %w", err)
	}
	metrics := make([]metric, 0, len(configs))
	for _, c := range configs {
		metrics = append(metrics, metric{
			Module:          c.Module,
			Name:            c.Name,
			Description:     c.Description.String,
			Unit:            c.Unit.String,
			Type:            c.Type,
			PollIntervalSec: c.PollInterval,
		})
	}
	return map[string]any{"metrics": metrics}, nil
}

// queryArgs are the arguments of query_metric, as the client sends them.
type queryArgs struct {
	Name   string `json:"name"`
	Module string `json:"module"`
	From   string `json:"from"`
	To     string `json:"to"`
	Limit  int32  `json:"limit"`
}

// reading is one query_metric entry; Value is a number, a string, or null.
type reading struct {
	RecordedAt time.Time `json:"recorded_at"`
	Value      any       `json:"value"`
}

// queryResult is the query_metric payload: the resolved metric plus readings.
type queryResult struct {
	Module   string    `json:"module"`
	Name     string    `json:"name"`
	Unit     string    `json:"unit,omitempty"`
	Type     string    `json:"type"`
	Readings []reading `json:"readings"`
}

// queryMetric resolves the named metric and returns its readings, newest first.
func queryMetric(ctx context.Context, q *db.Queries, args json.RawMessage) (any, error) {
	var a queryArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return nil, fmt.Errorf("could not read the arguments: %v", err)
	}
	from, err := parseBound(a.From, "from")
	if err != nil {
		return nil, err
	}
	to, err := parseBound(a.To, "to")
	if err != nil {
		return nil, err
	}

	configs, err := q.ListConfigurations(ctx)
	if err != nil {
		return nil, fmt.Errorf("list configurations: %w", err)
	}
	cfg, err := findConfiguration(configs, a.Name, a.Module)
	if err != nil {
		return nil, err
	}

	rows, err := q.ListReadings(ctx, db.ListReadingsParams{
		ConfigurationID: cfg.ID,
		From:            from,
		To:              to,
		Limit:           clampLimit(a.Limit),
	})
	if err != nil {
		return nil, fmt.Errorf("list readings: %w", err)
	}

	readings := make([]reading, 0, len(rows))
	for _, r := range rows {
		readings = append(readings, reading{RecordedAt: r.RecordedAt, Value: value(r)})
	}
	return queryResult{
		Module:   cfg.Module,
		Name:     cfg.Name,
		Unit:     cfg.Unit.String,
		Type:     cfg.Type,
		Readings: readings,
	}, nil
}

// value unwraps whichever of the two nullable columns the metric uses.
func value(r db.Reading) any {
	switch {
	case r.ValueNum.Valid:
		return r.ValueNum.Float64
	case r.ValueText.Valid:
		return r.ValueText.String
	default:
		return nil
	}
}

// parseBound parses an optional RFC 3339 bound; empty means unbounded.
func parseBound(s, field string) (sql.NullTime, error) {
	if s == "" {
		return sql.NullTime{}, nil
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return sql.NullTime{}, fmt.Errorf(
			"%q is not an RFC 3339 timestamp, for example 2026-09-14T00:00:00Z: %s", field, s,
		)
	}
	return sql.NullTime{Time: t, Valid: true}, nil
}

// clampLimit keeps limit within 1..maxLimit, defaulting to defaultLimit.
func clampLimit(limit int32) int32 {
	switch {
	case limit <= 0:
		return defaultLimit
	case limit > maxLimit:
		return maxLimit
	default:
		return limit
	}
}

// findConfiguration resolves a metric by name and optional module. Names are
// unique per module only, so an ambiguous name is an error listing the modules.
func findConfiguration(
	configs []db.Configuration,
	name, module string,
) (db.Configuration, error) {
	var matches []db.Configuration
	for _, c := range configs {
		if !strings.EqualFold(c.Name, name) {
			continue
		}
		if module != "" && !strings.EqualFold(c.Module, module) {
			continue
		}
		matches = append(matches, c)
	}

	switch len(matches) {
	case 1:
		return matches[0], nil
	case 0:
		return db.Configuration{}, fmt.Errorf(
			"no metric named %q; call list_metrics for the available metrics", name,
		)
	default:
		modules := make([]string, 0, len(matches))
		for _, m := range matches {
			modules = append(modules, m.Module)
		}
		return db.Configuration{}, fmt.Errorf(
			"metric %q exists in modules %s; pass module to choose one",
			name, strings.Join(modules, ", "),
		)
	}
}
