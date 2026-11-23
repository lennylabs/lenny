// SPDX-License-Identifier: MIT

package tokenservice

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/audit/auditstore"
	"github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/auth/jwt"
	"github.com/lennylabs/lenny/pkg/storage/issuedtokenstore"
	"github.com/lennylabs/lenny/tests/testinfra/jwtsign"
)

// spec: §13.3 (Credential Flow)
//
// A request that names no audience requests lenny-gateway (gap G1 of change
// 0165's model): the issued token carries that audience, a self-exchange of
// a lenny-gateway subject without audience is a rotation that revokes the
// subject, and a subject without an aud claim grants no audience, so an
// exchange of it without audience is refused with audience_broadened.
func TestExchangeWithoutAudienceRequestsTheDefault(t *testing.T) {
	t.Run("self exchange is a rotation", func(t *testing.T) {
		store := &fakeRotationStore{prevRevoked: true, prevSub: "alice@acme.com"}
		srv, signer := rotationServer(t, store, &recordingAuditor{}, nil)
		alice := mintToken(t, signer, lineageClaims("alice@acme.com", "alice-1", auth.TokenUserBearer))
		resp := doExchange(t, srv, alice, Request{GrantType: grantTypeExchange, SubjectToken: alice})
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200", resp.StatusCode)
		}
		var body Response
		if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		out, err := signer.Verify(body.AccessToken)
		if err != nil {
			t.Fatalf("verify issued token: %v", err)
		}
		if !reflect.DeepEqual([]string(out.Audience), []string{"lenny-gateway"}) {
			t.Errorf("aud = %v, want [lenny-gateway]", out.Audience)
		}
		if store.gotPrevJTI != "alice-1" {
			t.Errorf("rotation revoked %q, want the subject alice-1", store.gotPrevJTI)
		}
		if len(store.minted) != 1 || !reflect.DeepEqual(store.minted[0].Audiences, []string{"lenny-gateway"}) {
			t.Errorf("issued row audiences = %+v, want [lenny-gateway]", store.minted)
		}
	})
	t.Run("subject without aud", func(t *testing.T) {
		srv, signer := rotationServer(t, &fakeRotationStore{}, &recordingAuditor{}, nil)
		claims := lineageClaims("alice@acme.com", "alice-1", auth.TokenUserBearer)
		// A non-nil empty list keeps mintToken from adding its default.
		claims.Audience = []string{}
		alice := mintToken(t, signer, claims)
		resp := doExchange(t, srv, alice, Request{GrantType: grantTypeExchange, SubjectToken: alice})
		assertOAuthRejection(t, resp, http.StatusBadRequest, "invalid_request", "audience_broadened")
	})
}

