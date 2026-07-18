// SPDX-License-Identifier: MIT

//go:build e2e_kind

// Tier-5 e2e Kind test for the §25.1 RFC 9068 scope-narrowing boundary
// on a live, deployed gateway.
//
// Every prior tier-5/8/9 scope-gate test drives the boundary one of two
// ways: tests/tier9_security/admin_scope_enforce_test.go builds the
// genuine admin.Router in-process and injects a Principal carrying a
// parsed scope set directly (bypassing JWT signing and verification
// entirely), and every other live-cluster caller authenticates with the
// dev-header shortcut (X-Lenny-Tenant-ID / X-Lenny-Roles /
// X-Lenny-User-ID), whose Claims carry no `scope` at all
// (pkg/gateway/middleware/auth/auth.go serveDevHeaders never sets
// Principal.Scopes from a header). Neither path proves the RFC 9068
// `scope` JWT claim is actually parsed off a real Bearer token and
// enforced by the deployed gateway's §25.1 admin-API scope gate.
//
// This test mints a real, RFC 9068-shaped Bearer JWT with a narrowed
// `scope` claim and presents it to the live gateway over HTTP. Minting
// it through the canonical RFC 8693 exchange endpoint (POST
// /v1/oauth/token) is not reachable on this install: the gateway and
// lenny-token-service each construct their KMS-backed signer with
// jwt.NewKMSSigner, which "generates a fresh HMAC-SHA256 signing key"
// per process (pkg/auth/jwt/kmssigner.go) and this e2e install's
// kms.provider is "local" (tests/testinfra/kind/e2e-values.yaml does
// not override it), so the two processes hold unrelated, ephemeral
// signing keys and a subject_token minted by one is never verifiable by
// the other.
//
// Instead this test signs the token with the §17.4 dev-bearer-trust
// HMAC key the e2e install already provisions for a different purpose
// (giving lenny-ops a RequireAuth AuthConfig, see install.sh "creating
// the lenny-ops bearer-trust key Secret"): e2e-values.yaml sets
// security.oidc.bearerTrustKeySecret, which the chart mounts onto the
// gateway too (charts/lenny/templates/gateway-deployment.yaml lines
// 328-335, 461-471) and passes as --bearer-trust-hmac-key-file. The
// gateway's cmd/lenny-gateway/stores.go buildTokenSigningStores wraps
// this trusted key in a jwt.MultiVerifier alongside the KMS-backed Token
// Service verifier (gated on --dev-mode, which this e2e install sets),
// so a token signed with this key — carrying aud: ["dev.local"], the
// only audience the wrapping ClaimChecker accepts (embeddedOIDCAudience
// in cmd/lenny-gateway/main.go) — verifies exactly as a genuine
// Token-Service-issued Bearer would, including the RFC 9068 `scope`
// claim parse at pkg/gateway/middleware/auth/auth.go line 466.
//
// This is the "shared gateway/token-service signing key" dependency
// documented in TEST-GAPS.md for this finding: the e2e install already
// provisions and mounts one for a different purpose, and this test
// reuses it as a live JWT-signing key rather than standing up new
// cluster infrastructure.
package tier5_e2e_kind_test

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/auth/jwt"
	"github.com/lennylabs/lenny/tests/testinfra/kind"
	"github.com/lennylabs/lenny/tests/testinfra/sessiondriver"
)

// e2eBearerTrustKeySecretName is the §17.4 dev-bearer-trust HMAC key
// Secret tests/testinfra/kind/e2e-values.yaml names via
// security.oidc.bearerTrustKeySecret and install.sh creates before the
// chart install. The chart mounts it onto both the gateway and
// lenny-ops.
const e2eBearerTrustKeySecretName = "lenny-e2e-bearer-trust-key"

// e2eBearerTrustAudience is the sole audience the gateway's wrapping
// ClaimChecker accepts for a token verified through the dev-bearer-trust
// key path (embeddedOIDCAudience, cmd/lenny-gateway/main.go). A token
// signed with the trusted key but carrying any other (or no) audience
// is rejected even though its signature is valid.
const e2eBearerTrustAudience = "dev.local"

