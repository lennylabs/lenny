//go:build component

// SPDX-License-Identifier: MIT

// Package tokenproperties_test checks the token rule properties of the
// Credential Flow section of the specification against the Token Service,
// the playground mint, the platform-admin impersonation issuer, and the
// full revoke of a user, all over one Postgres issued-token store with the
// production migrations applied. A generated test drives random sequences
// of operations and checks every property after every step; a second test
// replays the scenarios change 0165's Alloy model found.
//
// The generated test runs a bounded number of sequences by default, so it
// fits the component tier's budget. Longer runs pass rapid's own flags,
// for example:
//
//	CGO_ENABLED=0 go test -tags component -timeout 60m \
//	  ./tests/component/tokenproperties -run Generated \
//	  -rapid.checks=5000
//
// The seed is fixed unless -rapid.seed or RAPID_SEED names one, and a
// failure prints the seed, the minimized sequence of operations, and the
// property it breaks.
package tokenproperties_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/audit/auditstore"
	"github.com/lennylabs/lenny/pkg/auth/jwt"
	"github.com/lennylabs/lenny/pkg/common/seqname"
	"github.com/lennylabs/lenny/pkg/credential/impersonation"
	"github.com/lennylabs/lenny/pkg/gateway/playground"
	"github.com/lennylabs/lenny/pkg/storage/issuedtokenstore"
	"github.com/lennylabs/lenny/pkg/storerouter"
	"github.com/lennylabs/lenny/pkg/tokenservice"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
	"github.com/lennylabs/lenny/tests/testinfra/jwtsign"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// platformTenant is the tenant the impersonation issuer writes its
// started events under.
const platformTenant = "platform"

// harness is one Token Service, one playground mint, and one impersonation
// issuer over a migrated Postgres, wired as the binaries wire their
// Postgres paths, with an HMAC key standing in for the signing key.
type harness struct {
	pg         *containers.Postgres
	store      *issuedtokenstore.Store
	signer     *jwt.HMACSigner
	tokens     http.Handler
	playground http.Handler
	imp        *impersonation.Service
}

// noopCache is the impersonation issuer's revocation cache. The properties
// read revocation from Postgres, the authoritative store, so the cache
// holds nothing.
type noopCache struct{}

func (noopCache) Revoke(string) {}

func newHarness(t *testing.T) *harness {
	t.Helper()
	pg := containers.StartPostgres(t, containers.PostgresOptions{
		MigrationsDir: schematest.RepoRoot(t) + "/migrations",
	})
	router, err := storerouter.NewSingleShardRouter(storerouter.Config{Postgres: pg.Pool})
	if err != nil {
		t.Fatalf("store router: %v", err)
	}
	store := issuedtokenstore.New(pg.Pool)
	audit := auditstore.New(router)
	signer := jwt.NewHMACSigner("prop-1", []byte("token-properties-secret"))
	srv, err := tokenservice.NewServer(tokenservice.Options{
		CapabilityTokenTTL: 15 * time.Minute,
		Signer:             jwtsign.HMAC(signer),
		Verifier:           signer,
		Issuer:             "https://lenny.dev.test/token",
		PerDialectCap:      map[string]time.Duration{"lenny-gateway": 24 * time.Hour, "lenny-ops": time.Hour},
		IssuedTokens:       store,
		Auditor:            audit,
	})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	pgHandler := playground.New(playground.Config{
		Enabled: true, AuthMode: playground.AuthModeAPIKey, CapabilityTokenTTL: 15 * time.Minute,
	}, playground.Options{Signer: jwtsign.HMAC(signer), Verifier: signer, IssuedTokens: store})
	imp := impersonation.New(store, noopCache{}, audit, audit, jwtsign.HMAC(signer), impersonation.Config{
		PlatformTenantID: platformTenant,
		Issuer:           "https://lenny.dev.test/gateway",
		Audience:         []string{"lenny-gateway"},
		NewID:            randomID,
	})
	h := &harness{
		pg: pg, store: store, signer: signer,
		tokens: srv.Handler(), playground: pgHandler.TokenRoutes(), imp: imp,
	}
	h.seedTenant(t, platformTenant)
	return h
}

