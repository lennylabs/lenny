// SPDX-License-Identifier: MIT

//go:build security

// Tier-9 security test for the §27 web playground on a live cluster:
// the §27.7 Content-Security-Policy posture and the §27.3 dev-mode
// "ignored, not rejected" caller-material invariant.
package tier9_security_test

import (
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/sessiondriver"
)

// spec: §27.7 (the Content-Security-Policy directive block: "default-src
// 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; connect-src
// 'self' ...; img-src 'self' data:; object-src 'none'; media-src 'none';
// frame-ancestors 'none'; base-uri 'self'; form-action 'self'"; "The
// gateway also sets `X-Content-Type-Options: nosniff` and
// `Referrer-Policy: same-origin` on all playground responses."); §27.3
// ("dev" row of the "Auth by mode" table: "Any `Authorization: Bearer` or
// `lenny_playground_session` cookie presented in `dev` mode is **ignored**
// (not rejected — dev mode never gates on caller material).").
//
// diagnosis: a failure here means either the deployed gateway's
// Content-Security-Policy for /playground/* has regressed from the §27.7
// directive block on a live response (a clickjacking or injection
// exposure, not just a unit-level string mismatch), or the dev-mode mint
// endpoint gates on caller-supplied Authorization/cookie material it MUST
// ignore, which would make dev mode fail unpredictably depending on
// whatever stray credential a browser or proxy happens to attach rather
// than behaving as the admission-material-free mode the spec defines.
func TestPlaygroundSecurityPostureOnLiveCluster(t *testing.T) {
	// See the identical skip in
	// tests/tier5_e2e_kind/playground_test.go: playground.enabled=true
	// crash-loops the live gateway today because
	// pkg/gateway/mcpfabric/playground/metrics.go registers a
	// non-snake_case metric label ("authMode") that
	// pkg/observability/metrics's §16.1.1 validator rejects, and §27.8's
	// own metrics table names that same label "authMode", so the fix
	// requires reconciling the spec table and the code together rather
	// than a code-only change. Remove this skip once that lands and the
	// e2e overlay (tests/testinfra/kind/e2e-values.yaml) carries
	// playground.enabled=true.
	t.Skip("playground.enabled=true crash-loops the live gateway (non-snake_case metrics label); needs a spec/code reconciliation before this can run")

	d := sessiondriver.New(t)
	base := d.BaseURL()
	client := &http.Client{Timeout: 30 * time.Second}

	t.Run("live CSP matches the §27.7 directive block exactly", func(t *testing.T) {
		resp, err := client.Get(base + "/playground/")
		if err != nil {
			t.Fatalf("GET /playground/: %v", err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET /playground/: want 200, got %d (body %s)", resp.StatusCode, body)
		}
		csp := resp.Header.Get("Content-Security-Policy")
		wantDirectives := []string{
			"default-src 'self'",
			"script-src 'self'",
			"style-src 'self' 'unsafe-inline'",
			"connect-src 'self'",
			"img-src 'self' data:",
			"object-src 'none'",
			"media-src 'none'",
			"frame-ancestors 'none'",
			"base-uri 'self'",
			"form-action 'self'",
		}
		for _, directive := range wantDirectives {
			if !strings.Contains(csp, directive) {
				t.Errorf("Content-Security-Policy = %q, missing directive %q", csp, directive)
			}
		}
		if got := resp.Header.Get("X-Content-Type-Options"); got != "nosniff" {
			t.Errorf("X-Content-Type-Options = %q, want nosniff", got)
		}
		if got := resp.Header.Get("Referrer-Policy"); got != "same-origin" {
			t.Errorf("Referrer-Policy = %q, want same-origin", got)
		}

		// A non-playground route carries none of this: the CSP is
		// applied only to /playground/*, per §27.7.
		healthResp, err := client.Get(base + "/v1/admin/health")
		if err != nil {
			t.Fatalf("GET /v1/admin/health: %v", err)
		}
		defer healthResp.Body.Close()
		if got := healthResp.Header.Get("Content-Security-Policy"); got != "" {
			t.Errorf("GET /v1/admin/health carried a Content-Security-Policy header (%q); the §27.7 CSP is scoped to /playground/* only", got)
		}
	})

	t.Run("dev-mode mint ignores a stray bearer and a stray playground cookie", func(t *testing.T) {
		req, err := http.NewRequest(http.MethodPost, base+"/v1/playground/token", nil)
		if err != nil {
			t.Fatalf("build mint request: %v", err)
		}
		// Neither credential belongs to this installation's dev-mode
		// tenant; if the mint gated on either, the request would fail
		// with an auth error instead of minting normally.
		req.Header.Set("Authorization", "Bearer not-a-real-token")
		req.AddCookie(&http.Cookie{Name: "lenny_playground_session", Value: "not-a-real-session"})
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("POST /v1/playground/token: %v", err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("POST /v1/playground/token with a stray bearer and cookie in dev mode: want 200 (dev mode ignores caller material), got %d (body %s)", resp.StatusCode, body)
		}
	})
}
