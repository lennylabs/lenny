// SPDX-License-Identifier: MIT

package tokenservice

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/auth/jwt"
	obsaudit "github.com/lennylabs/lenny/pkg/observability/audit"
	"github.com/lennylabs/lenny/pkg/storage/issuedtokenstore"
	"github.com/lennylabs/lenny/tests/testinfra/jwtsign"
)

// fakeRevocationStore implements IssuedTokenStore + RevocationStore so
// the handler's §13.3 recursive-revocation branch can be
// exercised without Postgres.
type fakeRevocationStore struct {
	revoked   []issuedtokenstore.RevokedToken
	err       error
	gotCaller string
	gotRoot   string
	gotRoot2Reason,
	gotChildReason string
}

func (f *fakeRevocationStore) Record(context.Context, issuedtokenstore.IssuedToken) error {
	return nil
}

func (f *fakeRevocationStore) RevokeCascadeByCaller(_ context.Context, _, callerJTI, rootJTI, rootReason, childReason string, _ time.Time) ([]issuedtokenstore.RevokedToken, error) {
	f.gotCaller = callerJTI
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
	return mustNewServer(t, Options{
		CapabilityTokenTTL:   15 * time.Minute,
		Signer:               jwtsign.HMAC(signer),
		Verifier:             signer,
		Issuer:               "https://lenny.dev.test/token",
		IssuedTokens:         store,
		Auditor:              auditor,
		RevocationPropagator: prop,
	}), signer
}

// spec: §13.3 / §16.7 — a recursive-revocation request
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

// spec: §16.7 — propagation_mode is `eventbus` when the
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

// spec: §16.7 — a publish failure after the durable commit
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

// spec: §13.3 — a recursive revocation against a store with no
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

// spec: §13.3 — revoking an unknown jti is invalid_grant.
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

// spec: §13.3 (Credential Flow)
//
// A recursive-revocation request is accepted from the subject token's own
// user and from a caller holding tenant-admin or platform-admin in the
// tenant. Another user in the tenant without an administrator role is
// refused with 403 unauthorized_client, and the store revokes nothing.
// The store receives the caller token's jti for the revoked-caller check.
func TestHandlerRecursiveRevocationAcceptsTheOwnerOrATenantAdministrator(t *testing.T) {
	cases := []struct {
		name       string
		callerSub  string
		roles      []auth.Role
		wantStatus int
	}{
		{"the token's own user", "alice@acme.com", nil, http.StatusOK},
		{"a tenant-admin", "carol@acme.com", []auth.Role{auth.RoleTenantAdmin}, http.StatusOK},
		{"a platform-admin", "carol@acme.com", []auth.Role{auth.RolePlatformAdmin}, http.StatusOK},
		{"another user", "bob@acme.com", []auth.Role{auth.RoleUser}, http.StatusForbidden},
		{"a tenant-viewer", "bob@acme.com", []auth.Role{auth.RoleTenantViewer}, http.StatusForbidden},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			store := &fakeRevocationStore{revoked: []issuedtokenstore.RevokedToken{{JTI: "root-jti", Subject: "alice@acme.com", IsRoot: true}}}
			auditor := &recordingAuditor{}
			srv, signer := revocationServer(t, store, auditor, nil)
			caller := mintToken(t, signer, jwt.Claims{Subject: c.callerSub, TenantID: "acme", JWTID: "caller-jti", Typ: auth.TokenUserBearer, Roles: c.roles})
			subject := mintToken(t, signer, jwt.Claims{Subject: "alice@acme.com", TenantID: "acme", JWTID: "root-jti", Typ: auth.TokenUserBearer})

			status, env := decodeExchange(t, doExchange(t, srv, caller, Request{GrantType: grantTypeExchange, SubjectToken: subject, RequestedTokenType: revokedTokenType}))
			if status != c.wantStatus {
				t.Fatalf("status %d body %v, want %d", status, env, c.wantStatus)
			}
			if status == http.StatusOK {
				if store.gotRoot != "root-jti" || store.gotCaller != "caller-jti" {
					t.Errorf("cascade (caller %q, root %q), want (caller-jti, root-jti)", store.gotCaller, store.gotRoot)
				}
				return
			}
			if env["error"] != "unauthorized_client" {
				t.Errorf("error = %v, want unauthorized_client", env["error"])
			}
			if store.gotRoot != "" {
				t.Errorf("a refused revocation reached the store with root %q", store.gotRoot)
			}
			if len(revokedPayloads(t, auditor.snapshot())) != 0 {
				t.Errorf("a refused revocation wrote token.revoked rows")
			}
		})
	}
}

// spec: §13.3 (Credential Flow)
//
// A revocation request whose caller token the store reports as revoked is
// refused with 401 invalid_client and writes no token.revoked row.
func TestHandlerRecursiveRevocationRefusesARevokedCaller(t *testing.T) {
	store := &fakeRevocationStore{err: issuedtokenstore.ErrCallerRevoked}
	auditor := &recordingAuditor{}
	srv, signer := revocationServer(t, store, auditor, nil)
	caller := mintToken(t, signer, jwt.Claims{Subject: "alice@acme.com", TenantID: "acme", JWTID: "leaked-jti", Typ: auth.TokenUserBearer})
	subject := mintToken(t, signer, jwt.Claims{Subject: "alice@acme.com", TenantID: "acme", JWTID: "root-jti", Typ: auth.TokenUserBearer})

	status, env := decodeExchange(t, doExchange(t, srv, caller, Request{GrantType: grantTypeExchange, SubjectToken: subject, RequestedTokenType: revokedTokenType}))
	if status != http.StatusUnauthorized || env["error"] != "invalid_client" {
		t.Fatalf("status %d body %v, want 401 invalid_client", status, env)
	}
	if store.gotCaller != "leaked-jti" {
		t.Errorf("caller jti passed to the store = %q, want leaked-jti", store.gotCaller)
	}
	if len(revokedPayloads(t, auditor.snapshot())) != 0 {
		t.Errorf("a refused revocation wrote token.revoked rows")
	}
}