// hmacKeyFile mirrors the unexported jwt.hmacKeyFile on-disk format
// (pkg/auth/jwt/keyfile.go): a key id and a raw secret, with
// encoding/json base64-decoding the secret automatically because its Go
// type is []byte. install.sh writes the lenny-e2e-bearer-trust-key
// Secret's `key` data entry in exactly this shape.
type hmacKeyFile struct {
	KeyID  string `json:"keyId"`
	Secret []byte `json:"secret"`
}

// loadE2EBearerTrustSigner reads the §17.4 dev-bearer-trust HMAC key
// Secret from the live cluster and returns a signer over it. Skips the
// test when the Secret is absent rather than failing: an install
// rendered with security.oidc.bearerTrustKeySecret empty is a valid,
// if less common, posture (the value defaults to "" per
// charts/lenny/values.yaml), and un-narrowed dev-header auth is
// unaffected by its absence.
func loadE2EBearerTrustSigner(t *testing.T, c *kind.Cluster) *jwt.HMACSigner {
	t.Helper()
	out, err := c.KubectlOut(t, "-n", t5SystemNS, "get", "secret", e2eBearerTrustKeySecretName,
		"-o", "jsonpath={.data.key}")
	if err != nil || strings.TrimSpace(out) == "" {
		t.Skip("lenny-e2e-bearer-trust-key Secret not present on this install; skipping the §25.1 scope-narrowing live-bearer test")
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(out))
	if err != nil {
		t.Fatalf("decode %s/%s data.key: %v", t5SystemNS, e2eBearerTrustKeySecretName, err)
	}
	var kf hmacKeyFile
	if err := json.Unmarshal(decoded, &kf); err != nil {
		t.Fatalf("parse %s/%s data.key as an hmac key file: %v", t5SystemNS, e2eBearerTrustKeySecretName, err)
	}
	if kf.KeyID == "" || len(kf.Secret) == 0 {
		t.Fatalf("%s/%s data.key carries an empty keyId or secret", t5SystemNS, e2eBearerTrustKeySecretName)
	}
	return jwt.NewHMACSigner(kf.KeyID, kf.Secret)
}

// mintScopedBearer signs an RFC 9068-shaped Bearer JWT for a synthetic
// platform-admin agent service account, carrying scope as the
// space-separated `scope` claim (empty means the claim is entirely
// absent, matching jwt.Claims' `omitempty` tag). subject is distinct
// from every user row this e2e install's bootstrap seeds
// (tests/testinfra/kind/e2e-values.yaml bootstrap.users lists only
// alice@acme.com), so the gateway's PlatformRoles override
// (userstorePlatformRoles.ResolveRoles) finds no stored assignment for
// (tenant, subject) and leaves the JWT's own `roles` claim — not a
// stored row — authoritative (cmd/lenny-gateway/main.go
// userstorePlatformRoles.ResolveRoles returns found=false on a missing
// row).
func mintScopedBearer(t *testing.T, signer *jwt.HMACSigner, scope string) string {
	t.Helper()
	now := time.Now()
	tok, err := signer.Sign(jwt.Claims{
		Subject:    "sa-e2e-scope-probe",
		TenantID:   "acme",
		CallerType: "agent",
		Roles:      []auth.Role{auth.RolePlatformAdmin},
		Scope:      scope,
		Audience:   []string{e2eBearerTrustAudience},
		IssuedAt:   now.Unix(),
		Expiry:     now.Add(10 * time.Minute).Unix(),
		JWTID:      "e2e-scope-probe-" + now.Format(time.RFC3339Nano),
	})
	if err != nil {
		t.Fatalf("sign scoped bearer (scope=%q): %v", scope, err)
	}
	return tok
}

// scopeForbiddenDetails extracts {error:{code, details}} from an admin
// 403 SCOPE_FORBIDDEN envelope (pkg/gateway/externalapi/admin/
// scope_enforce.go writeScopeForbidden).
func scopeForbiddenDetails(t *testing.T, body map[string]any) (code string, requiredScope, activeScope any) {
	t.Helper()
	errObj, _ := body["error"].(map[string]any)
	code, _ = errObj["code"].(string)
	details, _ := errObj["details"].(map[string]any)
	return code, details["requiredScope"], details["activeScope"]
}

