// SPDX-License-Identifier: MIT

package health

// runbookLinks is the §4.0 centralized component → runbook reference
// table. Each Checker resolves its RunbookRef through this map so the
// (component, runbook) edges live in one place and a backend probe in
// `backends/` can add a new component without re-declaring the
// runbook URL inline.
var runbookLinks = map[string]string{
	"postgres":             "postgres-failover",
	"postgres_pool":        "postgres-failover",
	"redis":                "redis-failure",
	"redis_pool":           "redis-failure",
	"circuit_breaker_cache": "redis-failure",
	"breaker_cache":        "redis-failure",
	"kubernetes":           "kubernetes-api",
	"gateway":              "gateway-degraded",
	"warm_pool":            "warm-pool-exhaustion",
	"upgrade":              "platform-upgrade",
	"sessionstore":         "sessionstore-failure",
	"blobstore":            "blobstore-failure",
	"executor":             "executor-stalled",
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
