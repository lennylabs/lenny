// SPDX-License-Identifier: MIT

//go:build security

package security_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/auth/jwt"
	"github.com/lennylabs/lenny/pkg/kms"
	"github.com/lennylabs/lenny/pkg/tokenservice"
	kmsstub "github.com/lennylabs/lenny/tests/testinfra/stubs/kms"
)

// identityRulesTokenService builds the Token Service over the KMS stub as
// cmd/lenny-token-service builds it, and returns a function that signs an
// acme platform-admin user bearer under the same key.
func identityRulesTokenService(t *testing.T) (*tokenservice.Server, func(sub, jti, scope, origin string) string) {
	t.Helper()
	srv, signRoles := identityRulesTokenServiceWithRoles(t)
	sign := func(sub, jti, scope, origin string) string {
		return signRoles(sub, jti, scope, origin, []auth.Role{auth.RolePlatformAdmin})
	}
	return srv, sign
}

// identityRulesTokenServiceWithRoles is identityRulesTokenService with a
// signer that takes the bearer's roles.
func identityRulesTokenServiceWithRoles(t *testing.T) (*tokenservice.Server, func(sub, jti, scope, origin string, roles []auth.Role) string) {
	t.Helper()
	stub := kmsstub.New(t)
	ks, signer, err := jwt.LoadSigning(context.Background(), stub.AsProvider(), map[string]string{kms.AliasJWTSigning: "key-1"}, nil)
	if err != nil {
		t.Fatalf("LoadSigning: %v", err)
	}
	srv, err := tokenservice.NewServer(tokenservice.Options{
		CapabilityTokenTTL: 15 * time.Minute,
		Signer:             signer, Verifier: ks, Issuer: "https://lenny.dev.test/token",
	})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	sign := func(sub, jti, scope, origin string, roles []auth.Role) string {
		now := time.Now()
		tok, err := signer.Sign(context.Background(), jwt.Claims{
			Subject: sub, TenantID: "acme", Typ: auth.TokenUserBearer, CallerType: "human",
			Roles: roles, Scope: scope, Origin: origin,
			Audience: []string{"lenny-gateway"}, IssuedAt: now.Unix(), Expiry: now.Add(time.Hour).Unix(),
			JWTID: jti,
		})
		if err != nil {
			t.Fatalf("Sign: %v", err)
		}
		return tok
	}
	return srv, sign
}

// postIdentityExchange posts one exchange and returns the status and body.
func postIdentityExchange(t *testing.T, srv *tokenservice.Server, caller, body string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/oauth/token", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+caller)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	var out map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return w.Code, out
}

// spec: §13.3 (Credential Flow)
// diagnosis: bob, holding alice's token in the same tenant, exchanged it
// for a token at alice's scope, with a scope parameter equal to hers or
// with none. He then holds a copy of her token under a fresh jti. Inspect
// the caller-identity check in tokenexchange.Validate.
func TestAnotherCallerCannotCopyAUsersTokenAtTheSameScope(t *testing.T) {
	srv, sign := identityRulesTokenService(t)
	alice := sign("alice@acme.com", "jti-alice", "tools:health:read tools:pool:read", "")
	bob := sign("bob@acme.com", "jti-bob", "", "")

	for _, scope := range []string{"", "tools:health:read tools:pool:read", "tools:pool:read tools:health:read"} {
		body := fmt.Sprintf(`{"grant_type":%q,"subject_token":%q,"scope":%q,"audience":"lenny-gateway"}`,
			"urn:ietf:params:oauth:grant-type:token-exchange", alice, scope)
		code, out := postIdentityExchange(t, srv, bob, body)
		if code != http.StatusBadRequest || out["error"] != "invalid_scope" {
			t.Errorf("scope %q: status %d body %v, want 400 invalid_scope", scope, code, out)
		}
		if _, ok := out["access_token"]; ok {
			t.Errorf("scope %q: bob received an access_token for alice at her scope", scope)
		}
	}
}

