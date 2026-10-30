// SPDX-License-Identifier: MIT

package loadctl

import (
	"crypto/subtle"
	"net/http"
	"strings"
)

// AuthConfig holds the bearer-token shared secrets the control plane
// validates incoming requests against. Tokens are split across two
// scopes:
//
//   - OperatorTokens authorise the human-facing API surface that
//     creates runs, lists runs, stops runs, and pins baselines.
//   - RunnerTokens authorise the runner-callback surface
//     (/api/v1/ack, /api/v1/progress, /api/v1/runners/register,
//     /api/v1/runners/{id}/heartbeat).
//
// Both lists are empty by default; an empty list disables auth for
// that scope so the dev/scaffold workflow keeps working without
// extra configuration. Production deployments MUST populate both.
//
// The /metrics endpoint, /healthz, and the embedded UI are always
// public — they carry no run-control surface area.
type AuthConfig struct {
	OperatorTokens []string
	RunnerTokens   []string
}

// authMiddleware applies bearer-token validation to /api/v1/*.
// Routes are classified by URL prefix into operator vs runner scope.
// The implementation uses subtle.ConstantTimeCompare to avoid timing
// leaks on token comparison.
type authMiddleware struct {
	cfg AuthConfig
}

// newAuthMiddleware returns the http.Handler wrapper.
func newAuthMiddleware(cfg AuthConfig) func(http.Handler) http.Handler {
	a := &authMiddleware{cfg: cfg}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			a.serve(w, r, next)
		})
	}
}

func (a *authMiddleware) serve(w http.ResponseWriter, r *http.Request, next http.Handler) {
	scope := classifyScope(r.URL.Path)
	if scope == "" || a.tokensFor(scope) == nil {
		next.ServeHTTP(w, r)
		return
	}
	token := extractBearer(r.Header.Get("Authorization"))
	if token == "" {
		writeError(w, http.StatusUnauthorized, "UNAUTHENTICATED", "missing bearer token")
		return
	}
	if !tokenMatches(token, a.tokensFor(scope)) {
		writeError(w, http.StatusForbidden, "FORBIDDEN", "token is not authorised for "+scope+" scope")
		return
	}
	next.ServeHTTP(w, r)
}

// tokensFor returns the configured tokens for the named scope. nil
// (rather than empty) signals "no tokens configured — skip auth";
// an empty non-nil slice means "configured-but-empty", treated as
// fail-closed.
func (a *authMiddleware) tokensFor(scope string) []string {
	switch scope {
	case "operator":
		if len(a.cfg.OperatorTokens) == 0 {
			return nil
		}
		return a.cfg.OperatorTokens
	case "runner":
		if len(a.cfg.RunnerTokens) == 0 {
			return nil
		}
		return a.cfg.RunnerTokens
	default:
		return nil
	}
}

// classifyScope returns the auth scope for a request path. Empty
// string means "no auth needed" (healthz, metrics, UI, assets).
func classifyScope(path string) string {
	switch {
	case path == "/healthz", path == "/metrics":
		return ""
	case !strings.HasPrefix(path, "/api/v1/"):
		return "" // UI + assets — public.
	case path == "/api/v1/ack",
		path == "/api/v1/progress",
		path == "/api/v1/runners/register":
		return "runner"
	case strings.HasPrefix(path, "/api/v1/runners/") && strings.HasSuffix(path, "/heartbeat"):
		return "runner"
	default:
		return "operator"
	}
}

// extractBearer extracts the token from an Authorization header.
func extractBearer(h string) string {
	const prefix = "Bearer "
	if !strings.HasPrefix(h, prefix) {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(h, prefix))
}

// tokenMatches checks token against the configured allow-list using
// constant-time comparison.
func tokenMatches(token string, allow []string) bool {
	tokenBytes := []byte(token)
	for _, a := range allow {
		if subtle.ConstantTimeCompare(tokenBytes, []byte(a)) == 1 {
			return true
		}
	}
	return false
}
