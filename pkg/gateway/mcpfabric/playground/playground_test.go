// SPDX-License-Identifier: MIT

package playground

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/auth/jwt"
)

// fakeTenants is an in-test TenantRegistry. registered names the
// tenants reported present; err forces a registry failure.
type fakeTenants struct {
	registered map[string]bool
	err        error
}

func (f fakeTenants) IsRegistered(id string) (bool, error) {
	if f.err != nil {
		return false, f.err
	}
	return f.registered[id], nil
}

// fakeOIDC is a deterministic OIDCExchanger. It returns subject on a
// successful Exchange and exchangeErr otherwise.
type fakeOIDC struct {
	subject     OIDCSubject
	exchangeErr error
	lastVerif   string
}

func (f *fakeOIDC) AuthorizationURL(state, challenge, redirectURI string) string {
	return "https://provider.example/authorize?state=" + state + "&code_challenge=" + challenge
}

func (f *fakeOIDC) Exchange(_ context.Context, code, verifier, _ string) (OIDCSubject, error) {
	f.lastVerif = verifier
	if f.exchangeErr != nil {
		return OIDCSubject{}, f.exchangeErr
	}
	return f.subject, nil
}

func devSigner() *jwt.HMACSigner {
	return jwt.NewHMACSigner("pg-test", []byte("playground-test-secret"))
}

// decodeJWTPayload decodes the payload segment of a compact JWT into
// a claim map without verifying the signature; the tests verify
// claim contents, the signer's own tests verify the signature.
func decodeJWTPayload(t *testing.T, token string) map[string]any {
	t.Helper()
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("token has %d segments, want 3", len(parts))
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	return m
}

func TestConfigValidateRejectsBadAuthMode(t *testing.T) {
	cfg := Config{AuthMode: "saml", BearerTTL: 900 * time.Second, MaxIdleTimeSeconds: 300}
	if err := cfg.Validate(); err == nil {
		t.Fatal("Validate accepted an unknown auth mode")
	}
}

func TestConfigValidateRejectsBearerTTLOutOfBound(t *testing.T) {
	cfg := Config{AuthMode: AuthModeOIDC, BearerTTL: 10 * time.Second, MaxIdleTimeSeconds: 300}
	if err := cfg.Validate(); err == nil {
		t.Fatal("Validate accepted a bearer TTL below the 60s lower bound")
	}
	cfg.BearerTTL = 2 * time.Hour
	if err := cfg.Validate(); err == nil {
		t.Fatal("Validate accepted a bearer TTL above the 3600s upper bound")
	}
}

func TestConfigValidateDevTenantRules(t *testing.T) {
	// dev mode with an empty devTenantId is rejected.
	cfg := Config{AuthMode: AuthModeDev, DevTenantID: "", BearerTTL: 900 * time.Second, MaxIdleTimeSeconds: 300}
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "LENNY_PLAYGROUND_DEV_TENANT_REQUIRED") {
		t.Fatalf("empty devTenantId: want LENNY_PLAYGROUND_DEV_TENANT_REQUIRED, got %v", err)
	}
	// dev mode with a malformed devTenantId is rejected.
	cfg.DevTenantID = "bad tenant!"
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "LENNY_PLAYGROUND_DEV_TENANT_INVALID") {
		t.Fatalf("malformed devTenantId: want LENNY_PLAYGROUND_DEV_TENANT_INVALID, got %v", err)
	}
	// dev + multiTenant + default tenant is rejected.
	cfg.DevTenantID = "default"
	cfg.MultiTenant = true
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "LENNY_PLAYGROUND_DEV_TENANT_REQUIRED") {
		t.Fatalf("multiTenant default devTenantId: want LENNY_PLAYGROUND_DEV_TENANT_REQUIRED, got %v", err)
	}
	// dev + an explicit, well-formed tenant is accepted.
	cfg.DevTenantID = "acme"
	if err := cfg.Validate(); err != nil {
		t.Fatalf("explicit devTenantId rejected: %v", err)
	}
}

// TestEffectiveLabelsDefaultsToOrigin exercises the §27.2 line 41
// default: an unset playground.sessionLabels yields the
// load-bearing {origin: "playground"} label. F-27.2.1.
func TestEffectiveLabelsDefaultsToOrigin_spec_27_2_41(t *testing.T) {
	cfg := Config{}
	labels := cfg.EffectiveLabels()
	if got := labels["origin"]; got != PlaygroundOrigin {
		t.Fatalf("EffectiveLabels()[origin] = %q, want %q", got, PlaygroundOrigin)
	}
	if len(labels) != 1 {
		t.Fatalf("EffectiveLabels() = %v, want a single origin entry", labels)
	}
}

