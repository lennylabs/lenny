// SPDX-License-Identifier: MIT

package tokenservice

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/auth/jwt"
	"github.com/lennylabs/lenny/pkg/gateway/issuedtokenstore"
	obsaudit "github.com/lennylabs/lenny/pkg/observability/audit"
)

// fakeRevocationStore implements IssuedTokenStore + RevocationStore so
// the handler's §13.3 line 603 recursive-revocation branch can be
// exercised without Postgres.
type fakeRevocationStore struct {
	revoked []issuedtokenstore.RevokedToken
	err     error
	gotRoot string
	gotRoot2Reason,
	gotChildReason string
}

func (f *fakeRevocationStore) Record(context.Context, issuedtokenstore.IssuedToken) error {
	return nil
}

func (f *fakeRevocationStore) RevokeCascade(_ context.Context, _, rootJTI, rootReason, childReason string, _ time.Time) ([]issuedtokenstore.RevokedToken, error) {
	f.gotRoot = rootJTI
	f.gotRoot2Reason = rootReason
	f.gotChildReason = childReason
	if f.err != nil {
		return nil, f.err
	}
	return f.revoked, nil
}

func revokedPayloads(t *testing.T, rows []recordedAudit) []revokedAuditPayload {
	t.Helper()
	var out []revokedAuditPayload
	for _, r := range rows {
		if r.EventType != string(obsaudit.EventTokenRevoked) {
			continue
		}
		var p revokedAuditPayload
		if err := json.Unmarshal(r.Payload, &p); err != nil {
			t.Fatalf("unmarshal token.revoked payload: %v", err)
		}
		out = append(out, p)
	}
	return out
}

func revocationServer(t *testing.T, store IssuedTokenStore, auditor Auditor, prop func(context.Context, string, string) error) (*Server, *jwt.HMACSigner) {
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

// spec: §13.3 line 603 / §16.7 line 666 — a recursive-revocation request
// revokes the root and every delegation descendant, emitting one
// token.revoked row per node: the root carries revocation_reason=
// explicit_revoke and no cascade_root_jti; each descendant carries
// cascade_from_parent and the root jti.
func TestHandlerRecursiveRevocationEmitsPerNode_spec_16_7_5(t *testing.T) {
	store := &fakeRevocationStore{revoked: []issuedtokenstore.RevokedToken{
		{JTI: "root-jti", Subject: "alice@acme.com", IsRoot: true},
		{JTI: "child-jti", Subject: "bob@acme.com", IsRoot: false},
	}}
	auditor := &recordingAuditor{}
	srv, signer := revocationServer(t, store, auditor, nil)

	caller := mintToken(t, signer, jwt.Claims{Subject: "alice@acme.com", TenantID: "acme", Typ: auth.TokenUserBearer})
	subject := mintToken(t, signer, jwt.Claims{Subject: "alice@acme.com", TenantID: "acme", JWTID: "root-jti", Typ: auth.TokenUserBearer})

	resp := doExchange(t, srv, caller, Request{
		GrantType:          grantTypeExchange,
		SubjectToken:       subject,
		RequestedTokenType: revokedTokenType,
		Scope:              "",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d, want 200", resp.StatusCode)
	}
	if store.gotRoot != "root-jti" {
		t.Errorf("cascade root = %q, want root-jti", store.gotRoot)
	}
	if store.gotRoot2Reason != revocationReasonExplicit || store.gotChildReason != revocationReasonCascade {
		t.Errorf("reasons = (%q, %q), want (explicit_revoke, cascade_from_parent)", store.gotRoot2Reason, store.gotChildReason)
	}
	payloads := revokedPayloads(t, auditor.snapshot())
	if len(payloads) != 2 {
		t.Fatalf("token.revoked rows = %d, want 2", len(payloads))
	}
	byJTI := map[string]revokedAuditPayload{}
	for _, p := range payloads {
		byJTI[p.RevokedJTI] = p
	}
	root := byJTI["root-jti"]
	if root.RevocationReason != revocationReasonExplicit || root.CascadeRootJTI != "" || root.RevokedSub != "alice@acme.com" {
		t.Errorf("root payload = %+v, want explicit_revoke / no cascade_root / alice", root)
	}
	child := byJTI["child-jti"]
	if child.RevocationReason != revocationReasonCascade || child.CascadeRootJTI != "root-jti" || child.RevokedSub != "bob@acme.com" {
		t.Errorf("child payload = %+v, want cascade_from_parent / cascade_root=root-jti / bob", child)
	}
	// nil propagator → every node postgres_only.
	for _, p := range payloads {
		if p.PropagationMode != propagationModePostgresOnly {
			t.Errorf("propagation_mode = %q, want postgres_only (nil propagator)", p.PropagationMode)
		}
	}
}

// spec: §16.7 line 666 — propagation_mode is `eventbus` when the
// cluster propagation publish succeeds.
func TestHandlerRecursiveRevocationEventBusMode_spec_16_7_5(t *testing.T) {
	store := &fakeRevocationStore{revoked: []issuedtokenstore.RevokedToken{
		{JTI: "root-jti", Subject: "alice@acme.com", IsRoot: true},
	}}
	auditor := &recordingAuditor{}
	srv, signer := revocationServer(t, store, auditor, func(context.Context, string, string) error { return nil })

	caller := mintToken(t, signer, jwt.Claims{Subject: "alice@acme.com", TenantID: "acme", Typ: auth.TokenUserBearer})
	subject := mintToken(t, signer, jwt.Claims{Subject: "alice@acme.com", TenantID: "acme", JWTID: "root-jti", Typ: auth.TokenUserBearer})

	resp := doExchange(t, srv, caller, Request{GrantType: grantTypeExchange, SubjectToken: subject, RequestedTokenType: revokedTokenType})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d, want 200", resp.StatusCode)
	}
	payloads := revokedPayloads(t, auditor.snapshot())
	if len(payloads) != 1 || payloads[0].PropagationMode != propagationModeEventBus {
		t.Fatalf("propagation_mode = %+v, want eventbus", payloads)
	}
}

// spec: §16.7 line 666 — a publish failure after the durable commit
// records propagation_mode=postgres_only (peers fall back to Postgres).
func TestHandlerRecursiveRevocationPublishFailureIsPostgresOnly_spec_16_7_5(t *testing.T) {
	store := &fakeRevocationStore{revoked: []issuedtokenstore.RevokedToken{{JTI: "root-jti", Subject: "alice@acme.com", IsRoot: true}}}
	auditor := &recordingAuditor{}
	srv, signer := revocationServer(t, store, auditor, func(context.Context, string, string) error { return errors.New("eventbus down") })

	caller := mintToken(t, signer, jwt.Claims{Subject: "alice@acme.com", TenantID: "acme", Typ: auth.TokenUserBearer})
	subject := mintToken(t, signer, jwt.Claims{Subject: "alice@acme.com", TenantID: "acme", JWTID: "root-jti", Typ: auth.TokenUserBearer})

	resp := doExchange(t, srv, caller, Request{GrantType: grantTypeExchange, SubjectToken: subject, RequestedTokenType: revokedTokenType})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d, want 200", resp.StatusCode)
	}
	payloads := revokedPayloads(t, auditor.snapshot())
	if len(payloads) != 1 || payloads[0].PropagationMode != propagationModePostgresOnly {
		t.Fatalf("propagation_mode = %+v, want postgres_only", payloads)
	}
}

