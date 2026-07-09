// SPDX-License-Identifier: MIT

//go:build e2e_kind

// Tier-5 e2e Kind test for the §10.2 standard gateway Bearer-JWT auth
// chain gating session creation on a live cluster.
//
// Every prior tier-5/8/9 live-session test drives session creation
// through tests/testinfra/sessiondriver, which authenticates with the
// X-Lenny-Tenant-ID / X-Lenny-Roles / X-Lenny-User-ID dev-mode headers
// (see sessiondriver's package doc). That path never exercises the
// Authorization: Bearer chain §10.2 names as the canonical Client ->
// Gateway auth mechanism, and which §27.3 confirms is the same
// credential behind both the "oidc" and "apiKey" playground auth modes
// ("apiKey" pastes that same bearer into a form; §27.2 and §10.2 are
// explicit that no separate API-key credential primitive exists in
// v1). This test drives POST /v1/sessions with a real, gateway-issued
// Bearer JWT -- the §17.6 bootstrap admin credential -- instead of the
// dev headers, and confirms the standard auth chain both admits a
// valid bearer and fails closed on an invalid one, on the live
// cluster.
package tier5_e2e_kind_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/kind"
	"github.com/lennylabs/lenny/tests/testinfra/sessiondriver"
)

// adminTokenSecretName is the §17.6 line 463 default Secret name the
// gateway bootstrap writes the initial platform-admin bearer JWT to
// (chart default gateway.adminToken.secretName; the e2e install takes
// the default).
const adminTokenSecretName = "lenny-admin-token"

// readAdminBearerSecret reads and base64-decodes whatever bearer JWT
// currently sits in the lenny-admin-token Secret. Skips the test when
// the Secret is absent (the chart was rendered with
// gateway.adminToken.enabled: false) rather than failing, since that
// is a valid, if less common, install posture.
func readAdminBearerSecret(t *testing.T, c *kind.Cluster) string {
	t.Helper()
	out, err := c.KubectlOut(t, "-n", t5SystemNS, "get", "secret", adminTokenSecretName,
		"-o", "jsonpath={.data.token}")
	if err != nil || strings.TrimSpace(out) == "" {
		t.Skip("lenny-admin-token Secret not present on this install; skipping standard-bearer auth-chain test")
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(out))
	if err != nil {
		t.Fatalf("decode admin bearer from %s/%s: %v", t5SystemNS, adminTokenSecretName, err)
	}
	if len(decoded) == 0 {
		t.Fatal("admin bearer secret decoded to an empty token")
	}
	return string(decoded)
}