// TestEffectiveLabelsMergesOperatorLabels exercises operator-supplied
// labels: the chart-rendered map is preserved and merged with the
// load-bearing origin entry. F-27.2.1.
func TestEffectiveLabelsMergesOperatorLabels_spec_27_2_41(t *testing.T) {
	cfg := Config{SessionLabels: map[string]string{"environment": "stage", "team": "platform"}}
	labels := cfg.EffectiveLabels()
	if labels["environment"] != "stage" {
		t.Fatalf("EffectiveLabels()[environment] = %q, want stage", labels["environment"])
	}
	if labels["team"] != "platform" {
		t.Fatalf("EffectiveLabels()[team] = %q, want platform", labels["team"])
	}
	if labels["origin"] != PlaygroundOrigin {
		t.Fatalf("EffectiveLabels()[origin] = %q, want %q", labels["origin"], PlaygroundOrigin)
	}
}

// TestEffectiveLabelsReStampsOrigin exercises the §27.3 mode-agnostic
// guarantee: an operator cannot silence the load-bearing origin
// label by overriding it. F-27.2.1.
func TestEffectiveLabelsReStampsOrigin_spec_27_3(t *testing.T) {
	cfg := Config{SessionLabels: map[string]string{"origin": "spoof"}}
	labels := cfg.EffectiveLabels()
	if labels["origin"] != PlaygroundOrigin {
		t.Fatalf("EffectiveLabels()[origin] = %q, want %q", labels["origin"], PlaygroundOrigin)
	}
}

// TestEffectiveLabelsReturnsACopy exercises the documented contract:
// mutating the returned map does not affect the stored Config.
func TestEffectiveLabelsReturnsACopy_spec_27_2_41(t *testing.T) {
	cfg := Config{SessionLabels: map[string]string{"team": "platform"}}
	labels := cfg.EffectiveLabels()
	labels["team"] = "other"
	if cfg.SessionLabels["team"] != "platform" {
		t.Fatalf("Config.SessionLabels mutated to %q, want platform", cfg.SessionLabels["team"])
	}
}

func TestEffectiveIdleAndDurationCaps(t *testing.T) {
	cfg := Config{MaxIdleTimeSeconds: 300, MaxSessionMinutes: 30}
	// The override tightens a looser runtime limit.
	if got := cfg.EffectiveIdleSeconds(600); got != 300 {
		t.Fatalf("EffectiveIdleSeconds(600) = %d, want 300", got)
	}
	// The override never relaxes a stricter runtime limit.
	if got := cfg.EffectiveIdleSeconds(120); got != 120 {
		t.Fatalf("EffectiveIdleSeconds(120) = %d, want 120", got)
	}
	if got := cfg.EffectiveSessionMinutes(15); got != 15 {
		t.Fatalf("EffectiveSessionMinutes(15) = %d, want 15", got)
	}
	if got := cfg.EffectiveSessionMinutes(60); got != 30 {
		t.Fatalf("EffectiveSessionMinutes(60) = %d, want 30", got)
	}
}

func TestDevModeMintStampsOriginClaim(t *testing.T) {
	signer := devSigner()
	h := New(Config{Enabled: true, AuthMode: AuthModeDev, DevTenantID: "acme"}, Options{
		Signer:  signer,
		Tenants: fakeTenants{registered: map[string]bool{"acme": true}},
	})
	srv := httptest.NewServer(h.TokenRoutes())
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/v1/playground/token", "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatalf("POST token: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("dev-mode mint status = %d, want 200", resp.StatusCode)
	}
	var body tokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.TokenType != "Bearer" || !body.Reusable {
		t.Fatalf("unexpected token response: %+v", body)
	}
	claims := decodeJWTPayload(t, body.BearerToken)
	if claims["origin"] != PlaygroundOrigin {
		t.Fatalf("minted JWT origin claim = %v, want %q", claims["origin"], PlaygroundOrigin)
	}
	if claims["tenant_id"] != "acme" {
		t.Fatalf("minted JWT tenant_id = %v, want acme", claims["tenant_id"])
	}
	if claims["typ"] != string(auth.TokenSessionCapability) {
		t.Fatalf("minted JWT typ = %v, want session_capability", claims["typ"])
	}
}