// spec: §13.3 (Credential Flow), §27.3 (Authentication)
// diagnosis: a playground bearer presented as the subject_token or the
// actor_token was exchanged for a token without the playground origin
// claim, which escapes the playground logout revocation and the
// playground session caps. Inspect the playground-origin check in
// tokenexchange.Validate and the Origin mapping in toExchangeToken.
func TestAPlaygroundBearerCannotBeExchanged(t *testing.T) {
	srv, sign := identityRulesTokenService(t)
	caller := sign("alice@acme.com", "jti-caller", "", "")
	plain := sign("alice@acme.com", "jti-plain", "", "")
	playground := sign("alice@acme.com", "jti-pg", "tools:health:read", jwt.OriginPlayground)

	for name, body := range map[string]string{
		"subject": fmt.Sprintf(`{"grant_type":%q,"subject_token":%q,"audience":"lenny-gateway"}`,
			"urn:ietf:params:oauth:grant-type:token-exchange", playground),
		"actor": fmt.Sprintf(`{"grant_type":%q,"subject_token":%q,"actor_token":%q,"audience":"lenny-gateway"}`,
			"urn:ietf:params:oauth:grant-type:token-exchange", plain, playground),
	} {
		code, out := postIdentityExchange(t, srv, caller, body)
		if code != http.StatusBadRequest || out["error"] != "invalid_grant" {
			t.Errorf("playground %s: status %d body %v, want 400 invalid_grant", name, code, out)
		}
		if _, ok := out["access_token"]; ok {
			t.Errorf("playground %s: exchange returned an access_token", name)
		}
	}
}

// spec: §13.3 (Credential Flow)
// diagnosis: bob, holding alice's token in the same tenant, presented it
// as the subject_token of a child mint with his own token as the
// actor_token, and received a child that carries alice's identity at her
// scope, outside the reach of the revocation of alice's token. Inspect the
// child-mint branch of the caller-identity check in tokenexchange.Validate.
func TestAChildMintCannotCopyAnotherUsersTokenAtTheSameScope(t *testing.T) {
	srv, sign := identityRulesTokenService(t)
	alice := sign("alice@acme.com", "jti-alice", "tools:health:read tools:pool:read", "")
	bob := sign("bob@acme.com", "jti-bob", "", "")

	for _, scope := range []string{"", "tools:health:read tools:pool:read"} {
		body := fmt.Sprintf(`{"grant_type":%q,"subject_token":%q,"actor_token":%q,"scope":%q,"audience":"lenny-gateway"}`,
			"urn:ietf:params:oauth:grant-type:token-exchange", alice, bob, scope)
		code, out := postIdentityExchange(t, srv, bob, body)
		if code != http.StatusBadRequest || out["error"] != "invalid_scope" {
			t.Errorf("scope %q: status %d body %v, want 400 invalid_scope", scope, code, out)
		}
		if _, ok := out["access_token"]; ok {
			t.Errorf("scope %q: bob received a child of alice's token at her scope", scope)
		}
	}
}

// spec: §13.3 (Credential Flow)
// diagnosis: carol presented bob's token as the actor_token of a child
// mint and received a child whose act claim and lineage name bob. Inspect
// the actor-is-caller check in tokenexchange.Validate.
func TestAChildMintRefusesAnActorTokenThatIsNotTheCallers(t *testing.T) {
	srv, sign := identityRulesTokenService(t)
	alice := sign("alice@acme.com", "jti-alice", "tools:health:read tools:pool:read", "")
	bob := sign("bob@acme.com", "jti-bob", "", "")
	carol := sign("carol@acme.com", "jti-carol", "", "")

	body := fmt.Sprintf(`{"grant_type":%q,"subject_token":%q,"actor_token":%q,"scope":"tools:health:read","audience":"lenny-gateway"}`,
		"urn:ietf:params:oauth:grant-type:token-exchange", alice, bob)
	code, out := postIdentityExchange(t, srv, carol, body)
	if code != http.StatusBadRequest || out["error"] != "invalid_grant" {
		t.Errorf("status %d body %v, want 400 invalid_grant", code, out)
	}
	if _, ok := out["access_token"]; ok {
		t.Errorf("carol received a child minted with bob's actor token")
	}
}

// spec: §13.3 (Credential Flow)
// diagnosis: bob, a user in alice's tenant without an administrator role,
// sent a recursive-revocation request for alice's token and was not
// refused with 403 unauthorized_client. Inspect authorizeRevocation in
// pkg/tokenservice/tokenservice.go.
func TestAnotherUserCannotRevokeAUsersToken(t *testing.T) {
	srv, sign := identityRulesTokenServiceWithRoles(t)
	alice := sign("alice@acme.com", "jti-alice", "", "", nil)
	for name, roles := range map[string][]auth.Role{
		"no role":       nil,
		"user":          {auth.RoleUser},
		"tenant-viewer": {auth.RoleTenantViewer},
	} {
		bob := sign("bob@acme.com", "jti-bob", "", "", roles)
		body := fmt.Sprintf(`{"grant_type":%q,"subject_token":%q,"requested_token_type":%q}`,
			"urn:ietf:params:oauth:grant-type:token-exchange", alice, "urn:ietf:params:oauth:token-type:access_token:revoked")
		code, out := postIdentityExchange(t, srv, bob, body)
		if code != http.StatusForbidden || out["error"] != "unauthorized_client" {
			t.Errorf("%s: status %d body %v, want 403 unauthorized_client", name, code, out)
		}
	}
}
