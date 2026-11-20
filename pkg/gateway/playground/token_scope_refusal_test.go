// SPDX-License-Identifier: MIT

package playground

import (
	"context"
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

// scopedMintResult is what one mint against the playground token routes
// returned and recorded.
type scopedMintResult struct {
	status     int
	env        errorEnvelope
	body       tokenResponse
	signs      int32
	rejections []MintRejectedEvent
	minted     []AuditEvent
}

// decodeMint reads a mint response into either the success body or the
// error envelope, by status.
func decodeMint(t *testing.T, resp *http.Response, res *scopedMintResult) {
	t.Helper()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read mint response: %v", err)
	}
	res.status = resp.StatusCode
	target := any(&res.env)
	if resp.StatusCode == http.StatusOK {
		target = &res.body
	}
	if err := json.Unmarshal(raw, target); err != nil {
		t.Fatalf("decode mint response: %v; body=%s", err, raw)
	}
}

// mintedEvents returns the playground.bearer_minted events of audit.
func mintedEvents(audit *MemoryAuditEmitter) []AuditEvent {
	var out []AuditEvent
	for _, ev := range audit.Events() {
		if ev.Type == "playground.bearer_minted" {
			out = append(out, ev)
		}
	}
	return out
}

// mintAPIKeyWithScope pastes a user_bearer carrying scope into an
// apiKey-mode handler and returns what the mint returned and recorded.
func mintAPIKeyWithScope(t *testing.T, scope string) scopedMintResult {
	t.Helper()
	verifier := devSigner()
	pasted, err := verifier.Sign(jwt.Claims{
		Subject: "bob", TenantID: "acme", Typ: auth.TokenUserBearer,
		JWTID: "scoped-jti-1", Scope: scope, Audience: []string{"lenny-gateway"},
		Expiry: time.Now().Add(time.Hour).Unix(),
	})
	if err != nil {
		t.Fatalf("sign pasted bearer: %v", err)
	}
	signer := &countingSigner{inner: devSigner()}
	audit := NewMemoryAuditEmitter()
	h := New(Config{Enabled: true, AuthMode: AuthModeAPIKey, CapabilityTokenTTL: 900 * time.Second},
		Options{Signer: signer, Verifier: verifier}).WithAuditEmitter(audit)
	srv := httptest.NewServer(h.TokenRoutes())
	defer srv.Close()
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/playground/token", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+pasted)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST token: %v", err)
	}
	defer resp.Body.Close()
	var res scopedMintResult
	decodeMint(t, resp, &res)
	res.signs = signer.calls.Load()
	res.rejections = audit.MintRejections()
	res.minted = mintedEvents(audit)
	return res
}

// assertScopeRefusal checks a refused mint: its status, code, and
// details, one playground.bearer_mint_rejected event with the invariant
// identifier naming the subject, no signature, and no bearer_minted event.
func assertScopeRefusal(t *testing.T, res scopedMintResult, wantStatus int, wantCode, wantInvariant, wantJTI string, wantDetails map[string]any) {
	t.Helper()
	if res.status != wantStatus {
		t.Fatalf("status = %d, want %d; error=%+v", res.status, wantStatus, res.env.Error)
	}
	if res.env.Error.Code != wantCode {
		t.Errorf("error.code = %q, want %q", res.env.Error.Code, wantCode)
	}
	for k, want := range wantDetails {
		if got := res.env.Error.Details[k]; got != want {
			t.Errorf("details.%s = %v, want %v", k, got, want)
		}
	}
	if res.signs != 0 {
		t.Errorf("signer calls = %d, want 0 on a refused mint", res.signs)
	}
	if len(res.minted) != 0 {
		t.Errorf("bearer_minted events = %d, want 0 on a refused mint", len(res.minted))
	}
	if len(res.rejections) != 1 {
		t.Fatalf("mint rejections = %+v, want exactly one", res.rejections)
	}
	got := res.rejections[0]
	if got.InvariantViolated != wantInvariant || got.TenantID != "acme" || got.SubjectJTI != wantJTI ||
		got.SubjectTyp != string(auth.TokenUserBearer) {
		t.Errorf("mint rejection = %+v, want invariant %s for (acme, %q, user_bearer)", got, wantInvariant, wantJTI)
	}
}

// TestAPIKeyMintRefusesAnUnparseableSubjectScope checks that a pasted bearer
// whose scope claim the canonical parser refuses, such as an identity
// provider's raw openid profile, is refused with 401 UNAUTHORIZED and
// details.reason subject_scope_invalid rather than minted with an empty,
// unrestricted scope claim.
//
// spec: §10.2 (Authentication)
func TestAPIKeyMintRefusesAnUnparseableSubjectScope(t *testing.T) {
	for _, scope := range []string{"openid profile", "tools:sessions:read openid"} {
		t.Run(scope, func(t *testing.T) {
			res := mintAPIKeyWithScope(t, scope)
			assertScopeRefusal(t, res, http.StatusUnauthorized, "UNAUTHORIZED", "subject_scope_invalid",
				"scoped-jti-1", map[string]any{"reason": "subject_scope_invalid"})
		})
	}
}