func TestDevModeReadyGateRejectsUnseededTenant(t *testing.T) {
	signer := devSigner()
	h := New(Config{Enabled: true, AuthMode: AuthModeDev, DevTenantID: "acme"}, Options{
		Signer:  signer,
		Tenants: fakeTenants{registered: map[string]bool{}}, // acme not seeded
	})
	srv := httptest.NewServer(h.TokenRoutes())
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/v1/playground/token", "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatalf("POST token: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("ready-gate status = %d, want 503", resp.StatusCode)
	}
	if resp.Header.Get("Retry-After") != "5" {
		t.Fatalf("Retry-After = %q, want 5", resp.Header.Get("Retry-After"))
	}
	var env errorEnvelope
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	if env.Error.Code != "LENNY_PLAYGROUND_DEV_TENANT_NOT_SEEDED" {
		t.Fatalf("error code = %q, want LENNY_PLAYGROUND_DEV_TENANT_NOT_SEEDED", env.Error.Code)
	}
}

// TestDevModeReadyGateSelfHealsOnceTenantSeeded exercises the §27.2
// layer-4 self-heal transition: a devTenantId that is well-formed but
// absent from the tenant registry returns 503
// LENNY_PLAYGROUND_DEV_TENANT_NOT_SEEDED, and once the tenant row
// commits the very next request to the same route succeeds without a
// process restart. The gateway does not cache the earlier negative
// lookup: it re-consults the registry on every request, so no
// additional signal beyond the row appearing is required to recover.
//
// spec: §27.2 (layer-4 Ready-gate self-heal) — "The gateway re-checks
// tenant existence on every /playground/* request ... so the 503
// self-heals the instant the bootstrap Job commits the tenant row; no
// gateway restart or rollout is required."
func TestDevModeReadyGateSelfHealsOnceTenantSeeded(t *testing.T) {
	signer := devSigner()
	tenants := fakeTenants{registered: map[string]bool{}} // acme not seeded yet
	h := New(Config{Enabled: true, AuthMode: AuthModeDev, DevTenantID: "acme"}, Options{
		Signer:  signer,
		Tenants: tenants,
	})
	srv := httptest.NewServer(h.TokenRoutes())
	defer srv.Close()

	// First request: the tenant row has not committed yet, so the
	// Ready-gate rejects with 503.
	resp1, err := http.Post(srv.URL+"/v1/playground/token", "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatalf("POST token (pre-seed): %v", err)
	}
	defer resp1.Body.Close()
	if resp1.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("pre-seed status = %d, want 503", resp1.StatusCode)
	}
	io.Copy(io.Discard, resp1.Body)

	// The lenny-bootstrap Job commits the tenant row. No gateway
	// restart or rollout occurs between the two requests: the same
	// running handler and the same underlying registry map are reused,
	// only the map's contents change.
	tenants.registered["acme"] = true

	// Second request against the same handler: the Ready-gate
	// re-consults the registry (it keeps no negative cache from the
	// first request) and now finds the tenant present, so the mint
	// succeeds.
	resp2, err := http.Post(srv.URL+"/v1/playground/token", "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatalf("POST token (post-seed): %v", err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("post-seed status = %d, want 200 (self-heal did not occur)", resp2.StatusCode)
	}
	var body tokenResponse
	if err := json.NewDecoder(resp2.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.TokenType != "Bearer" {
		t.Fatalf("post-seed token response = %+v, want a minted Bearer token", body)
	}
}

