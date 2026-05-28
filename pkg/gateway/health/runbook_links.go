// SPDX-License-Identifier: MIT

package health

// runbookLinks is the §4.0 centralized component → runbook reference
// table. Each Checker resolves its RunbookRef through this map so the
// (component, runbook) edges live in one place and a backend probe in
// `backends/` can add a new component without re-declaring the
// runbook URL inline.
var runbookLinks = map[string]string{
	"postgres":              "postgres-failover",
	"postgres_pool":         "postgres-failover",
	"redis":                 "redis-failure",
	"redis_pool":            "redis-failure",
	"circuit_breaker_cache": "redis-failure",
	"breaker_cache":         "redis-failure",
	"kubernetes":            "kubernetes-api",
	"gateway":               "gateway-degraded",
	"warm_pool":             "warm-pool-exhaustion",
	"upgrade":               "platform-upgrade",
	"sessionstore":          "sessionstore-failure",
	"blobstore":             "blobstore-failure",
	"executor":              "executor-stalled",
}

// issueRunbooks is the §25.7 Path B (lines 3217–3231) lookup that maps
// a health-API issue code to the runbook the agent should fetch. The
// gateway's health checkers stamp `Component.Issue` with one of these
// codes when the component is unhealthy, and the §25.3 response then
// surfaces `suggestedAction.runbook` from this map. §17.7 line 741
// names the table as the source of truth and lists the eight codes
// that are required by Path B.
// spec: §25.7 lines 3217-3231; §17.7 line 741.
var issueRunbooks = map[string]string{
	"WARM_POOL_EXHAUSTED":       "warm-pool-exhaustion",
	"WARM_POOL_LOW":             "warm-pool-exhaustion",
	"CREDENTIAL_POOL_EXHAUSTED": "credential-pool-exhaustion",
	"POSTGRES_UNREACHABLE":      "postgres-failover",
	"REDIS_UNREACHABLE":         "redis-failure",
	"MINIO_UNREACHABLE":         "minio-failure",
	"CERT_EXPIRY_IMMINENT":      "cert-manager-outage",
	"CIRCUIT_BREAKER_OPEN":      "gateway-replica-failure",
}

// RunbookFor returns the runbook reference registered for the named
// component, or an empty string if no link is registered. Callers
// (backend Checkers, custom probes) consult this table instead of
// hard-coding the runbook URL so the link table is the single source
// of truth for §4.0 health → runbook routing.
func RunbookFor(component string) string {
	return runbookLinks[component]
}

// RegisterRunbook installs a (component → runbook) link. v1 callers
// register their links at package init; future extensions can refresh
// the table at runtime when an operator updates `lenny-ops` runbook
// metadata.
func RegisterRunbook(component, runbook string) {
	runbookLinks[component] = runbook
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