// spec: §13.3 (Credential Flow)
//
// Step (1a) of write-before-issue re-reads the caller token's row, and a
// mint whose caller token is revoked is refused with 401 invalid_client
// and reason caller_token_revoked (the scenario CX-10 of change 0165's
// model: alice rotates T into T', which revokes T, then narrows T' with T as
// caller). The rejection token.exchanged row carries error_code
// invalid_client. A caller token that is an impersonation row is not
// refused by the re-read, which applies the impersonation predicate only to
// the subject and the actor.
func TestMintRefusesARevokedCallerToken(t *testing.T) {
	t.Run("revoked caller", func(t *testing.T) {
		store := &txStore{revoked: map[string]time.Time{"caller-1": time.Unix(1_700_000_000, 0)}}
		auditor := &recordingAuditor{}
		srv, signer := rotationServer(t, store, auditor, nil)
		resp := doExchange(t, srv, mintToken(t, signer, recheckClaims("caller-1")), Request{
			GrantType:    grantTypeExchange,
			SubjectToken: mintToken(t, signer, recheckClaims("subj-1")),
			Scope:        "tools:sessions:read",
		})
		assertOAuthRejection(t, resp, http.StatusUnauthorized, "invalid_client", "caller_token_revoked")
		if len(store.records) != 0 {
			t.Errorf("records = %d, want none", len(store.records))
		}
		var exchanged []exchangeAuditPayload
		for _, r := range auditor.snapshot() {
			var p exchangeAuditPayload
			if err := json.Unmarshal(r.Payload, &p); err != nil {
				t.Fatalf("unmarshal audit payload: %v", err)
			}
			exchanged = append(exchanged, p)
		}
		if len(exchanged) != 1 || exchanged[0].ErrorCode != "invalid_client" ||
			exchanged[0].PolicyResult != "rejected:caller_token_revoked" {
			t.Errorf("rejection rows = %+v, want one invalid_client rejected:caller_token_revoked row", exchanged)
		}
	})
	t.Run("impersonation caller", func(t *testing.T) {
		store := &txStore{impersonation: map[string]bool{"caller-1": true}}
		srv, signer := rotationServer(t, store, &recordingAuditor{}, nil)
		resp := doExchange(t, srv, mintToken(t, signer, recheckClaims("caller-1")), Request{
			GrantType:    grantTypeExchange,
			SubjectToken: mintToken(t, signer, recheckClaims("subj-1")),
			Scope:        "tools:sessions:read",
		})
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200", resp.StatusCode)
		}
	})
	t.Run("caller is the subject", func(t *testing.T) {
		got := exchangeSources(recheckClaims("subj-1"), nil, recheckClaims("subj-1"))
		want := []issuedtokenstore.SourceToken{{Role: issuedtokenstore.SourceSubjectToken, JTI: "subj-1"}}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("sources = %+v, want %+v", got, want)
		}
		actor := recheckClaims("actor-1")
		got = exchangeSources(recheckClaims("subj-1"), &actor, recheckClaims("actor-1"))
		if len(got) != 2 || got[1].Role != issuedtokenstore.SourceActorToken {
			t.Errorf("sources = %+v, want the subject and the actor only", got)
		}
		got = exchangeSources(recheckClaims("subj-1"), nil, jwt.Claims{Subject: "alice@acme.com"})
		if !reflect.DeepEqual(got, want) {
			t.Errorf("sources for a caller without jti = %+v, want %+v", got, want)
		}
	})
}

// spec: §13.3 (Credential Flow)
//
// A recursive-revocation request whose root has no issued_tokens row (the
// scenario CX-5 of change 0165's model) supplies the row the store records
// for it: the subject's jti, tenant, sub, scope, audience, expiry, and the
// SHA-256 of the raw subject token. A subject without exp supplies no token
// hash, so the store refuses it as unknown.
func TestRevocationRequestSuppliesTheRowOfARootWithoutOne(t *testing.T) {
	store := &fakeRevocationStore{revoked: []issuedtokenstore.RevokedToken{{JTI: "root-jti", Subject: "alice@acme.com", IsRoot: true}}}
	srv, signer := revocationServer(t, store, &recordingAuditor{}, nil)
	claims := lineageClaims("alice@acme.com", "root-jti", auth.TokenUserBearer)
	claims.IssuedAt = 1_700_000_000
	subject := mintToken(t, signer, claims)
	caller := mintToken(t, signer, lineageClaims("alice@acme.com", "caller-jti", auth.TokenUserBearer))
	resp := doExchange(t, srv, caller, Request{
		GrantType: grantTypeExchange, SubjectToken: subject, RequestedTokenType: revokedTokenType,
	})
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	row := store.gotRootRow
	wantHash := sha256.Sum256([]byte(subject))
	if row.JTI != "root-jti" || row.TenantID != "acme" || row.Subject != "alice@acme.com" {
		t.Errorf("root row identity = (%q, %q, %q)", row.JTI, row.TenantID, row.Subject)
	}
	if !reflect.DeepEqual(row.TokenHash, wantHash[:]) {
		t.Error("root row token hash is not the SHA-256 of the subject token")
	}
	if !reflect.DeepEqual(row.Scope, []string{"tools:sessions:read", "tools:sessions:write"}) ||
		!reflect.DeepEqual(row.Audiences, []string{"lenny-gateway"}) {
		t.Errorf("root row scope %v audiences %v", row.Scope, row.Audiences)
	}
	if row.ExpiresAt.IsZero() || !row.IssuedAt.Equal(time.Unix(1_700_000_000, 0)) {
		t.Errorf("root row issued_at %v exp %v", row.IssuedAt, row.ExpiresAt)
	}

	noExp := revocationRoot("raw", jwt.Claims{JWTID: "j", TenantID: "acme", Subject: "alice@acme.com"}, time.Now())
	if len(noExp.TokenHash) != 0 || noExp.JTI != "j" {
		t.Errorf("root without exp = %+v, want only its jti", noExp)
	}
}

