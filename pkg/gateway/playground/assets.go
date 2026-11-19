// SPDX-License-Identifier: MIT

package playground

import (
	"embed"
	"encoding/json"
	"html"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// uiFS embeds the §27.4 single-page-app bundle. The gateway serves
// every /playground/ asset from this filesystem; there is no separate
// deployment target (§27.2).
//
//go:embed ui
var uiFS embed.FS

// assetFS is the ui/ subtree rooted so a request path of
// /playground/app.js resolves to ui/app.js.
var assetFS = func() fs.FS {
	sub, err := fs.Sub(uiFS, "ui")
	if err != nil {
		panic("playground: embedded ui subtree missing: " + err.Error())
	}
	return sub
}()

// handleAsset serves a §27.4 SPA asset from the embedded filesystem.
// It applies the §27.7 cache headers: hashed bundles are immutable
// and cacheable for a year; index.html is no-store so a new release
// propagates immediately. An unknown path falls back to index.html so
// the client-side router can resolve it.
func (h *Handler) handleAsset(w http.ResponseWriter, r *http.Request) {
	rel := strings.TrimPrefix(r.URL.Path, "/playground")
	rel = strings.TrimPrefix(rel, "/")
	if rel == "" || rel == "index.html" {
		h.serveIndex(w)
		return
	}
	clean := path.Clean(rel)
	if clean == "." || strings.HasPrefix(clean, "..") {
		h.serveIndex(w)
		return
	}
	data, err := fs.ReadFile(assetFS, clean)
	if err != nil {
		// SPA fallback: an unknown path is a client-side route.
		h.serveIndex(w)
		return
	}
	w.Header().Set("Content-Type", contentType(clean))
	// §27.7: hashed *.js and *.css bundles are immutable.
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

// serveIndex serves index.html with the §27.7 no-store cache header
// and increments the §27.8 page-views counter.
func (h *Handler) serveIndex(w http.ResponseWriter) {
	data, err := fs.ReadFile(assetFS, "index.html")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR",
			"the playground index asset is missing from the bundle", nil)
		return
	}
	h.metrics.pageView(h.cfg.AuthMode)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

// uiConfig is the §27.4 / §27.9 server-sourced configuration the SPA
// fetches at GET /playground/config.json on load. The dev-mode and
// apiKey-mode banner text is emitted by the gateway so swapping the
// embedded bundle cannot suppress it (§27.9).
type uiConfig struct {
	AuthMode        string   `json:"authMode"`
	AllowedRuntimes []string `json:"allowedRuntimes"`
	MaxSessionMin   int      `json:"maxSessionMinutes"`
	Banner          string   `json:"banner,omitempty"`
	BannerSeverity  string   `json:"bannerSeverity,omitempty"`
	WSPath          string   `json:"wsPath"`
}

// handleConfigJSON serves GET /playground/config.json: the
// server-sourced SPA configuration. It carries the §27.9 dev-mode and
// apiKey-mode banner text so the persistent banner cannot be removed
// by patching the asset bundle.
func (h *Handler) handleConfigJSON(w http.ResponseWriter, _ *http.Request) {
	cfg := uiConfig{
		AuthMode:        string(h.cfg.AuthMode),
		AllowedRuntimes: h.cfg.AllowedRuntimes,
		MaxSessionMin:   h.cfg.MaxSessionMinutes,
		WSPath:          "/mcp/v1/ws",
	}
	switch h.cfg.AuthMode {
	case AuthModeDev:
		cfg.Banner = "DEV MODE — NOT FOR PRODUCTION"
		cfg.BannerSeverity = "danger"
	case AuthModeAPIKey:
		cfg.Banner = "API KEY MODE — paste only operator-issued tokens"
		cfg.BannerSeverity = "warning"
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(cfg)
}

// contentType maps a bundle file extension to its MIME type. The
// gateway sets the type explicitly so X-Content-Type-Options: nosniff
// does not block a correctly-typed asset.
func contentType(name string) string {
	switch {
	case strings.HasSuffix(name, ".html"):
		return "text/html; charset=utf-8"
	case strings.HasSuffix(name, ".js"):
		return "text/javascript; charset=utf-8"
	case strings.HasSuffix(name, ".css"):
		return "text/css; charset=utf-8"
	case strings.HasSuffix(name, ".json"):
		return "application/json"
	case strings.HasSuffix(name, ".svg"):
		return "image/svg+xml"
	case strings.HasSuffix(name, ".ico"):
		return "image/x-icon"
	case strings.HasSuffix(name, ".png"):
		return "image/png"
	default:
		return "application/octet-stream"
	}
}

// securityHeaders wraps inner with the §27.7 playground security
// headers. The Content-Security-Policy is applied here, on the
// /playground/* and /v1/playground/token routes only; the rest of
// the gateway is unaffected.
func (h *Handler) securityHeaders(inner http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", h.contentSecurityPolicy())
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "same-origin")
		inner.ServeHTTP(w, r)
	})
}

// errorPageHTML renders the §27.3.1 playground OIDC error page. The
// page surfaces the error code the callback redirect carried. The
// code is HTML-escaped because it is a query parameter. The page is
// CSP-compatible: no inline script, styles only.
func errorPageHTML(code string) []byte {
	if code == "" {
		code = "unknown_error"
	}
	page := `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<title>Playground sign-in error</title>
<style>
body{font-family:Helvetica,Arial,sans-serif;background:#fffaf0;color:#1f2933;margin:0;padding:48px}
.card{max-width:520px;margin:0 auto;background:#fff;border:2px solid #c84a1d;border-radius:10px;padding:28px}
h1{font-size:20px;margin:0 0 12px}
code{background:#fde2d6;padding:2px 6px;border-radius:4px;font-family:'SF Mono',Menlo,Consolas,monospace}
a{color:#b56b1f}
</style>
</head>
<body>
<div class="card">
<h1>Playground sign-in could not complete</h1>
<p>The OIDC sign-in flow returned the error <code>` + html.EscapeString(code) + `</code>.</p>
<p>Retry the sign-in from <a href="/playground/auth/login">the login page</a>. If the error persists, the gateway operator should check the playground auth-mode configuration.</p>
</div>
</body>
</html>
`
	return []byte(page)
}

// contentSecurityPolicy returns the §27.7 Content-Security-Policy
// header value. connect-src includes the gateway host so the SPA can
// open the MCP WebSocket; object-src and media-src are 'none'
// explicitly per §27.7.
func (h *Handler) contentSecurityPolicy() string {
	connectSrc := "connect-src 'self'"
	if h.cfg.GatewayHost != "" {
		connectSrc += " wss://" + h.cfg.GatewayHost
	}
	directives := []string{
		"default-src 'self'",
		"script-src 'self'",
		"style-src 'self' 'unsafe-inline'",
		connectSrc,
		"img-src 'self' data:",
		"object-src 'none'",
		"media-src 'none'",
		"frame-ancestors 'none'",
		"base-uri 'self'",
		"form-action 'self'",
	}
	return strings.Join(directives, "; ")
}
