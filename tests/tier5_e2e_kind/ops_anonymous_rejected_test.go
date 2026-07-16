// SPDX-License-Identifier: MIT

//go:build e2e_kind

// Tier-5 e2e Kind test that the §25.4 "no anonymous access except
// /healthz" authentication mandate is enforced end-to-end on the
// deployed lenny-ops binary, not just in-process.
//
// The §25.4 authentication rule is pinned in-process at tier 9
// (TestOpsAnonymousRejectedOnEveryEndpoint), which constructs an
// *opsserver.Server directly with an AuthConfig. That path does not
// exercise cmd/lenny-ops/main.go's buildAuthConfig flag plumbing, nor
// the Helm chart wiring that mounts the bearer-trust key and renders
// --bearer-trust-hmac-key-file onto the deployed pod. Without that
// wiring the deployed operability surface serves the platform-admin
// remediation-lock / backup / drift / diagnostics APIs to any
// anonymous caller. This test drives the live deployed lenny-ops over
// a port-forward and asserts that an unauthenticated, dev-header-free
// request is rejected on a non-/healthz endpoint while /healthz stays
// open, so the whole main.go + chart + deployment path is verified.
//
// The e2e overlay wires security.oidc.bearerTrustKeySecret and
// tests/testinfra/kind/install.sh creates that Secret, so the deployed
// lenny-ops runs with a non-nil AuthConfig (RequireAuth). A dev-header
// positive control confirms the same endpoint admits an authenticated
// caller, so the anonymous rejection is a genuine authorization decision
// rather than an unconditionally closed or missing route.
//
// §25.4's error taxonomy (line 328) places both 401 and 403 in the AUTH
// category, and the sentence under test mandates denial rather than a
// specific status. The deployed dev-mode binary runs single-tenant with
// AllowDevHeaders, so a no-credential request is admitted through the
// dev-header path as an empty principal and denied at the admin-role gate
// with 403; a production binary rejects it at authentication with 401.
// Both deny anonymous access, so the assertion accepts either.
//
// spec: §25.4 line 1567 (lenny-ops authentication; no anonymous access
// except /healthz); §25.4 line 328 (AUTH category is 401 or 403).

package tier5_e2e_kind_test

import (
	"context"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/kind"
)

// opsAnonEndpoints are non-/healthz operability endpoints that read
// platform state. §25.4 line 1567 requires every one of them to reject
// an anonymous caller; none is on the /healthz-and-/readyz exemption
// list.
var opsAnonEndpoints = []struct {
	name string
	path string
}{
	{"identity", "/v1/admin/me"},
	{"operations list", "/v1/admin/operations"},
	{"remediation-locks list", "/v1/admin/remediation-locks"},
}

// spec: §25.4 line 1567 — "Requires platform-admin or tenant-admin role
// on all endpoints. No anonymous access except /healthz." Verified
// end-to-end against the deployed lenny-ops binary, whose auth wiring
// runs through cmd/lenny-ops/main.go and the Helm chart's
// --bearer-trust-hmac-key-file mount rather than an in-process server.
//
// diagnosis: the deployed lenny-ops served a non-/healthz endpoint to an
// unauthenticated caller (2xx rather than the AUTH-category 401/403).
// Either the chart did not render
// --bearer-trust-hmac-key-file (security.oidc.bearerTrustKeySecret
// unset, so buildAuthConfig returns a nil AuthConfig and the surface is
// unauthenticated), the bearer-trust key Secret is missing, or the
// §25.4 auth middleware fails open when the Authorization header and the
// dev identity headers are both absent. Any of these serves the
// platform-admin operability surface anonymously.
func TestOpsAnonymousRejectedOnDeployedBinary_spec_25_4_1567(t *testing.T) {
	c := kind.InstallLenny(t)

	if !t5DeploymentReady(t, c, "lenny-ops") {
		t.Skip("precondition not met: lenny-ops is not Ready; the §25.4 authentication surface is served by lenny-ops")
	}

	baseURL, stop := c.PortForward(t, "svc/lenny-ops", t5SystemNS, opsHTTPPort)
	defer stop()

	for _, ep := range opsAnonEndpoints {
		t.Run("anonymous/"+ep.name, func(t *testing.T) {
			code := opsAnonStatus(t, http.MethodGet, baseURL+ep.path, false)
			if code != http.StatusUnauthorized && code != http.StatusForbidden {
				t.Fatalf("anonymous GET %s: status = %d, want 401 or 403; the deployed lenny-ops must deny anonymous callers on every non-/healthz endpoint (§25.4 line 1567; AUTH category is 401 or 403 per line 328)",
					ep.path, code)
			}
		})
	}

	// /healthz is the sole §25.4 exemption so the kubelet liveness and
	// readiness probes reach it without credentials. It must stay open.
	t.Run("anonymous/healthz-open", func(t *testing.T) {
		code := opsAnonStatus(t, http.MethodGet, baseURL+"/healthz", false)
		if code == http.StatusUnauthorized || code == http.StatusForbidden {
			t.Fatalf("anonymous GET /healthz: status = %d, want it open; /healthz is the sole §25.4 exemption and the kubelet probes it without credentials",
				code)
		}
	})

	// Positive control: the same endpoint admits an authenticated caller.
	// The e2e ops binary runs production=false, so the dev identity
	// headers (X-Lenny-Tenant-ID / X-Lenny-Roles / X-Lenny-User-ID) are an
	// accepted transport. A non-denied response here proves the anonymous
	// denial above is an authorization decision rather than an
	// unconditionally closed or absent route.
	t.Run("dev-header/admitted", func(t *testing.T) {
		code := opsAnonStatus(t, http.MethodGet, baseURL+"/v1/admin/me", true)
		if code == http.StatusUnauthorized || code == http.StatusForbidden {
			t.Fatalf("dev-header GET /v1/admin/me: status = %d; an authenticated platform-admin caller must be admitted, so the anonymous denial is not merely a closed route", code)
		}
	})
}

// opsAnonStatus issues one request to the deployed lenny-ops and returns
// its status code. When devHeaders is true it carries the dev-mode
// platform-admin identity headers; otherwise it carries no credentials
// at all (no Authorization, no dev headers), the anonymous case §25.4
// requires the surface to reject.
func opsAnonStatus(t *testing.T, method, url string, devHeaders bool) int {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, url, nil)
	if err != nil {
		t.Fatalf("build request %s %s: %v", method, url, err)
	}
	if devHeaders {
		req.Header.Set("X-Lenny-Tenant-ID", "platform")
		req.Header.Set("X-Lenny-Roles", "platform-admin")
		req.Header.Set("X-Lenny-User-ID", "alice")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request %s %s: %v", method, url, err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode
}
