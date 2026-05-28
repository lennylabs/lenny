// SPDX-License-Identifier: MIT

package tokenservice

import (
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/auth/jwt"
)

// spec: §13.3 line 595 — when this replica's NTP drift exceeds the 5s
// ceiling, /v1/oauth/token returns 503 token_validation_unavailable
// rather than issuing a token whose `exp` it cannot trust. F-13.3.5.
func TestDriftDegradedReturns503(t *testing.T) {
	hmac := jwt.NewHMACSigner("dev-1", []byte("dev-secret"))
	degraded := true
	srv := NewServer(Options{
		Signer:        hmac,
		Verifier:      hmac,
		Issuer:        "https://lenny.dev.test/token",
		DriftDegraded: func() bool { return degraded },
	})

	farFuture := time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC).Unix()
	callerTok, err := hmac.Sign(jwt.Claims{
		Subject: "alice@acme.com", TenantID: "acme", Typ: auth.TokenUserBearer,
		Audience: []string{"lenny-gateway"}, Expiry: farFuture,
	})
	if err != nil {
		t.Fatalf("mint caller: %v", err)
	}

	resp := doExchange(t, srv, callerTok, Request{
		GrantType: grantTypeExchange, SubjectToken: callerTok,
		Audience: "lenny-gateway",
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status=%d, want 503", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	var env struct {
		Error            string `json:"error"`
		ErrorDescription string `json:"error_description"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("decode body: %v (%s)", err, body)
	}
	if env.Error != "token_validation_unavailable" {
		t.Errorf("error=%q, want token_validation_unavailable", env.Error)
	}
	if env.ErrorDescription == "" {
		t.Errorf("error_description empty")
	}
}

// spec: §13.3 line 595 — when DriftDegraded reports false (or is nil),
// the exchange proceeds normally. F-13.3.5.
func TestDriftHealthyDoesNotShortCircuit(t *testing.T) {
	hmac := jwt.NewHMACSigner("dev-1", []byte("dev-secret"))
	srv := NewServer(Options{
		Signer:        hmac,
		Verifier:      hmac,
		Issuer:        "https://lenny.dev.test/token",
		DriftDegraded: func() bool { return false },
	})

	farFuture := time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC).Unix()
	callerTok, err := hmac.Sign(jwt.Claims{
		Subject: "alice@acme.com", TenantID: "acme", Typ: auth.TokenUserBearer,
		Audience: []string{"lenny-gateway"}, Expiry: farFuture,
	})
	if err != nil {
		t.Fatalf("mint caller: %v", err)
	}

	resp := doExchange(t, srv, callerTok, Request{
		GrantType: grantTypeExchange, SubjectToken: callerTok,
		Audience: "lenny-gateway",
	})
	defer resp.Body.Close()
	// Verifying the response is non-503 is enough — the exchange
	// passes the drift gate and is processed by the normal handler.
	if resp.StatusCode == http.StatusServiceUnavailable {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("healthy replica returned 503: %s", body)
	}
}

// spec: §13.3 line 595 — when DriftDegraded is nil, the exchange
// proceeds (the option is opt-in). F-13.3.5.
func TestDriftNilOptionLeavesHandlerOpen(t *testing.T) {
	hmac := jwt.NewHMACSigner("dev-1", []byte("dev-secret"))
	srv := NewServer(Options{
		Signer:   hmac,
		Verifier: hmac,
		Issuer:   "https://lenny.dev.test/token",
	})

	farFuture := time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC).Unix()
	callerTok, err := hmac.Sign(jwt.Claims{
		Subject: "alice@acme.com", TenantID: "acme", Typ: auth.TokenUserBearer,
		Audience: []string{"lenny-gateway"}, Expiry: farFuture,
	})
	if err != nil {
		t.Fatalf("mint caller: %v", err)
	}

	resp := doExchange(t, srv, callerTok, Request{
		GrantType: grantTypeExchange, SubjectToken: callerTok,
		Audience: "lenny-gateway",
	})
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusServiceUnavailable {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("nil-option replica returned 503: %s", body)
	}
}