// ensureDefaultTenantSeeded runs the §15.1 bootstrap-seed path for the
// built-in "default" tenant. auth.ExtractTenant admits "default"
// unconditionally for the auth chain "even before the row is
// persisted" (cmd/lenny-gateway/main.go bearerTenantRegistry), but the
// per-tenant billing_seq_/audit_seq_ Postgres sequences a tenant needs
// before its first audit-log write are provisioned only by the
// tenant-create paths (POST /v1/admin/tenants, or this bootstrap-seed
// path) -- explicitly including "the Day-1 default tenant" per
// pkg/gateway/externalapi/admin/bootstrap.go's upsertTenants comment.
// This e2e install's bootstrap.tenants Helm value seeds only "acme"
// (tests/testinfra/kind/e2e-values.yaml), so "default" was never
// tenant-created here and its audit sequence does not exist yet. The
// seed call is idempotent (a second run is a no-op for an existing
// row), so leaving the tenant provisioned after the test matches how
// Embedded Mode's own bootstrap Job seeds "default" and does not need
// to be torn down.
func ensureDefaultTenantSeeded(t *testing.T, d *sessiondriver.Driver) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, d.BaseURL()+"/v1/admin/bootstrap",
		strings.NewReader(`{"tenants":[{"id":"default"}]}`))
	if err != nil {
		t.Fatalf("build bootstrap request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Lenny-Tenant-ID", "platform")
	req.Header.Set("X-Lenny-Roles", "platform-admin")
	req.Header.Set("X-Lenny-User-ID", "alice")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("bootstrap default tenant: %v", err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusOK && res.StatusCode != http.StatusMultiStatus {
		t.Fatalf("POST /v1/admin/bootstrap for the default tenant: want 200/207, got %d (body %s)", res.StatusCode, raw)
	}
}

// ensureDefaultTenantAllowsSessionsWithNoEnvironment sets the built-in
// "default" tenant's §10.6 noEnvironmentPolicy to allow-all via
// GET/PUT .../rbac-config, preserving every other rbac-config field on
// the tenant. This e2e Kind cluster is a long-lived, reused install
// (tests/testinfra/kind/install.sh's idempotent bring-up), and an
// earlier, unrelated §10.6/§11.1 test run against this same shared
// cluster can leave "default" pinned at the stricter deny-all policy;
// requireEnvironmentAdmission (pkg/gateway/sessionserver/runtimes.go)
// then rejects any session-creation request that names no explicit
// environment with 403 FORBIDDEN
// reason=no_environment_policy_deny_all. This test's session-creation
// assertions are about the §10.2 Bearer auth chain, not the §10.6
// environment-membership axis, so it pins the precondition explicitly
// through the documented admin path rather than depending on
// whatever state an earlier, unrelated test left behind.
func ensureDefaultTenantAllowsSessionsWithNoEnvironment(t *testing.T, d *sessiondriver.Driver) {
	t.Helper()
	getReq, err := http.NewRequest(http.MethodGet, d.BaseURL()+"/v1/admin/tenants/default/rbac-config", nil)
	if err != nil {
		t.Fatalf("build rbac-config GET request: %v", err)
	}
	getReq.Header.Set("X-Lenny-Tenant-ID", "default")
	getReq.Header.Set("X-Lenny-Roles", "platform-admin")
	getReq.Header.Set("X-Lenny-User-ID", "alice")
	getRes, err := http.DefaultClient.Do(getReq)
	if err != nil {
		t.Fatalf("GET rbac-config: %v", err)
	}
	defer getRes.Body.Close()
	raw, _ := io.ReadAll(getRes.Body)
	if getRes.StatusCode != http.StatusOK {
		t.Fatalf("GET /v1/admin/tenants/default/rbac-config: want 200, got %d (body %s)", getRes.StatusCode, raw)
	}
	etag := getRes.Header.Get("ETag")
	if etag == "" {
		t.Fatal("GET rbac-config carried no ETag")
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("decode rbac-config body: %v; body %s", err, raw)
	}
	if payload["noEnvironmentPolicy"] == "allow-all" {
		return // already the precondition this test needs.
	}
	payload["noEnvironmentPolicy"] = "allow-all"
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("re-encode rbac-config payload: %v", err)
	}
	putReq, err := http.NewRequest(http.MethodPut, d.BaseURL()+"/v1/admin/tenants/default/rbac-config", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("build rbac-config PUT request: %v", err)
	}
	putReq.Header.Set("Content-Type", "application/json")
	putReq.Header.Set("If-Match", etag)
	putReq.Header.Set("X-Lenny-Tenant-ID", "default")
	putReq.Header.Set("X-Lenny-Roles", "platform-admin")
	putReq.Header.Set("X-Lenny-User-ID", "alice")
	putRes, err := http.DefaultClient.Do(putReq)
	if err != nil {
		t.Fatalf("PUT rbac-config: %v", err)
	}
	defer putRes.Body.Close()
	raw, _ = io.ReadAll(putRes.Body)
	if putRes.StatusCode != http.StatusOK {
		t.Fatalf("PUT /v1/admin/tenants/default/rbac-config: want 200, got %d (body %s)", putRes.StatusCode, raw)
	}
}

