// SPDX-License-Identifier: MIT

package tokenservice

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/audit"
	"github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/auth/jwt"
	"github.com/lennylabs/lenny/pkg/gateway/issuedtokenstore"
	obsaudit "github.com/lennylabs/lenny/pkg/observability/audit"
)

// recordingAuditor captures audit appends for assertion.
type recordingAuditor struct {
	mu   sync.Mutex
	rows []recordedAudit
}

type recordedAudit struct {
	TenantID  string
	EventType string
	Payload   json.RawMessage
	At        time.Time
}

func (r *recordingAuditor) Append(_ context.Context, tenantID, eventType string, payload json.RawMessage, at time.Time) (audit.Row, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.rows = append(r.rows, recordedAudit{
		TenantID: tenantID, EventType: eventType, Payload: append(json.RawMessage(nil), payload...), At: at,
	})
	return audit.Row{Seq: uint64(len(r.rows)), TenantID: tenantID, EventType: eventType,
		Payload: payload, Timestamp: at}, nil
}

func (r *recordingAuditor) snapshot() []recordedAudit {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]recordedAudit, len(r.rows))
	copy(out, r.rows)
	return out
}

// recordingMetrics counts metric emissions for assertion.
type recordingMetrics struct {
	mu                  sync.Mutex
	requestDurations    []string
	errors              []string
	rateLimited         []string
	rateLimitedSampled  []string
}

func (m *recordingMetrics) RecordRequestDuration(op string, _ time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.requestDurations = append(m.requestDurations, op)
}
func (m *recordingMetrics) IncErrors(op, class string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.errors = append(m.errors, op+":"+class)
}
func (m *recordingMetrics) IncRateLimited(tier string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rateLimited = append(m.rateLimited, tier)
}
func (m *recordingMetrics) IncRateLimitedSampled(tier string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rateLimitedSampled = append(m.rateLimitedSampled, tier)
}

// recordingIssuedTokenStore records what was stored. Implements
// IssuedTokenStore (not IssuedTokenAuditStore) so the handler takes
// the in-memory dev path that calls Auditor.
type recordingIssuedTokenStore struct {
	mu      sync.Mutex
	records []issuedtokenstore.IssuedToken
}

func (s *recordingIssuedTokenStore) Record(_ context.Context, tok issuedtokenstore.IssuedToken) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.records = append(s.records, tok)
	return nil
}

func (s *recordingIssuedTokenStore) snapshot() []issuedtokenstore.IssuedToken {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]issuedtokenstore.IssuedToken, len(s.records))
	copy(out, s.records)
	return out
}

// txStore satisfies IssuedTokenAuditStore. The handler binds the
// issued-token record and the audit row through it (the Postgres
// path). The test asserts the handler takes that path when the store
// implements the interface.
type txStore struct {
	mu      sync.Mutex
	records []issuedtokenstore.IssuedToken
	audits  []recordedAudit
	failAt  string
}

func (t *txStore) Record(_ context.Context, tok issuedtokenstore.IssuedToken) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.records = append(t.records, tok)
	return nil
}

func (t *txStore) RecordWithAudit(_ context.Context, tok issuedtokenstore.IssuedToken,
	eventType string, payload json.RawMessage, at time.Time) (audit.Row, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.failAt == "audit" {
		return audit.Row{}, &txStoreErr{"audit insert failed"}
	}
	if t.failAt == "issued" {
		return audit.Row{}, &txStoreErr{"issued insert failed"}
	}
	t.audits = append(t.audits, recordedAudit{TenantID: tok.TenantID,
		EventType: eventType, Payload: append(json.RawMessage(nil), payload...), At: at})
	t.records = append(t.records, tok)
	return audit.Row{Seq: uint64(len(t.audits)), TenantID: tok.TenantID,
		EventType: eventType, Payload: payload, Timestamp: at}, nil
}

func (t *txStore) snapshot() ([]issuedtokenstore.IssuedToken, []recordedAudit) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]issuedtokenstore.IssuedToken(nil), t.records...),
		append([]recordedAudit(nil), t.audits...)
}

type txStoreErr struct{ msg string }

func (e *txStoreErr) Error() string { return e.msg }

func newAuditTestServer(t *testing.T) (*Server, *jwt.HMACSigner) {
	t.Helper()
	signer := jwt.NewHMACSigner("dev-1", []byte("dev-secret"))
	return NewServer(Options{
		Signer: signer,
		Issuer: "https://lenny.dev.test/token",
	}), signer
}

func mintToken(t *testing.T, signer *jwt.HMACSigner, c jwt.Claims) string {
	t.Helper()
	farFuture := time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC).Unix()
	if c.Expiry == 0 {
		c.Expiry = farFuture
	}
	if c.IssuedAt == 0 {
		c.IssuedAt = time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC).Add(-time.Hour).Unix()
	}
	if c.Audience == nil {
		c.Audience = []string{"lenny-gateway"}
	}
	tok, err := signer.Sign(c)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	return tok
}

