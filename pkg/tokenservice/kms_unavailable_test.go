// SPDX-License-Identifier: MIT

package tokenservice

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/auth/jwt"
)

// breakerStubSigner wraps an HMACSigner so we can inject failure
// behavior while keeping the verifier path real (used to verify the
// caller token).
type breakerStubSigner struct {
	inner *jwt.HMACSigner
	fail  bool
}

func (s *breakerStubSigner) Sign(c jwt.Claims) (string, error) {
	if s.fail {
		return "", errors.New("kms outage")
	}
	return s.inner.Sign(c)
}

func (s *breakerStubSigner) KeyID() string                     { return s.inner.KeyID() }
func (s *breakerStubSigner) Verify(t string) (jwt.Claims, error) { return s.inner.Verify(t) }

// spec: §10.2 line 225 — when the JWTSigner breaker has tripped open,
// /v1/oauth/token returns 503 KMS_SIGNING_UNAVAILABLE with
// retryable: true. F-10.2.6.
func TestTokenServiceReturnsKMSUnavailableWhenBreakerOpen(t *testing.T) {
	hmac := jwt.NewHMACSigner("dev-1", []byte("dev-secret"))
	stub := &breakerStubSigner{inner: hmac, fail: true}
	now := time.Unix(5_000_000, 0)
	breaker := &jwt.BreakerSigner{
		Inner:     stub,
		Threshold: 3,
		Window:    jwt.SigningBreakerWindow,
		Cooldown:  jwt.SigningBreakerCooldown,
		Now:       func() time.Time { return now },
	}
	srv := NewServer(Options{
		Signer:   breaker,
		Verifier: hmac, // verify caller via the real signer
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

	// Drive the breaker through 4 failures so it trips open.
	for i := 0; i < 4; i++ {
		resp := doExchange(t, srv, callerTok, Request{
			GrantType: grantTypeExchange, SubjectToken: callerTok,
			Audience: "lenny-gateway",
		})
		resp.Body.Close()
	}
	// Next call: the breaker short-circuits to ErrSigningUnavailable
	// before reaching the (still-failing) stub. The handler maps it to
	// 503 KMS_SIGNING_UNAVAILABLE.
	resp := doExchange(t, srv, callerTok, Request{
		GrantType: grantTypeExchange, SubjectToken: callerTok,
		Audience: "lenny-gateway",
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status=%d, want 503", resp.StatusCode)
	}
	if got := resp.Header.Get("Retry-After"); got == "" {
		t.Errorf("Retry-After header missing")
	}
	body, _ := io.ReadAll(resp.Body)
	var env struct {
		Error struct {
			Code      string `json:"code"`
			Retryable bool   `json:"retryable"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("decode body: %v (%s)", err, body)
	}
	if env.Error.Code != "KMS_SIGNING_UNAVAILABLE" {
		t.Errorf("error.code=%q, want KMS_SIGNING_UNAVAILABLE", env.Error.Code)
	}
	if !env.Error.Retryable {
		t.Errorf("error.retryable=false, want true")
	}
}