func TestAPIKeyModeRejectsNonUserBearer(t *testing.T) {
	signer := devSigner()
	// A session_capability JWT pasted into the API-key form must be
	// rejected so a narrowly-scoped capability is not re-minted.
	capToken, err := signer.Sign(jwt.Claims{
		Subject:  "alice",
		TenantID: "acme",
		Typ:      auth.TokenSessionCapability,
		JWTID:    "subject-jti-cap",
		Expiry:   time.Now().Add(time.Hour).Unix(),
	})
	if err != nil {
		t.Fatalf("sign cap token: %v", err)
	}
	audit := NewMemoryAuditEmitter()
	h := New(Config{Enabled: true, AuthMode: AuthModeAPIKey}, Options{Signer: signer}).WithAuditEmitter(audit)
	srv := httptest.NewServer(h.TokenRoutes())
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/playground/token", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+capToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST token: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
	var env errorEnvelope
	_ = json.NewDecoder(resp.Body).Decode(&env)
	if env.Error.Code != "LENNY_PLAYGROUND_BEARER_TYPE_REJECTED" {
		t.Fatalf("error code = %q, want LENNY_PLAYGROUND_BEARER_TYPE_REJECTED", env.Error.Code)
	}
	// spec: §10.2 line 243 — every rejected mint emits the
	// playground.bearer_mint_rejected audit event with the canonical
	// payload (tenant_id, subject_jti, subject_typ, invariant_violated,
	// ingress_path).
	rejs := audit.MintRejections()
	if len(rejs) != 1 {
		t.Fatalf("mint-rejected emissions: got %d, want 1", len(rejs))
	}
	ev := rejs[0]
	if ev.InvariantViolated != "subject_typ_invalid" {
		t.Fatalf("invariant: got %q, want subject_typ_invalid", ev.InvariantViolated)
	}
	if ev.SubjectTyp != string(auth.TokenSessionCapability) {
		t.Fatalf("subject_typ: got %q, want %q", ev.SubjectTyp, auth.TokenSessionCapability)
	}
	if ev.SubjectJTI != "subject-jti-cap" {
		t.Fatalf("subject_jti: got %q, want subject-jti-cap", ev.SubjectJTI)
	}
	if ev.TenantID != "acme" {
		t.Fatalf("tenant_id: got %q, want acme", ev.TenantID)
	}
	if ev.IngressPath != "/v1/playground/token" {
		t.Fatalf("ingress_path: got %q, want /v1/playground/token", ev.IngressPath)
	}
	if ev.At.IsZero() {
		t.Fatal("emit-time stamp not set")
	}
}

// spec: §10.2 line 243 — the multi-tenant tenant-claim rejection paths
// (claim missing, malformed, unknown) all emit the canonical
// playground.bearer_mint_rejected audit event with the invariant id as
// the metric reason label.
func TestAPIKeyModeTenantClaimRejectionsEmitMintRejected(t *testing.T) {
	signer := devSigner()
	mint := func(claims jwt.Claims) string {
		t.Helper()
		s, err := signer.Sign(claims)
		if err != nil {
			t.Fatalf("sign: %v", err)
		}
		return s
	}
	cases := []struct {
		name     string
		claims   jwt.Claims
		tenants  TenantRegistry
		wantCode string
		wantInv  string
	}{
		{
			name:     "claim missing",
			claims:   jwt.Claims{Subject: "alice", Typ: auth.TokenUserBearer, JWTID: "jti-1", Expiry: time.Now().Add(time.Hour).Unix()},
			tenants:  fakeTenants{registered: map[string]bool{"acme": true}},
			wantCode: "TENANT_CLAIM_MISSING",
			wantInv:  "tenant_claim_missing",
		},
		{
			name:     "claim malformed",
			claims:   jwt.Claims{Subject: "alice", TenantID: "with space", Typ: auth.TokenUserBearer, JWTID: "jti-2", Expiry: time.Now().Add(time.Hour).Unix()},
			tenants:  fakeTenants{registered: map[string]bool{"acme": true}},
			wantCode: "TENANT_CLAIM_INVALID_FORMAT",
			wantInv:  "tenant_claim_invalid_format",
		},
		{
			name:     "claim unknown",
			claims:   jwt.Claims{Subject: "alice", TenantID: "ghost", Typ: auth.TokenUserBearer, JWTID: "jti-3", Expiry: time.Now().Add(time.Hour).Unix()},
			tenants:  fakeTenants{registered: map[string]bool{"acme": true}},
			wantCode: "TENANT_NOT_FOUND",
			wantInv:  "tenant_not_found",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			audit := NewMemoryAuditEmitter()
			h := New(Config{Enabled: true, AuthMode: AuthModeAPIKey, MultiTenant: true}, Options{
				Signer:  signer,
				Tenants: tc.tenants,
			}).WithAuditEmitter(audit)
			srv := httptest.NewServer(h.TokenRoutes())
			defer srv.Close()

			req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/playground/token", strings.NewReader("{}"))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", "Bearer "+mint(tc.claims))
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("POST: %v", err)
			}
			defer resp.Body.Close()
			var env errorEnvelope
			_ = json.NewDecoder(resp.Body).Decode(&env)
			if env.Error.Code != tc.wantCode {
				t.Fatalf("code: got %q, want %q", env.Error.Code, tc.wantCode)
			}
			rejs := audit.MintRejections()
			if len(rejs) != 1 || rejs[0].InvariantViolated != tc.wantInv {
				t.Fatalf("mint-rejected events: got %+v, want one with invariant=%q", rejs, tc.wantInv)
			}
		})
	}
}

