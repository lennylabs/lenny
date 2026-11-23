//go:build component

// SPDX-License-Identifier: MIT

// Component tests for the lineage of a rotation and for who may send a
// recursive-revocation request. They drive the in-process Token Service
// handler against a real Postgres container with the production
// migrations applied, through the lineage harness of
// delegation_lineage_test.go.
package issuedtokencascade_test

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/auth/jwt"
	"github.com/lennylabs/lenny/pkg/storage/issuedtokenstore"
	"github.com/lennylabs/lenny/pkg/tokenservice"
)

// roleToken signs a tenant bearer for sub with roles that has no
// issued_tokens row.
func (h *lineageHarness) roleToken(t *testing.T, tenant, sub, jti string, roles []auth.Role) string {
	t.Helper()
	now := time.Now()
	tok, err := h.signer.Sign(jwt.Claims{
		Subject: sub, TenantID: tenant, JWTID: jti, Typ: auth.TokenUserBearer, Roles: roles,
		Audience: []string{"lenny-gateway"}, IssuedAt: now.Unix(), Expiry: now.Add(time.Hour).Unix(),
	})
	if err != nil {
		t.Fatalf("sign %s: %v", jti, err)
	}
	return tok
}

// revokedState reports whether each jti's row is revoked.
func (h *lineageHarness) revokedState(t *testing.T, ctx context.Context, tenant string, jtis ...string) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	for _, jti := range jtis {
		row, err := h.store.Get(ctx, tenant, jti)
		if err != nil {
			t.Fatalf("Get %s: %v", jti, err)
		}
		out[jti] = row.Revoked()
	}
	return out
}

// spec: §13.3 (Credential Flow), §12.2 (Storage Roles)
// diagnosis: the token a self rotation issued does not record the token it
// replaced as its parent, which means derivedLineage in
// pkg/tokenservice/tokenservice.go does not name a rotation's subject as
// parent_jti. A holder of the replaced token alone, which the rotation
// revoked, revoked anything, which means the revocation request does not
// refuse a revoked caller (RevokeCascadeByCaller). A token narrowed from
// the replaced token before the rotation, or the rotated token, that stays
// live after the owner's request means the cascade from an already-revoked
// root stopped at the root or does not follow the rotation's parent_jti.
func TestARotationRecordsTheTokenItReplaces(t *testing.T) {
	t.Parallel()
	h := startLineageHarness(t)
	ctx := context.Background()
	tenant := freshTenant(t, ctx, h.pg)
	alice := h.userToken(t, tenant, "alice@acme.com", "alice-idp-"+tenant)

	// leaked is an exchanged token of alice's, so it has a row.
	leaked, leakedJTI := h.mint(t, alice, tokenservice.Request{
		SubjectToken: alice, Scope: "tools:sessions:read tools:sessions:write", Audience: "lenny-gateway",
	})
	// narrowed derives from leaked before the rotation.
	_, narrowedJTI := h.mint(t, alice, tokenservice.Request{
		SubjectToken: leaked, Scope: "tools:sessions:read", Audience: "lenny-gateway",
	})
	// alice rotates away from leaked: same scope and audience, leaked as
	// both caller and subject.
	current, currentJTI := h.mint(t, leaked, tokenservice.Request{
		SubjectToken: leaked, Audience: "lenny-gateway",
	})
	row, err := h.store.Get(ctx, tenant, currentJTI)
	if err != nil {
		t.Fatalf("Get %s: %v", currentJTI, err)
	}
	if row.ParentJTI != leakedJTI || row.ActSubject != "" {
		t.Errorf("rotated row parent_jti=%q act_sub=%q, want %s / empty", row.ParentJTI, row.ActSubject, leakedJTI)
	}
	if got := h.revokedState(t, ctx, tenant, leakedJTI); !got[leakedJTI] {
		t.Fatalf("the rotation did not revoke the replaced token")
	}

	t.Run("a holder of the replaced token alone revokes nothing", func(t *testing.T) {
		for name, subject := range map[string]string{"the replaced token": leaked, "the current token": current} {
			code, body := h.post(t, leaked, tokenservice.Request{SubjectToken: subject, RequestedTokenType: lineageRevokedType})
			if code != http.StatusUnauthorized || body["error"] != "invalid_client" {
				t.Errorf("revocation of %s by the replaced token: status %d body %v, want 401 invalid_client", name, code, body)
			}
		}
		if got := h.revokedState(t, ctx, tenant, narrowedJTI, currentJTI); got[narrowedJTI] || got[currentJTI] {
			t.Errorf("revoked state %v after refused requests, want both live", got)
		}
	})

	t.Run("revoking the replaced token cascades to its derived tokens and its successor", func(t *testing.T) {
		code, body := h.post(t, alice, tokenservice.Request{SubjectToken: leaked, RequestedTokenType: lineageRevokedType})
		if code != http.StatusOK || body["revoked_count"] != float64(2) {
			t.Fatalf("revocation of the replaced token: status %d body %v, want 200 with revoked_count 2", code, body)
		}
		for _, jti := range []string{narrowedJTI, currentJTI} {
			row, err := h.store.Get(ctx, tenant, jti)
			if err != nil || !row.Revoked() || row.RevokedReason != "cascade_from_parent" {
				t.Errorf("%s revoked=%v revoked_reason=%q (err %v), want revoked with cascade_from_parent", jti, row.Revoked(), row.RevokedReason, err)
			}
		}
	})
}

