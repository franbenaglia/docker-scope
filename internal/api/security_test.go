package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func secHandler() http.Handler {
	ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })
	return Security{ListenPort: "8080", AllowedOrigins: []string{"http://127.0.0.1:5173"}}.Middleware(ok)
}

func TestHostValidation(t *testing.T) {
	cases := map[string]int{
		"127.0.0.1:8080":    http.StatusNoContent,
		"localhost:8080":    http.StatusNoContent,
		"LOCALHOST:8080":    http.StatusNoContent,
		"[::1]:8080":        http.StatusNoContent,
		"127.0.0.2:8080":    http.StatusNoContent,
		"localhost":         http.StatusNoContent,
		"evil.example:8080": http.StatusForbidden, // DNS rebinding
		"192.168.1.5:8080":  http.StatusForbidden,
		"127.0.0.1.nip.io":  http.StatusForbidden,
		"":                  http.StatusForbidden,
	}
	for host, want := range cases {
		req := httptest.NewRequest("GET", "/api/summary", nil)
		req.Host = host
		rec := httptest.NewRecorder()
		secHandler().ServeHTTP(rec, req)
		if rec.Code != want {
			t.Errorf("Host %q: %d, want %d", host, rec.Code, want)
		}
	}
}

func TestMutationOriginChecks(t *testing.T) {
	type tc struct {
		origin, site, ctype string
		want                int
	}
	cases := []tc{
		{"http://127.0.0.1:8080", "same-origin", "application/json", http.StatusNoContent},
		{"http://localhost:8080", "", "application/json; charset=utf-8", http.StatusNoContent},
		{"http://127.0.0.1:5173", "same-site", "application/json", http.StatusNoContent}, // dev server
		{"", "", "application/json", http.StatusNoContent},                               // curl
		{"", "same-origin", "application/json", http.StatusNoContent},
		{"https://evil.example", "cross-site", "application/json", http.StatusForbidden},
		{"https://evil.example", "", "application/json", http.StatusForbidden},
		{"http://127.0.0.1:9999", "same-site", "application/json", http.StatusForbidden}, // other local app
		{"null", "", "application/json", http.StatusForbidden},
		{"", "cross-site", "application/json", http.StatusForbidden},
		{"http://127.0.0.1:8080", "same-origin", "text/plain", http.StatusUnsupportedMediaType},
		{"http://127.0.0.1:8080", "same-origin", "", http.StatusUnsupportedMediaType}, // the panel always sends JSON, even "{}"
		{"http://127.0.0.1:8080", "same-origin", "application/x-www-form-urlencoded", http.StatusUnsupportedMediaType},
	}
	for _, c := range cases {
		req := httptest.NewRequest("POST", "/api/maintenance", strings.NewReader("{}"))
		req.Host = "127.0.0.1:8080"
		if c.origin != "" {
			req.Header.Set("Origin", c.origin)
		}
		if c.site != "" {
			req.Header.Set("Sec-Fetch-Site", c.site)
		}
		if c.ctype != "" {
			req.Header.Set("Content-Type", c.ctype)
		}
		rec := httptest.NewRecorder()
		secHandler().ServeHTTP(rec, req)
		if rec.Code != c.want {
			t.Errorf("%+v: %d, want %d", c, rec.Code, c.want)
		}
	}
}

func TestCrossSiteReadsAndHeaders(t *testing.T) {
	req := httptest.NewRequest("GET", "/api/summary", nil)
	req.Host = "127.0.0.1:8080"
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	rec := httptest.NewRecorder()
	secHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("GET /api cross-site: %d", rec.Code)
	}
	if rec.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Error("nunca se envían cabeceras CORS")
	}
	for _, h := range []string{"Content-Security-Policy", "X-Content-Type-Options", "X-Frame-Options"} {
		if rec.Header().Get(h) == "" {
			t.Errorf("falta %s", h)
		}
	}
	// Static assets may be requested cross-site (e.g. favicon) without harm.
	req = httptest.NewRequest("GET", "/", nil)
	req.Host = "127.0.0.1:8080"
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	rec = httptest.NewRecorder()
	secHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Errorf("GET / cross-site: %d", rec.Code)
	}
}