// impersonationRevocationStore is a fakeRevocationStore that also ends
// impersonation sessions through the impersonator link.
type impersonationRevocationStore struct {
	fakeRevocationStore
	ended      []issuedtokenstore.RevokedImpersonation
	endErr     error
	subtreeErr error
	gotTenant  string
	gotJTIs    []string
	gotReason  string
	// gotSubtreeCaller is the caller jti LineageSubtree was asked about,
	// and cascaded reports whether RevokeLineage ran.
	gotSubtreeCaller string
	cascaded         bool
}

func (f *impersonationRevocationStore) LineageSubtree(_ context.Context, _, callerJTI, _ string) ([]string, error) {
	f.gotSubtreeCaller = callerJTI
	if f.subtreeErr != nil {
		return nil, f.subtreeErr
	}
	return f.subtree, nil
}

func (f *impersonationRevocationStore) RevokeLineage(ctx context.Context, req issuedtokenstore.LineageRevocation) (issuedtokenstore.CascadeResult, error) {
	f.cascaded = true
	return f.fakeRevocationStore.RevokeLineage(ctx, req)
}

func (f *impersonationRevocationStore) RevokeImpersonationsByImpersonator(_ context.Context, tenant string, jtis []string, reason string, _ time.Time) ([]issuedtokenstore.RevokedImpersonation, error) {
	f.gotTenant, f.gotJTIs, f.gotReason = tenant, jtis, reason
	if f.endErr != nil {
		return nil, f.endErr
	}
	return f.ended, nil
}

// fakeCrossTenantReads records the cross_tenant_read events.
type fakeCrossTenantReads struct {
	mu     sync.Mutex
	events []auditstore.CrossTenantRead
	err    error
}

func (f *fakeCrossTenantReads) EmitCrossTenantRead(_ context.Context, r auditstore.CrossTenantRead) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, r)
	return f.err
}

