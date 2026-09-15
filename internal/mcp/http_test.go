package mcp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"homepoll/internal/db"
)

const testToken = "s3cr3t"

// initializeBody is answered without touching the database, so the handler can
// be exercised with a nil handle.
const initializeBody = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`

// send runs one request against the handler and returns the recorded response.
// A nil headers map means "no headers at all", which is how the unauthenticated
// cases are expressed.
func send(method, body string, headers map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, Path, strings.NewReader(body))
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	Handler(db.New(nil), testToken).ServeHTTP(w, req)
	return w
}

// authorized is the header set a well-behaved native client sends.
func bearer() map[string]string {
	return map[string]string{"Authorization": "Bearer " + testToken}
}

func TestHandlerAuth(t *testing.T) {
	t.Run("should refuse a request with no Authorization header", func(t *testing.T) {
		w := send(http.MethodPost, initializeBody, nil)
		if w.Code != http.StatusUnauthorized {
			t.Errorf("status = %d, want %d", w.Code, http.StatusUnauthorized)
		}
		if got := w.Header().Get("WWW-Authenticate"); got != "Bearer" {
			t.Errorf("WWW-Authenticate = %q, want Bearer", got)
		}
	})

	t.Run("should refuse a wrong token", func(t *testing.T) {
		w := send(http.MethodPost, initializeBody, map[string]string{
			"Authorization": "Bearer " + testToken + "x",
		})
		if w.Code != http.StatusUnauthorized {
			t.Errorf("status = %d, want %d", w.Code, http.StatusUnauthorized)
		}
	})

	t.Run("should refuse a token sent without the Bearer scheme", func(t *testing.T) {
		w := send(http.MethodPost, initializeBody, map[string]string{"Authorization": testToken})
		if w.Code != http.StatusUnauthorized {
			t.Errorf("status = %d, want %d", w.Code, http.StatusUnauthorized)
		}
	})

	t.Run("should refuse a browser origin even with a valid token", func(t *testing.T) {
		headers := bearer()
		headers["Origin"] = "https://evil.example"
		w := send(http.MethodPost, initializeBody, headers)
		if w.Code != http.StatusForbidden {
			t.Errorf("status = %d, want %d (DNS-rebinding defence)", w.Code, http.StatusForbidden)
		}
	})
}

func TestHandlerRequests(t *testing.T) {
	t.Run("should answer an authenticated request with the JSON-RPC result", func(t *testing.T) {
		w := send(http.MethodPost, initializeBody, bearer())
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
		}
		if ct := w.Header().Get("Content-Type"); ct != "application/json" {
			t.Errorf("Content-Type = %q, want application/json", ct)
		}
		var resp struct {
			JSONRPC string         `json:"jsonrpc"`
			Result  map[string]any `json:"result"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		if resp.JSONRPC != "2.0" {
			t.Errorf("jsonrpc = %q, want 2.0", resp.JSONRPC)
		}
		if resp.Result["protocolVersion"] != defaultProtocolVersion {
			t.Errorf("protocolVersion = %v, want %s",
				resp.Result["protocolVersion"], defaultProtocolVersion)
		}
	})

	t.Run("should accept a notification with 202 and an empty body", func(t *testing.T) {
		w := send(
			http.MethodPost,
			`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
			bearer(),
		)
		if w.Code != http.StatusAccepted {
			t.Errorf("status = %d, want %d", w.Code, http.StatusAccepted)
		}
		if w.Body.Len() != 0 {
			t.Errorf("body = %q, want empty", w.Body.String())
		}
	})

	t.Run("should reject a malformed body", func(t *testing.T) {
		w := send(http.MethodPost, "not json", bearer())
		if w.Code != http.StatusBadRequest {
			t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
		}
	})

	t.Run("should decline GET, which would open a server-initiated stream", func(t *testing.T) {
		w := send(http.MethodGet, "", bearer())
		if w.Code != http.StatusMethodNotAllowed {
			t.Errorf("status = %d, want %d", w.Code, http.StatusMethodNotAllowed)
		}
	})
}
