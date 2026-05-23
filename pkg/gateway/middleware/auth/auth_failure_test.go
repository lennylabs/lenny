// SPDX-License-Identifier: MIT

package auth

import (
	"bytes"
	"context"
	"errors"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	pkgauth "github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/auth/jwt"
)

// recordingSink captures every AuthFailureEvent the middleware emits.
type recordingSink struct {
	mu     sync.Mutex
	events []AuthFailureEvent
}

func (s *recordingSink) EmitAuthFailure(_ context.Context, e AuthFailureEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, e)
}

func (s *recordingSink) snapshot() []AuthFailureEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]AuthFailureEvent, len(s.events))
	copy(out, s.events)
	return out
}

// captureLog redirects the standard logger to buf for the duration of
// the test and restores it on cleanup. Used to assert the §4.2 line 185
// INFO log line fires alongside the audit emission.
func captureLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	buf := &bytes.Buffer{}
	prev := log.Writer()
	prevFlags := log.Flags()
	log.SetOutput(buf)
	log.SetFlags(0)
	t.Cleanup(func() {
		log.SetOutput(prev)
		log.SetFlags(prevFlags)
	})
	return buf
}

type missingRegistry struct{}

func (missingRegistry) IsRegistered(string) (bool, error) { return false, nil }

type erroringRegistry struct{}

func (erroringRegistry) IsRegistered(string) (bool, error) { return false, errors.New("lookup failed") }

// spec: §4.2 line 185 — TENANT_CLAIM_MISSING rejection emits an
// auth_failure audit event and an INFO log line carrying user_id + jti.
func TestAuthFailureClaimMissingEmitsAuditAndLog(t *testing.T) {
	signer := jwt.NewHMACSigner("test", []byte("secret"))
	// Bearer with no tenant_id claim under multi-tenant mode.
	tok, err := signer.Sign(jwt.Claims{
		Subject: "alice@acme.com",
		JWTID:   "jti-claim-missing",
		Expiry:  time.Now().Add(time.Hour).Unix(),
		Typ:     pkgauth.TokenUserBearer,
	})
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	sink := &recordingSink{}
	logBuf := captureLog(t)
	now := time.Date(2026, 5, 23, 12, 0, 0, 0, time.UTC)
	inner, _ := captureHandler()
	h := Wrap(inner, Options{
		Verifier:        signer,
		MultiTenant:     true,
		Registry:        permissiveRegistry{},
		AuthFailureSink: sink,
		Clock:           func() time.Time { return now },
	})

	req := httptest.NewRequest(http.MethodGet, "/v1/sessions", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401; body = %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "TENANT_CLAIM_MISSING") {
		t.Errorf("body should carry TENANT_CLAIM_MISSING envelope: %s", rr.Body.String())
	}
	events := sink.snapshot()
	if len(events) != 1 {
		t.Fatalf("audit emission count: got %d, want 1", len(events))
	}
	got := events[0]
	if got.Reason != "TENANT_CLAIM_MISSING" {
		t.Errorf("event.Reason = %q, want TENANT_CLAIM_MISSING", got.Reason)
	}
	if got.UserID != "alice@acme.com" {
		t.Errorf("event.UserID = %q, want alice@acme.com", got.UserID)
	}
	if got.JTI != "jti-claim-missing" {
		t.Errorf("event.JTI = %q, want jti-claim-missing", got.JTI)
	}
	if got.TenantID != "" {
		t.Errorf("event.TenantID = %q, want empty (no claim present)", got.TenantID)
	}
	if !got.At.Equal(now) {
		t.Errorf("event.At = %v, want %v", got.At, now)
	}

	line := logBuf.String()
	if !strings.Contains(line, "TENANT_CLAIM_MISSING") {
		t.Errorf("INFO log should name the rejection reason: %q", line)
	}
	if !strings.Contains(line, `user_id="alice@acme.com"`) {
		t.Errorf("INFO log should carry user_id for traceability: %q", line)
	}
	if !strings.Contains(line, `jti="jti-claim-missing"`) {
		t.Errorf("INFO log should carry jti for traceability: %q", line)
	}
}

