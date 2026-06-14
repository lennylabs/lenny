// SPDX-License-Identifier: MIT

package tokenservice

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/audit"
	"github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/auth/jwt"
	"github.com/lennylabs/lenny/pkg/gateway/issuedtokenstore"
)

// fakeRotationStore satisfies IssuedTokenRotationStore so the handler's
// §13.3 line 597 self-rotation branch can be exercised without Postgres.
// prevRevoked / prevSub control what the atomic rotation transaction
// reports back about the previous token.
type fakeRotationStore struct {
	prevRevoked bool
	prevSub     string

	minted        []issuedtokenstore.IssuedToken
	exchangeRows  []recordedAudit
	gotPrevJTI    string
	gotReason     string
	recordWithAud int
}

func (f *fakeRotationStore) Record(_ context.Context, tok issuedtokenstore.IssuedToken) error {
	f.minted = append(f.minted, tok)
	return nil
}

func (f *fakeRotationStore) RecordWithAudit(_ context.Context, tok issuedtokenstore.IssuedToken,
	eventType string, payload json.RawMessage, at time.Time,
) (audit.Row, error) {
	f.recordWithAud++
	f.minted = append(f.minted, tok)
	f.exchangeRows = append(f.exchangeRows, recordedAudit{
		TenantID:  tok.TenantID,
		EventType: eventType, Payload: append(json.RawMessage(nil), payload...), At: at,
	})
	return audit.Row{Seq: uint64(len(f.exchangeRows)), TenantID: tok.TenantID, EventType: eventType}, nil
}

func (f *fakeRotationStore) RecordWithRotationAudit(_ context.Context, tok issuedtokenstore.IssuedToken,
	prevJTI, revokedReason, eventType string, payload json.RawMessage, at time.Time,
) (string, bool, error) {
	f.minted = append(f.minted, tok)
	f.exchangeRows = append(f.exchangeRows, recordedAudit{
		TenantID:  tok.TenantID,
		EventType: eventType, Payload: append(json.RawMessage(nil), payload...), At: at,
	})
	f.gotPrevJTI = prevJTI
	f.gotReason = revokedReason
	return f.prevSub, f.prevRevoked, nil
}

func rotationServer(t *testing.T, store IssuedTokenStore, auditor Auditor, prop func(context.Context, string, string) error) (*Server, *jwt.HMACSigner) {
	t.Helper()
	signer := jwt.NewHMACSigner("dev-1", []byte("dev-secret"))
	return NewServer(Options{
		Signer:               signer,
		Issuer:               "https://lenny.dev.test/token",
		IssuedTokens:         store,
		Auditor:              auditor,
		RevocationPropagator: prop,
	}), signer
}

