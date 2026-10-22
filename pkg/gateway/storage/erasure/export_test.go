// SPDX-License-Identifier: MIT

package erasure

// storeTableUserSession and storeTableUserScoped are the user-path wiring of
// the Store table: every ranked store the user path wires, session-scoped
// stores first, each in rank order.
var (
	storeTableUserSession = []string{
		"coordination_mirror", "artifacts", "checkpoint_manifests", "checkpoint_retention",
		"checkpoint_meta", "transcripts", "session_message_queues", "session_tree_archive",
		"eval_results", "delegation_budget",
	}
	storeTableUserScoped = []string{
		"leases", "semantic_cache", "experiment_sticky", "billing_buffer", "quota", "memory",
		"interactions", "audit", "sessions", "tokens", "issued_tokens", "credential_pool",
	}
	// storeTableTenant is the tenant-path Phase 4 wiring of the Store table,
	// in rank order.
	storeTableTenant = []string{
		"leases", "coordination_mirror", "semantic_cache", "experiment_sticky", "billing_buffer",
		"quota", "artifacts", "checkpoint_manifests", "checkpoint_retention", "checkpoint_meta",
		"transcripts", "interactions", "memory", "session_message_queues", "session_tree_archive",
		"audit", "billing", "eval_results", "delegation_budget", "sessions", "tokens",
		"issued_tokens", "credential_pool", "runtime_tenant_access", "pool_tenant_access",
		"user_roles", "custom_roles", "tenant_definitions", "tenant_config",
	}
)

// StoreTableUserConfig returns the Store table's user-path wiring with every
// entry listed by name and a nil function, which the validators accept as
// wired. Tests replace an entry's function to exercise one store.
func StoreTableUserConfig() Config {
	cfg := Config{}
	for _, n := range storeTableUserSession {
		cfg.SessionScoped = append(cfg.SessionScoped, SessionEraser{Name: n})
	}
	for _, n := range storeTableUserScoped {
		cfg.UserScoped = append(cfg.UserScoped, Eraser{Name: n})
	}
	return cfg
}

// StoreTableTenantNames returns the Store table's tenant-path Phase 4 names
// in rank order.
func StoreTableTenantNames() []string {
	return append([]string(nil), storeTableTenant...)
}
