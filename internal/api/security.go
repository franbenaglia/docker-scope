package api

import (
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
)

// Security holds the request checks applied to every request (SPEC "Seguridad").
type Security struct {
	// ListenPort is the port the agent serves on, used to build same-origin values.
	ListenPort string
	// ExtraHosts are additional loopback host names accepted in Host (e.g. the
	// configured listen address 127.0.0.2).
	ExtraHosts []string
	// AllowedOrigins are extra origins accepted for mutations (dev server).
	AllowedOrigins []string
}

// Middleware rejects requests that could come from other sites:
//   - Host must be a loopback name (mitigates DNS rebinding: a hostile domain
//     resolving to 127.0.0.1 still sends its own name in Host).
//   - /api requests marked by the browser as cross-site are rejected.
//   - Mutations (anything but GET/HEAD) need a same-origin or allowed Origin
//     and a JSON body type (no simple-form CSRF).
//
// No CORS headers are ever sent, so browsers block cross-origin reads.
func (sec Security) Middleware(next http.Handler) http.Handler {
	hosts := map[string]bool{"localhost": true, "127.0.0.1": true, "::1": true}
	for _, h := range sec.ExtraHosts {
		hosts[strings.ToLower(h)] = true
	}
	origins := map[string]bool{}
	for h := range hosts {
		origins["http://"+net.JoinHostPort(h, sec.ListenPort)] = true
	}
	for _, o := range sec.AllowedOrigins {
		origins[strings.TrimSuffix(strings.ToLower(o), "/")] = true
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		setSecurityHeaders(w.Header())
		if !hostAllowed(r.Host, hosts) {
			writeError(w, http.StatusForbidden, "forbidden_host",
				"Host no permitido: acceda por http://127.0.0.1:"+sec.ListenPort)
			return
		}
		isAPI := strings.HasPrefix(r.URL.Path, "/api/")
		if isAPI && r.Header.Get("Sec-Fetch-Site") == "cross-site" {
			writeError(w, http.StatusForbidden, "forbidden_origin", "petición desde otro sitio")
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			if !originAllowed(r, origins) {
				writeError(w, http.StatusForbidden, "forbidden_origin", "origen no permitido")
				return
			}
			if isAPI && r.Method != http.MethodOptions && !isJSON(r.Header.Get("Content-Type")) {
				writeError(w, http.StatusUnsupportedMediaType, "unsupported_media_type",
					"las modificaciones requieren Content-Type: application/json")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func hostAllowed(host string, allowed map[string]bool) bool {
	h := host
	if hh, _, err := net.SplitHostPort(host); err == nil {
		h = hh
	}
	h = strings.ToLower(strings.Trim(h, "[]"))
	if allowed[h] {
		return true
	}
	addr, err := netip.ParseAddr(h)
	return err == nil && addr.IsLoopback()
}

// originAllowed requires an Origin from the allowed set. Without Origin, the
// fetch metadata must say same-origin; requests with neither (non-browser
// clients such as curl) are accepted, since they cannot be forged by a page.
func originAllowed(r *http.Request, allowed map[string]bool) bool {
	o := r.Header.Get("Origin")
	if o == "" {
		site := r.Header.Get("Sec-Fetch-Site")
		return site == "" || site == "same-origin" || site == "none"
	}
	if o == "null" {
		return false
	}
	u, err := url.Parse(o)
	if err != nil {
		return false
	}
	return allowed[strings.ToLower(u.Scheme+"://"+u.Host)]
}

func isJSON(ct string) bool {
	mt, _, _ := strings.Cut(ct, ";")
	return strings.EqualFold(strings.TrimSpace(mt), "application/json")
}

func setSecurityHeaders(h http.Header) {
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("X-Frame-Options", "DENY")
	h.Set("Cross-Origin-Opener-Policy", "same-origin")
	h.Set("Cross-Origin-Resource-Policy", "same-origin")
	// Everything is served by the agent itself (no CDN). Svelte may set style
	// attributes, hence 'unsafe-inline' for styles only.
	h.Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; "+
		"img-src 'self' data:; connect-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'; form-action 'none'")
}
