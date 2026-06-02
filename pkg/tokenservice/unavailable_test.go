// SPDX-License-Identifier: MIT

package tokenservice

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

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

// A non-connectivity store failure stays 500 server_error: the alert
// must not misfire on a genuine internal error. F-13.3.4.
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
	if body["error"] != "server_error" {
		t.Errorf("error=%v, want server_error", body["error"])
	}
	if !containsStr(metrics.fivexx, "server_error") {
		t.Errorf("lenny_oauth_token_5xx_total not incremented for server_error; got %v", metrics.fivexx)
	}
	if containsStr(metrics.fivexx, "token_store_unavailable") {
		t.Errorf("a constraint violation must not be classified token_store_unavailable; got %v", metrics.fivexx)
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