// spec: §13.3 (Credential Flow)
// diagnosis: bob holds a token of alice's that leaked and rotates it before
// alice acts. A successor that stays live after alice revokes the leaked
// token with another of her tokens means the rotation does not record the
// token it replaced (derivedLineage in pkg/tokenservice/tokenservice.go),
// so the leak's holder keeps a token after its owner revoked the leak.
func TestRevokingALeakedTokenRevokesTheTokenItsHolderRotatedFromIt(t *testing.T) {
	t.Parallel()
	h := startLineageHarness(t)
	ctx := context.Background()
	tenant := freshTenant(t, ctx, h.pg)
	alice := h.userToken(t, tenant, "alice@acme.com", "alice-idp-"+tenant)

	leaked, leakedJTI := h.mint(t, alice, tokenservice.Request{
		SubjectToken: alice, Scope: "tools:sessions:read tools:sessions:write", Audience: "lenny-gateway",
	})
	own, ownJTI := h.mint(t, alice, tokenservice.Request{
		SubjectToken: alice, Scope: "tools:sessions:read tools:sessions:write", Audience: "lenny-gateway",
	})
	// bob rotates the leaked token first and keeps the successor.
	_, keptJTI := h.mint(t, leaked, tokenservice.Request{SubjectToken: leaked, Audience: "lenny-gateway"})

	code, body := h.post(t, own, tokenservice.Request{SubjectToken: leaked, RequestedTokenType: lineageRevokedType})
	if code != http.StatusOK || body["revoked_count"] != float64(1) {
		t.Fatalf("alice's revocation of the leaked token: status %d body %v, want 200 with revoked_count 1", code, body)
	}
	got := h.revokedState(t, ctx, tenant, leakedJTI, keptJTI, ownJTI)
	if !got[keptJTI] {
		t.Errorf("the successor bob rotated from the leaked token is live after alice revoked the leaked token")
	}
	if got[ownJTI] {
		t.Errorf("alice's own token was revoked, want live")
	}
}