// TestAPIKeyMintRefusesADisjointSubjectScope checks that a pasted bearer
// narrowed to scopes outside the playground's allowed set, such as
// tools:health:read, is refused with 403 SCOPE_FORBIDDEN naming the
// subject's scope and the allowed set, rather than minted with an empty,
// unrestricted scope claim.
//
// spec: §10.2 (Authentication), §15.1 (REST API)
func TestAPIKeyMintRefusesADisjointSubjectScope(t *testing.T) {
	res := mintAPIKeyWithScope(t, "tools:health:read")
	assertScopeRefusal(t, res, http.StatusForbidden, "SCOPE_FORBIDDEN", "scope_intersection_empty",
		"scoped-jti-1", map[string]any{
			"activeScope":   "tools:health:read",
			"requiredScope": allowedCeiling,
		})
}

// TestAPIKeyMintNarrowsAnOverlappingSubjectScope checks the success paths
// the refusals sit beside: a subject overlapping the allowed set mints with
// the intersection, and a subject whose scope is exactly the allowed set
// mints with that set, each with no rejection recorded.
//
// spec: §10.2 (Authentication), §27.3.1 (OIDC cookie-to-MCP-bearer exchange)
func TestAPIKeyMintNarrowsAnOverlappingSubjectScope(t *testing.T) {
	cases := []struct {
		name, scope, want string
	}{
		{"overlap keeps the intersection", "tools:sessions:read tools:health:read", "tools:sessions:read"},
		{"exactly the allowed set", allowedCeiling, allowedCeiling},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := mintAPIKeyWithScope(t, tc.scope)
			if res.status != http.StatusOK {
				t.Fatalf("status = %d, want 200; error=%+v", res.status, res.env.Error)
			}
			if !sameScopeValues(res.body.EffectiveScope, tc.want) {
				t.Errorf("effectiveScope = %q, want %q", res.body.EffectiveScope, tc.want)
			}
			if claim, _ := decodeJWTPayload(t, res.body.BearerToken)["scope"].(string); claim != res.body.EffectiveScope {
				t.Errorf("bearer scope claim %q != effectiveScope %q", claim, res.body.EffectiveScope)
			}
			if res.signs != 1 || len(res.rejections) != 0 {
				t.Errorf("signer calls = %d, rejections = %d; want 1 and 0", res.signs, len(res.rejections))
			}
		})
	}
}

// TestOIDCMintRefusesASessionRecordScopeItCannotNarrow checks the oidc
// path, including a silent refresh against an established session record:
// a record whose scope is unparseable or disjoint is refused with the same
// status and code as in apiKey mode, and the refusal leaves the record's
// tracked bearer identifiers unchanged.
//
// spec: §10.2 (Authentication), §27.3.1 (OIDC cookie-to-MCP-bearer exchange)
func TestOIDCMintRefusesASessionRecordScopeItCannotNarrow(t *testing.T) {
	cases := []struct {
		name, scope, wantCode, wantInvariant string
		wantStatus                           int
	}{
		{"unparseable", "openid profile email", "UNAUTHORIZED", "subject_scope_invalid", http.StatusUnauthorized},
		{"disjoint", "tools:credential:write", "SCOPE_FORBIDDEN", "scope_intersection_empty", http.StatusForbidden},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := NewMemorySessionStore()
			rec := SessionRecord{
				UserID: "alice", TenantID: "acme", Scope: tc.scope, Origin: PlaygroundOrigin,
				IssuedAt: time.Now(), LastActivityAt: time.Now(), BearerJTIs: []string{"earlier-jti"},
			}
			if err := store.PutSession(context.Background(), "acme", "sess-1", rec, time.Hour); err != nil {
				t.Fatalf("PutSession: %v", err)
			}
			signer := &countingSigner{inner: devSigner()}
			audit := NewMemoryAuditEmitter()
			h := New(Config{Enabled: true, AuthMode: AuthModeOIDC, OIDCSessionTTL: time.Hour, CapabilityTokenTTL: 900 * time.Second},
				Options{Signer: signer, Verifier: devSigner(), Sessions: store}).WithAuditEmitter(audit)
			srv := httptest.NewServer(h.TokenRoutes())
			defer srv.Close()
			req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/playground/token", strings.NewReader("{}"))
			req.Header.Set("Content-Type", "application/json")
			req.AddCookie(&http.Cookie{Name: sessionCookie, Value: "sess-1"})
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("POST token: %v", err)
			}
			defer resp.Body.Close()
			var res scopedMintResult
			decodeMint(t, resp, &res)
			res.signs = signer.calls.Load()
			res.rejections = audit.MintRejections()
			res.minted = mintedEvents(audit)
			assertScopeRefusal(t, res, tc.wantStatus, tc.wantCode, tc.wantInvariant, "", nil)

			after, err := store.GetSession(context.Background(), "acme", "sess-1")
			if err != nil {
				t.Fatalf("GetSession after refusal: %v", err)
			}
			if len(after.BearerJTIs) != 1 || after.BearerJTIs[0] != "earlier-jti" {
				t.Errorf("record BearerJTIs = %v, want unchanged [earlier-jti]", after.BearerJTIs)
			}
		})
	}
}
