// SPDX-License-Identifier: MIT

//go:build security

// Tier-9 security test for the §13.3 write-before-issue step (1a)
// re-read. A real Token Service over a real Postgres issued-token store
// rotates a token, which revokes it, and is then handed the revoked token
// as the subject_token and as the actor_token of an a2a delegation
// exchange. No revocation cache is wired in front of the Token Service,
// so the in-transaction re-read is the only check that can refuse the
// exchange. Before the re-read existed both exchanges minted a child.
package tier9_security

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/auth/jwt"
	"github.com/lennylabs/lenny/pkg/common/seqname"
	"github.com/lennylabs/lenny/pkg/gateway/audit/auditstore"
	"github.com/lennylabs/lenny/pkg/gateway/storage/issuedtokenstore"
	"github.com/lennylabs/lenny/pkg/storerouter"
	"github.com/lennylabs/lenny/pkg/tokenservice"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
	"github.com/lennylabs/lenny/tests/testinfra/jwtsign"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// sourceRevokedHarness is a Token Service wired to a Postgres
// issued-token store and the durable audit chain, as
// cmd/lenny-token-service wires it.
type sourceRevokedHarness struct {
	srv    *tokenservice.Server
	signer *jwt.HMACSigner
	store  *issuedtokenstore.Store
	tenant string
}

func newSourceRevokedHarness(t *testing.T) *sourceRevokedHarness {
	t.Helper()
	ctx := context.Background()
	pg := containers.StartPostgres(t, containers.PostgresOptions{MigrationsDir: filepath.Join(schematest.RepoRoot(t), "migrations")})
	const tenant = "acme"
	if _, err := pg.Pool.Exec(ctx,
		`INSERT INTO tenants (id, genesis_nonce) VALUES ($1, $2) ON CONFLICT DO NOTHING`, tenant, []byte{0x01}); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	if _, err := pg.Pool.Exec(ctx,
		"CREATE SEQUENCE IF NOT EXISTS "+seqname.AuditSequenceName(tenant)+" START WITH 1 INCREMENT BY 1 NO CYCLE"); err != nil {
		t.Fatalf("provision audit sequence: %v", err)
	}
	router, err := storerouter.NewSingleShardRouter(storerouter.Config{Postgres: pg.Pool})
	if err != nil {
		t.Fatalf("store router: %v", err)
	}
	signer := jwt.NewHMACSigner("dev-1", []byte("dev-secret"))
	store := issuedtokenstore.New(pg.Pool)
	srv, err := tokenservice.NewServer(tokenservice.Options{
		CapabilityTokenTTL: 15 * time.Minute,
		Signer:             jwtsign.HMAC(signer),
		Verifier:           signer,
		Issuer:             "https://lenny.dev.test/token",
		PerDialectCap:      map[string]time.Duration{"lenny-gateway": 24 * time.Hour},
		IssuedTokens:       store,
		Auditor:            auditstore.New(router),
	})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	return &sourceRevokedHarness{srv: srv, signer: signer, store: store, tenant: tenant}
}

// record signs claims for jti and records the token as issued, so the
// store holds a live row for it.
func (h *sourceRevokedHarness) record(t *testing.T, jti string, typ auth.TokenType, depth int) string {
	t.Helper()
	now := time.Now().UTC()
	claims := jwt.Claims{
		Subject: "alice@acme.com", TenantID: h.tenant, JWTID: jti, Typ: typ,
		Scope: "sessions:read", Audience: []string{"lenny-gateway"},
		IssuedAt: now.Add(-time.Minute).Unix(), Expiry: now.Add(time.Hour).Unix(),
		DelegationDepth: depth,
	}
	if typ == auth.TokenSessionCapability {
		claims.SessionID = "sess_parent"
	}
	tok, err := h.signer.Sign(claims)
	if err != nil {
		t.Fatalf("Sign %s: %v", jti, err)
	}
	if err := h.store.Record(context.Background(), issuedtokenstore.IssuedToken{
		JTI: jti, TenantID: h.tenant, Subject: claims.Subject, TokenHash: []byte(jti),
		Scope: []string{"sessions:read"}, Audiences: []string{"lenny-gateway"},
		IssuedAt: now, ExpiresAt: now.Add(time.Hour),
	}); err != nil {
		t.Fatalf("Record %s: %v", jti, err)
	}
	return tok
}

// exchange posts body to the Token Service with caller as the bearer.
func (h *sourceRevokedHarness) exchange(t *testing.T, caller string, body tokenservice.Request) (int, map[string]any) {
	t.Helper()
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/v1/oauth/token", strings.NewReader(string(b)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+caller)
	w := httptest.NewRecorder()
	h.srv.Handler().ServeHTTP(w, req)
	raw, _ := io.ReadAll(w.Result().Body)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	return w.Code, out
}

// rotateAway rotates tok through the self-rotation grant, which revokes
// it in the same transaction that mints its replacement.
func (h *sourceRevokedHarness) rotateAway(t *testing.T, tok string) {
	t.Helper()
	code, body := h.exchange(t, tok, tokenservice.Request{
		GrantType: "urn:ietf:params:oauth:grant-type:token-exchange", SubjectToken: tok,
		Scope: "sessions:read", Audience: "lenny-gateway",
	})
	if code != http.StatusOK {
		t.Fatalf("rotation status = %d, want 200 (body %v)", code, body)
	}
}

// spec: 13.3 (Credential Flow)
// diagnosis: a failure means a token revoked through rotation was accepted
// as the subject_token or the actor_token of a delegation exchange, so a
// rotated-away credential can still produce a child token. Check that
// pkg/tokenservice passes the subject and actor jtis to the store and
// that issuedtokenstore re-reads revoked_at under the §11.7 lock before
// it INSERTs, and that the handler maps the typed error to invalid_grant.
func TestTokenServiceRefusesRevokedSubjectAndActor_spec_13_3(t *testing.T) {
	for _, tc := range []struct {
		name       string
		asActor    bool
		wantReason string
	}{
		{"revoked subject_token", false, "subject_token_revoked"},
		{"revoked actor_token", true, "actor_token_revoked"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newSourceRevokedHarness(t)
			rotated := h.record(t, "rotated-1", auth.TokenUserBearer, 0)
			h.rotateAway(t, rotated)
			if got, err := h.store.Get(context.Background(), h.tenant, "rotated-1"); err != nil || !got.Revoked() {
				t.Fatalf("rotation did not revoke the token: err=%v", err)
			}

			caller := h.record(t, "caller-1", auth.TokenUserBearer, 0)
			req := tokenservice.Request{
				GrantType: "urn:ietf:params:oauth:grant-type:token-exchange",
				Scope:     "sessions:read", Audience: "lenny-gateway",
			}
			if tc.asActor {
				req.SubjectToken = h.record(t, "subject-1", auth.TokenUserBearer, 0)
				req.ActorToken = rotated
			} else {
				req.SubjectToken = rotated
				req.ActorToken = h.record(t, "actor-1", auth.TokenSessionCapability, 1)
			}
			code, body := h.exchange(t, caller, req)
			if code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 (body %v)", code, body)
			}
			if body["error"] != "invalid_grant" || body["error_description"] != tc.wantReason {
				t.Errorf("error = %v / %v, want invalid_grant / %s", body["error"], body["error_description"], tc.wantReason)
			}
			if body["access_token"] != nil {
				t.Error("a revoked source produced an access_token")
			}
		})
	}
}
