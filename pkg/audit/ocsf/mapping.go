// SPDX-License-Identifier: MIT

package ocsf

import (
	"sort"
	"strings"
)

// ClassMapping is one row of the §11.7 event-type → OCSF class/activity
// catalog. §11.7 assigns each Lenny event type to an OCSF class
// (primarily 3002 Authentication, 6003 API Activity, 6004 File System
// Activity, 3006 Account Change, 5001 Entity Management, and 2004
// Application Security Finding).
type ClassMapping struct {
	ClassUID    int
	CategoryUID int
	ActivityID  int
}

// authn / accountChange / entityMgmt / apiActivity / fileSystem /
// finding are the §11.7 class constructors. They pin class_uid +
// category_uid together so a caller cannot pair a class with the
// wrong OCSF category.
func authn(activity int) ClassMapping {
	return ClassMapping{ClassAuthentication, categoryIAM, activity}
}

func accountChange(activity int) ClassMapping {
	return ClassMapping{ClassAccountChange, categoryIAM, activity}
}

func entityMgmt(activity int) ClassMapping {
	return ClassMapping{ClassEntityManagement, categoryDiscovery, activity}
}

func apiActivity(activity int) ClassMapping {
	return ClassMapping{ClassAPIActivity, categoryApplication, activity}
}

func fileSystem(activity int) ClassMapping {
	return ClassMapping{ClassFileSystemActivity, categoryApplication, activity}
}

func finding(activity int) ClassMapping {
	return ClassMapping{ClassAppSecurityFinding, categoryFindings, activity}
}

// exactCatalog pins the §11.7 class/activity for the named event
// types. §16.7 enumerates the §25 audit event catalog; the credential
// (§4.9.2) and admin event families are pinned here. Event types not
// listed by exact name fall through to the prefix table below.
var exactCatalog = map[string]ClassMapping{
	// §4.9.2 credential audit events. The lifecycle events map to
	// Authentication (3002); the security-salient events
	// (rotation_ceiling_hit, lease_spiffe_mismatch,
	// proxy_mode_spiffe_binding_disabled) map to Application Security
	// Finding (2004). Names match the §4.9.2 catalog exactly — the event
	// types are declared as typed constants in pkg/credential.
	"credential.registered":                         authn(ActivityCreate),
	"credential.deleted":                            authn(ActivityDelete),
	"credential.rotated":                            authn(ActivityUpdate),
	"credential.user_revoked":                       authn(ActivityDelete),
	"credential.leased":                             authn(ActivityLogon),
	"credential.revoked":                            authn(ActivityDelete),
	"credential.re_enabled":                         authn(ActivityCreate),
	"credential.renewed":                            authn(ActivityUpdate),
	"credential.fallback_exhausted":                 authn(ActivityUnknown),
	"credential.rotation_ceiling_hit":               finding(ActivityCreate),
	"credential.lease_spiffe_mismatch":              finding(ActivityCreate),
	"credential.proxy_mode_spiffe_binding_disabled": finding(ActivityCreate),

	// §13.3 / §16.7 token lifecycle → Authentication (3002).
	"token.exchanged":             authn(ActivityCreate),
	"token.revoked":               authn(ActivityDelete),
	"token.exchange_rate_limited": authn(ActivityUnknown),

	// §16.7 impersonation → Account Change (3006).
	"admin.impersonation_started": accountChange(ActivityCreate),
	"admin.impersonation_ended":   accountChange(ActivityDelete),

	// §11.7 forged-tenant rejection → Application Security Finding (2004).
	"security.audit_write_rejected": finding(ActivityCreate),

	// §16.7 self-recursion / cycle decisions → Application Security
	// Finding (2004): each is a security-salient admission decision.
	"delegation.self_recursion_allowed": finding(ActivityCreate),
	"delegation.cycle_warning":          finding(ActivityCreate),

	// §11.7 line 62 — `delegation.spawned` records the creation of a
	// child session via recursive delegation. API Activity (6003)
	// because it is a successful admission rather than a security
	// finding. spec: F-8.5.8.
	"delegation.spawned": apiActivity(ActivityCreate),

	// §16.7 elicitation content tamper → Application Security Finding.
	"elicitation.content_tamper_detected": finding(ActivityCreate),

	// §16.7 circuit-breaker lifecycle → API Activity (6003).
	"circuit_breaker.state_changed":         apiActivity(ActivityUpdate),
	"admission.circuit_breaker_rejected":    apiActivity(ActivityUnknown),
	"admission.circuit_breaker_cache_stale": apiActivity(ActivityUnknown),

	// §16.7 compliance posture → Account Change (3006).
	"compliance.profile_decommissioned": accountChange(ActivityUpdate),

	// §16.7 GDPR erasure receipts → Entity Management (5001), Delete.
	"gdpr.erasure_deadletter_redacted":            entityMgmt(ActivityDelete),
	"gdpr.erasure_deadletter_downstream_notified": entityMgmt(ActivityDelete),
	"gdpr.erasure_blocked_by_hold":                entityMgmt(ActivityUnknown),
	"gdpr.legal_hold_overridden":                  entityMgmt(ActivityUpdate),
	"gdpr.legal_hold_overridden_tenant":           entityMgmt(ActivityUpdate),
}

