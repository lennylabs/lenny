// SPDX-License-Identifier: MIT

package remediationlock_test

import (
	"errors"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/remediationlock"
)

func TestAuthorizePlatformAdminMayTouchEveryScope(t *testing.T) {
	for _, scope := range []string{
		"pool:default-gvisor", "credential-pool:anthropic", "session:sess-1",
		"tenant:globex:*", "platform:*", "upgrade:platform", "restore:platform",
		"config:global",
	} {
		if err := remediationlock.Authorize(
			remediationlock.PlatformAdmin, "acme", scope, "globex",
		); err != nil {
			t.Errorf("platform-admin denied scope %q: %v", scope, err)
		}
	}
}

func TestAuthorizeTenantAdminTenantResourceScopes(t *testing.T) {
	for _, scope := range []string{"pool:p", "credential-pool:c", "session:s"} {
		// Owned by the caller's tenant — allowed.
		if err := remediationlock.Authorize(
			remediationlock.TenantAdmin, "acme", scope, "acme",
		); err != nil {
			t.Errorf("tenant-admin denied own-tenant scope %q: %v", scope, err)
		}
		// Owned by another tenant — forbidden.
		if err := remediationlock.Authorize(
			remediationlock.TenantAdmin, "acme", scope, "globex",
		); !errors.Is(err, remediationlock.ErrScopeForbidden) {
			t.Errorf("tenant-admin allowed cross-tenant scope %q: %v", scope, err)
		}
		// Owner unknown — fail closed.
		if err := remediationlock.Authorize(
			remediationlock.TenantAdmin, "acme", scope, "",
		); !errors.Is(err, remediationlock.ErrScopeForbidden) {
			t.Errorf("tenant-admin allowed scope %q with no resolved owner", scope)
		}
	}
}

func TestAuthorizeTenantAdminTenantScope(t *testing.T) {
	if err := remediationlock.Authorize(
		remediationlock.TenantAdmin, "acme", "tenant:acme:*", "",
	); err != nil {
		t.Errorf("tenant-admin denied its own tenant scope: %v", err)
	}
	if err := remediationlock.Authorize(
		remediationlock.TenantAdmin, "acme", "tenant:globex:*", "",
	); !errors.Is(err, remediationlock.ErrScopeForbidden) {
		t.Error("tenant-admin allowed another tenant's scope")
	}
}

func TestAuthorizeTenantAdminPlatformScopesForbidden(t *testing.T) {
	for _, scope := range []string{
		"platform:*", "upgrade:platform", "restore:platform", "config:global",
	} {
		err := remediationlock.Authorize(remediationlock.TenantAdmin, "acme", scope, "")
		if !errors.Is(err, remediationlock.ErrScopeForbidden) {
			t.Errorf("tenant-admin allowed platform scope %q: %v", scope, err)
		}
	}
}

func TestAuthorizeRejectsUnknownRoleAndScope(t *testing.T) {
	if err := remediationlock.Authorize(
		remediationlock.Role("auditor"), "acme", "pool:p", "acme",
	); !errors.Is(err, remediationlock.ErrScopeForbidden) {
		t.Error("an unknown role was authorized")
	}
	if err := remediationlock.Authorize(
		remediationlock.TenantAdmin, "acme", "teleport:x", "acme",
	); !errors.Is(err, remediationlock.ErrScopeForbidden) {
		t.Error("tenant-admin authorized an unrecognized scope kind")
	}
}

func TestExpired(t *testing.T) {
	acquired := time.Date(2026, 5, 17, 12, 0, 0, 0, time.UTC)
	ttl := 5 * time.Minute
	if remediationlock.Expired(acquired, ttl, acquired.Add(3*time.Minute)) {
		t.Error("a lock within its TTL reported expired")
	}
	if !remediationlock.Expired(acquired, ttl, acquired.Add(6*time.Minute)) {
		t.Error("a lock past its TTL reported not expired")
	}
}
