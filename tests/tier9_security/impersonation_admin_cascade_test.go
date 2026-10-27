// SPDX-License-Identifier: MIT

//go:build security

// Tier-9 security test for the §13.3 Platform-admin impersonation rule 6
// cascade: a §11.4 full revoke of a platform-admin by a platform-admin
// caller revokes, in every tenant, the impersonation bearers that admin
// minted, so the gateway and lenny-ops refuse them; a caller without
// platform-admin runs no cross-tenant query and reports the skipped
// cascade.
package tier9_security_test

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/tests/testinfra/gateway"
	"github.com/lennylabs/lenny/tests/testinfra/opsprocess"
)

// cascadeAdmin is the platform-admin whose impersonation bearers the
// cascade revokes. It is distinct from the harness's default caller.
const cascadeAdmin = "dave@acme.com"

// mintImpersonationAs creates subject as a tenant-admin of tenant and
// returns an impersonation bearer for it minted by admin.
func (s *opsRevStack) mintImpersonationAs(t *testing.T, admin, tenant, subject string) string {
	t.Helper()
	s.admin(t, http.MethodPost, "/v1/admin/users", map[string]any{
		"tenantId": tenant, "subject": subject, "roles": []string{"tenant-admin"},
	}, http.StatusCreated)
	body := s.adminAs(t, admin, "platform-admin", http.MethodPost, "/v1/admin/impersonation", map[string]any{
		"targetTenantId": tenant, "targetUserId": subject,
		"reason": "impersonation cascade probe", "ticketId": "cascade", "durationSeconds": 600,
	}, http.StatusOK)
	tok, _ := body["bearerToken"].(string)
	if tok == "" {
		t.Fatalf("impersonation of %s/%s by %s returned no bearer: %v", tenant, subject, admin, body)
	}
	return tok
}

// gatewayMeStatus returns the status GET /v1/admin/me answers for bearer on
// the gateway.
func (s *opsRevStack) gatewayMeStatus(t *testing.T, bearer string) int {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.gw.BaseURL()+"/v1/admin/me", nil)
	if err != nil {
		t.Fatalf("new /v1/admin/me request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+bearer)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /v1/admin/me: %v", err)
	}
	_ = resp.Body.Close()
	return resp.StatusCode
}

// rowRevoked reports whether tok's issued_tokens row carries revoked_at.
func (s *opsRevStack) rowRevoked(t *testing.T, tok string) bool {
	t.Helper()
	var revoked bool
	if err := s.pg.Pool.QueryRow(context.Background(),
		`SELECT revoked_at IS NOT NULL FROM issued_tokens WHERE jti = $1`, tokenJTI(t, tok)).Scan(&revoked); err != nil {
		t.Fatalf("read issued_tokens row: %v", err)
	}
	return revoked
}

// fullRevokeCascadeAdmin fully revokes cascadeAdmin in the default tenant as
// caller holding roles and returns the response body.
func (s *opsRevStack) fullRevokeCascadeAdmin(t *testing.T, caller, roles string) map[string]any {
	t.Helper()
	return s.adminAs(t, caller, roles, http.MethodPost, "/v1/admin/users/"+cascadeAdmin+"/invalidate", map[string]any{
		"tenantId": auth.DefaultTenantID, "mode": "full_revoke", "reason": "impersonation cascade probe",
	}, http.StatusOK)
}

// spec: 13.3 (Platform-admin impersonation), 11.4 (User Invalidation), 25.4 (The lenny-ops Service)
// diagnosis: a bearer still admitted after the platform-admin full revoke
// means the §11.4 step 5 cascade did not revoke the impersonation bearers
// the revoked admin minted, so fully revoking a compromised platform-admin
// leaves those bearers live until exp. A bearer revoked by the tenant-admin
// full revoke means the cascade ran a cross-tenant query for a caller
// without platform-admin, or keyed on the revoked user's roles. A missing
// tokenRevocationError on that call means the skipped cascade is silent.
func TestFullRevokeOfPlatformAdminRevokesItsImpersonationBearers_spec_13_3(t *testing.T) {
	gateway.SkipUnlessAvailable(t)
	s := startOpsRevStack(t)
	// The revoked admin's own record lists no platform-admin, so the cascade
	// can only follow the caller's role.
	s.admin(t, http.MethodPost, "/v1/admin/users", map[string]any{
		"tenantId": auth.DefaultTenantID, "subject": cascadeAdmin, "roles": []string{"user"},
	}, http.StatusCreated)
	bearers := []string{
		s.mintImpersonationAs(t, cascadeAdmin, "acme", "bob@acme.com"),
		s.mintImpersonationAs(t, cascadeAdmin, "globex", "erin@globex.com"),
	}
	for _, tok := range bearers {
		s.ops.WaitAccepted(t, tok, opsRevBound)
	}

	t.Run("tenant-admin caller skips the cascade", func(t *testing.T) {
		resp := s.fullRevokeCascadeAdmin(t, "frank@acme.com", "tenant-admin")
		msg, _ := resp["tokenRevocationError"].(string)
		if !strings.Contains(msg, "platform-admin") {
			t.Errorf("tokenRevocationError = %q, want it to name the skipped impersonation cascade", msg)
		}
		for i, tok := range bearers {
			if s.rowRevoked(t, tok) {
				t.Errorf("bearer %d revoked by a caller without platform-admin", i)
			}
			if code := s.gatewayMeStatus(t, tok); code != http.StatusOK {
				t.Errorf("bearer %d: gateway answered %d after the skipped cascade, want 200", i, code)
			}
		}
	})

	t.Run("platform-admin caller revokes every minted bearer", func(t *testing.T) {
		resp := s.fullRevokeCascadeAdmin(t, "carol@acme.com", "platform-admin")
		if v, ok := resp["tokenRevocationError"]; ok {
			t.Errorf("tokenRevocationError = %v, want none", v)
		}
		if n, _ := resp["tokensRevoked"].(float64); int(n) < len(bearers) {
			t.Errorf("tokensRevoked = %v, want at least %d", resp["tokensRevoked"], len(bearers))
		}
		for i, tok := range bearers {
			if code := s.gatewayMeStatus(t, tok); code != http.StatusUnauthorized {
				t.Errorf("bearer %d: gateway answered %d after the cascade, want 401", i, code)
			}
			s.requireRevokedOnOps(t, tok)
		}
		s.ops.WaitRefused(t, bearers[0], http.StatusUnauthorized, opsprocess.CodeRevoked, opsRevBound)
	})
}
