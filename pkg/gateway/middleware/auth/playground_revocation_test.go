// SPDX-License-Identifier: MIT

package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	pkgauth "github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/auth/jwt"
)

// spec: §27.6 line 204 / §27.3.1 lines 95-97 — the authoritative
// per-request revocation check for origin=playground bearers.

// fakePlaygroundRevocations is an in-test PlaygroundRevocationChecker.
// It records the (tenant, jti) of the last call so a test can assert
// the check was (or was not) consulted, and replays a fixed verdict.
type fakePlaygroundRevocations struct {
	revoked    bool
	err        error
	calls      int
	lastTenant string
	lastJTI    string
}

func (f *fakePlaygroundRevocations) IsBearerRevoked(_ context.Context, tenant, jti string) (bool, error) {
	f.calls++
	f.lastTenant = tenant
	f.lastJTI = jti
	return f.revoked, f.err
}

// playgroundToken mints an HMAC bearer carrying the §27.3 origin claim
// so the auth middleware routes it through the playground revocation
// check.
func playgroundToken(t *testing.T, signer *jwt.HMACSigner, jti string, origin string) string {
	t.Helper()
	tok, err := signer.Sign(jwt.Claims{
		Subject:  "alice@acme.com",
		TenantID: "acme",
		Expiry:   time.Now().Add(time.Hour).Unix(),
		Typ:      pkgauth.TokenSessionCapability,
		JWTID:    jti,
		Origin:   origin,
	})
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	return tok
}

// A revoked playground bearer is rejected 401 with
// details.reason=bearer_revoked. spec: §27.3.1 line 95, §27 line 166.
func TestPlaygroundBearerRejectedWhenRevoked_spec_27_3_1_95(t *testing.T) {
	signer := jwt.NewHMACSigner("test", []byte("secret"))
	tok := playgroundToken(t, signer, "jti-pg-revoked", "playground")
	rev := &fakePlaygroundRevocations{revoked: true}

	inner, _ := captureHandler()
	h := Wrap(inner, Options{
		Verifier:              signer,
		MultiTenant:           true,
		Registry:              permissiveRegistry{},
		PlaygroundRevocations: rev,
	})

	req := httptest.NewRequest(http.MethodGet, "/v1/sessions", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("revoked playground bearer: status = %d, want 401; body = %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "bearer_revoked") {
		t.Errorf("rejection should carry details.reason=bearer_revoked: %s", rr.Body.String())
	}
	if rev.calls != 1 || rev.lastTenant != "acme" || rev.lastJTI != "jti-pg-revoked" {
		t.Errorf("checker call = (%d, %q, %q), want (1, acme, jti-pg-revoked)", rev.calls, rev.lastTenant, rev.lastJTI)
	}
}

// A live (non-revoked) playground bearer passes the check and reaches
// the inner handler. spec: §27.3.1 line 95.
func TestPlaygroundBearerAllowedWhenLive(t *testing.T) {
	signer := jwt.NewHMACSigner("test", []byte("secret"))
	tok := playgroundToken(t, signer, "jti-pg-live", "playground")
	rev := &fakePlaygroundRevocations{revoked: false}

	inner, got := captureHandler()
	h := Wrap(inner, Options{
		Verifier:              signer,
		MultiTenant:           true,
		Registry:              permissiveRegistry{},
		PlaygroundRevocations: rev,
	})

	req := httptest.NewRequest(http.MethodGet, "/v1/sessions", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Fatalf("live playground bearer: status = %d, want 204; body = %s", rr.Code, rr.Body.String())
	}
	if got.Subject != "alice@acme.com" {
		t.Errorf("principal subject: got %q", got.Subject)
	}
	if rev.calls != 1 {
		t.Errorf("checker calls = %d, want 1", rev.calls)
	}
}

// A backing-store error fails closed: the bearer is rejected 503
// REDIS_UNAVAILABLE rather than honored. spec: §27.3.1 line 97, §27
// line 168.
func TestPlaygroundBearerFailsClosedOnStoreError_spec_27_3_1_97(t *testing.T) {
	signer := jwt.NewHMACSigner("test", []byte("secret"))
	tok := playgroundToken(t, signer, "jti-pg-redis-down", "playground")
	rev := &fakePlaygroundRevocations{err: context.DeadlineExceeded}

	inner, _ := captureHandler()
	h := Wrap(inner, Options{
		Verifier:              signer,
		MultiTenant:           true,
		Registry:              permissiveRegistry{},
		PlaygroundRevocations: rev,
	})

	req := httptest.NewRequest(http.MethodGet, "/v1/sessions", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("store error: status = %d, want 503; body = %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "REDIS_UNAVAILABLE") {
		t.Errorf("fail-closed rejection should carry REDIS_UNAVAILABLE: %s", rr.Body.String())
	}
}

// A non-playground bearer (no origin claim) never consults the
// playground revocation check, even when one is wired. The §13.3
// revocation path is the only revocation gate for ordinary bearers.
// spec: §27.3.1 line 95 — the check keys on the origin claim.
func TestNonPlaygroundBearerSkipsPlaygroundRevocation(t *testing.T) {
	signer := jwt.NewHMACSigner("test", []byte("secret"))
	// No origin claim: a normal session-capability bearer.
	tok := playgroundToken(t, signer, "jti-normal", "")
	// The checker would reject everything if consulted.
	rev := &fakePlaygroundRevocations{revoked: true}

	inner, _ := captureHandler()
	h := Wrap(inner, Options{
		Verifier:              signer,
		MultiTenant:           true,
		Registry:              permissiveRegistry{},
		PlaygroundRevocations: rev,
	})

	req := httptest.NewRequest(http.MethodGet, "/v1/sessions", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Fatalf("non-playground bearer: status = %d, want 204; body = %s", rr.Code, rr.Body.String())
	}
	if rev.calls != 0 {
		t.Errorf("playground checker consulted %d times for a non-playground bearer, want 0", rev.calls)
	}
}

// When no playground revocation checker is wired (playground disabled),
// an origin=playground bearer still passes the auth chain — the check
// is an optional hook, not a hard dependency.
func TestPlaygroundBearerPassesWhenNoCheckerWired(t *testing.T) {
	signer := jwt.NewHMACSigner("test", []byte("secret"))
	tok := playgroundToken(t, signer, "jti-pg-nohook", "playground")

	inner, _ := captureHandler()
	h := Wrap(inner, Options{
		Verifier:    signer,
		MultiTenant: true,
		Registry:    permissiveRegistry{},
		// PlaygroundRevocations intentionally nil.
	})

	req := httptest.NewRequest(http.MethodGet, "/v1/sessions", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Fatalf("origin=playground bearer with no checker: status = %d, want 204; body = %s", rr.Code, rr.Body.String())
	}
}
