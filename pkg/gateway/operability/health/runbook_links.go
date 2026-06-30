// SPDX-License-Identifier: MIT

package health

// issueRunbooks is the §25.7 Path B (lines 3217–3231) lookup that maps
// a health-API issue code to the runbook the agent should fetch. The
// gateway's health checkers stamp `Component.Issue` with one of these
// codes when the component is unhealthy, and the §25.3 response then
// surfaces `suggestedAction.runbook` from this map. §17.7 line 741
// names the table as the source of truth and lists the eight codes
// that are required by Path B.
// spec: §25.7 lines 3217-3231; §17.7 line 741.
var issueRunbooks = map[string]string{
	"WARM_POOL_EXHAUSTED":          "warm-pool-exhaustion",
	"WARM_POOL_LOW":                "warm-pool-exhaustion",
	"CREDENTIAL_POOL_EXHAUSTED":    "credential-pool-exhaustion",
	"POSTGRES_UNREACHABLE":         "postgres-failover",
	"REDIS_UNREACHABLE":            "redis-failure",
	"MINIO_UNREACHABLE":            "minio-failure",
	"CERT_EXPIRY_IMMINENT":         "cert-manager-outage",
	"CIRCUIT_BREAKER_OPEN":         "gateway-replica-failure",
	"AUDIT_SIEM_DELIVERY_DEGRADED": "siem-delivery-failure",
}

// RunbookForIssue returns the runbook reference registered for the
// named health-API issue code, or the empty string when the issue is
// not registered. The §25.7 Path B contract: when the issue code is
// known, the §25.3 health response carries the runbook so an external
// agent can fetch the document in one hop.
// spec: §25.7 lines 3217-3234.
func RunbookForIssue(issue string) string {
	return issueRunbooks[issue]
}

// RegisterIssueRunbook installs an (issue → runbook) link. Out-of-tree
// checkers can register their issue codes at init so the §17.7
// lookup table stays the single source of truth.
// spec: §17.7 line 741.
func RegisterIssueRunbook(issue, runbook string) {
	issueRunbooks[issue] = runbook
}