// spec: §13.3 (Credential Flow)
// diagnosis: this test documents the trade-off §13.3 accepts with the
// rotation link. bob holds two of alice's leaked tokens: leaked, which
// alice has rotated away from, and live, which is still valid. When bob's
// request with live as caller and leaked as subject_token is refused, or
// does not revoke alice's current token, the rotation link or the
// acceptance of a revoked root (decision 9 of change 0164) has changed and
// §13.3 "Rotation lineage and leaked tokens." no longer describes the
// code. When it also revokes live, or the token alice obtains by signing in
// again, the cascade reaches beyond the subject_token's lineage, which
// means the recursive walk in RevokeCascadeByCaller does not start from the
// subject_token.
func TestAHolderOfTwoLeakedTokensRevokesTheUsersRotatedToken(t *testing.T) {
	t.Parallel()
	h := startLineageHarness(t)
	ctx := context.Background()
	tenant := freshTenant(t, ctx, h.pg)
	alice := h.userToken(t, tenant, "alice@acme.com", "alice-idp-"+tenant)

	leaked, leakedJTI := h.mint(t, alice, tokenservice.Request{
		SubjectToken: alice, Scope: "tools:sessions:read tools:sessions:write", Audience: "lenny-gateway",
	})
	live, liveJTI := h.mint(t, alice, tokenservice.Request{
		SubjectToken: alice, Scope: "tools:sessions:read tools:sessions:write", Audience: "lenny-gateway",
	})
	// alice rotates away from leaked; bob already holds leaked and live.
	_, currentJTI := h.mint(t, leaked, tokenservice.Request{SubjectToken: leaked, Audience: "lenny-gateway"})
	if got := h.revokedState(t, ctx, tenant, leakedJTI); !got[leakedJTI] {
		t.Fatalf("the rotation did not revoke the replaced token")
	}

	code, body := h.post(t, live, tokenservice.Request{SubjectToken: leaked, RequestedTokenType: lineageRevokedType})
	if code != http.StatusOK || body["revoked_count"] != float64(1) {
		t.Fatalf("bob's revocation of the rotated-away token: status %d body %v, want 200 with revoked_count 1", code, body)
	}
	if got := h.revokedState(t, ctx, tenant, currentJTI, liveJTI); !got[currentJTI] || got[liveJTI] {
		t.Errorf("revoked state %v, want alice's current token revoked and the caller token live", got)
	}

	// alice signs in again: her new sign-in token, and a token derived
	// from it, are outside the leaked tokens' lineage, and bob's tokens do
	// not reach them.
	signIn := h.userToken(t, tenant, "alice@acme.com", "alice-idp-2-"+tenant)
	_, freshJTI := h.mint(t, signIn, tokenservice.Request{
		SubjectToken: signIn, Scope: "tools:sessions:read", Audience: "lenny-gateway",
	})
	code, body = h.post(t, live, tokenservice.Request{SubjectToken: leaked, RequestedTokenType: lineageRevokedType})
	if code != http.StatusOK || body["revoked_count"] != float64(0) {
		t.Fatalf("bob's repeated revocation: status %d body %v, want 200 with revoked_count 0", code, body)
	}
	if got := h.revokedState(t, ctx, tenant, freshJTI); got[freshJTI] {
		t.Errorf("the token from alice's new sign-in was revoked by a holder of her leaked tokens")
	}
}

// spec: §13.3 (Credential Flow)
// diagnosis: a recursive-revocation request from another user in the
// tenant without an administrator role revoked a token, or one from a
// tenant-admin or platform-admin was refused, which means authorizeRevocation
// in pkg/tokenservice/tokenservice.go does not compare the caller with the
// subject or does not read the roles. A request from a caller whose own
// row is revoked that revoked anything means RevokeCascadeByCaller does not
// read the caller's row.
func TestARevocationRequestIsAcceptedOnlyFromTheOwnerOrAnAdministrator(t *testing.T) {
	t.Parallel()
	h := startLineageHarness(t)
	ctx := context.Background()
	tenant := freshTenant(t, ctx, h.pg)
	alice := h.userToken(t, tenant, "alice@acme.com", "alice-idp-"+tenant)
	mintAlice := func(t *testing.T) (string, string) {
		return h.mint(t, alice, tokenservice.Request{
			SubjectToken: alice, Scope: "tools:sessions:read", Audience: "lenny-gateway",
		})
	}

	for _, c := range []struct {
		name       string
		roles      []auth.Role
		wantStatus int
	}{
		{"another user", []auth.Role{auth.RoleUser}, http.StatusForbidden},
		{"a tenant-viewer", []auth.Role{auth.RoleTenantViewer}, http.StatusForbidden},
		{"a tenant-admin", []auth.Role{auth.RoleTenantAdmin}, http.StatusOK},
		{"a platform-admin", []auth.Role{auth.RolePlatformAdmin}, http.StatusOK},
	} {
		t.Run(c.name, func(t *testing.T) {
			target, targetJTI := mintAlice(t)
			caller := h.roleToken(t, tenant, "bob@acme.com", "bob-"+targetJTI, c.roles)
			code, body := h.post(t, caller, tokenservice.Request{SubjectToken: target, RequestedTokenType: lineageRevokedType})
			if code != c.wantStatus {
				t.Fatalf("status %d body %v, want %d", code, body, c.wantStatus)
			}
			if got := h.revokedState(t, ctx, tenant, targetJTI); got[targetJTI] != (c.wantStatus == http.StatusOK) {
				t.Errorf("target revoked = %v, want %v", got[targetJTI], c.wantStatus == http.StatusOK)
			}
		})
	}

	t.Run("a revoked caller", func(t *testing.T) {
		target, targetJTI := mintAlice(t)
		caller, callerJTI := mintAlice(t)
		if _, err := h.store.RevokeCascade(ctx, tenant, callerJTI, "explicit_revoke", "cascade_from_parent", time.Now()); err != nil {
			t.Fatalf("revoke the caller: %v", err)
		}
		code, body := h.post(t, caller, tokenservice.Request{SubjectToken: target, RequestedTokenType: lineageRevokedType})
		if code != http.StatusUnauthorized || body["error"] != "invalid_client" {
			t.Fatalf("status %d body %v, want 401 invalid_client", code, body)
		}
		if got := h.revokedState(t, ctx, tenant, targetJTI); got[targetJTI] {
			t.Errorf("a revoked caller revoked the target")
		}
		if _, err := h.store.RevokeCascadeByCaller(ctx, tenant, callerJTI, targetJTI, "explicit_revoke", "cascade_from_parent", time.Now()); !errors.Is(err, issuedtokenstore.ErrCallerRevoked) {
			t.Errorf("RevokeCascadeByCaller with a revoked caller: err %v, want ErrCallerRevoked", err)
		}
	})
}

