// SPDX-License-Identifier: MIT

package mcp

import (
	"net/http"
	"strings"
)

// §27.3.1 line 142 — the MCP WebSocket bearer carrier. A browser cannot
// set an `Authorization` header on a WebSocket upgrade, so the playground
// sends the bearer through the `Sec-WebSocket-Protocol` sub-protocol
// header as `lenny.mcp.v1, lenny.bearer.<bearerToken>`. The gateway must
// treat the `lenny.bearer.*` entry as a credential: it is not logged, not
// emitted in access logs, and stripped before audit-event emission. This
// middleware performs that strip-and-promote at the outermost boundary so
// no downstream middleware (access logging, correlation, audit) ever
// observes the token in a header.

// bearerSubprotocolPrefix is the §27.3.1 line 142 credential carrier
// prefix inside the Sec-WebSocket-Protocol header.
const bearerSubprotocolPrefix = "lenny.bearer."

// WebSocketBearerCarrier returns a middleware that promotes the §27.3.1
// `Sec-WebSocket-Protocol: lenny.bearer.<token>` carrier to a standard
// `Authorization: Bearer <token>` header and strips the credential entry
// from the sub-protocol header before any downstream handler runs.
//
// The middleware is a no-op for a request whose Sec-WebSocket-Protocol
// header carries no `lenny.bearer.` entry, so it is safe to install
// gateway-wide as the outermost wrapper. It must run before any access
// log or audit middleware so the credential is never recorded, and
// before the auth middleware so the promoted Authorization header is
// validated on the standard bearer path (§27.3.1 line 142 states the
// gateway validates the bearer "exactly as it would for any non-playground
// MCP client"). When the caller already presented an Authorization header
// (the non-browser upgrade path), the carrier does not overwrite it.
func WebSocketBearerCarrier(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header := r.Header.Get("Sec-WebSocket-Protocol")
		if header == "" || !strings.Contains(header, bearerSubprotocolPrefix) {
			next.ServeHTTP(w, r)
			return
		}
		token, sanitized := splitBearerCarrier(header)
		// Strip the credential from the request header first so the
		// §27.3.1 line 142 "not logged, redacted in audit traces"
		// obligation holds regardless of what runs downstream.
		if sanitized == "" {
			r.Header.Del("Sec-WebSocket-Protocol")
		} else {
			r.Header.Set("Sec-WebSocket-Protocol", sanitized)
		}
		// Promote the carrier to the standard bearer path. A pre-existing
		// Authorization header wins (the carrier is the browser fallback
		// for callers that cannot set one).
		if token != "" && r.Header.Get("Authorization") == "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		next.ServeHTTP(w, r)
	})
}

// splitBearerCarrier parses a Sec-WebSocket-Protocol header value, returns
// the bearer token carried by the first `lenny.bearer.<token>` entry, and
// returns the sanitized header with every `lenny.bearer.*` entry removed
// (the remaining offered sub-protocols, e.g. `lenny.mcp.v1`, joined by
// ", "). Sub-protocol tokens cannot contain commas per RFC 6455, and a
// gateway JWT is comma-free, so splitting on comma is unambiguous.
func splitBearerCarrier(header string) (token, sanitized string) {
	parts := strings.Split(header, ",")
	kept := make([]string, 0, len(parts))
	for _, p := range parts {
		entry := strings.TrimSpace(p)
		if entry == "" {
			continue
		}
		if strings.HasPrefix(entry, bearerSubprotocolPrefix) {
			if token == "" {
				token = strings.TrimPrefix(entry, bearerSubprotocolPrefix)
			}
			continue
		}
		kept = append(kept, entry)
	}
	return token, strings.Join(kept, ", ")
}