// spec: §13.3 (Credential Flow)
//
// Before a recursive revocation's cascade, the Token Service ends the
// impersonation sessions whose impersonator link names a token of the
// root's subtree (the scenario CX-4 of change 0165's model: carol's bearer C
// started an impersonation of alice in another tenant, and revoking C ends
// it). It passes the request's tenant and the whole subtree, writes a
// token.revoked row for each ended session in that session's tenant with
// the request's root as cascade root, propagates it, emits one
// cross_tenant_read event, and keeps revoked_count to its own tenant. A
// failed revocation of the sessions returns 500 token_exchange_failed
// before the cascade, so a retry by the same caller runs both steps; a
// revoked caller is refused with 401 invalid_client before either step; a
// missing cross_tenant_read emitter fails closed without running the
// query; and a failed emit is logged and keeps the 200.
func TestRevocationRequestEndsTheImpersonationsOfItsSubtree(t *testing.T) {
	newServer := func(t *testing.T, store IssuedTokenStore, auditor *recordingAuditor, reads CrossTenantReads, propagated *[]string) (*Server, *jwt.HMACSigner) {
		t.Helper()
		signer := jwt.NewHMACSigner("dev-1", []byte("dev-secret"))
		return mustNewServer(t, Options{
			CapabilityTokenTTL: 15 * time.Minute,
			Signer:             jwtsign.HMAC(signer),
			Verifier:           signer,
			Issuer:             "https://lenny.dev.test/token",
			IssuedTokens:       store,
			Auditor:            auditor,
			CrossTenantReads:   reads,
			RevocationPropagator: func(_ context.Context, tenantID, jti string) error {
				*propagated = append(*propagated, tenantID+"/"+jti)
				return nil
			},
		}), signer
	}
	revoke := func(t *testing.T, srv *Server, signer *jwt.HMACSigner) *http.Response {
		t.Helper()
		carol := mintToken(t, signer, lineageClaims("carol@globex.com", "carol-1", auth.TokenUserBearer))
		return doExchange(t, srv, carol, Request{
			GrantType: grantTypeExchange, SubjectToken: carol, RequestedTokenType: revokedTokenType,
		})
	}
	base := func() *impersonationRevocationStore {
		return &impersonationRevocationStore{
			fakeRevocationStore: fakeRevocationStore{
				revoked: []issuedtokenstore.RevokedToken{{JTI: "carol-1", Subject: "carol@globex.com", IsRoot: true}},
				subtree: []string{"carol-1", "carol-rotated"},
			},
			ended: []issuedtokenstore.RevokedImpersonation{{JTI: "imp-1", TenantID: "initech", Subject: "alice@initech.com"}},
		}
	}

	t.Run("ends the sessions", func(t *testing.T) {
		store, auditor, reads := base(), &recordingAuditor{}, &fakeCrossTenantReads{}
		var propagated []string
		srv, signer := newServer(t, store, auditor, reads, &propagated)
		resp := revoke(t, srv, signer)
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200", resp.StatusCode)
		}
		var body map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if body["revoked_count"] != float64(1) {
			t.Errorf("revoked_count = %v, want 1, the request's own tenant only", body["revoked_count"])
		}
		if store.gotSubtreeCaller != "carol-1" || !store.cascaded {
			t.Errorf("subtree read for caller %q, cascaded %v", store.gotSubtreeCaller, store.cascaded)
		}
		if store.gotTenant != "acme" || !reflect.DeepEqual(store.gotJTIs, []string{"carol-1", "carol-rotated"}) ||
			store.gotReason != revocationReasonCascade {
			t.Errorf("impersonator lookup = (%q, %v, %q)", store.gotTenant, store.gotJTIs, store.gotReason)
		}
		var ended []revokedAuditPayload
		for _, r := range auditor.snapshot() {
			if r.TenantID == "initech" {
				ended = append(ended, revokedPayloads(t, []recordedAudit{r})...)
			}
		}
		if len(ended) != 1 || ended[0].RevokedJTI != "imp-1" || ended[0].CascadeRootJTI != "carol-1" ||
			ended[0].RevocationReason != revocationReasonCascade {
			t.Errorf("token.revoked rows in the session's tenant = %+v", ended)
		}
		if !reflect.DeepEqual(propagated, []string{"initech/imp-1", "acme/carol-1"}) {
			t.Errorf("propagated = %v", propagated)
		}
		if len(reads.events) != 1 || reads.events[0].Category != "impersonation_session" || reads.events[0].RowCount != 1 ||
			reads.events[0].CallerSubject != "carol@globex.com" {
			t.Errorf("cross_tenant_read events = %+v", reads.events)
		}
	})
	t.Run("store failure", func(t *testing.T) {
		store := base()
		store.endErr = errors.New("primary unreachable")
		var propagated []string
		srv, signer := newServer(t, store, &recordingAuditor{}, &fakeCrossTenantReads{}, &propagated)
		assertOAuthRejection(t, revoke(t, srv, signer), http.StatusInternalServerError, "token_exchange_failed", "retry")
		if store.cascaded || len(propagated) != 0 {
			t.Error("the cascade ran after the impersonation step failed, so a retry by the revoked caller would be refused")
		}
	})
	t.Run("revoked caller", func(t *testing.T) {
		store := base()
		store.subtreeErr = issuedtokenstore.ErrCallerRevoked
		var propagated []string
		srv, signer := newServer(t, store, &recordingAuditor{}, &fakeCrossTenantReads{}, &propagated)
		assertOAuthRejection(t, revoke(t, srv, signer), http.StatusUnauthorized, "invalid_client", "revoked")
		if store.cascaded || store.gotTenant != "" {
			t.Error("a revoked caller reached the impersonator lookup or the cascade")
		}
	})
	t.Run("no cross_tenant_read emitter", func(t *testing.T) {
		store := base()
		var propagated []string
		srv, signer := newServer(t, store, &recordingAuditor{}, nil, &propagated)
		assertOAuthRejection(t, revoke(t, srv, signer), http.StatusInternalServerError, "token_exchange_failed", "cross_tenant_read")
		if store.gotTenant != "" {
			t.Error("the impersonator lookup ran without a cross_tenant_read emitter")
		}
	})
	t.Run("emit failure keeps the revocation", func(t *testing.T) {
		var propagated []string
		srv, signer := newServer(t, base(), &recordingAuditor{}, &fakeCrossTenantReads{err: errors.New("audit down")}, &propagated)
		resp := revoke(t, srv, signer)
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200", resp.StatusCode)
		}
	})
}