// spec: §13.3 line 587 — every accepted exchange emits one
// token.exchanged audit row with policy_result=accepted and the jti.
func TestHandlerEmitsTokenExchangedAudit_InMemoryPath(t *testing.T) {
	signer := jwt.NewHMACSigner("dev-1", []byte("dev-secret"))
	auditor := &recordingAuditor{}
	store := &recordingIssuedTokenStore{}
	srv := NewServer(Options{
		Signer:       signer,
		Issuer:       "https://lenny.dev.test/token",
		IssuedTokens: store,
		Auditor:      auditor,
	})
	callerTok := mintToken(t, signer, jwt.Claims{
		Subject: "alice@acme.com", TenantID: "acme", Typ: auth.TokenUserBearer,
		Scope: "sessions:read", Audience: []string{"lenny-gateway"},
	})
	subjectTok := callerTok // self-rotation

	resp := doExchange(t, srv, callerTok, Request{
		GrantType: grantTypeExchange, SubjectToken: subjectTok,
		Scope: "sessions:read", Audience: "lenny-gateway",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d, want 200", resp.StatusCode)
	}

	rows := auditor.snapshot()
	if len(rows) != 1 {
		t.Fatalf("audit rows = %d, want 1", len(rows))
	}
	r := rows[0]
	if r.TenantID != "acme" {
		t.Errorf("tenant=%q, want %q", r.TenantID, "acme")
	}
	if r.EventType != string(obsaudit.EventTokenExchanged) {
		t.Errorf("event_type=%q, want %q", r.EventType, obsaudit.EventTokenExchanged)
	}
	var payload exchangeAuditPayload
	if err := json.Unmarshal(r.Payload, &payload); err != nil {
		t.Fatalf("payload unmarshal: %v", err)
	}
	if payload.PolicyResult != "accepted" {
		t.Errorf("policy_result=%q, want accepted", payload.PolicyResult)
	}
	if payload.JTI == "" {
		t.Errorf("payload missing jti")
	}
	// §13.3 line 587 — no raw tokens in audit body.
	if bytes.Contains(r.Payload, []byte(subjectTok)) {
		t.Errorf("audit payload leaked raw subject_token")
	}
}

// spec: §13.3 line 589 — when the IssuedTokenStore is also an
// IssuedTokenAuditStore the handler binds the issued_tokens INSERT
// and the audit_log INSERT in one transactional call. The audit row
// from the in-memory Auditor must NOT be additionally produced (the
// Postgres path covers it).
func TestHandlerUsesTxStorePathWhenAvailable(t *testing.T) {
	signer := jwt.NewHMACSigner("dev-1", []byte("dev-secret"))
	auditor := &recordingAuditor{}
	tx := &txStore{}
	srv := NewServer(Options{
		Signer:       signer,
		Issuer:       "https://lenny.dev.test/token",
		IssuedTokens: tx,
		Auditor:      auditor,
	})
	callerTok := mintToken(t, signer, jwt.Claims{
		Subject: "alice@acme.com", TenantID: "acme", Typ: auth.TokenUserBearer,
		Scope: "sessions:read", Audience: []string{"lenny-gateway"},
	})
	resp := doExchange(t, srv, callerTok, Request{
		GrantType: grantTypeExchange, SubjectToken: callerTok,
		Scope: "sessions:read", Audience: "lenny-gateway",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d, want 200", resp.StatusCode)
	}
	records, audits := tx.snapshot()
	if len(records) != 1 {
		t.Fatalf("tx.records = %d, want 1 (handler should take tx path)", len(records))
	}
	if len(audits) != 1 {
		t.Fatalf("tx.audits = %d, want 1", len(audits))
	}
	if audits[0].EventType != string(obsaudit.EventTokenExchanged) {
		t.Errorf("event_type=%q, want %q", audits[0].EventType, obsaudit.EventTokenExchanged)
	}
	// In-memory auditor must NOT be touched on the success path when
	// tx-store covers it.
	if got := auditor.snapshot(); len(got) != 0 {
		t.Errorf("in-memory auditor saw %d rows on tx path; want 0", len(got))
	}
}

// spec: §13.3 line 589 — write-before-issue fail-closed: when the
// tx-store INSERT fails, the handler returns 500 and does NOT return
// the signed token to the caller.
func TestHandlerTxFailureReturns500NoToken(t *testing.T) {
	signer := jwt.NewHMACSigner("dev-1", []byte("dev-secret"))
	tx := &txStore{failAt: "audit"}
	srv := NewServer(Options{
		Signer:       signer,
		Issuer:       "https://lenny.dev.test/token",
		IssuedTokens: tx,
	})
	callerTok := mintToken(t, signer, jwt.Claims{
		Subject: "alice@acme.com", TenantID: "acme", Typ: auth.TokenUserBearer,
		Scope: "sessions:read", Audience: []string{"lenny-gateway"},
	})
	resp := doExchange(t, srv, callerTok, Request{
		GrantType: grantTypeExchange, SubjectToken: callerTok,
		Scope: "sessions:read", Audience: "lenny-gateway",
	})
	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("status=%d, want 500", resp.StatusCode)
	}
	// The handler must not surface the access_token on the failure
	// path. Decode the body and confirm.
	var body map[string]any
	if resp.Body != nil {
		_ = json.NewDecoder(resp.Body).Decode(&body)
		resp.Body.Close()
	}
	if _, hasToken := body["access_token"]; hasToken {
		t.Errorf("500 response leaked access_token; body=%v", body)
	}
}

// spec: §13.3 line 585 — rejected exchanges emit a token.exchanged
// audit row with policy_result=rejected:<reason>.
func TestHandlerEmitsRejectionAuditOnInvalidScope(t *testing.T) {
	signer := jwt.NewHMACSigner("dev-1", []byte("dev-secret"))
	auditor := &recordingAuditor{}
	srv := NewServer(Options{
		Signer:  signer,
		Issuer:  "https://lenny.dev.test/token",
		Auditor: auditor,
	})
	callerTok := mintToken(t, signer, jwt.Claims{
		Subject: "alice@acme.com", TenantID: "acme", Typ: auth.TokenUserBearer,
		Scope: "sessions:read", Audience: []string{"lenny-gateway"},
	})
	resp := doExchange(t, srv, callerTok, Request{
		GrantType: grantTypeExchange, SubjectToken: callerTok,
		// Broader scope than subject — must reject.
		Scope: "sessions:write", Audience: "lenny-gateway",
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status=%d, want 400", resp.StatusCode)
	}
	rows := auditor.snapshot()
	if len(rows) != 1 {
		t.Fatalf("audit rows=%d, want 1", len(rows))
	}
	var p exchangeAuditPayload
	_ = json.Unmarshal(rows[0].Payload, &p)
	if !strings.HasPrefix(p.PolicyResult, "rejected:") {
		t.Errorf("policy_result=%q, want rejected:", p.PolicyResult)
	}
}

// spec: §13.3 line 607 — rate limit returns 429 with Retry-After and
// emits both the unconditional counter and the sampled audit row.
func TestHandlerRateLimitedReturns429AndAudits(t *testing.T) {
	signer := jwt.NewHMACSigner("dev-1", []byte("dev-secret"))
	auditor := &recordingAuditor{}
	metrics := &recordingMetrics{}
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	srv := NewServer(Options{
		Signer:    signer,
		Issuer:    "https://lenny.dev.test/token",
		Auditor:   auditor,
		Metrics:   metrics,
		RateLimit: RateLimitOptions{CallerPerSecond: 1, SampleWindow: 10 * time.Second},
		Now:       func() time.Time { return now },
	})
	callerTok := mintToken(t, signer, jwt.Claims{
		Subject: "alice@acme.com", TenantID: "acme", Typ: auth.TokenUserBearer,
		Scope: "sessions:read", Audience: []string{"lenny-gateway"},
	})

	// First request lands.
	first := doExchange(t, srv, callerTok, Request{
		GrantType: grantTypeExchange, SubjectToken: callerTok,
		Scope: "sessions:read", Audience: "lenny-gateway",
	})
	if first.StatusCode != http.StatusOK {
		t.Fatalf("first call status=%d", first.StatusCode)
	}
	// Second request rate-limited.
	second := doExchange(t, srv, callerTok, Request{
		GrantType: grantTypeExchange, SubjectToken: callerTok,
		Scope: "sessions:read", Audience: "lenny-gateway",
	})
	if second.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("second call status=%d, want 429", second.StatusCode)
	}
	if ra := second.Header.Get("Retry-After"); ra == "" || ra == "0" {
		t.Errorf("Retry-After=%q, want >0", ra)
	}
	// Audit body has token.exchange_rate_limited.
	var sawRateLimitAudit bool
	for _, r := range auditor.snapshot() {
		if r.EventType == string(obsaudit.EventTokenExchangeRateLimited) {
			sawRateLimitAudit = true
		}
	}
	if !sawRateLimitAudit {
		t.Errorf("no token.exchange_rate_limited audit row emitted; rows=%v", auditor.snapshot())
	}
	if len(metrics.rateLimited) == 0 {
		t.Errorf("rate_limited counter not incremented")
	}
	if len(metrics.rateLimitedSampled) == 0 {
		t.Errorf("rate_limited_sampled counter not incremented")
	}
}

func doExchange(t *testing.T, srv *Server, callerTok string, body Request) *http.Response {
	t.Helper()
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/v1/oauth/token", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+callerTok)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	return w.Result()
}