func TestAPIKeyModeScopeIsNarrowedToIntersection(t *testing.T) {
	signer := devSigner()
	// The subject token holds one in-policy scope and one out-of-policy
	// scope; the minted JWT must carry only the intersection.
	subjectToken, err := signer.Sign(jwt.Claims{
		Subject:  "bob",
		TenantID: "acme",
		Typ:      auth.TokenUserBearer,
		// tools:sessions:read is inside the playground_allowed_scope
		// ceiling (tools:sessions:*); tools:credential:write is outside
		// it. Both are canonical §25.1 values so the minted scope claim
		// parses through the §10.2 auth chain.
		Scope:  "tools:sessions:read tools:credential:write",
		Expiry: time.Now().Add(time.Hour).Unix(),
	})
	if err != nil {
		t.Fatalf("sign subject token: %v", err)
	}
	h := New(Config{Enabled: true, AuthMode: AuthModeAPIKey}, Options{Signer: signer})
	srv := httptest.NewServer(h.TokenRoutes())
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/playground/token", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+subjectToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST token: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var body tokenResponse
	_ = json.NewDecoder(resp.Body).Decode(&body)
	claims := decodeJWTPayload(t, body.BearerToken)
	scope, _ := claims["scope"].(string)
	if strings.Contains(scope, "credential") {
		t.Fatalf("minted scope %q leaked the out-of-policy tools:credential:write scope", scope)
	}
	if !strings.Contains(scope, "tools:sessions:read") {
		t.Fatalf("minted scope %q dropped the in-policy tools:sessions:read scope", scope)
	}
}