// spec: §25.1 ("Admin API middleware — every endpoint is mapped to a
// canonical scope via its `x-lenny-scope` OpenAPI extension. The
// middleware checks scopes before routing to the handler."; "A request
// for a tool not permitted by any scope returns `403 SCOPE_FORBIDDEN`
// with a response body listing the caller's active scopes."; "Absent
// `scope` claim: no scope restriction — the token's role ceiling
// applies unmodified.")
//
// diagnosis: a failure here means the deployed gateway's §25.1 scope
// gate does not actually parse and enforce the RFC 9068 `scope` claim
// off a real, signed Bearer JWT on a live cluster — either a
// scope-narrowed token reaches an out-of-scope handler at its full role
// ceiling (the ADM-1 fail-open tests/tier9_security/
// admin_scope_enforce_test.go pins in-process, now unverified against
// the actual deployed HTTP surface), a correctly-scoped token is
// wrongly rejected (the gate over-restricts), or an absent scope claim
// stops deferring to the role ceiling (a regression in the
// dev-header-adjacent bearer path). Every prior live-cluster caller in
// this suite authenticates with the dev-header shortcut or an
// unscoped admin bearer, so none of them would catch a regression here.
func TestScopeNarrowedBearerGatesAdminAPI(t *testing.T) {
	c := kind.InstallLenny(t)
	d := sessiondriver.New(t)
	signer := loadE2EBearerTrustSigner(t, c)

	t.Run("narrowed token is rejected on an out-of-scope endpoint", func(t *testing.T) {
		// GET /v1/admin/tenants declares x-lenny-scope tools:tenant:read.
		// A token scoped only to tools:me:read (a real, but narrower,
		// grant) must be denied before the handler runs.
		bearer := mintScopedBearer(t, signer, "tools:me:read")
		status, body := bearerRequest(t, d, http.MethodGet, "/v1/admin/tenants", bearer, nil)
		if status != http.StatusForbidden {
			t.Fatalf("GET /v1/admin/tenants with scope=tools:me:read: want 403, got %d (body %v)", status, body)
		}
		code, required, active := scopeForbiddenDetails(t, body)
		if code != "SCOPE_FORBIDDEN" {
			t.Errorf("error code = %q, want SCOPE_FORBIDDEN (body %v)", code, body)
		}
		if required != "tools:tenant:read" {
			t.Errorf("details.requiredScope = %v, want tools:tenant:read", required)
		}
		if active != "tools:me:read" {
			t.Errorf("details.activeScope = %v, want tools:me:read", active)
		}
	})

	t.Run("narrowed token passes the gate on its own matching endpoint", func(t *testing.T) {
		// GET /v1/admin/me declares x-lenny-scope tools:me:read — exactly
		// the scope the token above was denied for a different endpoint
		// carries. The same narrowing must admit this call.
		bearer := mintScopedBearer(t, signer, "tools:me:read")
		status, body := bearerRequest(t, d, http.MethodGet, "/v1/admin/me", bearer, nil)
		if status != http.StatusOK {
			t.Fatalf("GET /v1/admin/me with scope=tools:me:read: want 200, got %d (body %v)", status, body)
		}
		if got, _ := body["subject"].(string); got != "sa-e2e-scope-probe" {
			t.Errorf("GET /v1/admin/me subject = %q, want sa-e2e-scope-probe (body %v); "+
				"the response identity may not reflect the minted bearer", got, body)
		}
	})

	t.Run("absent scope claim defers to the role ceiling", func(t *testing.T) {
		// spec: §25.1 line 90 — "Absent `scope` claim: no scope
		// restriction — the token's role ceiling applies unmodified."
		// The same tools:tenant:read endpoint the first subtest denied
		// must admit a platform-admin token that carries no scope claim
		// at all.
		bearer := mintScopedBearer(t, signer, "")
		status, body := bearerRequest(t, d, http.MethodGet, "/v1/admin/tenants", bearer, nil)
		if status != http.StatusOK {
			t.Fatalf("GET /v1/admin/tenants with no scope claim: want 200, got %d (body %v)", status, body)
		}
	})
}
