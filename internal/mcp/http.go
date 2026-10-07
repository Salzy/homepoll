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

// maxRequestBytes caps a request body against hostile clients.
const maxRequestBytes = 1 << 20

// Handler serves the Streamable HTTP transport: POST only, answered inline. The
// server is stateless and never speaks first, so no sessions and GET is 405.
//
// Parameters:
//   - q: the read-only data access used by the tools.
//   - token: the bearer token every request must present; must not be empty.
//
// Returns the handler, ready to mount on an http.Server.
func Handler(q *db.Queries, token string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST "+Path, func(w http.ResponseWriter, r *http.Request) {
		// Browsers send Origin, native clients do not: DNS-rebinding defence.
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
			// Notification: the transport asks for 202.
			w.WriteHeader(http.StatusAccepted)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(answer(r.Context(), q, req))
	})
	return mux
}

// authorized checks the bearer token in constant time.
func authorized(header, token string) bool {
	got, ok := strings.CutPrefix(header, "Bearer ")
	return ok && subtle.ConstantTimeCompare([]byte(got), []byte(token)) == 1
}