// TestMintResponseEffectiveScopeMirrorsJWTScope pins the §27.3.1
// effectiveScope carrier: the mint-response field equals the minted
// JWT's scope claim by construction across the four narrowing cases the
// SPA must gate the §27.4 delegation-policy affordance on. The
// apiKey-mode cases drive the intersection through a subject token; the
// dev-mode case carries the full ceiling literal. effectiveScope is the
// space-separated intersection(subject.scope, playground_allowed_scope),
// so a subject scoped to the narrower tools:sessions:write survives as
// tools:sessions:write (Intersect keeps the narrower operand), an
// absent subject scope is the §25.1 absent-claim case that returns the
// full ceiling, and a present-but-disjoint subject scope yields the
// empty string via the intersectScope sentinel translation.
//
// spec: 27.3.1 (mint-response effectiveScope carrier), 25.1
// (playground-allowed scope set), 10.2 (scope is the intersection,
// never the union)
func TestMintResponseEffectiveScopeMirrorsJWTScope_spec_27_3_1(t *testing.T) {
	signer := devSigner()
	mint := func(c jwt.Claims) string {
		token, err := signer.Sign(c)
		if err != nil {
			t.Fatalf("sign subject token: %v", err)
		}
		return token
	}
	const fullCeiling = "tools:sessions:* tools:me:read tools:runtimes:read " +
		"tools:pools:read tools:operations:read tools:events:read"

	cases := []struct {
		name string
		// devMode selects the dev-mode mint (synthetic subject carrying the
		// full ceiling); otherwise the apiKey-mode mint drives the
		// intersection through subjectScope.
		devMode      bool
		subjectScope string
		// wantContains is asserted as a substring of effectiveScope when
		// set; exactScope (with wantExact) is asserted as an exact match
		// when assertExact is true, so the empty-string case is checkable.
		wantContains string
		assertExact  bool
		wantExact    string
	}{
		{
			// Intersect keeps the narrower operand: the subject's
			// tools:sessions:write lies inside the ceiling's
			// tools:sessions:*, so it survives verbatim.
			name:         "subject scoped to tools:sessions:write survives",
			subjectScope: "tools:sessions:write",
			wantContains: "tools:sessions:write",
		},
		{
			// The dev-mode synthetic subject carries the full ceiling, so
			// the effective scope carries the tools:sessions:* literal.
			name:         "dev-mode subject carries the ceiling wildcard",
			devMode:      true,
			wantContains: "tools:sessions:*",
		},
		{
			// §25.1 absent-claim case: Set.Intersect returns the other
			// operand when the subject set is not present, so an empty
			// subject scope yields the full ceiling string.
			name:         "absent subject scope yields the full ceiling",
			subjectScope: "",
			assertExact:  true,
			wantExact:    fullCeiling,
		},
		{
			// Present-but-disjoint subject scope: the intersection is empty
			// and intersectScope translates the sentinel to "".
			name:         "present-but-disjoint subject scope yields empty",
			subjectScope: "tools:credential:write",
			assertExact:  true,
			wantExact:    "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var h *Handler
			var authHeader string
			if tc.devMode {
				h = New(Config{Enabled: true, AuthMode: AuthModeDev, DevTenantID: "acme"},
					Options{Signer: signer})
			} else {
				h = New(Config{Enabled: true, AuthMode: AuthModeAPIKey}, Options{Signer: signer})
				authHeader = "Bearer " + mint(jwt.Claims{
					Subject:  "bob",
					TenantID: "acme",
					Typ:      auth.TokenUserBearer,
					Scope:    tc.subjectScope,
					Expiry:   time.Now().Add(time.Hour).Unix(),
				})
			}
			srv := httptest.NewServer(h.TokenRoutes())
			defer srv.Close()

			req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/playground/token", strings.NewReader("{}"))
			req.Header.Set("Content-Type", "application/json")
			if authHeader != "" {
				req.Header.Set("Authorization", authHeader)
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("POST token: %v", err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status = %d, want 200", resp.StatusCode)
			}
			var body tokenResponse
			if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
				t.Fatalf("decode response: %v", err)
			}

			// The response field must equal the minted JWT's scope claim by
			// construction; the SPA relies on this equality to gate §27.4.
			claims := decodeJWTPayload(t, body.BearerToken)
			jwtScope, _ := claims["scope"].(string)
			if body.EffectiveScope != jwtScope {
				t.Fatalf("effectiveScope %q != minted JWT scope claim %q", body.EffectiveScope, jwtScope)
			}
			if tc.wantContains != "" && !strings.Contains(body.EffectiveScope, tc.wantContains) {
				t.Fatalf("effectiveScope %q does not contain %q", body.EffectiveScope, tc.wantContains)
			}
			if tc.assertExact && body.EffectiveScope != tc.wantExact {
				t.Fatalf("effectiveScope = %q, want exactly %q", body.EffectiveScope, tc.wantExact)
			}
		})
	}
}

func TestOIDCModeRejectsBearerOnTokenEndpoint(t *testing.T) {
	h := New(Config{Enabled: true, AuthMode: AuthModeOIDC}, Options{
		Signer:   devSigner(),
		Sessions: NewMemorySessionStore(),
	})
	srv := httptest.NewServer(h.TokenRoutes())
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/playground/token", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer some-token")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST token: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	var env errorEnvelope
	_ = json.NewDecoder(resp.Body).Decode(&env)
	if env.Error.Code != "LENNY_PLAYGROUND_WRONG_AUTH_MATERIAL" {
		t.Fatalf("error code = %q, want LENNY_PLAYGROUND_WRONG_AUTH_MATERIAL", env.Error.Code)
	}
	if env.Error.Details["configuredAuthMode"] != "oidc" {
		t.Fatalf("details.configuredAuthMode = %v, want oidc", env.Error.Details["configuredAuthMode"])
	}
}