// seedTenant inserts a tenant row and the audit sequence its hash chain
// draws from, as tenant creation does in production.
func (h *harness) seedTenant(t tb, id string) {
	t.Helper()
	ctx := context.Background()
	if _, err := h.pg.Pool.Exec(ctx,
		`INSERT INTO tenants (id, genesis_nonce) VALUES ($1, $2) ON CONFLICT DO NOTHING`, id, []byte{0x01}); err != nil {
		t.Fatalf("seed tenant %s: %v", id, err)
	}
	if _, err := h.pg.Pool.Exec(ctx,
		"CREATE SEQUENCE IF NOT EXISTS "+seqname.AuditSequenceName(id)+" START WITH 1 INCREMENT BY 1 NO CYCLE"); err != nil {
		t.Fatalf("provision audit sequence for %s: %v", id, err)
	}
}

// rowState is the part of an issued_tokens row the properties read.
type rowState struct {
	tenant, sub, parent, actSub, impTenant, impJTI, reason string
	revoked                                                bool
	exp                                                    time.Time
}

// snapshot reads every issued_tokens row of the listed tenants. The
// container's role is the superuser, which row-level security does not
// restrict, so one query reads rows of several tenants.
func (h *harness) snapshot(t tb, tenants []string) map[string]rowState {
	t.Helper()
	rows, err := h.pg.Pool.Query(context.Background(),
		`SELECT jti, tenant_id, sub, COALESCE(parent_jti, ''), COALESCE(act_sub, ''),
		        COALESCE(impersonator_tenant_id, ''), COALESCE(impersonator_jti, ''),
		        revoked_at IS NOT NULL, COALESCE(revoked_reason, ''), exp
		   FROM issued_tokens WHERE tenant_id = ANY($1)`, tenants)
	if err != nil {
		t.Fatalf("snapshot issued_tokens: %v", err)
	}
	defer rows.Close()
	out := map[string]rowState{}
	for rows.Next() {
		var jti string
		var r rowState
		if err := rows.Scan(&jti, &r.tenant, &r.sub, &r.parent, &r.actSub, &r.impTenant, &r.impJTI,
			&r.revoked, &r.reason, &r.exp); err != nil {
			t.Fatalf("scan issued_tokens: %v", err)
		}
		out[jti] = r
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read issued_tokens: %v", err)
	}
	return out
}

// tokenReply is the decoded reply of POST /v1/oauth/token.
type tokenReply struct {
	status int
	token  string
	reason string
}

// postToken sends one RFC 8693 request to the Token Service.
func (h *harness) postToken(t tb, caller string, body tokenservice.Request) tokenReply {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal token request: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/oauth/token", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+caller)
	w := httptest.NewRecorder()
	h.tokens.ServeHTTP(w, req)
	var out struct {
		AccessToken string `json:"access_token"`
		Error       string `json:"error"`
		Description string `json:"error_description"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode token reply %q: %v", w.Body.String(), err)
	}
	return tokenReply{status: w.Code, token: out.AccessToken, reason: strings.TrimSpace(out.Error + " " + out.Description)}
}

// postPlayground sends the apiKey-mode playground mint with subject pasted
// as the bearer.
func (h *harness) postPlayground(t tb, subject string) tokenReply {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/playground/token", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+subject)
	w := httptest.NewRecorder()
	h.playground.ServeHTTP(w, req)
	var out struct {
		BearerToken string `json:"bearerToken"`
		Error       struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode playground reply %q: %v", w.Body.String(), err)
	}
	return tokenReply{status: w.Code, token: out.BearerToken, reason: out.Error.Code}
}

// randomID returns 128 random bits in hex, for jti values and tenant names
// that no other run shares.
func randomID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(fmt.Sprintf("crypto/rand: %v", err))
	}
	return hex.EncodeToString(b[:])
}
