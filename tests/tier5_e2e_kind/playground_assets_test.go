// SPDX-License-Identifier: MIT

//go:build e2e_kind

// Tier-5 e2e Kind test for the §27.7 asset cache-header and SPA-fallback
// behavior. It installs against the live cluster with
// playground.enabled=true (see tests/testinfra/kind/e2e-values.yaml,
// the only overlay knob this test depends on), then walks the index
// page, a hashed bundle asset, and an unknown client-side route through
// the real chart Ingress, asserting the differentiated Cache-Control
// header on each and the SPA-fallback Content-Type on the unknown
// route.
package tier5_e2e_kind_test

import (
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/sessiondriver"
)

// spec: §27.7 ("Static assets (`index.html`, hashed `*.js` and `*.css`
// bundles) are served from the embedded FS with long cache headers
// (`Cache-Control: public, max-age=31536000, immutable`). `index.html`
// is served with `Cache-Control: no-store` so new releases propagate
// immediately.")
//
// diagnosis: a failure here means the §27.7 roll-forward correctness
// guarantee does not hold once the playground is actually deployed
// behind the real chart Ingress: either a hashed bundle asset lost its
// year-long immutable cache header in transit (which would make a new
// release fail to propagate to already-cached clients, the opposite of
// the guarantee's intent), or index.html (served directly or via the
// SPA fallback for an unmatched client-side route) picked up a
// cacheable header instead of no-store (which would let a stale shell
// keep loading old hashed bundle references after a new release). Every
// other playground cache-header test (playground_test.go's
// TestStaticAssetCacheHeaders, TestSecurityHeadersAndCSP) exercises this
// logic in-process via httptest (tier1/tier2) only; this is the first
// proof the headers survive a real gateway-plus-Ingress deployment.
func TestPlaygroundAssetCacheHeadersOnLiveCluster(t *testing.T) {
	// The e2e overlay (tests/testinfra/kind/e2e-values.yaml) does not set
	// playground.enabled=true: doing so currently crash-loops every
	// gateway replica at startup. pkg/gateway/mcpfabric/playground/metrics.go
	// registers the lenny_playground_page_views_total counter with the
	// label "authMode", which pkg/observability/metrics's §16.1.1
	// snake_case validator rejects (ValidationError: label "authMode" is
	// not snake_case); cmd/lenny-gateway/main.go treats that error as
	// fatal, so no replica ever becomes Ready. Confirmed live on this
	// cluster (grep of pkg/gateway/mcpfabric/playground/metrics.go still
	// shows the camelCase label, and tests/testinfra/kind/e2e-values.yaml
	// still carries no playground stanza): this is the identical,
	// already-tracked defect blocking every other live-cluster playground
	// test in this package (BUILD-GAPS.md §16.1 Metrics Finding 8).
	// §27.8's own metrics table names this same label "authMode"
	// (camelCase), so fixing the registration to "auth_mode" contradicts
	// the literal spec table until that table is corrected through the
	// proposal pipeline; this is not a call this test can make for
	// itself. Once the label naming is reconciled and the overlay carries
	// playground.enabled=true (authMode: dev, devTenantId: acme, matching
	// the "acme" tenant bootstrap.tenants seeds), remove this skip.
	t.Skip("playground.enabled=true crash-loops the live gateway (non-snake_case metrics label); needs a spec/code reconciliation before this can run")

	d := sessiondriver.New(t)
	base := d.BaseURL()
	client := &http.Client{Timeout: 30 * time.Second}

	t.Run("index.html is served no-store", func(t *testing.T) {
		resp, err := client.Get(base + "/playground/")
		if err != nil {
			t.Fatalf("GET /playground/: %v", err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET /playground/: want 200, got %d (body %s)", resp.StatusCode, body)
		}
		if got := resp.Header.Get("Cache-Control"); got != "no-store" {
			t.Errorf("index Cache-Control = %q, want no-store", got)
		}
		if got := resp.Header.Get("Content-Type"); got == "" {
			t.Errorf("index Content-Type is empty")
		}
	})

	t.Run("a hashed bundle asset is served with the immutable cache header", func(t *testing.T) {
		resp, err := client.Get(base + "/playground/app.js")
		if err != nil {
			t.Fatalf("GET /playground/app.js: %v", err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET /playground/app.js: want 200, got %d (body %s)", resp.StatusCode, body)
		}
		if got := resp.Header.Get("Cache-Control"); got != "public, max-age=31536000, immutable" {
			t.Errorf("app.js Cache-Control = %q, want the immutable header", got)
		}
		if got := resp.Header.Get("Content-Type"); got != "text/javascript; charset=utf-8" {
			t.Errorf("app.js Content-Type = %q, want text/javascript; charset=utf-8", got)
		}
	})

	t.Run("an unknown client-side route falls back to the no-store index", func(t *testing.T) {
		resp, err := client.Get(base + "/playground/sessions/does-not-exist")
		if err != nil {
			t.Fatalf("GET /playground/sessions/does-not-exist: %v", err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET unknown client-side route: want 200 (SPA fallback), got %d (body %s)", resp.StatusCode, body)
		}
		// The fallback response is index.html itself, so it must carry
		// the same no-store header as the index, not the hashed-bundle
		// immutable header: this is the §27.7 roll-forward guarantee
		// applied to the fallback path, not a separate rule.
		if got := resp.Header.Get("Cache-Control"); got != "no-store" {
			t.Errorf("unknown-route Cache-Control = %q, want no-store (the SPA fallback serves index.html)", got)
		}
		if got := resp.Header.Get("Content-Type"); got != "text/html; charset=utf-8" {
			t.Errorf("unknown-route Content-Type = %q, want text/html; charset=utf-8 (the SPA fallback serves index.html)", got)
		}
	})
}
