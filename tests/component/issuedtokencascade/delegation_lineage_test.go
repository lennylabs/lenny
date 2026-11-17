//go:build component

// SPDX-License-Identifier: MIT

// Component test for the lineage the Token Service records on an external
// a2a_delegation child. It drives the in-process Token Service handler
// against a real Postgres container with the production migrations
// applied, so the rows RevokeCascade walks are the rows the exchange wrote.
package issuedtokencascade_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/auth/jwt"
	"github.com/lennylabs/lenny/pkg/gateway/audit/auditstore"
	"github.com/lennylabs/lenny/pkg/gateway/storage/issuedtokenstore"
	"github.com/lennylabs/lenny/pkg/storerouter"
	"github.com/lennylabs/lenny/pkg/tokenservice"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
	"github.com/lennylabs/lenny/tests/testinfra/jwtsign"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

const (
	lineageGrantExchange = "urn:ietf:params:oauth:grant-type:token-exchange"
	lineageRevokedType   = "urn:ietf:params:oauth:token-type:access_token:revoked"
)

// lineageHarness is one in-process Token Service over a migrated Postgres,
// wired as cmd/lenny-token-service wires its Postgres path.
type lineageHarness struct {
	pg     *containers.Postgres
	store  *issuedtokenstore.Store
	signer *jwt.HMACSigner
	srv    *tokenservice.Server
}

func startLineageHarness(t *testing.T) *lineageHarness {
	t.Helper()
	pg := containers.StartPostgres(t, containers.PostgresOptions{
		MigrationsDir: schematest.RepoRoot(t) + "/migrations",
	})
	router, err := storerouter.NewSingleShardRouter(storerouter.Config{Postgres: pg.Pool})
	if err != nil {
		t.Fatalf("store router: %v", err)
	}
	store := issuedtokenstore.New(pg.Pool)
	signer := jwt.NewHMACSigner("dev-1", []byte("dev-secret"))
	srv, err := tokenservice.NewServer(tokenservice.Options{
		CapabilityTokenTTL: 15 * time.Minute,
		Signer:             jwtsign.HMAC(signer),
		Verifier:           signer,
		Issuer:             "https://lenny.dev.test/token",
		IssuedTokens:       store,
		Auditor:            auditstore.New(router),
	})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	return &lineageHarness{pg: pg, store: store, signer: signer, srv: srv}
}

// userToken signs a tenant user bearer that has no issued_tokens row, as an
// IdP-derived caller would present.
func (h *lineageHarness) userToken(t *testing.T, tenant, sub, jti string) string {
	t.Helper()
	now := time.Now()
	tok, err := h.signer.Sign(jwt.Claims{
		Subject: sub, TenantID: tenant, JWTID: jti, Typ: auth.TokenUserBearer,
		Scope: "sessions:read sessions:write", Audience: []string{"lenny-gateway"},
		IssuedAt: now.Unix(), Expiry: now.Add(time.Hour).Unix(),
	})
	if err != nil {
		t.Fatalf("sign %s: %v", jti, err)
	}
	return tok
}

// post sends one /v1/oauth/token request and decodes the JSON body.
func (h *lineageHarness) post(t *testing.T, caller string, req tokenservice.Request) (int, map[string]any) {
	t.Helper()
	req.GrantType = lineageGrantExchange
	b, _ := json.Marshal(req)
	r := httptest.NewRequest(http.MethodPost, "/v1/oauth/token", bytes.NewReader(b))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer "+caller)
	w := httptest.NewRecorder()
	h.srv.Handler().ServeHTTP(w, r)
	var out map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return w.Code, out
}

// mint runs an exchange that must succeed and returns the token and its jti.
func (h *lineageHarness) mint(t *testing.T, caller string, req tokenservice.Request) (string, string) {
	t.Helper()
	code, body := h.post(t, caller, req)
	tok, _ := body["access_token"].(string)
	if code != http.StatusOK || tok == "" {
		t.Fatalf("exchange: status %d body %v", code, body)
	}
	claims, err := h.signer.Verify(tok)
	if err != nil || claims.JWTID == "" {
		t.Fatalf("minted token carries no verifiable jti: %v", err)
	}
	return tok, claims.JWTID
}