// spec: §13.3 line 597 / §16.7 line 666 — a self-rotation (caller
// presents its own current token, requests a privilege-equivalent
// replacement) revokes the previous token atomically with the mint and
// emits one token.revoked row with revocation_reason=rotation_replaced,
// no cascade_root_jti.
func TestHandlerRotationRevokesPreviousToken_spec_16_7_5(t *testing.T) {
	store := &fakeRotationStore{prevRevoked: true, prevSub: "alice@acme.com"}
	auditor := &recordingAuditor{}
	srv, signer := rotationServer(t, store, auditor, nil)

	// The caller authenticates with — and rotates — the same token, so
	// caller.jti == subject.jti.
	claims := jwt.Claims{
		Subject: "alice@acme.com", TenantID: "acme", JWTID: "tok-1",
		Typ: auth.TokenUserBearer, Scope: "sessions:read", Audience: []string{"lenny-gateway"},
	}
	tok := mintToken(t, signer, claims)

	resp := doExchange(t, srv, tok, Request{
		GrantType:    grantTypeExchange,
		SubjectToken: tok,
		Scope:        "sessions:read",
		Audience:     "lenny-gateway",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d, want 200", resp.StatusCode)
	}
	// The rotation path, not the plain RecordWithAudit path, must run.
	if store.recordWithAud != 0 {
		t.Errorf("plain RecordWithAudit called %d times; want 0 (rotation path expected)", store.recordWithAud)
	}
	if store.gotPrevJTI != "tok-1" {
		t.Errorf("revoked prevJTI = %q, want tok-1", store.gotPrevJTI)
	}
	if store.gotReason != revocationReasonRotationReplaced {
		t.Errorf("revoked reason = %q, want rotation_replaced", store.gotReason)
	}
	payloads := revokedPayloads(t, auditor.snapshot())
	if len(payloads) != 1 {
		t.Fatalf("token.revoked rows = %d, want 1", len(payloads))
	}
	p := payloads[0]
	if p.RevocationReason != revocationReasonRotationReplaced {
		t.Errorf("revocation_reason = %q, want rotation_replaced", p.RevocationReason)
	}
	if p.RevokedJTI != "tok-1" || p.RevokedSub != "alice@acme.com" {
		t.Errorf("payload identity = (%q,%q), want (tok-1, alice@acme.com)", p.RevokedJTI, p.RevokedSub)
	}
	if p.CascadeRootJTI != "" {
		t.Errorf("cascade_root_jti = %q, want empty (rotation is not a cascade)", p.CascadeRootJTI)
	}
	if p.PropagationMode != propagationModePostgresOnly {
		t.Errorf("propagation_mode = %q, want postgres_only (nil propagator)", p.PropagationMode)
	}
}

// spec: §16.7 line 666 — a successful cluster propagation publish sets
// propagation_mode=eventbus on the rotation token.revoked row.
func TestHandlerRotationEventBusMode_spec_16_7_5(t *testing.T) {
	store := &fakeRotationStore{prevRevoked: true, prevSub: "alice@acme.com"}
	auditor := &recordingAuditor{}
	srv, signer := rotationServer(t, store, auditor, func(context.Context, string, string) error { return nil })

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
	payloads := revokedPayloads(t, auditor.snapshot())
	if len(payloads) != 1 || payloads[0].PropagationMode != propagationModeEventBus {
		t.Fatalf("propagation_mode = %+v, want eventbus", payloads)
	}
}

// spec: §13.3 line 597 — a self-exchange that narrows scope is a
// scope_narrow, NOT a rotation: the previous token stays live and no
// token.revoked is emitted.
func TestHandlerScopeNarrowSelfExchangeIsNotRotation_spec_16_7_5(t *testing.T) {
	store := &fakeRotationStore{prevRevoked: true, prevSub: "alice@acme.com"}
	auditor := &recordingAuditor{}
	srv, signer := rotationServer(t, store, auditor, nil)

	tok := mintToken(t, signer, jwt.Claims{
		Subject: "alice@acme.com", TenantID: "acme", JWTID: "tok-1",
		Typ: auth.TokenUserBearer, Scope: "sessions:read sessions:write", Audience: []string{"lenny-gateway"},
	})

	resp := doExchange(t, srv, tok, Request{
		GrantType: grantTypeExchange, SubjectToken: tok,
		Scope: "sessions:read", Audience: "lenny-gateway",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d, want 200", resp.StatusCode)
	}
	if store.gotPrevJTI != "" {
		t.Errorf("scope-narrow self-exchange revoked prevJTI=%q; want none", store.gotPrevJTI)
	}
	if store.recordWithAud != 1 {
		t.Errorf("plain RecordWithAudit called %d times; want 1 (non-rotation path)", store.recordWithAud)
	}
	if rows := revokedPayloads(t, auditor.snapshot()); len(rows) != 0 {
		t.Errorf("token.revoked rows = %d, want 0 for a scope_narrow", len(rows))
	}
}

// spec: §13.3 line 597 — a self-exchange that drops an audience is a
// dialect derivation, NOT a rotation: the previous token stays live.
func TestHandlerCrossAudienceSelfExchangeIsNotRotation_spec_16_7_5(t *testing.T) {
	store := &fakeRotationStore{prevRevoked: true, prevSub: "alice@acme.com"}
	auditor := &recordingAuditor{}
	srv, signer := rotationServer(t, store, auditor, nil)

	tok := mintToken(t, signer, jwt.Claims{
		Subject: "alice@acme.com", TenantID: "acme", JWTID: "tok-1",
		Typ: auth.TokenUserBearer, Scope: "sessions:read", Audience: []string{"lenny-gateway", "llm-proxy"},
	})

	resp := doExchange(t, srv, tok, Request{
		GrantType: grantTypeExchange, SubjectToken: tok,
		Scope: "sessions:read", Audience: "llm-proxy",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d, want 200", resp.StatusCode)
	}
	if store.gotPrevJTI != "" {
		t.Errorf("cross-audience self-exchange revoked prevJTI=%q; want none", store.gotPrevJTI)
	}
	if rows := revokedPayloads(t, auditor.snapshot()); len(rows) != 0 {
		t.Errorf("token.revoked rows = %d, want 0 for a dialect derivation", len(rows))
	}
}

// spec: §13.3 line 597 — a delegation child mint (actor_token present)
// is never a rotation even when the actor is the caller's own token.
func TestHandlerDelegationMintIsNotRotation_spec_16_7_5(t *testing.T) {
	store := &fakeRotationStore{prevRevoked: true, prevSub: "alice@acme.com"}
	auditor := &recordingAuditor{}
	srv, signer := rotationServer(t, store, auditor, nil)

	tok := mintToken(t, signer, jwt.Claims{
		Subject: "alice@acme.com", TenantID: "acme", JWTID: "tok-1",
		Typ: auth.TokenUserBearer, Scope: "sessions:read", Audience: []string{"lenny-gateway"},
	})

	resp := doExchange(t, srv, tok, Request{
		GrantType: grantTypeExchange, SubjectToken: tok,
		ActorToken: tok, Scope: "sessions:read", Audience: "lenny-gateway",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d, want 200; body indicates delegation mint should succeed", resp.StatusCode)
	}
	if store.gotPrevJTI != "" {
		t.Errorf("delegation mint revoked prevJTI=%q; want none", store.gotPrevJTI)
	}
	if rows := revokedPayloads(t, auditor.snapshot()); len(rows) != 0 {
		t.Errorf("token.revoked rows = %d, want 0 for a delegation mint", len(rows))
	}
}

// spec: §13.3 line 597 — when the previous token is absent or already
// revoked the store reports revoked=false; the replacement still mints
// but no token.revoked row is emitted (idempotent re-rotation).
func TestHandlerRotationPreviousAlreadyGoneEmitsNoRevoked_spec_16_7_5(t *testing.T) {
	store := &fakeRotationStore{prevRevoked: false}
	auditor := &recordingAuditor{}
	srv, signer := rotationServer(t, store, auditor, nil)

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
	if store.gotPrevJTI != "tok-1" {
		t.Errorf("rotation path should still run; gotPrevJTI=%q", store.gotPrevJTI)
	}
	if rows := revokedPayloads(t, auditor.snapshot()); len(rows) != 0 {
		t.Errorf("token.revoked rows = %d, want 0 when previous token was already gone", len(rows))
	}
}

// spec: §13.3 line 597 — when the issued-token store has no atomic
// rotation surface (the in-memory dev path), a self-rotation still mints
// the replacement; there is no durable previous-token row to revoke, so
// no token.revoked is emitted.
func TestHandlerRotationOnInMemoryStoreMintsWithoutRevoke_spec_16_7_5(t *testing.T) {
	store := &recordingIssuedTokenStore{}
	auditor := &recordingAuditor{}
	srv, signer := rotationServer(t, store, auditor, nil)

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
	if len(store.snapshot()) != 1 {
		t.Errorf("minted records = %d, want 1", len(store.snapshot()))
	}
	if rows := revokedPayloads(t, auditor.snapshot()); len(rows) != 0 {
		t.Errorf("token.revoked rows = %d, want 0 on the in-memory dev path", len(rows))
	}
}

// sameStringSet is the rotation guard's privilege-equivalence test.
func TestSameStringSet_spec_16_7_5(t *testing.T) {
	cases := []struct {
		a, b []string
		want bool
	}{
		{nil, nil, true},
		{[]string{"a"}, []string{"a"}, true},
		{[]string{"a", "b"}, []string{"b", "a"}, true},
		{[]string{"a", "a"}, []string{"a"}, true},
		{[]string{"a"}, []string{"a", "b"}, false},
		{[]string{"a", "b"}, []string{"a"}, false},
		{nil, []string{"a"}, false},
		{[]string{"", "a"}, []string{"a"}, true},
	}
	for i, c := range cases {
		if got := sameStringSet(c.a, c.b); got != c.want {
			t.Errorf("case %d: sameStringSet(%v,%v)=%v, want %v", i, c.a, c.b, got, c.want)
		}
	}
}
