package verifier

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHTTP(t *testing.T) {
	s, _ := fakeService(t, twoContracts(), nil)
	c, err := NewCompilers(CompilersConfig{BundledDir: bundledDir(t, "x"), Logger: quiet})
	if err != nil {
		t.Fatal(err)
	}
	s.Compilers = c
	h := s.Handler(4096)

	do := func(method, path, body string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(method, path, strings.NewReader(body)))
		return w
	}

	if w := do("GET", "/api/health", ""); w.Code != 200 || strings.TrimSpace(w.Body.String()) != `{"status":"ok"}` {
		t.Fatalf("health %d %s", w.Code, w.Body)
	}
	if w := do("GET", "/compilers", ""); w.Code != 200 || strings.TrimSpace(w.Body.String()) != `{"versions":["`+v828+`"]}` {
		t.Fatalf("compilers %d %s", w.Code, w.Body)
	}

	body, _ := json.Marshal(req("", runB, nil))
	w := do("POST", "/verify", string(body))
	var r Response
	if w.Code != 200 || w.Header().Get("Content-Type") != "application/json" || json.Unmarshal(w.Body.Bytes(), &r) != nil || r.Status != "success" {
		t.Fatalf("verify %d %s", w.Code, w.Body)
	}

	// A verification that fails is still a 200.
	body, _ = json.Marshal(req("", hx(codeC), nil))
	if w := do("POST", "/verify", string(body)); w.Code != 200 || !strings.Contains(w.Body.String(), `"status":"failure"`) {
		t.Fatalf("failed verify %d %s", w.Code, w.Body)
	}

	if w := do("POST", "/verify", "{not json"); w.Code != 400 || !strings.Contains(w.Body.String(), `"message"`) {
		t.Fatalf("bad json %d %s", w.Code, w.Body)
	}
	if w := do("POST", "/verify", `{"standard_json":"`+strings.Repeat("a", 5000)+`"}`); w.Code != 413 || !strings.Contains(w.Body.String(), "MAX_INPUT_BYTES") {
		t.Fatalf("too large %d %s", w.Code, w.Body)
	}
	if w := do("GET", "/verify", ""); w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET /verify %d", w.Code)
	}
	if w := do("GET", "/nope", ""); w.Code != 404 {
		t.Fatalf("unknown path %d", w.Code)
	}
}
