// SPDX-License-Identifier: MIT

//go:build integration

// Tier-4 integration test: the §27.2 playground.enabled=false gating,
// driven through the real cmd/lenny-gateway binary. When the
// playground is disabled — the Helm default, and the default here
// since the harness passes no --playground-enabled flag — the
// /playground/* and /v1/playground/token routes are not mounted at
// all and return 404, per spec/27_web-playground.md.
package tier4_integration_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/gateway"
)

// spec: §27.2 ("Feature-flag gating" table) — "`playground.enabled` |
// `false` | When `false`, `/playground/*` returns `404` and the asset
// bundle is unmounted"
//
// diagnosis: a failure means the real cmd/lenny-gateway binary
// mounted the playground SPA bundle, the mode-polymorphic
// POST /v1/playground/token mint endpoint, or both, even though
// playground.enabled was left at its default (false). Check the
// `if *playgroundEnabled` gate in cmd/lenny-gateway/httpsurface.go:
// the /playground, /playground/, and /v1/playground/token mux.Handle
// calls must stay inside that block so the routes are never
// registered on Go's default http.ServeMux (which already 404s any
// unregistered path) when the flag is unset.
func TestPlaygroundDisabledReturns404OnAllRoutes_spec_27_2(t *testing.T) {
	// No --playground-enabled flag: exercises the documented default
	// (playground.enabled=false) rather than an explicit --playground-
	// enabled=false override.
	gw := gateway.StartWith(t, "--dev-mode")
	base := gw.BaseURL()
	client := http.DefaultClient

	cases := []struct {
		name   string
		method string
		path   string
	}{
		{"playground index", http.MethodGet, "/playground/"},
		{"playground asset bundle", http.MethodGet, "/playground/app.js"},
		{"playground token mint", http.MethodPost, "/v1/playground/token"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var body strings.Reader
			req, err := http.NewRequest(tc.method, base+tc.path, &body)
			if err != nil {
				t.Fatalf("build request: %v", err)
			}
			req.Header.Set("X-Lenny-Tenant-ID", "acme")
			req.Header.Set("X-Lenny-User-ID", "alice@acme.com")
			resp, err := client.Do(req)
			if err != nil {
				t.Fatalf("%s %s: %v", tc.method, tc.path, err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusNotFound {
				t.Fatalf("%s %s: status = %d, want 404 (playground.enabled=false unmounts the route)", tc.method, tc.path, resp.StatusCode)
			}
		})
	}
}
