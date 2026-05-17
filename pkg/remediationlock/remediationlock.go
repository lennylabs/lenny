// SPDX-License-Identifier: MIT

// Package remediationlock implements the §25.4 remediation-lock
// scope-based authorization and TTL expiry. A remediation lock
// coordinates exclusive access to a platform resource; §25.4 governs
// which caller roles may touch which lock scopes. The package is pure
// — the tiered lock store and outage-epoch machinery live in the
// lenny-ops service — so the authorization rule has one implementation
// the service and its tests share.
package remediationlock

import (
	"errors"
	"strings"
	"time"
)

// Role is a §25.4 caller role for lock authorization.
type Role string

const (
	// PlatformAdmin may touch every lock scope.
	PlatformAdmin Role = "platform-admin"
	// TenantAdmin may touch only scopes belonging to its own tenant.
	TenantAdmin Role = "tenant-admin"
)

// ErrScopeForbidden is the §25.4 LOCK_SCOPE_FORBIDDEN rejection: the
// caller's role may not operate on the lock scope.
var ErrScopeForbidden = errors.New("remediationlock: caller's role may not touch this lock scope")

// Authorize applies the §25.4 scope-based authorization for a
// remediation-lock operation (Acquire, Release, Steal, List). role is
// the caller's role; callerTenant is the caller's tenant; scope is the
// lock scope; ownerTenant is the tenant that owns the scoped resource
// for a pool, credential-pool, or session scope (the caller resolves
// it before authorizing) and is ignored for tenant- and
// platform-scoped scopes.
//
// platform-admin may touch every scope. tenant-admin may touch a
// pool, credential-pool, or session scope only when the resource
// belongs to its tenant, and a tenant scope only when the embedded
// tenant id is its own; platform-scoped locks (platform, upgrade,
// restore, config) are forbidden to tenant-admin so a tenant admin
// cannot block a platform upgrade. Authorize returns nil when
// authorized and ErrScopeForbidden otherwise.
func Authorize(role Role, callerTenant, scope, ownerTenant string) error {
	if role == PlatformAdmin {
		return nil
	}
	if role != TenantAdmin {
		return ErrScopeForbidden
	}
	kind, rest, _ := strings.Cut(scope, ":")
	switch kind {
	case "pool", "credential-pool", "session":
		if ownerTenant != "" && ownerTenant == callerTenant {
			return nil
		}
		return ErrScopeForbidden
	case "tenant":
		// The scope is tenant:{tenantID}:* — the tenant id is the
		// segment after the kind.
		tenantID, _, _ := strings.Cut(rest, ":")
		if tenantID != "" && tenantID == callerTenant {
			return nil
		}
		return ErrScopeForbidden
	default:
		// platform, upgrade, restore, config, and any unrecognized
		// scope are platform-scoped and forbidden to tenant-admin.
		return ErrScopeForbidden
	}
}

// Expired reports whether a lock acquired at acquiredAt with the given
// TTL has expired as of now.
func Expired(acquiredAt time.Time, ttl time.Duration, now time.Time) bool {
	return !now.Before(acquiredAt.Add(ttl))
}
