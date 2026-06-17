// SPDX-License-Identifier: MIT

//go:build contract

// Tier-3 contract tests for the §10.2 auth middleware. Exercises
// Bearer-token validation, the §10.2 tenant-claim extraction state
// machine, and the dev-header transport fallback.

package rest_auth_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/auth/jwt"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore/memstore"
)

// Token-claim anchors. The jwt package compares Expiry against real
// wall-clock during Verify, so a far-future Expiry stays valid for
// any plausible test-run timestamp; a far-past Expiry is always
// rejected.
var (
	farFutureExpiry = time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC).Unix()
	farFutureIssued = time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC).Add(-time.Hour).Unix()
	farPastExpiry   = time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC).Unix()
)

type fakeRegistry struct {
	registered map[string]bool
}

func (f *fakeRegistry) IsRegistered(tenantID string) (bool, error) {
	return f.registered[tenantID], nil
}

func newTestServer(t *testing.T, opts authmw.Options) (*httptest.Server, *jwt.HMACSigner) {
	t.Helper()
	signer := jwt.NewHMACSigner("dev-1", []byte("dev-secret"))
	opts.Verifier = signer
	store := memstore.New()
	srv := sessionserver.New(store, sessionserver.Options{})
	wrapped := authmw.Wrap(srv.Handler(), opts)
	ts := httptest.NewServer(wrapped)
	t.Cleanup(ts.Close)
	return ts, signer
}

func issueToken(t *testing.T, signer *jwt.HMACSigner, tenantID, subject string) string {
	t.Helper()
	tok, err := signer.Sign(jwt.Claims{
		Subject:  subject,
		TenantID: tenantID,
		Typ:      auth.TokenUserBearer,
		Expiry:   farFutureExpiry,
		IssuedAt: farFutureIssued,
	})
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	return tok
}

func do(t *testing.T, ts *httptest.Server, headers map[string]string, body any) (*http.Response, map[string]any) {
	t.Helper()
	var reqBody io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		reqBody = bytes.NewReader(b)
	}
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, ts.URL+"/v1/sessions", reqBody)
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var out map[string]any
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &out)
	}
	return resp, out
}

// spec: 10.2 (single-tenant deployment collapses every request to "default")
// diagnosis: A non-default tenantId on the response shows the auth
//
//	middleware respected the claim. Single-tenant mode must
//	short-circuit before claim parsing.
func TestSingleTenantIgnoresClaim(t *testing.T) {
	ts, signer := newTestServer(t, authmw.Options{
		MultiTenant: false,
		RequireAuth: true,
	})
	tok := issueToken(t, signer, "ignored", "alice@acme.com")
	resp, body := do(t, ts, map[string]string{"Authorization": "Bearer " + tok},
		map[string]any{"runtimeRef": "claude-code"})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("single-tenant accept: want 201, got %d (body %v)", resp.StatusCode, body)
	}
	if got := body["tenantId"]; got != "default" {
		t.Errorf("single-tenant tenantId: want default, got %v", got)
	}
}

// spec: 10.2 (TENANT_CLAIM_MISSING when tenant_id absent in multi-tenant mode)
// diagnosis: A 200 / 403 instead of 401 means tenant extraction is
//
//	wrong, or the envelope mapping dropped the code. Inspect
//	resolveTenant in pkg/gateway/middleware/auth.
func TestMultiTenantClaimMissingReturns401(t *testing.T) {
	registry := &fakeRegistry{registered: map[string]bool{"acme": true}}
	ts, signer := newTestServer(t, authmw.Options{
		MultiTenant: true,
		Registry:    registry,
		RequireAuth: true,
	})
	tok := issueToken(t, signer, "", "alice@acme.com")
	resp, body := do(t, ts, map[string]string{"Authorization": "Bearer " + tok},
		map[string]any{"runtimeRef": "claude-code"})
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d (body %v)", resp.StatusCode, body)
	}
	envelope, _ := body["error"].(map[string]any)
	if envelope["code"] != "TENANT_CLAIM_MISSING" {
		t.Errorf("error code: want TENANT_CLAIM_MISSING, got %v", envelope["code"])
	}
}

