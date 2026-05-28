// SPDX-License-Identifier: MIT

// Package deprecation hosts the gateway middleware that stamps the
// `X-Lenny-Deprecated-Version` response header onto every REST response
// whose URL path version prefix appears in the operator-configured
// deprecated-version set.
//
// spec: §15.5 item 1 ("The previous version is supported for at least
// 6 months after a new version ships") + docs/api/index.md line 124
// ("The gateway adds an `X-Lenny-Deprecated-Version` header to every
// response"). F-15.5.11.
//
// The middleware is a no-op when the deprecated-version set is empty,
// which is the v1 default: no version is deprecated yet because /v2/
// has not shipped. When the first /v2/ surface lands and /v1/ enters
// its 6-month sunset window, operators add `v1` to the chart-level
// gateway.deprecatedAPIVersions list (rendered as the
// --deprecated-api-versions CLI flag), and the middleware begins
// emitting the header on every /v1/ response without further code
// changes.
package deprecation

import (
	"net/http"
	"strings"
)

// HeaderName is the spec-promised response header name. Lower-case Go
// canonicalization preserves the documented capitalization on the
// wire.
//
// spec: docs/api/index.md line 124. F-15.5.11.
const HeaderName = "X-Lenny-Deprecated-Version"

// PathVersionPrefix derives the API version prefix from a request URL
// path. The function recognizes `/vN`-style version segments at the
// path root (`/v1/...` → `v1`, `/v2/foo` → `v2`) and returns an empty
// string when the path does not begin with a version segment (e.g.
// `/healthz`, `/openapi.json`, `/.well-known/jwks.json`).
//
// The check is intentionally narrow: the spec ties the deprecation
// surface to URL path prefixes (§15.5 item 1), and infra paths
// (`/healthz`, `/metrics`) are unversioned by design.
func PathVersionPrefix(path string) string {
	if path == "" || path[0] != '/' {
		return ""
	}
	tail := path[1:]
	slash := strings.IndexByte(tail, '/')
	var seg string
	if slash < 0 {
		seg = tail
	} else {
		seg = tail[:slash]
	}
	if len(seg) < 2 || seg[0] != 'v' {
		return ""
	}
	for i := 1; i < len(seg); i++ {
		c := seg[i]
		if c < '0' || c > '9' {
			return ""
		}
	}
	return seg
}

// Wrap returns a handler that stamps the X-Lenny-Deprecated-Version
// header onto every response whose request URL begins with a path
// version prefix in deprecated. Empty deprecated leaves the response
// untouched (the no-op v1 default).
//
// The header carries the deprecated version verbatim (e.g. `v1`) so a
// client can route the response through its own
// version-handling logic. Responses for unversioned paths
// (`/healthz`, `/metrics`, `/openapi.json`) never carry the header
// because the deprecation policy is keyed on URL path version
// prefixes.
//
// spec: §15.5 item 1; docs/api/index.md line 124. F-15.5.11.
func Wrap(next http.Handler, deprecated ...string) http.Handler {
	set := map[string]struct{}{}
	for _, v := range deprecated {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		set[v] = struct{}{}
	}
	if len(set) == 0 {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if v := PathVersionPrefix(r.URL.Path); v != "" {
			if _, ok := set[v]; ok {
				w.Header().Set(HeaderName, v)
			}
		}
		next.ServeHTTP(w, r)
	})
}
