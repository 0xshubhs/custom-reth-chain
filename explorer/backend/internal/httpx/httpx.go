// Package httpx holds the HTTP behaviour the explorer's services share:
// nosniff on every response, HEAD served as GET, OPTIONS without Origin
// answered 204 with Allow, minimal health bodies through a proxy, and
// TRUSTED_PROXY_IP client-IP resolution.
package httpx

import (
	"encoding/json"
	"log/slog"
	"net"
	"net/http"
	"runtime/debug"
	"strings"
)

// WriteJSON writes v with status code. HEAD gets headers only.
func WriteJSON(w http.ResponseWriter, r *http.Request, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	if r != nil && r.Method == http.MethodHead {
		return
	}
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}

// Error writes {"message": msg}.
func Error(w http.ResponseWriter, r *http.Request, code int, msg string) {
	WriteJSON(w, r, code, map[string]string{"message": msg})
}

// WithNosniff sends X-Content-Type-Options: nosniff on every response. It is
// set before the handler runs, so a handler that sets it as well still
// produces exactly one header.
func WithNosniff(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		next.ServeHTTP(w, r)
	})
}

// MethodCompat answers two methods a strict router refuses for no good reason:
//
//   - HEAD is served as the GET it stands for (net/http drops the body
//     because the client asked with HEAD). Streams are left alone, so a HEAD
//     never holds one open.
//   - OPTIONS without an Origin header is not a CORS preflight, so it gets 204
//     and an Allow header listing the methods mux has for the path. An OPTIONS
//     with an Origin goes on unchanged (this API sends no CORS headers, so the
//     browser refuses the cross-origin request, which is the point).
//
// A path mux has no route for at all gets a JSON 404, and a path that exists
// for other methods a JSON 405 with Allow.
func MethodCompat(next http.Handler, mux *http.ServeMux) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodHead && !IsStreamPath(r.URL.Path):
			get := r.Clone(r.Context())
			get.Method = http.MethodGet
			serveRouted(w, get, next, mux)
		case r.Method == http.MethodOptions && r.Header.Get("Origin") == "":
			allow := AllowedMethods(mux, r)
			if allow == "" {
				Error(w, r, http.StatusNotFound, "not found")
				return
			}
			w.Header().Set("Allow", allow)
			w.WriteHeader(http.StatusNoContent)
		default:
			serveRouted(w, r, next, mux)
		}
	})
}

func serveRouted(w http.ResponseWriter, r *http.Request, next http.Handler, mux *http.ServeMux) {
	if _, pattern := mux.Handler(r); pattern == "" {
		if allow := AllowedMethods(mux, r); allow != "" {
			w.Header().Set("Allow", allow)
			Error(w, r, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		Error(w, r, http.StatusNotFound, "not found")
		return
	}
	next.ServeHTTP(w, r)
}

// AllowedMethods is the Allow header for r's path: every method some route on
// mux accepts there (HEAD wherever GET is), plus OPTIONS. "" when no route
// matches the path at all.
func AllowedMethods(mux *http.ServeMux, r *http.Request) string {
	var allow []string
	for _, m := range []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		probe := r.WithContext(r.Context())
		probe.Method = m
		if _, pattern := mux.Handler(probe); pattern != "" {
			allow = append(allow, m)
			if m == http.MethodGet {
				allow = append(allow, http.MethodHead)
			}
		}
	}
	if len(allow) == 0 {
		return ""
	}
	return strings.Join(append(allow, http.MethodOptions), ", ")
}

// IsStreamPath matches the long-lived event streams.
func IsStreamPath(path string) bool {
	return strings.HasPrefix(path, "/api/stream") || strings.HasSuffix(path, "/stream")
}

// Recover turns a handler panic into a JSON 500 and a log line.
func Recover(log *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				if v == http.ErrAbortHandler {
					panic(v)
				}
				log.Error("handler panic", "path", r.URL.Path, "panic", v, "stack", string(debug.Stack()))
				Error(w, r, http.StatusInternalServerError, "internal error")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// forwardingHeaders are what Cloudflare and every nginx in front of an engine
// add. A request from the internet always carries at least one of them.
var forwardingHeaders = [...]string{
	"X-Forwarded-For",
	"X-Real-Ip",
	"Cf-Connecting-Ip",
	"True-Client-Ip",
	"Forwarded",
}

// HealthProbeIsInternal reports whether a health request came straight to
// this engine from a loopback or private address, with no proxy on the way.
func HealthProbeIsInternal(r *http.Request) bool {
	for _, h := range forwardingHeaders {
		if r.Header.Get(h) != "" {
			return false
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	ip := net.ParseIP(host)
	return ip != nil && (ip.IsLoopback() || ip.IsPrivate())
}

// WriteHealth answers a health probe with code and {"status": status}. An
// internal probe also gets detail merged in.
func WriteHealth(w http.ResponseWriter, r *http.Request, code int, status string, detail map[string]any) {
	body := map[string]any{"status": status}
	if HealthProbeIsInternal(r) {
		for k, v := range detail {
			body[k] = v
		}
	}
	WriteJSON(w, r, code, body)
}

// ClientIP resolves the real client address. Only a connection from a
// trusted proxy (loopback, or an entry of trustedProxy: comma-separated IPs
// or CIDRs) may name the client through CF-Connecting-IP, X-Real-IP or
// X-Forwarded-For (first entry); anyone else is taken at their socket address.
func ClientIP(r *http.Request, trustedProxy string) string {
	remoteHost, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		remoteHost = r.RemoteAddr
	}
	if !(trustsProxy(remoteHost, trustedProxy) || remoteHost == "127.0.0.1" || remoteHost == "::1") {
		return remoteHost
	}
	if cf := r.Header.Get("CF-Connecting-IP"); cf != "" && net.ParseIP(cf) != nil {
		return cf
	}
	if real := r.Header.Get("X-Real-IP"); real != "" && net.ParseIP(real) != nil {
		return real
	}
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		candidate := xff
		if i := strings.IndexByte(xff, ','); i != -1 {
			candidate = xff[:i]
		}
		candidate = strings.TrimSpace(candidate)
		if net.ParseIP(candidate) != nil {
			return candidate
		}
	}
	return remoteHost
}

func trustsProxy(remoteHost, trusted string) bool {
	if trusted == "" {
		return false
	}
	peer := net.ParseIP(remoteHost)
	for _, entry := range strings.Split(trusted, ",") {
		entry = strings.TrimSpace(entry)
		switch {
		case entry == "":
			continue
		case entry == remoteHost:
			return true
		case peer == nil:
			continue
		}
		if _, subnet, err := net.ParseCIDR(entry); err == nil && subnet.Contains(peer) {
			return true
		}
	}
	return false
}
