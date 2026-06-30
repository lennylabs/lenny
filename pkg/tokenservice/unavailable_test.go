// SPDX-License-Identifier: MIT

package tokenservice

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/lennylabs/lenny/pkg/audit"
	"github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/auth/jwt"
)

// spec: §13.3 line 591 — during a Postgres outage token issuance is
// unavailable by design: the caller receives 503 token_store_unavailable
// and the §16.5 TokenStoreUnavailable alert fires
// (lenny_oauth_token_5xx_total{error_type="token_store_unavailable"}).
// F-13.3.4.
func TestHandlerPostgresOutageReturns503TokenStoreUnavailable_F1334(t *testing.T) {
	signer := jwt.NewHMACSigner("dev-1", []byte("dev-secret"))
	metrics := &recordingMetrics{}
	// A connection-exception PgError stands in for a primary that is
	// unreachable or in failover.
	tx := &txStore{failErr: &pgconn.PgError{Code: "08006", Message: "connection failure"}}
	srv := NewServer(Options{
		Signer:       signer,
		Issuer:       "https://lenny.dev.test/token",
		IssuedTokens: tx,
		Metrics:      metrics,
	})
	callerTok := mintToken(t, signer, jwt.Claims{
		Subject: "alice@acme.com", TenantID: "acme", Typ: auth.TokenUserBearer,
		Scope: "sessions:read", Audience: []string{"lenny-gateway"},
	})
	resp := doExchange(t, srv, callerTok, Request{
		GrantType: grantTypeExchange, SubjectToken: callerTok,
		Scope: "sessions:read", Audience: "lenny-gateway",
	})
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status=%d, want 503", resp.StatusCode)
	}
	if ra := resp.Header.Get("Retry-After"); ra == "" || ra == "0" {
		t.Errorf("Retry-After=%q, want >0", ra)
	}
	var body map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&body)
	resp.Body.Close()
	if body["error"] != "token_store_unavailable" {
		t.Errorf("error=%v, want token_store_unavailable", body["error"])
	}
	if _, leaked := body["access_token"]; leaked {
		t.Errorf("503 response leaked access_token; body=%v", body)
	}
	// The §16.1 5xx counter must carry the token_store_unavailable label so
	// the alert can fire.
	if !containsStr(metrics.fivexx, "token_store_unavailable") {
		t.Errorf("lenny_oauth_token_5xx_total not incremented for token_store_unavailable; got %v", metrics.fivexx)
	}
}

// A non-connectivity write-before-issue store failure is a
// 500 token_exchange_failed: the write-before-issue invariant could not
// be satisfied so no token is issued. The class must not be
// token_store_unavailable (a constraint violation is not an outage), and
// it must still feed the §16.1 5xx counter so a genuine store-failure 500
// is visible to the TokenStoreUnavailable alert.
// spec: §13.3 line 589 (500 token_exchange_failed on a failed exchange
// write), §16.1 line 243 (lenny_oauth_token_5xx_total carries every 5xx
// class). F-13.3.4.
func TestHandlerNonOutageStoreFailureReturns500_F1334(t *testing.T) {
	signer := jwt.NewHMACSigner("dev-1", []byte("dev-secret"))
	metrics := &recordingMetrics{}
	tx := &txStore{failErr: &txStoreErr{"constraint violation"}}
	srv := NewServer(Options{
		Signer:       signer,
		Issuer:       "https://lenny.dev.test/token",
		IssuedTokens: tx,
		Metrics:      metrics,
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
		t.Fatalf("status=%d, want 500", resp.StatusCode)
	}
	var body map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&body)
	resp.Body.Close()
	if body["error"] != "token_exchange_failed" {
		t.Errorf("error=%v, want token_exchange_failed", body["error"])
	}
	if !containsStr(metrics.fivexx, "token_exchange_failed") {
		t.Errorf("lenny_oauth_token_5xx_total not incremented for token_exchange_failed; got %v", metrics.fivexx)
	}
	if containsStr(metrics.fivexx, "token_store_unavailable") {
		t.Errorf("a constraint violation must not be classified token_store_unavailable; got %v", metrics.fivexx)
	}
}

// failingAuditor returns an error from Append, standing in for a
// transient durable-auditstore outage on the rejection-audit write path.
type failingAuditor struct{ err error }

func (f *failingAuditor) Append(context.Context, string, string, json.RawMessage, time.Time) (audit.Row, error) {
	return audit.Row{}, f.err
}

// When the rejection-audit write fails on a rejected exchange, the Token
// Service must fail closed: return 500 token_exchange_failed with the
// originally intended rejection reason in the error body's detail rather
// than the 4xx the client could retry, so the rejection leaves a durable
// signal (the 5xx telemetry) and operators can reconstruct the attempt.
// Pre-fix this path discarded the auditor.Append error and returned the
// 4xx, silently swallowing the rejection.
// spec: §13.3 line 589 (500 token_exchange_failed on a failed
// rejection-audit write), §16.1 line 243 (lenny_oauth_token_5xx_total
// carries every 5xx class).
// diagnosis: a failure here means a rejected token exchange whose audit
// write fails is returned as a retryable 4xx with no durable record,
// reopening the silent-swallow fail-open the §13.3 write-before-issue
// ordering closes (proposal 0019 C2, token_exchange_failed).
func TestHandlerRejectionAuditWriteFailureReturns500TokenExchangeFailed(t *testing.T) {
	signer := jwt.NewHMACSigner("dev-1", []byte("dev-secret"))
	metrics := &recordingMetrics{}
	srv := NewServer(Options{
		Signer:  signer,
		Issuer:  "https://lenny.dev.test/token",
		Auditor: &failingAuditor{err: &txStoreErr{"auditstore outage"}},
		Metrics: metrics,
	})
	callerTok := mintToken(t, signer, jwt.Claims{
		Subject: "alice@acme.com", TenantID: "acme", Typ: auth.TokenUserBearer,
		Scope: "sessions:read", Audience: []string{"lenny-gateway"},
	})
	// Broader scope than the subject token: tokenexchange.Validate
	// rejects with invalid_scope, which drives the rejection-audit path.
	resp := doExchange(t, srv, callerTok, Request{
		GrantType: grantTypeExchange, SubjectToken: callerTok,
		Scope: "sessions:write", Audience: "lenny-gateway",
	})
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status=%d, want 500 (fail closed on rejection-audit write failure)", resp.StatusCode)
	}
	var body map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&body)
	resp.Body.Close()
	if body["error"] != "token_exchange_failed" {
		t.Errorf("error=%v, want token_exchange_failed", body["error"])
	}
	// The originally intended rejection reason must be reconstructable
	// from the error body's detail.
	desc, _ := body["error_description"].(string)
	if !strings.Contains(desc, "scope") {
		t.Errorf("error_description=%q, want the intended rejection reason (invalid scope) in detail", desc)
	}
	// No token leaks on the fail-closed path.
	if _, leaked := body["access_token"]; leaked {
		t.Errorf("500 response leaked access_token; body=%v", body)
	}
	// The §16.1 5xx counter carries the renamed class so the 500 is
	// visible to the 5xx telemetry the TokenStoreUnavailable alert reads.
	if !containsStr(metrics.fivexx, "token_exchange_failed") {
		t.Errorf("lenny_oauth_token_5xx_total not incremented for token_exchange_failed; got %v", metrics.fivexx)
	}
}

func containsStr(s []string, want string) bool {
	for _, v := range s {
		if v == want {
			return true
		}
	}
	return false
}