// spec: §4.2 line 185 — TENANT_NOT_FOUND rejection emits an
// auth_failure audit event carrying the inferred tenant id.
func TestAuthFailureTenantNotFoundEmitsAuditAndLog(t *testing.T) {
	signer := jwt.NewHMACSigner("test", []byte("secret"))
	tok, err := signer.Sign(jwt.Claims{
		Subject:  "bob@globex.com",
		TenantID: "globex",
		JWTID:    "jti-not-found",
		Expiry:   time.Now().Add(time.Hour).Unix(),
		Typ:      pkgauth.TokenUserBearer,
	})
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	sink := &recordingSink{}
	logBuf := captureLog(t)
	inner, _ := captureHandler()
	h := Wrap(inner, Options{
		Verifier:        signer,
		MultiTenant:     true,
		Registry:        missingRegistry{},
		AuthFailureSink: sink,
	})

	req := httptest.NewRequest(http.MethodGet, "/v1/sessions", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body = %s", rr.Code, rr.Body.String())
	}
	events := sink.snapshot()
	if len(events) != 1 {
		t.Fatalf("audit emission count: got %d, want 1", len(events))
	}
	if events[0].Reason != "TENANT_NOT_FOUND" {
		t.Errorf("event.Reason = %q, want TENANT_NOT_FOUND", events[0].Reason)
	}
	if events[0].TenantID != "globex" {
		t.Errorf("event.TenantID = %q, want globex (inferred from claim)", events[0].TenantID)
	}
	if events[0].UserID != "bob@globex.com" || events[0].JTI != "jti-not-found" {
		t.Errorf("event.UserID/JTI: got %q/%q", events[0].UserID, events[0].JTI)
	}
	if !strings.Contains(logBuf.String(), "TENANT_NOT_FOUND") {
		t.Errorf("INFO log should name TENANT_NOT_FOUND: %q", logBuf.String())
	}
}

// spec: §4.2 line 185 — TENANT_CLAIM_INVALID_FORMAT rejection emits an
// auth_failure audit event carrying the rejected claim value.
func TestAuthFailureClaimInvalidFormatEmitsAuditAndLog(t *testing.T) {
	signer := jwt.NewHMACSigner("test", []byte("secret"))
	// The §10.2 tenant-id pattern rejects spaces; use one to trigger
	// the format error.
	tok, err := signer.Sign(jwt.Claims{
		Subject:  "carol@initech.com",
		TenantID: "bad tenant",
		JWTID:    "jti-bad-format",
		Expiry:   time.Now().Add(time.Hour).Unix(),
		Typ:      pkgauth.TokenUserBearer,
	})
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	sink := &recordingSink{}
	logBuf := captureLog(t)
	inner, _ := captureHandler()
	h := Wrap(inner, Options{
		Verifier:        signer,
		MultiTenant:     true,
		Registry:        permissiveRegistry{},
		AuthFailureSink: sink,
	})

	req := httptest.NewRequest(http.MethodGet, "/v1/sessions", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401; body = %s", rr.Code, rr.Body.String())
	}
	events := sink.snapshot()
	if len(events) != 1 {
		t.Fatalf("audit emission count: got %d, want 1", len(events))
	}
	if events[0].Reason != "TENANT_CLAIM_INVALID_FORMAT" {
		t.Errorf("event.Reason = %q, want TENANT_CLAIM_INVALID_FORMAT", events[0].Reason)
	}
	if events[0].TenantID != "bad tenant" {
		t.Errorf("event.TenantID = %q, want bad tenant (the rejected value)", events[0].TenantID)
	}
	if !strings.Contains(logBuf.String(), "TENANT_CLAIM_INVALID_FORMAT") {
		t.Errorf("INFO log should name TENANT_CLAIM_INVALID_FORMAT: %q", logBuf.String())
	}
}

