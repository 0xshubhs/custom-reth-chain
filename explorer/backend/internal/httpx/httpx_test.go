package httpx

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func testHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v2/thing", func(w http.ResponseWriter, r *http.Request) {
		WriteJSON(w, r, http.StatusOK, map[string]string{"method": r.Method})
	})
	mux.HandleFunc("POST /api/v2/thing/call", func(w http.ResponseWriter, r *http.Request) {
		WriteJSON(w, r, http.StatusOK, map[string]string{"ok": "yes"})
	})
	mux.HandleFunc("GET /api/v2/stream", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("method=" + r.Method))
	})
	return WithNosniff(MethodCompat(mux, mux))
}

func do(h http.Handler, method, path string, hdr map[string]string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, nil)
	for k, v := range hdr {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestMethodCompat(t *testing.T) {
	h := testHandler()

	// HEAD is routed as GET (the handler sees GET); net/http drops the body.
	w := do(h, http.MethodHead, "/api/v2/thing", nil)
	if w.Code != http.StatusOK || w.Body.String() != "{\"method\":\"GET\"}\n" {
		t.Errorf("HEAD routed: %d %q", w.Code, w.Body.String())
	}
	srv := httptest.NewServer(h)
	defer srv.Close()
	resp, err := http.Head(srv.URL + "/api/v2/thing")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || len(body) != 0 || resp.Header.Get("Content-Type") != "application/json" {
		t.Errorf("HEAD over the wire: %d %q %v", resp.StatusCode, body, resp.Header)
	}
	w = do(h, http.MethodOptions, "/api/v2/thing", nil)
	if w.Code != http.StatusNoContent || w.Header().Get("Allow") != "GET, HEAD, OPTIONS" {
		t.Errorf("OPTIONS: %d Allow=%q", w.Code, w.Header().Get("Allow"))
	}
	w = do(h, http.MethodOptions, "/api/v2/thing/call", nil)
	if w.Code != http.StatusNoContent || w.Header().Get("Allow") != "POST, OPTIONS" {
		t.Errorf("OPTIONS call: %d Allow=%q", w.Code, w.Header().Get("Allow"))
	}
	// A preflight (Origin set) is not answered with 204: no CORS here.
	w = do(h, http.MethodOptions, "/api/v2/thing", map[string]string{"Origin": "https://evil.example"})
	if w.Code == http.StatusNoContent || w.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Errorf("preflight: %d %v", w.Code, w.Header())
	}
	w = do(h, http.MethodGet, "/nope", nil)
	if w.Code != http.StatusNotFound || w.Body.String() != "{\"message\":\"not found\"}\n" {
		t.Errorf("404: %d %q", w.Code, w.Body.String())
	}
	w = do(h, http.MethodDelete, "/api/v2/thing", nil)
	if w.Code != http.StatusMethodNotAllowed || w.Header().Get("Allow") != "GET, HEAD, OPTIONS" {
		t.Errorf("405: %d Allow=%q", w.Code, w.Header().Get("Allow"))
	}
	// HEAD on a stream is passed through unchanged (never turned into a GET).
	w = do(h, http.MethodHead, "/api/v2/stream", nil)
	if w.Code != http.StatusOK {
		t.Errorf("HEAD stream: %d", w.Code)
	}
	// nosniff on everything, errors included.
	for _, p := range []string{"/api/v2/thing", "/nope"} {
		if got := do(h, http.MethodGet, p, nil).Header().Get("X-Content-Type-Options"); got != "nosniff" {
			t.Errorf("%s: nosniff=%q", p, got)
		}
	}
}

func TestClientIP(t *testing.T) {
	req := func(remote string, hdr map[string]string) *http.Request {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.RemoteAddr = remote
		for k, v := range hdr {
			r.Header.Set(k, v)
		}
		return r
	}
	cases := []struct {
		remote  string
		hdr     map[string]string
		trusted string
		want    string
	}{
		{"203.0.113.9:5000", map[string]string{"X-Real-IP": "1.2.3.4"}, "127.0.0.1", "203.0.113.9"}, // untrusted peer: headers ignored
		{"127.0.0.1:5000", map[string]string{"X-Real-IP": "1.2.3.4"}, "", "1.2.3.4"},                // loopback always trusted
		{"172.18.0.5:5000", map[string]string{"CF-Connecting-IP": "9.9.9.9", "X-Real-IP": "1.1.1.1"}, "172.18.0.0/16", "9.9.9.9"},
		{"172.18.0.5:5000", map[string]string{"X-Forwarded-For": "5.6.7.8, 10.0.0.1"}, "10.0.0.1, 172.18.0.5", "5.6.7.8"},
		{"172.18.0.5:5000", map[string]string{"X-Real-IP": "garbage"}, "172.18.0.5", "172.18.0.5"},
	}
	for i, c := range cases {
		if got := ClientIP(req(c.remote, c.hdr), c.trusted); got != c.want {
			t.Errorf("case %d: %s, want %s", i, got, c.want)
		}
	}
}

func TestHealthDetailOnlyInternal(t *testing.T) {
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		WriteHealth(w, r, http.StatusOK, "ok", map[string]any{"last_block": 5})
	})
	r := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	r.RemoteAddr = "127.0.0.1:1234"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Body.String() != "{\"last_block\":5,\"status\":\"ok\"}\n" {
		t.Errorf("internal: %s", w.Body.String())
	}
	r.Header.Set("X-Forwarded-For", "1.2.3.4")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Body.String() != "{\"status\":\"ok\"}\n" {
		t.Errorf("proxied: %s", w.Body.String())
	}
}
