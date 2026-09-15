package mcp

import (
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"strings"

	"homepoll/internal/db"
)

// Path is the single endpoint of the Streamable HTTP transport.
const Path = "/mcp"

// maxRequestBytes caps a request body. Every message this server accepts is a
// few hundred bytes of JSON-RPC, so the limit only exists to keep a hostile
// body from being read into memory.
const maxRequestBytes = 1 << 20

// Handler returns the HTTP handler for the MCP Streamable HTTP transport: one
// endpoint that takes a JSON-RPC message by POST and answers it inline.
//
// The optional half of the transport is left out, because this server holds no
// state between requests and never speaks first: there is no Mcp-Session-Id,
// and a GET (which would open the stream of server-initiated messages) is
// refused by the router with 405.
//
// Parameters:
//   - q: the read-only data access used by the tools.
//   - token: the bearer token every request must present; must not be empty.
//
// Returns the handler, ready to mount on an http.Server.
func Handler(q *db.Queries, token string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST "+Path, func(w http.ResponseWriter, r *http.Request) {
		// A browser always sends Origin and a native MCP client never does, so
		// refusing any request that carries one is the DNS-rebinding defence
		// the transport asks for - without an allowlist nobody would fill in.
		if r.Header.Get("Origin") != "" {
			http.Error(w, "browser origins are not allowed", http.StatusForbidden)
			return
		}
		if !authorized(r.Header.Get("Authorization"), token) {
			w.Header().Set("WWW-Authenticate", "Bearer")
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		var req request
		body := http.MaxBytesReader(w, r.Body, maxRequestBytes)
		if err := json.NewDecoder(body).Decode(&req); err != nil {
			http.Error(w, "malformed JSON-RPC request", http.StatusBadRequest)
			return
		}
		if len(req.ID) == 0 {
			// A notification. There is nothing to answer, and the transport
			// asks for 202 rather than an empty response body.
			w.WriteHeader(http.StatusAccepted)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(answer(r.Context(), q, req))
	})
	return mux
}

// authorized reports whether the Authorization header carries the expected
// bearer token. The comparison is constant time so that a wrong token leaks
// nothing about how much of it was right.
func authorized(header, token string) bool {
	got, ok := strings.CutPrefix(header, "Bearer ")
	return ok && subtle.ConstantTimeCompare([]byte(got), []byte(token)) == 1
}