// freshAdminBearer forces a §17.6 admin-token rotation through
// POST /v1/admin/users/lenny-admin/rotate-token (authenticated with
// the dev-mode headers every other tier-5 test already relies on) and
// returns the freshly minted bearer read back from the Secret.
//
// The bootstrap Secret can otherwise hold a bearer signed under a KEK
// from an earlier gateway process: this e2e install's --kms-provider
// is local with no persisted master-key file (see
// pkg/kms/providerflags.Resolve), so a long-lived dev cluster whose
// gateway Deployment has rolled since the Secret was first written
// carries a token the *current* replica's KMS-backed signer can no
// longer verify (RotatingVerifier has no matching key at all, not
// merely an expired one). Rotating through the documented §17.6
// procedure before the test's own assertions removes that dependency
// on how long the cluster has been up, exactly as an operator would
// recover per the spec's rotation procedure.
func freshAdminBearer(t *testing.T, d *sessiondriver.Driver, c *kind.Cluster) string {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, d.BaseURL()+"/v1/admin/users/lenny-admin/rotate-token", nil)
	if err != nil {
		t.Fatalf("build rotate-token request: %v", err)
	}
	req.Header.Set("X-Lenny-Tenant-ID", "default")
	req.Header.Set("X-Lenny-Roles", "platform-admin")
	req.Header.Set("X-Lenny-User-ID", "alice")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("rotate-token request: %v", err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	if res.StatusCode == http.StatusNotFound {
		t.Skip("admin-token rotation is not configured on this gateway (gateway.adminToken.enabled: false)")
	}
	if res.StatusCode != http.StatusOK {
		t.Fatalf("POST /v1/admin/users/lenny-admin/rotate-token: want 200, got %d (body %s)", res.StatusCode, raw)
	}
	return readAdminBearerSecret(t, c)
}

// bearerRequest issues method against path on the live gateway with
// Authorization: Bearer bearer (rather than the sessiondriver dev
// headers) and returns the status code and decoded JSON body. A nil
// body is passed through as no request body.
func bearerRequest(t *testing.T, d *sessiondriver.Driver, method, path, bearer string, body []byte) (int, map[string]any) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var reqBody io.Reader
	if body != nil {
		reqBody = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, d.BaseURL()+path, reqBody)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+bearer)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request %s %s: %v", method, path, err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	var out map[string]any
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatalf("decode response body for %s %s: %v; body: %s", method, path, err, raw)
		}
	}
	return res.StatusCode, out
}

