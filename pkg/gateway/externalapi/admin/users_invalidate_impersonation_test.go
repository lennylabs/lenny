// SPDX-License-Identifier: MIT

package admin_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strings"
	"testing"

	pkgauth "github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/gateway/environment/userstore"
	"github.com/lennylabs/lenny/pkg/gateway/externalapi/admin"
)

// fullRevokeAs runs a full_revoke of subject in acme as the given caller
// and returns the decoded response body.
func fullRevokeAs(t *testing.T, router *admin.Router, subject string, as func(*http.Request) *http.Request) map[string]any {
	t.Helper()
	rr := invalidateUser(t, router.Handler(), subject,
		admin.InvalidateUserRequest{TenantID: "acme", Mode: admin.InvalidateFullRevoke, Reason: "compromised"}, as)
	if rr.Code != http.StatusOK {
		t.Fatalf("full_revoke: status %d, body %s", rr.Code, rr.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v (body %s)", err, rr.Body.String())
	}
	return resp
}

// TestFullRevokeCascadeFollowsCallerRole_spec_13_3 asserts that the
// impersonation cascade decision is the caller's platform-admin role and
// never the target user's roles: a platform-admin caller revoking a plain
// user requests the cascade, and a tenant-admin caller revoking a user
// whose record lists platform-admin does not, and reports the skipped
// cascade in tokenRevocationError.
//
// spec: §13.3 (Platform-admin impersonation, rule 6), §11.4 step 5
func TestFullRevokeCascadeFollowsCallerRole_spec_13_3(t *testing.T) {
	cases := []struct {
		name        string
		as          func(*http.Request) *http.Request
		targetRoles []pkgauth.Role
		wantCascade bool
	}{
		{
			name:        "platform-admin caller, target without platform-admin",
			as:          withAdminPrincipal,
			targetRoles: []pkgauth.Role{pkgauth.RoleUser},
			wantCascade: true,
		},
		{
			name:        "tenant-admin caller, target without platform-admin",
			as:          withTenantAdminPrincipal,
			targetRoles: []pkgauth.Role{pkgauth.RoleUser},
		},
		{
			name:        "tenant-admin caller, target record listing platform-admin",
			as:          withTenantAdminPrincipal,
			targetRoles: []pkgauth.Role{pkgauth.RolePlatformAdmin},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tokens := &fakeUserTokenRevoker{jtis: []string{"jti-own"}}
			router, users, _, cache := newFullRevokeAdmin(t, nil, nil, tokens)
			if err := users.Create(context.Background(), userstore.User{
				Subject: "dave@acme.com", TenantID: "acme", Roles: tc.targetRoles,
			}); err != nil {
				t.Fatalf("seed user: %v", err)
			}
			resp := fullRevokeAs(t, router, "dave@acme.com", tc.as)
			if tokens.calls != 1 {
				t.Fatalf("token revoker called %d times, want 1", tokens.calls)
			}
			if tokens.gotRevokeImpersonations != tc.wantCascade {
				t.Errorf("revokeImpersonations = %v, want %v", tokens.gotRevokeImpersonations, tc.wantCascade)
			}
			msg, _ := resp["tokenRevocationError"].(string)
			if tc.wantCascade && msg != "" {
				t.Errorf("tokenRevocationError = %q, want none when the cascade runs", msg)
			}
			if !tc.wantCascade && !strings.Contains(msg, "platform-admin") {
				t.Errorf("tokenRevocationError = %q, want it to name the skipped impersonation cascade", msg)
			}
			// The user's own tokens are revoked and pushed either way.
			if resp["tokensRevoked"] != float64(1) || len(cache.revoked) != 1 {
				t.Errorf("tokensRevoked = %v, cache = %v; want the user's own token revoked", resp["tokensRevoked"], cache.revoked)
			}
		})
	}
}

// TestFullRevokePushesJTIsReturnedWithAnError_spec_11_4 asserts that a
// partial step 5 result survives its error: every JTI the revoker returns
// alongside an error is pushed into the revocation cache and counted, and
// the error is reported. Discarding the JTIs would leave the user's
// already-revoked tokens valid on the cache until the rehydrate.
//
// spec: §11.4 step 5, §13.3 (Platform-admin impersonation, rule 6)
func TestFullRevokePushesJTIsReturnedWithAnError_spec_11_4(t *testing.T) {
	tokens := &fakeUserTokenRevoker{
		jtis: []string{"jti-own-1", "jti-own-2"},
		err:  errors.New("revoke impersonation bearers: issued-token index unreachable"),
	}
	router, users, _, cache := newFullRevokeAdmin(t, nil, nil, tokens)
	seedUser(t, users, "acme", "dave@acme.com")

	resp := fullRevokeAs(t, router, "dave@acme.com", withAdminPrincipal)
	sort.Strings(cache.revoked)
	if strings.Join(cache.revoked, ",") != "jti-own-1,jti-own-2" {
		t.Errorf("revocation cache holds %v, want every returned JTI", cache.revoked)
	}
	if resp["tokensRevoked"] != float64(2) {
		t.Errorf("tokensRevoked = %v, want 2", resp["tokensRevoked"])
	}
	msg, _ := resp["tokenRevocationError"].(string)
	if !strings.Contains(msg, "issued-token index unreachable") {
		t.Errorf("tokenRevocationError = %q, want the revoker's error", msg)
	}
}

// TestFullRevokeNoTokenRevokerReportsNoCascadeError_spec_11_4 asserts that
// a deployment without an issued-token index skips step 5 entirely and
// reports no token revocation error for a tenant-admin caller.
//
// spec: §11.4 step 5
func TestFullRevokeNoTokenRevokerReportsNoCascadeError_spec_11_4(t *testing.T) {
	router, users, _, _ := newFullRevokeAdmin(t, nil, nil, nil)
	seedUser(t, users, "acme", "dave@acme.com")
	resp := fullRevokeAs(t, router, "dave@acme.com", withTenantAdminPrincipal)
	if v, ok := resp["tokenRevocationError"]; ok {
		t.Errorf("tokenRevocationError = %v, want absent without a token revoker", v)
	}
}
