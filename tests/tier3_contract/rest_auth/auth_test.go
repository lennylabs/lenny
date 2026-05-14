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
		Expiry:   time.Now().Add(time.Hour).Unix(),
		IssuedAt: time.Now().Unix(),
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

// Single-tenant deployment: every request collapses to the
// "default" tenant per §10.2, regardless of claim contents.
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

// Multi-tenant + missing tenant claim → 401 TENANT_CLAIM_MISSING.
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

// Multi-tenant + malformed tenant claim → 401 TENANT_CLAIM_INVALID_FORMAT.
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

// Multi-tenant + unregistered tenant → 403 TENANT_NOT_FOUND.
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

// Multi-tenant + registered tenant: request proceeds with the
// authenticated tenant attached.
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

// Expired Bearer token: 401 TOKEN_EXPIRED.
func TestExpiredTokenReturns401(t *testing.T) {
	ts, signer := newTestServer(t, authmw.Options{
		MultiTenant: false,
		RequireAuth: true,
	})
	tok, _ := signer.Sign(jwt.Claims{
		Subject:  "alice",
		TenantID: "acme",
		Typ:      auth.TokenUserBearer,
		Expiry:   time.Now().Add(-time.Hour).Unix(),
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

// Tampered Bearer token: 401 TOKEN_INVALID.
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

// RequireAuth=true + no credentials: 401 AUTH_REQUIRED.
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
	if envelope["code"] != "AUTH_REQUIRED" {
		t.Errorf("error code: want AUTH_REQUIRED, got %v", envelope["code"])
	}
}

// Dev-header transport: when AllowDevHeaders is true, the
// X-Lenny-Tenant-ID header substitutes for Bearer.
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

// Dev-header with unregistered tenant still emits TENANT_NOT_FOUND.
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