// spec: 10.2 (tenant id syntax: alphanumeric + hyphen, no slashes)
// diagnosis: A claim containing "/" passed through. The tenant-id
//
//	format check must run before registry lookup; check
//	the regex in jwt.Claims.TenantID validation.
func TestMultiTenantClaimBadFormatReturns401(t *testing.T) {
	registry := &fakeRegistry{registered: map[string]bool{"acme": true}}
	ts, signer := newTestServer(t, authmw.Options{
		MultiTenant: true,
		Registry:    registry,
		RequireAuth: true,
	})
	tok := issueToken(t, signer, "acme/bad", "alice@acme.com")
	resp, body := do(t, ts, map[string]string{"Authorization": "Bearer " + tok},
		map[string]any{"runtimeRef": "claude-code"})
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d (body %v)", resp.StatusCode, body)
	}
	envelope, _ := body["error"].(map[string]any)
	if envelope["code"] != "TENANT_CLAIM_INVALID_FORMAT" {
		t.Errorf("error code: want TENANT_CLAIM_INVALID_FORMAT, got %v", envelope["code"])
	}
}

// spec: 10.2 (TENANT_NOT_FOUND when registry lookup fails)
// diagnosis: An unregistered tenant id was accepted. The Registry
//
//	interface returned true for an unknown tenant or the
//	middleware skipped the lookup.
func TestMultiTenantUnregisteredReturns403(t *testing.T) {
	registry := &fakeRegistry{registered: map[string]bool{"acme": true}}
	ts, signer := newTestServer(t, authmw.Options{
		MultiTenant: true,
		Registry:    registry,
		RequireAuth: true,
	})
	tok := issueToken(t, signer, "globex", "alice@globex.com")
	resp, body := do(t, ts, map[string]string{"Authorization": "Bearer " + tok},
		map[string]any{"runtimeRef": "claude-code"})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("want 403, got %d (body %v)", resp.StatusCode, body)
	}
	envelope, _ := body["error"].(map[string]any)
	if envelope["code"] != "TENANT_NOT_FOUND" {
		t.Errorf("error code: want TENANT_NOT_FOUND, got %v", envelope["code"])
	}
}

// spec: 10.2 (happy path: registered tenant id propagates through to the response)
// diagnosis: The response carried the wrong tenantId. The Principal
//
//	is either not being attached to the request context or
//	sessionserver did not read it back out via auth.FromContext.
func TestMultiTenantRegisteredAccepted(t *testing.T) {
	registry := &fakeRegistry{registered: map[string]bool{"acme": true}}
	ts, signer := newTestServer(t, authmw.Options{
		MultiTenant: true,
		Registry:    registry,
		RequireAuth: true,
	})
	tok := issueToken(t, signer, "acme", "alice@acme.com")
	resp, body := do(t, ts, map[string]string{"Authorization": "Bearer " + tok},
		map[string]any{"runtimeRef": "claude-code"})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("want 201, got %d (body %v)", resp.StatusCode, body)
	}
	if got := body["tenantId"]; got != "acme" {
		t.Errorf("tenantId: want acme, got %v", got)
	}
}

// spec: 10.2 / 13.3 (token expiry with ±1s skew on the exp check)
// diagnosis: An expired token was accepted. Inspect Verify() in
//
//	pkg/auth/jwt and the VerifyError.Reason mapping in the
//	auth middleware.
func TestExpiredTokenReturns401(t *testing.T) {
	ts, signer := newTestServer(t, authmw.Options{
		MultiTenant: false,
		RequireAuth: true,
	})
	tok, _ := signer.Sign(jwt.Claims{
		Subject:  "alice",
		TenantID: "acme",
		Typ:      auth.TokenUserBearer,
		Expiry:   farPastExpiry,
	})
	resp, body := do(t, ts, map[string]string{"Authorization": "Bearer " + tok},
		map[string]any{"runtimeRef": "claude-code"})
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d (body %v)", resp.StatusCode, body)
	}
	envelope, _ := body["error"].(map[string]any)
	if envelope["code"] != "TOKEN_EXPIRED" {
		t.Errorf("error code: want TOKEN_EXPIRED, got %v", envelope["code"])
	}
}