// prefixCatalog maps an event-type namespace prefix to a §11.7 class.
// An event type with no exact-catalog entry is matched against the
// longest prefix here. The prefixes mirror the §16.6 / §16.7 event
// namespaces.
var prefixCatalog = []struct {
	prefix  string
	mapping ClassMapping
}{
	// admin.*.created / .updated / .deleted → Entity Management (5001).
	{"admin.tenant.created", entityMgmt(ActivityCreate)},
	{"admin.tenant.updated", entityMgmt(ActivityUpdate)},
	{"admin.tenant.deleted", entityMgmt(ActivityDelete)},
	{"admin.user.created", accountChange(ActivityCreate)},
	{"admin.user.updated", accountChange(ActivityUpdate)},
	{"admin.user.deleted", accountChange(ActivityDelete)},
	{"admin.user", accountChange(ActivityUnknown)},
	{"admin.runtime.created", entityMgmt(ActivityCreate)},
	{"admin.runtime.updated", entityMgmt(ActivityUpdate)},
	{"admin.runtime.deleted", entityMgmt(ActivityDelete)},
	{"admin.environment", entityMgmt(ActivityUnknown)},
	{"admin.", apiActivity(ActivityUnknown)},

	// workspace file access → File System Activity (6004).
	{"workspace.file.read", fileSystem(ActivityRead)},
	{"workspace.file.write", fileSystem(ActivityUpdate)},
	{"workspace.file", fileSystem(ActivityUnknown)},

	// session lifecycle → API Activity (6003).
	{"session.created", apiActivity(ActivityCreate)},
	{"session.", apiActivity(ActivityUnknown)},

	// delegation → API Activity (6003).
	{"delegation.", apiActivity(ActivityUnknown)},

	// interceptor / policy decisions → Application Security Finding.
	{"interceptor.", finding(ActivityCreate)},
	{"policy.", finding(ActivityCreate)},

	// backup / restore → API Activity (6003).
	{"backup.", apiActivity(ActivityUnknown)},
	{"restore.", apiActivity(ActivityUnknown)},

	// platform lifecycle → API Activity (6003).
	{"platform.", apiActivity(ActivityUnknown)},

	// §25 ops / diagnostics / drift / audit-query → API Activity.
	{"ops_event.", apiActivity(ActivityUnknown)},
	{"diagnostics.", apiActivity(ActivityRead)},
	{"drift.", apiActivity(ActivityUnknown)},
	{"audit.", apiActivity(ActivityRead)},
	{"eventbus.", apiActivity(ActivityUnknown)},
	{"remediation.", apiActivity(ActivityUnknown)},
	{"identity.", apiActivity(ActivityRead)},
	{"operations.", apiActivity(ActivityRead)},
	{"experiment.", apiActivity(ActivityUpdate)},
	{"legal_hold.", entityMgmt(ActivityUnknown)},
	{"gdpr.", entityMgmt(ActivityUnknown)},
	{"deployment.", apiActivity(ActivityUpdate)},
	{"tenant.", entityMgmt(ActivityUpdate)},
	{"gateway.", apiActivity(ActivityUpdate)},
	{"quota_failopen", apiActivity(ActivityUnknown)},
}

// LookupClass resolves a Lenny event type to its §11.7 OCSF class and
// activity. It tries an exact-name match first, then the longest
// namespace-prefix match. The second return is false when the event
// type has no mapping at all — the §11.7 class_mapping_missing case
// that dead-letters the row.
func LookupClass(eventType string) (ClassMapping, bool) {
	if m, ok := exactCatalog[eventType]; ok {
		return m, true
	}
	best := -1
	var bestMapping ClassMapping
	for _, e := range prefixCatalog {
		if strings.HasPrefix(eventType, e.prefix) && len(e.prefix) > best {
			best = len(e.prefix)
			bestMapping = e.mapping
		}
	}
	if best >= 0 {
		return bestMapping, true
	}
	return ClassMapping{}, false
}

// CatalogEventTypes returns every event type with an exact catalog
// entry, sorted. The §12.3.5 contract test generates one of each to
// confirm the translation passes schema validation.
func CatalogEventTypes() []string {
	out := make([]string, 0, len(exactCatalog))
	for k := range exactCatalog {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