// spec: §13.3 — cross-tenant revocation is forbidden.
func TestHandlerRecursiveRevocationCrossTenantRejected_spec_16_7_5(t *testing.T) {
	store := &fakeRevocationStore{}
	srv, signer := revocationServer(t, store, &recordingAuditor{}, nil)

	caller := mintToken(t, signer, jwt.Claims{Subject: "alice@acme.com", TenantID: "acme", Typ: auth.TokenUserBearer})
	subject := mintToken(t, signer, jwt.Claims{Subject: "eve@globex.com", TenantID: "globex", JWTID: "x", Typ: auth.TokenUserBearer})

	resp := doExchange(t, srv, caller, Request{GrantType: grantTypeExchange, SubjectToken: subject, RequestedTokenType: revokedTokenType})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status=%d, want 403", resp.StatusCode)
	}
	if store.gotRoot != "" {
		t.Errorf("RevokeCascade should not be called on a cross-tenant revoke")
	}
}

// spec: §13.3 line 603 — a recursive revocation against a store with no
// durable revocation surface fails closed with token_store_unavailable.
func TestHandlerRecursiveRevocationNoDurableStore_spec_16_7_5(t *testing.T) {
	// recordingIssuedTokenStore implements only IssuedTokenStore.
	srv, signer := revocationServer(t, &recordingIssuedTokenStore{}, &recordingAuditor{}, nil)
	caller := mintToken(t, signer, jwt.Claims{Subject: "alice@acme.com", TenantID: "acme", Typ: auth.TokenUserBearer})
	subject := mintToken(t, signer, jwt.Claims{Subject: "alice@acme.com", TenantID: "acme", JWTID: "root-jti", Typ: auth.TokenUserBearer})

	resp := doExchange(t, srv, caller, Request{GrantType: grantTypeExchange, SubjectToken: subject, RequestedTokenType: revokedTokenType})
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status=%d, want 503", resp.StatusCode)
	}
}

// spec: §13.3 line 603 — revoking an unknown jti is invalid_grant.
func TestHandlerRecursiveRevocationUnknownRoot_spec_16_7_5(t *testing.T) {
	store := &fakeRevocationStore{err: issuedtokenstore.ErrNotFound}
	srv, signer := revocationServer(t, store, &recordingAuditor{}, nil)
	caller := mintToken(t, signer, jwt.Claims{Subject: "alice@acme.com", TenantID: "acme", Typ: auth.TokenUserBearer})
	subject := mintToken(t, signer, jwt.Claims{Subject: "alice@acme.com", TenantID: "acme", JWTID: "ghost", Typ: auth.TokenUserBearer})

	resp := doExchange(t, srv, caller, Request{GrantType: grantTypeExchange, SubjectToken: subject, RequestedTokenType: revokedTokenType})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status=%d, want 400", resp.StatusCode)
	}
}