// spec: 13.3 (Credential Flow) "RFC 8693 parameter mapping to Lenny claims" actor_token, 13.3 (Credential Flow) "Token rotation and revocation" recursive child revocation, 13.3 (Credential Flow) "Platform-admin impersonation" rule 2, 12.2 (Storage Roles) TokenIssuanceStore, 8.3 (Delegation Policy and Lease), 16.7 (Section 25 Audit Events) token.revoked
// diagnosis: a delegation child row without parent_jti or act_sub means
// issuedRecord in pkg/tokenservice/tokenservice.go no longer takes the
// lineage from the actor_token. A live child or grandchild after the
// parent's revocation request means the recursive revocation does not walk
// the parent_jti edge the exchange wrote. A revoked unrelated row means the
// cascade escapes the parent's subtree. A delegation row in the
// impersonation listing, or revoked by the platform-admin full-revoke
// query, means a delegation row matches the impersonation predicate.
func TestRecursiveRevocationReachesExternalDelegationChildren_spec_13_3(t *testing.T) {
	t.Parallel()
	h := startLineageHarness(t)
	ctx := context.Background()
	tenant := freshTenant(t, ctx, h.pg)

	alice := h.userToken(t, tenant, "alice@acme.com", "alice-root-"+tenant)
	bob := h.userToken(t, tenant, "bob@acme.com", "bob-root-"+tenant)

	// The parent is a scope narrowing of alice's bearer, so it has a row.
	parent, parentJTI := h.mint(t, alice, tokenservice.Request{
		SubjectToken: alice, Scope: "sessions:read sessions:write", Audience: "lenny-gateway",
	})
	// The child carries bob's identity and names the parent as its actor.
	child, childJTI := h.mint(t, parent, tokenservice.Request{
		SubjectToken: bob, ActorToken: parent, Scope: "sessions:read", Audience: "lenny-gateway",
	})
	_, grandchildJTI := h.mint(t, child, tokenservice.Request{
		SubjectToken: bob, ActorToken: child, Scope: "sessions:read", Audience: "lenny-gateway",
	})
	// An unrelated tree: a second narrowing of alice and its own child.
	other, otherJTI := h.mint(t, alice, tokenservice.Request{
		SubjectToken: alice, Scope: "sessions:read", Audience: "lenny-gateway",
	})
	_, otherChildJTI := h.mint(t, other, tokenservice.Request{
		SubjectToken: alice, ActorToken: other, Scope: "sessions:read", Audience: "lenny-gateway",
	})

	wantLineage := map[string][2]string{
		parentJTI:     {"", ""},
		childJTI:      {"alice@acme.com", parentJTI},
		grandchildJTI: {"bob@acme.com", childJTI},
		otherJTI:      {"", ""},
		otherChildJTI: {"alice@acme.com", otherJTI},
	}
	for jti, want := range wantLineage {
		row, err := h.store.Get(ctx, tenant, jti)
		if err != nil {
			t.Fatalf("Get %s: %v", jti, err)
		}
		if row.ActSubject != want[0] || row.ParentJTI != want[1] {
			t.Errorf("row %s act_sub=%q parent_jti=%q, want %q / %q", jti, row.ActSubject, row.ParentJTI, want[0], want[1])
		}
	}

	t.Run("no delegation row matches the impersonation predicate", func(t *testing.T) {
		delegationRows := []string{childJTI, grandchildJTI, otherChildJTI}
		active, err := h.store.ListActiveImpersonations(ctx, time.Now())
		if err != nil {
			t.Fatalf("ListActiveImpersonations: %v", err)
		}
		for _, row := range active {
			if slices.Contains(delegationRows, row.JTI) {
				t.Errorf("impersonation listing returned delegation row %s", row.JTI)
			}
		}
		// The platform-admin full revoke keys on act_sub under the
		// predicate; alice is the act_sub of two delegation rows.
		revoked, err := h.store.RevokeImpersonationsByActor(ctx, "alice@acme.com", "full_revoke", time.Now())
		if err != nil {
			t.Fatalf("RevokeImpersonationsByActor: %v", err)
		}
		if len(revoked) != 0 {
			t.Errorf("full-revoke impersonation query revoked %v, want none", revoked)
		}
	})

	code, body := h.post(t, alice, tokenservice.Request{
		SubjectToken: parent, RequestedTokenType: lineageRevokedType,
	})
	if code != http.StatusOK || body["revoked_count"] != float64(3) {
		t.Fatalf("revocation request: status %d body %v, want 200 with revoked_count 3", code, body)
	}

	wantReason := map[string]string{
		parentJTI:     "explicit_revoke",
		childJTI:      "cascade_from_parent",
		grandchildJTI: "cascade_from_parent",
		otherJTI:      "",
		otherChildJTI: "",
	}
	for jti, reason := range wantReason {
		row, err := h.store.Get(ctx, tenant, jti)
		if err != nil {
			t.Fatalf("Get %s: %v", jti, err)
		}
		if row.Revoked() != (reason != "") || row.RevokedReason != reason {
			t.Errorf("row %s revoked=%v reason=%q, want revoked=%v reason=%q", jti, row.Revoked(), row.RevokedReason, reason != "", reason)
		}
	}

	rows, err := h.pg.Pool.Query(ctx,
		`SELECT payload->>'revoked_jti', payload->>'revocation_reason', COALESCE(payload->>'cascade_root_jti', '')
		 FROM audit_log WHERE tenant_id = $1 AND event_type = 'token.revoked'`, tenant)
	if err != nil {
		t.Fatalf("read token.revoked rows: %v", err)
	}
	defer rows.Close()
	got := map[string][2]string{}
	for rows.Next() {
		var jti, reason, root string
		if err := rows.Scan(&jti, &reason, &root); err != nil {
			t.Fatalf("scan token.revoked row: %v", err)
		}
		got[jti] = [2]string{reason, root}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("token.revoked rows: %v", err)
	}
	wantAudit := map[string][2]string{
		parentJTI:     {"explicit_revoke", ""},
		childJTI:      {"cascade_from_parent", parentJTI},
		grandchildJTI: {"cascade_from_parent", parentJTI},
	}
	if len(got) != len(wantAudit) {
		t.Errorf("token.revoked rows = %v, want exactly %v", got, wantAudit)
	}
	for jti, want := range wantAudit {
		if got[jti] != want {
			t.Errorf("token.revoked for %s = %v, want %v", jti, got[jti], want)
		}
	}

	// The revoked child can no longer act as the parent of a new child.
	if code, body := h.post(t, alice, tokenservice.Request{
		SubjectToken: bob, ActorToken: child, Scope: "sessions:read", Audience: "lenny-gateway",
	}); code != http.StatusBadRequest || body["error"] != "invalid_grant" {
		t.Errorf("mint with the revoked child as actor: status %d body %v, want 400 invalid_grant", code, body)
	}
}