// spec: §13.3 (Credential Flow), §12.2 (Storage Roles)
// diagnosis: a token alice narrowed from her token, or one bob exchanged
// from it, rotated itself into a token that alice's revocation of her
// token did not reach, which means the rotation does not record the token
// it replaced as its parent (derivedLineage in
// pkg/tokenservice/tokenservice.go).
func TestARotatedDerivedTokenStaysInItsAncestorsLineage(t *testing.T) {
	t.Parallel()
	h := startLineageHarness(t)
	ctx := context.Background()
	tenant := freshTenant(t, ctx, h.pg)
	alice := h.userToken(t, tenant, "alice@acme.com", "alice-idp-"+tenant)
	bob := h.userToken(t, tenant, "bob@acme.com", "bob-idp-"+tenant)

	root, _ := h.mint(t, alice, tokenservice.Request{
		SubjectToken: alice, Scope: "tools:sessions:read tools:sessions:write", Audience: "lenny-gateway",
	})
	narrowed, narrowedJTI := h.mint(t, alice, tokenservice.Request{
		SubjectToken: root, Scope: "tools:sessions:read", Audience: "lenny-gateway",
	})
	bobs, bobsJTI := h.mint(t, bob, tokenservice.Request{
		SubjectToken: root, Scope: "tools:sessions:write", Audience: "lenny-gateway",
	})
	// Each derived token rotates itself: the caller is the token, and the
	// request names its own audience and no scope.
	rotatedJTIs := map[string]string{}
	replacedJTIs := map[string]string{"narrowing": narrowedJTI, "bob's exchange": bobsJTI}
	for name, tok := range map[string]string{"narrowing": narrowed, "bob's exchange": bobs} {
		_, jti := h.mint(t, tok, tokenservice.Request{SubjectToken: tok, Audience: "lenny-gateway"})
		row, err := h.store.Get(ctx, tenant, jti)
		if err != nil {
			t.Fatalf("Get %s: %v", jti, err)
		}
		if row.ParentJTI != replacedJTIs[name] || row.ActSubject != "" {
			t.Errorf("rotated %s parent_jti=%q act_sub=%q, want %s / empty", name, row.ParentJTI, row.ActSubject, replacedJTIs[name])
		}
		rotatedJTIs[name] = jti
	}
	if got := h.revokedState(t, ctx, tenant, narrowedJTI, bobsJTI); !got[narrowedJTI] || !got[bobsJTI] {
		t.Fatalf("the rotations did not revoke the tokens they replaced: %v", got)
	}

	code, body := h.post(t, alice, tokenservice.Request{SubjectToken: root, RequestedTokenType: lineageRevokedType})
	if code != http.StatusOK {
		t.Fatalf("revocation of the root: status %d body %v", code, body)
	}
	for name, jti := range rotatedJTIs {
		if got := h.revokedState(t, ctx, tenant, jti); !got[jti] {
			t.Errorf("rotated %s is live after the root's revocation, want revoked", name)
		}
	}
}

