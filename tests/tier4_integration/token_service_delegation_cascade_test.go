//go:build integration

// SPDX-License-Identifier: MIT

package tier4_integration_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/auth/jwt"
	"github.com/lennylabs/lenny/pkg/kms"
	"github.com/lennylabs/lenny/tests/testinfra/opsprocess"
	"github.com/lennylabs/lenny/tests/testinfra/stackkey"
	"github.com/lennylabs/lenny/tests/testinfra/tlsredis"
	tokensvc "github.com/lennylabs/lenny/tests/testinfra/tokenservice"
)

// delegationCascadeBound is how soon after the recursive revocation
// lenny-ops must refuse a revoked delegation child. It is far below the
// 30 s revocation set read, so only the Redis path meets it.
const delegationCascadeBound = 5 * time.Second

// tokenServicePost posts form to base's POST /v1/oauth/token with caller as
// the caller token and returns the status and decoded body.
func tokenServicePost(t *testing.T, base, caller string, form url.Values) (int, map[string]any) {
	t.Helper()
	form.Set("grant_type", "urn:ietf:params:oauth:grant-type:token-exchange")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/v1/oauth/token", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatalf("new token request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Authorization", "Bearer "+caller)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST %s/v1/oauth/token: %v", base, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	return resp.StatusCode, out
}

// mintDelegationChild runs an a2a_delegation exchange on base: subject is
// the token whose identity the child carries and actor is the delegating
// token, which also authenticates the call.
func mintDelegationChild(t *testing.T, base, subject, actor string) string {
	t.Helper()
	code, body := tokenServicePost(t, base, actor, url.Values{
		"subject_token":      {subject},
		"subject_token_type": {"urn:ietf:params:oauth:token-type:jwt"},
		"actor_token":        {actor},
		"actor_token_type":   {"urn:ietf:params:oauth:token-type:jwt"},
	})
	tok, _ := body["access_token"].(string)
	if code != http.StatusOK || tok == "" {
		t.Fatalf("a2a_delegation exchange on %s: status %d body %v", base, code, body)
	}
	return tok
}

// spec: 13.3 (Credential Flow) "RFC 8693 parameter mapping to Lenny claims" actor_token, 13.3 (Credential Flow) "Token rotation and revocation" recursive child revocation, 12.2 (Storage Roles) TokenIssuanceStore, 8.3 (Delegation Policy and Lease), 25.4 (The lenny-ops Service: Authentication, revocation set rule 2)
// diagnosis: a delegation child or grandchild that lenny-ops still admits
// after the parent's revocation request means the child row the minting
// replica wrote carries no parent_jti, so the revoking replica's
// RevokeCascade did not reach it (check issuedRecord in
// pkg/tokenservice/tokenservice.go), or the revoking replica published no
// jti for a cascaded row. A refused unrelated child means the cascade
// escaped the parent's subtree. A revoked_count other than 3 separates the
// two: a count of 1 means the lineage is missing.
func TestRecursiveRevocationReachesDelegationChildOnAnotherReplica_spec_13_3(t *testing.T) {
	tokensvc.SkipUnlessAvailable(t)
	refs := map[string]string{kms.AliasJWTSigning: "local-key-1"}
	_, signer := stackkey.Signer(t, stackkey.MasterKeyFile(t), refs)
	pg := startKeySetPostgres(t)
	rd := tlsredis.Start(t, "redis-secret")
	tsArgs := append(keySetTokenServiceArgs(pg.DSN, refs), "--redis-url="+rd.URL)
	minter := tokensvc.StartWith(t, tsArgs...)
	revoker := tokensvc.StartWith(t, tsArgs...)
	ops := opsprocess.StartOpts(t, opsprocess.Options{
		Args: append(keySetOpsArgs(t, pg.DSN, refs), "--redis-url="+rd.URL),
	})

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	sign := func(sub, jti string) string {
		now := time.Now()
		tok, err := signer.Sign(ctx, jwt.Claims{
			Subject: sub, TenantID: auth.DefaultTenantID, Typ: auth.TokenUserBearer,
			IssuedAt: now.Unix(), Expiry: now.Add(10 * time.Minute).Unix(), JWTID: jti,
		})
		if err != nil {
			t.Fatalf("sign %s: %v", jti, err)
		}
		return tok
	}
	alice := sign("alice@acme.com", "delegation-alice")
	bob := sign("bob@acme.com", "delegation-bob")

	// The parent and an unrelated root are exchanged tokens, so each has an
	// issued_tokens row the revocation request can name.
	parent := mintExchanged(t, minter.BaseURL(), alice, alice, "")
	other := mintExchanged(t, minter.BaseURL(), bob, bob, "")
	child := mintDelegationChild(t, minter.BaseURL(), bob, parent)
	grandchild := mintDelegationChild(t, minter.BaseURL(), bob, child)
	otherChild := mintDelegationChild(t, minter.BaseURL(), alice, other)

	for name, tok := range map[string]string{"child": child, "grandchild": grandchild, "unrelated child": otherChild} {
		if o := ops.WaitAuthenticated(t, tok, 30*time.Second); !o.Authenticated() {
			t.Fatalf("%s not admitted before the revocation: %+v", name, o)
		}
	}

	code, body := tokenServicePost(t, revoker.BaseURL(), alice, url.Values{
		"subject_token":        {parent},
		"subject_token_type":   {"urn:ietf:params:oauth:token-type:jwt"},
		"requested_token_type": {"urn:ietf:params:oauth:token-type:access_token:revoked"},
	})
	if code != http.StatusOK || body["revoked_count"] != float64(3) {
		t.Fatalf("revocation request on the second replica: status %d body %v, want 200 with revoked_count 3\n%s", code, body, revoker.Logs())
	}

	ops.WaitRefused(t, child, http.StatusUnauthorized, opsprocess.CodeRevoked, delegationCascadeBound)
	ops.WaitRefused(t, grandchild, http.StatusUnauthorized, opsprocess.CodeRevoked, delegationCascadeBound)
	if o := ops.Me(t, otherChild); !o.Authenticated() {
		t.Errorf("unrelated delegation child refused after the parent's revocation: %+v", o)
	}
}