// spec: §4.2 line 185 — the dev-header transport also emits the audit
// event so the observability contract is uniform across transports.
func TestAuthFailureDevHeadersTransportEmitsAudit(t *testing.T) {
	sink := &recordingSink{}
	logBuf := captureLog(t)
	inner, _ := captureHandler()
	h := Wrap(inner, Options{
		AllowDevHeaders: true,
		MultiTenant:     true,
		Registry:        missingRegistry{},
		AuthFailureSink: sink,
	})

	req := httptest.NewRequest(http.MethodGet, "/v1/sessions", nil)
	req.Header.Set("X-Lenny-Tenant-ID", "ghost")
	req.Header.Set("X-Lenny-User-ID", "dave@acme.com")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body = %s", rr.Code, rr.Body.String())
	}
	events := sink.snapshot()
	if len(events) != 1 {
		t.Fatalf("audit emission count: got %d, want 1", len(events))
	}
	got := events[0]
	if got.Reason != "TENANT_NOT_FOUND" || got.TenantID != "ghost" || got.UserID != "dave@acme.com" {
		t.Errorf("event = %+v, want Reason=TENANT_NOT_FOUND TenantID=ghost UserID=dave@acme.com", got)
	}
	if got.JTI != "" {
		t.Errorf("dev-header path carries no JTI; got %q", got.JTI)
	}
	if !strings.Contains(logBuf.String(), "TENANT_NOT_FOUND") {
		t.Errorf("INFO log should name TENANT_NOT_FOUND: %q", logBuf.String())
	}
}

// spec: §4.2 line 185 — a tenant claim that the registry can resolve
// produces no audit_failure event (the contract is rejection-only).
func TestAuthFailureNoEventOnSuccess(t *testing.T) {
	signer := jwt.NewHMACSigner("test", []byte("secret"))
	tok, err := signer.Sign(jwt.Claims{
		Subject:  "eve@acme.com",
		TenantID: "acme",
		JWTID:    "jti-ok",
		Expiry:   time.Now().Add(time.Hour).Unix(),
		Typ:      pkgauth.TokenUserBearer,
	})
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	sink := &recordingSink{}
	inner, _ := captureHandler()
	h := Wrap(inner, Options{
		Verifier:        signer,
		MultiTenant:     true,
		Registry:        permissiveRegistry{},
		AuthFailureSink: sink,
	})

	req := httptest.NewRequest(http.MethodGet, "/v1/sessions", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body = %s", rr.Code, rr.Body.String())
	}
	if got := sink.snapshot(); len(got) != 0 {
		t.Errorf("audit emission on success: got %d, want 0", len(got))
	}
}

// spec: §4.2 line 185 — internal registry errors fall through to a 500
// envelope and do NOT emit an audit_failure event (the spec scopes
// auth_failure to the three rejection reasons, not transport-level
// outages).
func TestAuthFailureNoEventOnRegistryError(t *testing.T) {
	signer := jwt.NewHMACSigner("test", []byte("secret"))
	tok, err := signer.Sign(jwt.Claims{
		Subject:  "frank@acme.com",
		TenantID: "acme",
		JWTID:    "jti-reg-err",
		Expiry:   time.Now().Add(time.Hour).Unix(),
		Typ:      pkgauth.TokenUserBearer,
	})
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	sink := &recordingSink{}
	inner, _ := captureHandler()
	h := Wrap(inner, Options{
		Verifier:        signer,
		MultiTenant:     true,
		Registry:        erroringRegistry{},
		AuthFailureSink: sink,
	})

	req := httptest.NewRequest(http.MethodGet, "/v1/sessions", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500; body = %s", rr.Code, rr.Body.String())
	}
	if got := sink.snapshot(); len(got) != 0 {
		t.Errorf("audit emission on transport error: got %d, want 0", len(got))
	}
}
