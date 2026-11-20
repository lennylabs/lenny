// SPDX-License-Identifier: MIT

package tokenservice

import (
	"net/http"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/auth/jwt"
	"github.com/lennylabs/lenny/pkg/storage/issuedtokenstore"
	"github.com/lennylabs/lenny/tests/testinfra/jwtsign"
)

// lineageClaims returns acme claims for sub and jti of the given typ.
func lineageClaims(sub, jti string, typ auth.TokenType) jwt.Claims {
	return jwt.Claims{
		Subject: sub, TenantID: "acme", JWTID: jti, Typ: typ,
		Scope: "tools:sessions:read tools:sessions:write", Audience: []string{"lenny-gateway"},
	}
}

// spec: 13.3 (Credential Flow) "RFC 8693 parameter mapping to Lenny claims" actor_token, 13.3 (Credential Flow) "Lineage of derived tokens", 13.3 (Credential Flow) "Platform-admin impersonation" rule 2, 12.2 (Storage Roles) TokenIssuanceStore act_sub and parent_jti, 8.3 (Delegation Policy and Lease) recursive revocation
//
// The issued_tokens row of an a2a_delegation mint records the actor
// token's sub in act_sub and the actor token's jti in parent_jti, which is
// the edge RevokeCascade walks, so a child whose subject differs from its
// actor still names the actor as its parent. Every other exchange without
// an actor records the subject token's jti in parent_jti and leaves
// act_sub empty: a narrowing by another caller and a lease issuance each
// name their subject as the parent. A self rotation does not name the
// token it replaces, and the in-memory store records neither column. No row the Token Service writes matches the
// impersonation predicate (act_sub set, parent_jti null).
func TestIssuedRecordLineagePerExchangeType_spec_13_3(t *testing.T) {
	signer := jwt.NewHMACSigner("dev-1", []byte("dev-secret"))
	alice := mintToken(t, signer, lineageClaims("alice@acme.com", "alice-1", auth.TokenUserBearer))
	bob := mintToken(t, signer, lineageClaims("bob@acme.com", "bob-1", auth.TokenUserBearer))
	bobNoJTI := mintToken(t, signer, lineageClaims("bob@acme.com", "", auth.TokenUserBearer))
	capability := mintToken(t, signer, lineageClaims("alice@acme.com", "cap-1", auth.TokenSessionCapability))

	cases := []struct {
		name          string
		caller        string
		req           Request
		wantActSub    string
		wantParentJTI string
	}{
		{
			name:   "delegation mint with a distinct actor",
			caller: bob,
			req: Request{
				SubjectToken: alice, ActorToken: bob,
				Scope: "tools:sessions:read", Audience: "lenny-gateway",
			},
			wantActSub: "bob@acme.com", wantParentJTI: "bob-1",
		},
		{
			name:   "delegation mint whose actor is the subject",
			caller: alice,
			req: Request{
				SubjectToken: alice, ActorToken: alice,
				Scope: "tools:sessions:read", Audience: "lenny-gateway",
			},
			wantActSub: "alice@acme.com", wantParentJTI: "alice-1",
		},
		{
			name:   "delegation mint whose actor carries no jti records no lineage",
			caller: bob,
			req: Request{
				SubjectToken: alice, ActorToken: bobNoJTI,
				Scope: "tools:sessions:read", Audience: "lenny-gateway",
			},
		},
		{
			name:   "self rotation starts a new lineage",
			caller: alice,
			req: Request{
				SubjectToken: alice,
				Scope:        "tools:sessions:read tools:sessions:write", Audience: "lenny-gateway",
			},
		},
		{
			name:   "scope narrowing by another caller",
			caller: bob,
			req: Request{
				SubjectToken: alice,
				Scope:        "tools:sessions:read", Audience: "lenny-gateway",
			},
			wantParentJTI: "alice-1",
		},
		{
			name:   "scope narrowing by the subject's owner",
			caller: alice,
			req: Request{
				SubjectToken: alice,
				Scope:        "tools:sessions:read", Audience: "lenny-gateway",
			},
			wantParentJTI: "alice-1",
		},
		{
			name:   "credential lease issuance",
			caller: bob,
			req: Request{
				SubjectToken: capability,
				Scope:        "tools:sessions:read", Audience: "lenny-gateway",
			},
			wantParentJTI: "cap-1",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			store := &recordingIssuedTokenStore{}
			srv := mustNewServer(t, Options{
				CapabilityTokenTTL: 15 * time.Minute,
				Signer:             jwtsign.HMAC(signer),
				Verifier:           signer,
				Issuer:             "https://lenny.dev.test/token",
				IssuedTokens:       store,
			})
			c.req.GrantType = grantTypeExchange
			resp := doExchange(t, srv, c.caller, c.req)
			defer func() { _ = resp.Body.Close() }()
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status = %d, want 200", resp.StatusCode)
			}
			recs := store.snapshot()
			if len(recs) != 1 {
				t.Fatalf("issued_tokens rows = %d, want 1", len(recs))
			}
			rec := recs[0]
			if rec.ActSubject != c.wantActSub {
				t.Errorf("act_sub = %q, want %q", rec.ActSubject, c.wantActSub)
			}
			if rec.ParentJTI != c.wantParentJTI {
				t.Errorf("parent_jti = %q, want %q", rec.ParentJTI, c.wantParentJTI)
			}
			if rec.Subject != "alice@acme.com" {
				t.Errorf("sub = %q, want the subject alice@acme.com", rec.Subject)
			}
			assertNotImpersonationRow(t, rec)
		})
	}
}