// spec: §13.3 (Credential Flow)
// diagnosis: a child token that bob minted of alice's token with his own
// actor rotated itself into a token that the revocation of bob's actor
// token did not reach, which means the rotation does not record the child
// as its parent (derivedLineage in pkg/tokenservice/tokenservice.go). A
// successor that carries act_sub means the rotation recorded an actor
// although the rotated token carries no act claim.
func TestARotatedChildTokenStaysUnderItsActor(t *testing.T) {
	t.Parallel()
	h := startLineageHarness(t)
	ctx := context.Background()
	tenant := freshTenant(t, ctx, h.pg)
	alice := h.userToken(t, tenant, "alice@acme.com", "alice-idp-"+tenant)
	bob := h.userToken(t, tenant, "bob@acme.com", "bob-idp-"+tenant)

	actor, _ := h.mint(t, bob, tokenservice.Request{
		SubjectToken: bob, Scope: "tools:sessions:read", Audience: "lenny-gateway",
	})
	child, childJTI := h.mint(t, actor, tokenservice.Request{
		SubjectToken: alice, ActorToken: actor, Scope: "tools:sessions:read", Audience: "lenny-gateway",
	})
	_, rotatedJTI := h.mint(t, child, tokenservice.Request{SubjectToken: child, Audience: "lenny-gateway"})
	rotated, err := h.store.Get(ctx, tenant, rotatedJTI)
	if err != nil {
		t.Fatalf("Get %s: %v", rotatedJTI, err)
	}
	if rotated.ParentJTI != childJTI || rotated.ActSubject != "" {
		t.Errorf("rotated child parent_jti=%q act_sub=%q, want %s / empty", rotated.ParentJTI, rotated.ActSubject, childJTI)
	}

	code, body := h.post(t, bob, tokenservice.Request{SubjectToken: actor, RequestedTokenType: lineageRevokedType})
	if code != http.StatusOK {
		t.Fatalf("revocation of the actor: status %d body %v", code, body)
	}
	got := h.revokedState(t, ctx, tenant, childJTI, rotatedJTI)
	if !got[childJTI] || !got[rotatedJTI] {
		t.Errorf("revoked state %v after the actor's revocation, want both revoked", got)
	}
}

// spec: §13.3 (Credential Flow)
// diagnosis: two revocation requests that revoke each other's caller
// token returned an error other than ErrCallerRevoked, or both revoked,
// which means RevokeCascadeByCaller does not retry a deadlocked
// transaction, or the retried request did not re-read its caller row.
func TestMutualRevocationRequestsRevokeOnce(t *testing.T) {
	t.Parallel()
	h := startLineageHarness(t)
	ctx := context.Background()
	tenant := freshTenant(t, ctx, h.pg)
	alice := h.userToken(t, tenant, "alice@acme.com", "alice-idp-"+tenant)

	for i := 0; i < 20; i++ {
		_, xJTI := h.mint(t, alice, tokenservice.Request{SubjectToken: alice, Scope: "tools:sessions:read", Audience: "lenny-gateway"})
		_, yJTI := h.mint(t, alice, tokenservice.Request{SubjectToken: alice, Scope: "tools:sessions:read", Audience: "lenny-gateway"})
		errs := make(chan error, 2)
		start := make(chan struct{})
		for _, pair := range [][2]string{{xJTI, yJTI}, {yJTI, xJTI}} {
			go func(caller, root string) {
				<-start
				_, err := h.store.RevokeCascadeByCaller(ctx, tenant, caller, root, "explicit_revoke", "cascade_from_parent", time.Now())
				errs <- err
			}(pair[0], pair[1])
		}
		close(start)
		var refused, accepted int
		for j := 0; j < 2; j++ {
			switch err := <-errs; {
			case err == nil:
				accepted++
			case errors.Is(err, issuedtokenstore.ErrCallerRevoked):
				refused++
			default:
				t.Fatalf("round %d: RevokeCascadeByCaller: %v", i, err)
			}
		}
		if accepted != 1 || refused != 1 {
			t.Fatalf("round %d: accepted %d refused %d, want one each", i, accepted, refused)
		}
	}
}