// spec: 10.2 (HMAC signature must verify byte-for-byte)
// diagnosis: A modified signature was accepted. Check the constant-
//
//	time comparison in pkg/auth/jwt.HMACSigner.Verify.
func TestTamperedTokenReturns401(t *testing.T) {
	ts, signer := newTestServer(t, authmw.Options{
		MultiTenant: false,
		RequireAuth: true,
	})
	tok := issueToken(t, signer, "acme", "alice")
	// Flip the last char of the signature.
	tampered := tok[:len(tok)-1] + "X"
	resp, body := do(t, ts, map[string]string{"Authorization": "Bearer " + tampered},
		map[string]any{"runtimeRef": "claude-code"})
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", resp.StatusCode)
	}
	envelope, _ := body["error"].(map[string]any)
	if envelope["code"] != "TOKEN_INVALID" {
		t.Errorf("error code: want TOKEN_INVALID, got %v", envelope["code"])
	}
}

// spec: §10.2 + §15.1 line 986 — a no-credentials request must reject
// with the canonical UNAUTHORIZED code (401) and carry details.reason
// = "auth_required" so callers can discriminate the missing-bearer
// case while scripting against the §15.1 catalog.
// diagnosis: a failure means a no-credentials request is not rejected
// with the canonical UNAUTHORIZED code and auth_required reason, so the
// missing-bearer case would be indistinguishable from other auth errors.
func TestNoCredentialsRejectedWhenAuthRequired(t *testing.T) {
	ts, _ := newTestServer(t, authmw.Options{
		MultiTenant: false,
		RequireAuth: true,
	})
	resp, body := do(t, ts, nil, map[string]any{"runtimeRef": "claude-code"})
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", resp.StatusCode)
	}
	envelope, _ := body["error"].(map[string]any)
	if envelope["code"] != "UNAUTHORIZED" {
		t.Errorf("error code: want UNAUTHORIZED, got %v", envelope["code"])
	}
	details, _ := envelope["details"].(map[string]any)
	if details == nil || details["reason"] != "auth_required" {
		t.Errorf("details.reason: want auth_required, got %v", details)
	}
}

// spec: 10.2 (AllowDevHeaders allows X-Lenny-Tenant-ID in place of Bearer)
// diagnosis: The dev header was either ignored or accepted without
//
//	registry lookup. Dev mode must still consult the Registry.
func TestDevHeaderTransportAccepted(t *testing.T) {
	ts, _ := newTestServer(t, authmw.Options{
		MultiTenant:     true,
		Registry:        &fakeRegistry{registered: map[string]bool{"acme": true}},
		AllowDevHeaders: true,
	})
	resp, body := do(t, ts, map[string]string{"X-Lenny-Tenant-ID": "acme"},
		map[string]any{"runtimeRef": "claude-code"})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("dev-header accept: want 201, got %d (body %v)", resp.StatusCode, body)
	}
	if got := body["tenantId"]; got != "acme" {
		t.Errorf("tenantId: want acme, got %v", got)
	}
}

// spec: 10.2 (dev-header transport still enforces registry lookup)
// diagnosis: The dev header bypassed the registry. AllowDevHeaders
//
//	must apply the same TENANT_NOT_FOUND check as Bearer.
func TestDevHeaderUnregisteredReturns403(t *testing.T) {
	ts, _ := newTestServer(t, authmw.Options{
		MultiTenant:     true,
		Registry:        &fakeRegistry{registered: map[string]bool{"acme": true}},
		AllowDevHeaders: true,
	})
	resp, body := do(t, ts, map[string]string{"X-Lenny-Tenant-ID": "globex"},
		map[string]any{"runtimeRef": "claude-code"})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("want 403, got %d", resp.StatusCode)
	}
	envelope, _ := body["error"].(map[string]any)
	if envelope["code"] != "TENANT_NOT_FOUND" {
		t.Errorf("error code: want TENANT_NOT_FOUND, got %v", envelope["code"])
	}
}