// assertNotImpersonationRow fails when rec matches the §13.3
// "Platform-admin impersonation" rule 2 predicate. The Token Service never
// mints an impersonation bearer, so none of its rows may match.
func assertNotImpersonationRow(t *testing.T, rec issuedtokenstore.IssuedToken) {
	t.Helper()
	if rec.ActSubject != "" && rec.ParentJTI == "" {
		t.Errorf("row %q has act_sub %q and no parent_jti, so it matches the impersonation predicate", rec.JTI, rec.ActSubject)
	}
}

// spec: 13.3 (Credential Flow) "Lineage of derived tokens", 13.3 (Credential Flow) "Platform-admin impersonation" rule 2
//
// derivedLineage takes act_sub and parent_jti from the actor when an actor
// is present, together or not at all, takes neither for a self rotation,
// and otherwise takes parent_jti from the subject with no act_sub. No
// result matches the impersonation predicate.
func TestDerivedLineageNamesTheActorOrTheSubject(t *testing.T) {
	subject := jwt.Claims{Subject: "alice@acme.com", JWTID: "alice-1"}
	cases := []struct {
		name       string
		subject    jwt.Claims
		actor      *jwt.Claims
		rotation   bool
		wantSub    string
		wantParent string
	}{
		{"no actor names the subject", subject, nil, false, "", "alice-1"},
		{"a rotation names no parent", subject, nil, true, "", ""},
		{"no actor and a subject without jti", jwt.Claims{Subject: "alice@acme.com"}, nil, false, "", ""},
		{"actor with jti", subject, &jwt.Claims{Subject: "bob@acme.com", JWTID: "bob-1"}, false, "bob@acme.com", "bob-1"},
		{"actor without jti", subject, &jwt.Claims{Subject: "bob@acme.com"}, false, "", ""},
		{"actor without sub", subject, &jwt.Claims{JWTID: "bob-1"}, false, "", "bob-1"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			sub, parent := derivedLineage(c.subject, c.actor, c.rotation)
			if sub != c.wantSub || parent != c.wantParent {
				t.Errorf("derivedLineage = (%q, %q), want (%q, %q)", sub, parent, c.wantSub, c.wantParent)
			}
			if sub != "" && parent == "" {
				t.Errorf("act_sub %q without parent_jti matches the impersonation predicate", sub)
			}
		})
	}
}
