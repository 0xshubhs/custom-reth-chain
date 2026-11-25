package verifier

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
)

// Handler serves the verifier's HTTP interface:
//
//	POST /verify      Request -> Response (200 whether or not it verified)
//	GET  /compilers   {"versions": [...]}, newest first
//	GET  /api/health  {"status":"ok"}
//
// Only a body that is not JSON (400) or is larger than maxInput (413) gets a
// non-200 answer, always {"message": "..."}. Everything about the submission
// itself, compile errors and unknown compilers included, is a 200 failure
// with a message the API can show as it is.
func (s *Service) Handler(maxInput int64) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /verify", func(w http.ResponseWriter, r *http.Request) {
		var req Request
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxInput))
		if err := dec.Decode(&req); err != nil {
			var tooBig *http.MaxBytesError
			if errors.As(err, &tooBig) {
				writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{
					"message": fmt.Sprintf("request larger than %d bytes (MAX_INPUT_BYTES)", maxInput),
				})
				return
			}
			writeJSON(w, http.StatusBadRequest, map[string]string{"message": "request body is not valid JSON: " + err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, s.Verify(r.Context(), req))
	})
	mux.HandleFunc("GET /compilers", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string][]string{"versions": s.Compilers.Versions(r.Context())})
	})
	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	return mux
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		status, b = http.StatusInternalServerError, []byte(`{"message":"encoding the answer failed"}`)
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = w.Write(b)
}