// spec: §27.7 (the CSP applied to /playground/*): "default-src 'self';
// script-src 'self'; style-src 'self' 'unsafe-inline'; connect-src
// 'self' wss://<gateway-host>; img-src 'self' data:; object-src
// 'none'; media-src 'none'; frame-ancestors 'none'; base-uri 'self';
// form-action 'self'"
func TestSecurityHeadersAndCSP(t *testing.T) {
	h := New(Config{Enabled: true, AuthMode: AuthModeDev, DevTenantID: "acme", GatewayHost: "gw.example.com"}, Options{
		Signer:  devSigner(),
		Tenants: fakeTenants{registered: map[string]bool{"acme": true}},
	})
	srv := httptest.NewServer(h.PlaygroundRoutes())
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/playground/")
	if err != nil {
		t.Fatalf("GET index: %v", err)
	}
	defer resp.Body.Close()
	if got := resp.Header.Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("X-Content-Type-Options = %q, want nosniff", got)
	}
	if got := resp.Header.Get("Referrer-Policy"); got != "same-origin" {
		t.Fatalf("Referrer-Policy = %q, want same-origin", got)
	}
	csp := resp.Header.Get("Content-Security-Policy")
	for _, want := range []string{
		"default-src 'self'",
		"script-src 'self'",
		"style-src 'self' 'unsafe-inline'",
		"connect-src 'self' wss://gw.example.com",
		"img-src 'self' data:",
		"object-src 'none'",
		"media-src 'none'",
		"frame-ancestors 'none'",
		"base-uri 'self'",
		"form-action 'self'",
	} {
		if !strings.Contains(csp, want) {
			t.Fatalf("CSP %q is missing directive %q", csp, want)
		}
	}
	if resp.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("index Cache-Control = %q, want no-store", resp.Header.Get("Cache-Control"))
	}
}

