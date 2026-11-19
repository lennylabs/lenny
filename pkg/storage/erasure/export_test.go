// SPDX-License-Identifier: MIT

package erasure

// gatewayUserSessionScoped and gatewayUserScoped are the gateway's user-path
// wiring: every ranked store the user path wires rather than exempts per
// spec: 12.8 (Compliance Interfaces: Storage backends in erasure scope),
// session-scoped stores first, each in rank order.
var (
	gatewayUserSessionScoped = []string{
		"coordination_mirror", "artifacts", "checkpoint_manifests", "checkpoint_retention",
		"checkpoint_meta", "transcripts", "session_message_queues", "session_tree_archive",
		"eval_results", "delegation_budget",
	}
	gatewayUserScoped = []string{
		"leases", "semantic_cache", "experiment_sticky", "billing_buffer", "quota", "memory",
		"interactions", "audit", "sessions", "tokens", "issued_tokens", "credential_pool",
	}
	// gatewayTenantPhase4 is the gateway's tenant-path wiring of the deletion
	// step of spec: 12.8 (Compliance Interfaces: Tenant deletion lifecycle
	// Phase 4), in rank order.
	gatewayTenantPhase4 = []string{
		"leases", "coordination_mirror", "semantic_cache", "experiment_sticky", "billing_buffer",
		"quota", "artifacts", "checkpoint_manifests", "checkpoint_retention", "checkpoint_meta",
		"transcripts", "interactions", "memory", "session_message_queues", "session_tree_archive",
		"audit", "billing", "eval_results", "delegation_budget", "sessions", "tokens",
		"issued_tokens", "credential_pool", "runtime_tenant_access", "pool_tenant_access",
		"user_roles", "custom_roles", "tenant_definitions", "tenant_config",
	}
)

// GatewayUserWiringConfig returns the gateway's user-path wiring with every
// entry listed by name and a nil function, which the validators accept as
// wired. Tests replace an entry's function to exercise one store.
func GatewayUserWiringConfig() Config {
	cfg := Config{}
	for _, n := range gatewayUserSessionScoped {
		cfg.SessionScoped = append(cfg.SessionScoped, SessionEraser{Name: n})
	}
	for _, n := range gatewayUserScoped {
		cfg.UserScoped = append(cfg.UserScoped, Eraser{Name: n})
	}
	return cfg
}

// GatewayTenantPhase4Names returns the gateway's tenant-path Phase 4 names
// in rank order.
func GatewayTenantPhase4Names() []string {
	return append([]string(nil), gatewayTenantPhase4...)
}
