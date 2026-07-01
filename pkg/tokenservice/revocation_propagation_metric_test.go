// SPDX-License-Identifier: MIT

package tokenservice

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/auth/jwt"
	"github.com/lennylabs/lenny/pkg/gateway/storage/issuedtokenstore"
)

// metricRevocationServer wires a recordingMetrics into the revocation
// server so a test can assert the SEC-TS-1
// lenny_token_revocation_propagation_seconds producer fired keyed by the
// §16.7 propagation_mode outcome.
func metricRevocationServer(t *testing.T, store IssuedTokenStore, prop func(context.Context, string, string) error) (*Server, *jwt.HMACSigner, *recordingMetrics) {
	t.Helper()
	signer := jwt.NewHMACSigner("dev-1", []byte("dev-secret"))
	m := &recordingMetrics{}
	srv := NewServer(Options{
		Signer:               signer,
		Issuer:               "https://lenny.dev.test/token",
		IssuedTokens:         store,
		Auditor:              &recordingAuditor{},
		Metrics:              m,
		RevocationPropagator: prop,
	})
	return srv, signer, m
}

// spec: §16.5, §16.7 — SEC-TS-1. The producer for
// lenny_token_revocation_propagation_seconds was absent: propagateRevocation
// returned the mode string for the §16.7 audit row but never observed a
// latency, so the TokenRevocationPropagationLag alert was unfireable. This
// asserts the histogram observes on the eventbus path and keys the
// observation with outcome="eventbus". It fails against the pre-fix code,
// which made no ObserveRevocationPropagation call.
func TestRevocationPropagationObservesEventBusOutcome_SEC_TS_1(t *testing.T) {
	store := &fakeRevocationStore{revoked: []issuedtokenstore.RevokedToken{
		{JTI: "root-jti", Subject: "alice@acme.com", IsRoot: true},
	}}
	srv, signer, metrics := metricRevocationServer(t, store, func(context.Context, string, string) error { return nil })

	caller := mintToken(t, signer, jwt.Claims{Subject: "alice@acme.com", TenantID: "acme", Typ: auth.TokenUserBearer})
	subject := mintToken(t, signer, jwt.Claims{Subject: "alice@acme.com", TenantID: "acme", JWTID: "root-jti", Typ: auth.TokenUserBearer})

	resp := doExchange(t, srv, caller, Request{GrantType: grantTypeExchange, SubjectToken: subject, RequestedTokenType: revokedTokenType})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d, want 200", resp.StatusCode)
	}

	obs := metrics.propagations()
	if len(obs) != 1 {
		t.Fatalf("propagation observations = %d, want 1 (one revoked node)", len(obs))
	}
	if obs[0].outcome != propagationModeEventBus {
		t.Errorf("observed outcome = %q, want eventbus", obs[0].outcome)
	}
	if obs[0].d < 0 {
		t.Errorf("observed latency = %v, want a non-negative duration", obs[0].d)
	}
}

// spec: §16.5, §16.7 — SEC-TS-1. A publish failure after the durable
// commit records outcome="postgres_only" on the histogram, matching the
// §16.7 propagation_mode audit field, so both outcome labels have a
// producer.
func TestRevocationPropagationObservesPostgresOnlyOutcome_SEC_TS_1(t *testing.T) {
	store := &fakeRevocationStore{revoked: []issuedtokenstore.RevokedToken{
		{JTI: "root-jti", Subject: "alice@acme.com", IsRoot: true},
	}}
	srv, signer, metrics := metricRevocationServer(t, store, func(context.Context, string, string) error { return errors.New("eventbus down") })

	caller := mintToken(t, signer, jwt.Claims{Subject: "alice@acme.com", TenantID: "acme", Typ: auth.TokenUserBearer})
	subject := mintToken(t, signer, jwt.Claims{Subject: "alice@acme.com", TenantID: "acme", JWTID: "root-jti", Typ: auth.TokenUserBearer})

	resp := doExchange(t, srv, caller, Request{GrantType: grantTypeExchange, SubjectToken: subject, RequestedTokenType: revokedTokenType})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d, want 200", resp.StatusCode)
	}

	obs := metrics.propagations()
	if len(obs) != 1 {
		t.Fatalf("propagation observations = %d, want 1", len(obs))
	}
	if obs[0].outcome != propagationModePostgresOnly {
		t.Errorf("observed outcome = %q, want postgres_only", obs[0].outcome)
	}
}

// spec: §16.5, §16.7 — SEC-TS-1. A nil propagator (no EventBus wired)
// still observes the histogram, keyed postgres_only, so the producer is
// present on every revocation path rather than only when EventBus is
// configured.
func TestRevocationPropagationNilPropagatorObservesPostgresOnly_SEC_TS_1(t *testing.T) {
	store := &fakeRevocationStore{revoked: []issuedtokenstore.RevokedToken{
		{JTI: "root-jti", Subject: "alice@acme.com", IsRoot: true},
		{JTI: "child-jti", Subject: "bob@acme.com", IsRoot: false},
	}}
	srv, signer, metrics := metricRevocationServer(t, store, nil)

	caller := mintToken(t, signer, jwt.Claims{Subject: "alice@acme.com", TenantID: "acme", Typ: auth.TokenUserBearer})
	subject := mintToken(t, signer, jwt.Claims{Subject: "alice@acme.com", TenantID: "acme", JWTID: "root-jti", Typ: auth.TokenUserBearer})

	resp := doExchange(t, srv, caller, Request{GrantType: grantTypeExchange, SubjectToken: subject, RequestedTokenType: revokedTokenType})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d, want 200", resp.StatusCode)
	}

	obs := metrics.propagations()
	if len(obs) != 2 {
		t.Fatalf("propagation observations = %d, want 2 (root + child)", len(obs))
	}
	for i, o := range obs {
		if o.outcome != propagationModePostgresOnly {
			t.Errorf("observation %d outcome = %q, want postgres_only (nil propagator)", i, o.outcome)
		}
	}
}

// spec: §16.5, §16.7 — SEC-TS-1. The rotation revocation path
// (RecordWithRotationAudit) also observes the histogram: a self-rotation
// revokes the previous token, and its propagation is timed and keyed by
// outcome. This covers the emitRotationRevoked caller of
// propagateRevocation, not only the recursive-revocation path.
func TestRotationRevocationObservesPropagation_SEC_TS_1(t *testing.T) {
	store := &fakeRotationStore{prevRevoked: true, prevSub: "alice@acme.com"}
	signer := jwt.NewHMACSigner("dev-1", []byte("dev-secret"))
	metrics := &recordingMetrics{}
	srv := NewServer(Options{
		Signer:               signer,
		Issuer:               "https://lenny.dev.test/token",
		IssuedTokens:         store,
		Auditor:              &recordingAuditor{},
		Metrics:              metrics,
		RevocationPropagator: func(context.Context, string, string) error { return nil },
	})

	tok := mintToken(t, signer, jwt.Claims{
		Subject: "alice@acme.com", TenantID: "acme", JWTID: "tok-1",
		Typ: auth.TokenUserBearer, Scope: "sessions:read", Audience: []string{"lenny-gateway"},
	})

	resp := doExchange(t, srv, tok, Request{
		GrantType: grantTypeExchange, SubjectToken: tok,
		Scope: "sessions:read", Audience: "lenny-gateway",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d, want 200", resp.StatusCode)
	}

	obs := metrics.propagations()
	if len(obs) != 1 || obs[0].outcome != propagationModeEventBus {
		t.Fatalf("rotation propagation observations = %+v, want one eventbus observation", obs)
	}
}