func TestStaticAssetCacheHeaders(t *testing.T) {
	h := New(Config{Enabled: true, AuthMode: AuthModeDev, DevTenantID: "acme"}, Options{
		Signer:  devSigner(),
		Tenants: fakeTenants{registered: map[string]bool{"acme": true}},
	})
	srv := httptest.NewServer(h.PlaygroundRoutes())
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/playground/app.js")
	if err != nil {
		t.Fatalf("GET app.js: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("app.js status = %d, want 200", resp.StatusCode)
	}
	if got := resp.Header.Get("Cache-Control"); got != "public, max-age=31536000, immutable" {
		t.Fatalf("app.js Cache-Control = %q, want the immutable header", got)
	}
}

// TestAppJSWorkspaceUploadWiring asserts the §27.4 item 2 workspace-plan
// tarball is actually staged and bound, not silently dropped. The SPA
// bundle must read the file, POST it to the upload-archive endpoint with
// the §7.1 uploadToken, bind it into a §14 uploadArchive plan at finalize,
// and start the session. There is no JS test runner, so this checks the
// served bundle carries the functional surface the finding requires.
// spec: §27.4 item 2 (workspace plan upload / drag-drop tarball).
func TestAppJSWorkspaceUploadWiring_spec_27_4_2(t *testing.T) {
	h := New(Config{Enabled: true, AuthMode: AuthModeDev, DevTenantID: "acme"}, Options{
		Signer:  devSigner(),
		Tenants: fakeTenants{registered: map[string]bool{"acme": true}},
	})
	srv := httptest.NewServer(h.PlaygroundRoutes())
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/playground/app.js")
	if err != nil {
		t.Fatalf("GET app.js: %v", err)
	}
	defer resp.Body.Close()
	buf := new(strings.Builder)
	if _, err := io.Copy(buf, resp.Body); err != nil {
		t.Fatalf("read app.js: %v", err)
	}
	js := buf.String()

	// The selected/dropped tarball must reach the upload endpoint with the
	// uploadToken, and the plan it produces must be finalized and started.
	for _, want := range []string{
		"createSessionWithWorkspace", // the decomposed-lifecycle driver
		"/upload-archive",            // staging POST
		"X-Lenny-Upload-Token",       // §7.1 uploadToken header
		"uploadArchive",              // §14 plan source type
		"/finalize",                  // plan binding
		"/start",                     // run the bound session
		"readFileBytes",              // the file is actually read
		"FileReader",                 // client-side read of the tarball
		"dragover",                   // §27.4 drag-drop affordance
		"workspacePlan",              // finalize body field
	} {
		if !strings.Contains(js, want) {
			t.Errorf("app.js missing workspace-upload wiring substring %q", want)
		}
	}
	// The drop must not be a no-op: the bare-input-only state the finding
	// flagged is gone once a drop handler sets the selected file.
	if !strings.Contains(js, "e.dataTransfer") {
		t.Errorf("app.js drag-drop handler does not read dropped files")
	}
}

func TestConfigJSONCarriesDevBanner(t *testing.T) {
	h := New(Config{Enabled: true, AuthMode: AuthModeDev, DevTenantID: "acme"}, Options{
		Signer:  devSigner(),
		Tenants: fakeTenants{registered: map[string]bool{"acme": true}},
	})
	srv := httptest.NewServer(h.PlaygroundRoutes())
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/playground/config.json")
	if err != nil {
		t.Fatalf("GET config.json: %v", err)
	}
	defer resp.Body.Close()
	var cfg uiConfig
	if err := json.NewDecoder(resp.Body).Decode(&cfg); err != nil {
		t.Fatalf("decode config.json: %v", err)
	}
	if cfg.Banner != "DEV MODE — NOT FOR PRODUCTION" || cfg.BannerSeverity != "danger" {
		t.Fatalf("dev banner not server-sourced: %+v", cfg)
	}
}

// TestConfigJSONCarriesAPIKeyBanner pins the §27.9 apiKey-mode warning
// banner: "the playground UI renders a persistent, server-sourced
// yellow banner "API KEY MODE — paste only operator-issued tokens" ...
// The banner text is emitted by the gateway (not the embedded bundle),
// so swapping or patching the asset bundle does not suppress it."
// apiKey mode's paste form is the phishing surface the same subsection
// flags ("apiKey mode ships a bearer-token paste form as its primary
// UX ... a well-known phishing vector"), so the persistent warning is
// the one user-visible mitigation and must not silently regress. The
// test also confirms the banner text is absent from the static
// bundle assets (app.js, index.html) so it cannot originate there;
// only the gateway-served config.json carries it, matching "not the
// embedded bundle". A live bundle-swap leg belongs at tier5 once the
// chart overlay needed to exercise playground.enabled=true in a real
// cluster is available.
// spec: §27.9 (security considerations — apiKey-mode warning banner).
func TestConfigJSONCarriesAPIKeyBanner(t *testing.T) {
	const wantBanner = "API KEY MODE — paste only operator-issued tokens"

	h := New(Config{Enabled: true, AuthMode: AuthModeAPIKey}, Options{
		Signer: devSigner(),
	})
	srv := httptest.NewServer(h.PlaygroundRoutes())
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/playground/config.json")
	if err != nil {
		t.Fatalf("GET config.json: %v", err)
	}
	defer resp.Body.Close()
	var cfg uiConfig
	if err := json.NewDecoder(resp.Body).Decode(&cfg); err != nil {
		t.Fatalf("decode config.json: %v", err)
	}
	if cfg.Banner != wantBanner || cfg.BannerSeverity != "warning" {
		t.Fatalf("apiKey banner not server-sourced: %+v", cfg)
	}

	// The banner must not be recoverable from (or removable via) the
	// static bundle: it is not baked into app.js or index.html.
	for _, asset := range []string{"app.js", "index.html"} {
		aresp, err := http.Get(srv.URL + "/playground/" + asset)
		if err != nil {
			t.Fatalf("GET %s: %v", asset, err)
		}
		buf := new(strings.Builder)
		if _, err := io.Copy(buf, aresp.Body); err != nil {
			t.Fatalf("read %s: %v", asset, err)
		}
		aresp.Body.Close()
		if strings.Contains(buf.String(), wantBanner) {
			t.Fatalf("%s embeds the apiKey banner text directly; it must come from config.json only", asset)
		}
	}
}

func TestOIDCLoginRedirectsAndSetsStateCookie(t *testing.T) {
	oidc := &fakeOIDC{}
	h := New(Config{Enabled: true, AuthMode: AuthModeOIDC}, Options{
		Signer:   devSigner(),
		Sessions: NewMemorySessionStore(),
		OIDC:     oidc,
	})
	srv := httptest.NewServer(h.PlaygroundRoutes())
	defer srv.Close()

	// A non-redirecting client so the 302 is observable.
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resp, err := client.Get(srv.URL + "/playground/auth/login")
	if err != nil {
		t.Fatalf("GET login: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("login status = %d, want 302", resp.StatusCode)
	}
	var sawState bool
	for _, c := range resp.Cookies() {
		if c.Name == oidcStateCookie {
			sawState = true
			if c.Path != statePath {
				t.Fatalf("state cookie Path = %q, want %q", c.Path, statePath)
			}
			if !c.HttpOnly {
				t.Fatal("state cookie is not HttpOnly")
			}
		}
	}
	if !sawState {
		t.Fatal("login did not set the OIDC state cookie")
	}
}
