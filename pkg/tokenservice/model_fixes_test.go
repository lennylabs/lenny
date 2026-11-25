// SPDX-License-Identifier: MIT

package tokenservice

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"testing"
	"time"

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
// subject, and a subject without an aud claim grants lenny-gateway only, so
// its self-exchange without audience is a rotation and an exchange naming
// another audience is refused with audience_broadened.
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
		store := &fakeRotationStore{prevRevoked: true, prevSub: "alice@acme.com"}
		srv, signer := rotationServer(t, store, &recordingAuditor{}, nil)
		claims := lineageClaims("alice@acme.com", "alice-1", auth.TokenUserBearer)
		// A non-nil empty list keeps mintToken from adding its default.
		claims.Audience = []string{}
		alice := mintToken(t, signer, claims)
		resp := doExchange(t, srv, alice, Request{GrantType: grantTypeExchange, SubjectToken: alice})
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK || store.gotPrevJTI != "alice-1" {
			t.Fatalf("status %d, rotation revoked %q; want a rotation of alice-1", resp.StatusCode, store.gotPrevJTI)
		}
		resp = doExchange(t, srv, alice, Request{GrantType: grantTypeExchange, SubjectToken: alice, Audience: "lenny-ops"})
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
		want := []issuedtokenstore.SourceToken{{Role: issuedtokenstore.SourceSubjectToken, JTI: "subj-1", Subject: "alice@acme.com"}}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("sources = %+v, want %+v", got, want)
		}
		actor := recheckClaims("actor-1")
		got = exchangeSources(recheckClaims("subj-1"), &actor, recheckClaims("actor-1"))
		if len(got) != 2 || got[1].Role != issuedtokenstore.SourceActorToken {
			t.Errorf("sources = %+v, want the subject and the actor only", got)
		}
		// A caller without a jti, such as an upstream OIDC ID token, has no
		// row to re-read and is listed by its sub, so its user row is read.
		got = exchangeSources(recheckClaims("subj-1"), nil, jwt.Claims{Subject: "bob@acme.com"})
		wantNoJTI := append(append([]issuedtokenstore.SourceToken{}, want...),
			issuedtokenstore.SourceToken{Role: issuedtokenstore.SourceCallerToken, Subject: "bob@acme.com"})
		if !reflect.DeepEqual(got, wantNoJTI) {
			t.Errorf("sources for a caller without jti = %+v, want %+v", got, wantNoJTI)
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

// impersonationStepStore is a fakeRevocationStore that also runs the
// impersonation step.
type impersonationStepStore struct {
	fakeRevocationStore
	stepResult issuedtokenstore.ImpersonationStepResult
	stepErr    error
	gotStep    issuedtokenstore.ImpersonationStep
	stepRan    bool
	// gotCascade is the request RevokeLineage received, and cascaded
	// reports whether it ran.
	gotCascade issuedtokenstore.LineageRevocation
	cascaded   bool
}

func (f *impersonationStepStore) RevokeImpersonationStep(_ context.Context, req issuedtokenstore.ImpersonationStep) (issuedtokenstore.ImpersonationStepResult, error) {
	f.stepRan, f.gotStep = true, req
	if f.stepErr != nil {
		return issuedtokenstore.ImpersonationStepResult{}, f.stepErr
	}
	return f.stepResult, nil
}

func (f *impersonationStepStore) RevokeLineage(ctx context.Context, req issuedtokenstore.LineageRevocation) (issuedtokenstore.CascadeResult, error) {
	f.cascaded, f.gotCascade = true, req
	return f.fakeRevocationStore.RevokeLineage(ctx, req)
}

// spec: §13.3 (Credential Flow)
//
// Before a recursive revocation's cascade, the Token Service runs the
// impersonation step through the store with the request's tenant, its
// caller's jti and sub, and the root. It writes a token.revoked row for
// each ended session in that session's tenant with the request's root as
// cascade root, propagates it, keeps revoked_count to its own tenant, and
// passes the jtis the step revoked in its own tenant to the cascade, which
// accepts its caller among them (F1). It emits no cross_tenant_read event.
// A failed step returns 500 token_exchange_failed before the cascade, so a
// retry runs both steps; a revoked caller is refused with 401
// invalid_client, and an invalidated caller user with 401 invalid_client
// and the reason caller_user_invalidated, at the step or at the cascade; a
// cascade that times out answers the retryable 503 REVOCATION_TIMEOUT.
func TestRevocationRequestEndsTheImpersonationsOfItsSubtree(t *testing.T) {
	newServer := func(t *testing.T, store IssuedTokenStore, auditor *recordingAuditor, propagated *[]string) (*Server, *jwt.HMACSigner) {
		t.Helper()
		signer := jwt.NewHMACSigner("dev-1", []byte("dev-secret"))
		return mustNewServer(t, Options{
			CapabilityTokenTTL: 15 * time.Minute,
			Signer:             jwtsign.HMAC(signer),
			Verifier:           signer,
			Issuer:             "https://lenny.dev.test/token",
			IssuedTokens:       store,
			Auditor:            auditor,
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
	base := func() *impersonationStepStore {
		return &impersonationStepStore{
			fakeRevocationStore: fakeRevocationStore{
				revoked: []issuedtokenstore.RevokedToken{{JTI: "carol-1", Subject: "carol@globex.com", IsRoot: true}},
				subtree: []string{"carol-1", "carol-rotated"},
			},
			stepResult: issuedtokenstore.ImpersonationStepResult{Revoked: []issuedtokenstore.RevokedImpersonation{
				{JTI: "imp-1", TenantID: "initech", Subject: "alice@initech.com"},
				{JTI: "imp-own", TenantID: "acme", Subject: "bob@acme.com"},
			}},
		}
	}

	t.Run("ends the sessions and passes its own tenant's jtis to the cascade", func(t *testing.T) {
		store, auditor := base(), &recordingAuditor{}
		var propagated []string
		srv, signer := newServer(t, store, auditor, &propagated)
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
		want := issuedtokenstore.ImpersonationStep{
			TenantID: "acme", CallerJTI: "carol-1", CallerSubject: "carol@globex.com",
			RootJTI: "carol-1", Reason: revocationReasonCascade, At: store.gotStep.At,
		}
		if !store.stepRan || store.gotStep != want {
			t.Errorf("step request = %+v, want %+v", store.gotStep, want)
		}
		if !store.cascaded || !reflect.DeepEqual(store.gotCascade.StepRevokedCallers, []string{"imp-own"}) ||
			store.gotCascade.CallerSubject != "carol@globex.com" || store.gotCascade.CallerJTI != "carol-1" {
			t.Errorf("cascade request = %+v", store.gotCascade)
		}
		var ended []revokedAuditPayload
		for _, r := range auditor.snapshot() {
			if r.TenantID == "initech" {
				ended = append(ended, revokedPayloads(t, []recordedAudit{r})...)
			}
			if r.EventType == "cross_tenant_read" {
				t.Errorf("the revocation request wrote a cross_tenant_read row: %+v", r)
			}
		}
		if len(ended) != 1 || ended[0].RevokedJTI != "imp-1" || ended[0].CascadeRootJTI != "carol-1" ||
			ended[0].RevocationReason != revocationReasonCascade {
			t.Errorf("token.revoked rows in the session's tenant = %+v", ended)
		}
		if !reflect.DeepEqual(propagated, []string{"initech/imp-1", "acme/imp-own", "acme/carol-1"}) {
			t.Errorf("propagated = %v", propagated)
		}
	})
	t.Run("step failure", func(t *testing.T) {
		store := base()
		store.stepErr = errors.New("primary unreachable")
		var propagated []string
		srv, signer := newServer(t, store, &recordingAuditor{}, &propagated)
		assertOAuthRejection(t, revoke(t, srv, signer), http.StatusInternalServerError, "token_exchange_failed", "retry")
		if store.cascaded || len(propagated) != 0 {
			t.Error("the cascade ran after the impersonation step failed, so a retry by the revoked caller would be refused")
		}
	})
	for _, tc := range []struct {
		name    string
		stepErr error
		reason  string
	}{
		{"revoked caller at the step", issuedtokenstore.ErrCallerRevoked, "revoked"},
		{"invalidated caller user at the step", issuedtokenstore.ErrCallerUserInvalidated, "caller_user_invalidated"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := base()
			store.stepErr = tc.stepErr
			var propagated []string
			srv, signer := newServer(t, store, &recordingAuditor{}, &propagated)
			assertOAuthRejection(t, revoke(t, srv, signer), http.StatusUnauthorized, "invalid_client", tc.reason)
			if store.cascaded {
				t.Error("a refused caller reached the cascade")
			}
		})
	}
	for _, tc := range []struct {
		name       string
		cascadeErr error
		reason     string
	}{
		{"revoked caller at the cascade", issuedtokenstore.ErrCallerRevoked, "revoked"},
		{"invalidated caller user at the cascade", fmt.Errorf("%w: user", issuedtokenstore.ErrCallerUserInvalidated), "caller_user_invalidated"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := base()
			store.err = tc.cascadeErr
			var propagated []string
			srv, signer := newServer(t, store, &recordingAuditor{}, &propagated)
			assertOAuthRejection(t, revoke(t, srv, signer), http.StatusUnauthorized, "invalid_client", tc.reason)
		})
	}
	t.Run("cascade timeout", func(t *testing.T) {
		store := base()
		store.err = fmt.Errorf("%w: canceling statement due to statement timeout", issuedtokenstore.ErrRevocationTimeout)
		var propagated []string
		srv, signer := newServer(t, store, &recordingAuditor{}, &propagated)
		resp := revoke(t, srv, signer)
		defer func() { _ = resp.Body.Close() }()
		assertRevocationTimeout(t, resp)
		if !reflect.DeepEqual(propagated, []string{"initech/imp-1", "acme/imp-own"}) {
			t.Errorf("propagated = %v, want the step's sessions only", propagated)
		}
	})
}

// assertRevocationTimeout asserts the retryable 503 REVOCATION_TIMEOUT in
// the Lenny error envelope with a Retry-After header.
func assertRevocationTimeout(t *testing.T, resp *http.Response) {
	t.Helper()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", resp.StatusCode)
	}
	if resp.Header.Get("Retry-After") == "" {
		t.Error("503 REVOCATION_TIMEOUT carries no Retry-After")
	}
	var body struct {
		Error struct {
			Code      string `json:"code"`
			Retryable bool   `json:"retryable"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Error.Code != "REVOCATION_TIMEOUT" || !body.Error.Retryable {
		t.Errorf("error = %+v, want REVOCATION_TIMEOUT with retryable true", body.Error)
	}
}