// fakePlatformRoles is a PlatformRoleResolver with one fixed answer that
// counts its lookups.
type fakePlatformRoles struct {
	roles   []auth.Role
	found   bool
	err     error
	lookups int
}

func (f *fakePlatformRoles) ResolveRoles(context.Context, string, string) ([]auth.Role, bool, error) {
	f.lookups++
	return f.roles, f.found, f.err
}

// spec: §13.3 (Credential Flow)
//
// The caller's administrator role for a recursive-revocation request is
// its platform-managed assignment when one exists: an assignment without
// tenant-admin refuses a caller whose roles claim lists it, and an
// assignment with tenant-admin admits a caller whose claim lists none. No
// assignment leaves the claim authoritative. A failed lookup refuses the
// request without revoking: 503 token_store_unavailable for a Postgres
// outage and 500 token_exchange_failed otherwise. The token's own user is
// accepted without a lookup.
func TestHandlerRecursiveRevocationResolvesThePlatformRoleAssignment(t *testing.T) {
	cases := []struct {
		name       string
		callerSub  string
		claim      []auth.Role
		resolver   *fakePlatformRoles
		wantStatus int
		wantError  string
		wantLookup int
	}{
		{
			"assignment demotes a claimed tenant-admin", "bob@acme.com",
			[]auth.Role{auth.RoleTenantAdmin},
			&fakePlatformRoles{roles: []auth.Role{auth.RoleUser}, found: true}, http.StatusForbidden, "unauthorized_client", 1,
		},
		{
			"empty assignment demotes a claimed platform-admin", "bob@acme.com",
			[]auth.Role{auth.RolePlatformAdmin},
			&fakePlatformRoles{found: true}, http.StatusForbidden, "unauthorized_client", 1,
		},
		{
			"assignment grants tenant-admin", "carol@acme.com", nil,
			&fakePlatformRoles{roles: []auth.Role{auth.RoleTenantAdmin}, found: true}, http.StatusOK, "", 1,
		},
		{
			"no assignment keeps the claim", "carol@acme.com",
			[]auth.Role{auth.RoleTenantAdmin},
			&fakePlatformRoles{}, http.StatusOK, "", 1,
		},
		{
			"Postgres outage fails closed", "carol@acme.com",
			[]auth.Role{auth.RoleTenantAdmin},
			&fakePlatformRoles{err: &pgconn.PgError{Code: "08006"}}, http.StatusServiceUnavailable, "token_store_unavailable", 1,
		},
		{
			"other lookup failure fails closed", "carol@acme.com",
			[]auth.Role{auth.RoleTenantAdmin},
			&fakePlatformRoles{err: errors.New("decode failed")}, http.StatusInternalServerError, "token_exchange_failed", 1,
		},
		{
			"the token's own user needs no lookup", "alice@acme.com", nil,
			&fakePlatformRoles{err: errors.New("unreachable")}, http.StatusOK, "", 0,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			store := &fakeRevocationStore{revoked: []issuedtokenstore.RevokedToken{{JTI: "root-jti", Subject: "alice@acme.com", IsRoot: true}}}
			signer := jwt.NewHMACSigner("dev-1", []byte("dev-secret"))
			srv := mustNewServer(t, Options{
				CapabilityTokenTTL: 15 * time.Minute,
				Signer:             jwtsign.HMAC(signer),
				Verifier:           signer,
				Issuer:             "https://lenny.dev.test/token",
				IssuedTokens:       store,
				Auditor:            &recordingAuditor{},
				PlatformRoles:      c.resolver,
			})
			caller := mintToken(t, signer, jwt.Claims{Subject: c.callerSub, TenantID: "acme", JWTID: "caller-jti", Typ: auth.TokenUserBearer, Roles: c.claim})
			subject := mintToken(t, signer, jwt.Claims{Subject: "alice@acme.com", TenantID: "acme", JWTID: "root-jti", Typ: auth.TokenUserBearer})

			status, env := decodeExchange(t, doExchange(t, srv, caller, Request{GrantType: grantTypeExchange, SubjectToken: subject, RequestedTokenType: revokedTokenType}))
			if status != c.wantStatus {
				t.Fatalf("status %d body %v, want %d", status, env, c.wantStatus)
			}
			if c.wantError != "" && env["error"] != c.wantError {
				t.Errorf("error = %v, want %s", env["error"], c.wantError)
			}
			if (status == http.StatusOK) != (store.gotRoot == "root-jti") {
				t.Errorf("store reached with root %q on status %d", store.gotRoot, status)
			}
			if c.resolver.lookups != c.wantLookup {
				t.Errorf("role lookups = %d, want %d", c.resolver.lookups, c.wantLookup)
			}
		})
	}
}