// spec: §10.2 ("Client → Gateway | OIDC/OAuth 2.1 (MCP-standard
// protected resource server)"; "Authorization: Bearer <jwt> — the
// canonical RFC 6750 path", pkg/gateway/middleware/auth.auth.go)
// diagnosis: A failure here means the live gateway's Bearer-JWT auth
// chain does not gate session creation the way the dev-header
// shortcut does -- either a valid, gateway-issued bearer is rejected
// (the standard Client → Gateway path from §10.2 is broken end to
// end), or an invalid bearer is admitted (the auth chain is not
// failing closed on a live cluster). Either failure means no
// clustered test actually proves the standard bearer path -- which
// backs both playground auth modes named "oidc" and "apiKey" per
// §27.3 -- gates real traffic, leaving header-injection as the only
// proven path.
func TestStandardBearerGatesSessionCreation(t *testing.T) {
	c := kind.InstallLenny(t)
	d := sessiondriver.New(t)
	ensureDefaultTenantSeeded(t, d)
	ensureDefaultTenantAllowsSessionsWithNoEnvironment(t, d)
	bearer := freshAdminBearer(t, d, c)

	t.Run("valid bearer creates and reads a session", func(t *testing.T) {
		body := []byte(`{"runtimeRef":"echo-runtime-sidecar","isolationProfile":"standard"}`)
		// Retry on the §5.2 transient pool-not-ready envelope, matching
		// sessiondriver.CreateAndStartWithPlan's retry convention: the
		// WarmPoolController scales the pool asynchronously, and a test
		// that lands before the next warm pod settles sees this 503.
		var status int
		var resp map[string]any
		deadline := time.Now().Add(30 * time.Second)
		for attempt := 0; time.Now().Before(deadline); attempt++ {
			status, resp = bearerRequest(t, d, http.MethodPost, "/v1/sessions", bearer, body)
			if status != http.StatusServiceUnavailable {
				break
			}
			errObj, _ := resp["error"].(map[string]any)
			if code, _ := errObj["code"].(string); code != "RUNTIME_UNAVAILABLE" {
				break
			}
			wait := time.Duration(attempt+1) * 2 * time.Second
			if wait > 10*time.Second {
				wait = 10 * time.Second
			}
			time.Sleep(wait)
		}
		if status == http.StatusServiceUnavailable {
			// A persistent pool-not-ready 503 is a warm-pool capacity
			// condition (this e2e Kind cluster is long-lived and reused
			// across many test runs; claimed pods can accumulate faster
			// than the pool releases them), not an auth-chain failure:
			// pool-claim logic runs only once auth, tenant resolution,
			// and role checks have already admitted the request, so
			// reaching this envelope is itself proof the bearer was
			// honoured. This mirrors sessiondriver.ErrPoolNotReady's
			// precedent for this exact envelope elsewhere in the suite.
			t.Skipf("pool %s did not free an idle pod within the retry window (body %v); "+
				"the 503 RUNTIME_UNAVAILABLE envelope itself confirms the bearer passed the auth chain "+
				"(an auth failure would be 401/403, not a pool-claim envelope)", "echo-pool-sidecar", resp)
		}
		if status != http.StatusCreated {
			t.Fatalf("POST /v1/sessions with a valid admin bearer: want 201, got %d (body %v)", status, resp)
		}
		id, _ := resp["id"].(string)
		if id == "" {
			t.Fatalf("session response carried no id: %v", resp)
		}
		t.Cleanup(func() {
			// Best-effort: DELETE with the same bearer the session was
			// created under, proving the whole lifecycle -- not only
			// creation -- honours the standard auth chain.
			status, _ := bearerRequest(t, d, http.MethodDelete, "/v1/sessions/"+id, bearer, nil)
			if status != http.StatusOK && status != http.StatusNoContent && status != http.StatusNotFound {
				t.Logf("cleanup: DELETE /v1/sessions/%s with admin bearer: status %d", id, status)
			}
		})

		// Read-your-own-write through the same bearer, confirming GET
		// is gated by the identical chain, not just POST.
		status, resp = bearerRequest(t, d, http.MethodGet, "/v1/sessions/"+id, bearer, nil)
		if status != http.StatusOK {
			t.Fatalf("GET /v1/sessions/%s with a valid admin bearer: want 200, got %d (body %v)", id, status, resp)
		}
		if got, _ := resp["id"].(string); got != id {
			t.Errorf("GET returned session id %q, want %q", got, id)
		}
	})

	t.Run("garbage bearer is rejected, not silently admitted", func(t *testing.T) {
		body := []byte(`{"runtimeRef":"echo-runtime-sidecar","isolationProfile":"standard"}`)
		status, resp := bearerRequest(t, d, http.MethodPost, "/v1/sessions", "not-a-real-jwt", body)
		if status != http.StatusUnauthorized {
			t.Fatalf("POST /v1/sessions with a garbage bearer: want 401, got %d (body %v)", status, resp)
		}
		errObj, _ := resp["error"].(map[string]any)
		code, _ := errObj["code"].(string)
		if code != "TOKEN_INVALID" {
			t.Errorf("garbage bearer rejection code: want TOKEN_INVALID, got %q (body %v)", code, resp)
		}
	})
}
