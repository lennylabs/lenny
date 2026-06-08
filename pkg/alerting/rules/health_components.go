// SPDX-License-Identifier: MIT

package rules

// The §25.3 Platform Health API derives each component's status from the
// firing state of the §16.5 alert catalogue: a component is `unhealthy`
// when a critical-severity alert mapped to it is firing, `degraded` when
// a warning-severity alert is firing, and `healthy` otherwise. The
// gateway's in-process alert tracker and the rendered Helm manifests both
// read this catalogue, so the alert→component association lives here
// alongside the rule definitions rather than in the health package — the
// "single source" the spec requires.
//
// spec: §25.3 lines 443-451 — "Component status is derived deterministically
// from the same threshold expressions used by the alerting rules (Section
// 16.5): healthy — no firing alerts for this component; degraded —
// warning-severity alerts firing; unhealthy — critical-severity alerts
// firing ... Both sides share the rule definitions via the
// pkg/alerting/rules package."

// Health-component identifiers. These match the component names the gateway
// registers on its §25.3 health aggregator (cmd/lenny-gateway) so a firing
// alert overlays the right component's probe verdict.
const (
	HealthComponentPostgres           = "postgres"
	HealthComponentRedis              = "redis"
	HealthComponentObjectStore        = "objectStore"
	HealthComponentCertManager        = "cert-manager"
	HealthComponentGateway            = "gateway"
	HealthComponentCircuitBreakerCache = "circuit-breaker-cache"
)

// healthComponents maps a §16.5 alert Name to the §25.3 health component
// whose verdict it derives. Only alerts that concern a tracked dependency
// (Postgres, Redis, the object store, cert-manager) or a gateway subsystem
// appear here. Capacity, per-pool, billing, audit, and policy alerts are
// deliberately absent: those surface through the §25.3 pool health resolver
// and the suggestedActions ranked-alternative path, not a dependency
// component's healthy/degraded/unhealthy verdict. An unmapped firing alert
// therefore leaves every component's probe-derived status unchanged.
//
// spec: §25.3 lines 443-451, 540-542 (degradation section names postgres /
// redis / objectStore by component).
var healthComponents = map[string]string{
	// Postgres dependency (session truth store, token store, replicas).
	"SessionStoreUnavailable":    HealthComponentPostgres,
	"TokenStoreUnavailable":      HealthComponentPostgres,
	"PostgresReplicationLagHigh": HealthComponentPostgres,
	"PostgresWriteSaturation":    HealthComponentPostgres,
	"PostgresWriteBurstIops":     HealthComponentPostgres,
	"PgBouncerAllReplicasDown":   HealthComponentPostgres,
	"PgBouncerPoolSaturated":     HealthComponentPostgres,

	// Redis dependency (quota/rate-limit, durable inbox, memory pressure).
	"RedisUnavailable":             HealthComponentRedis,
	"RedisMemoryHigh":              HealthComponentRedis,
	"DurableInboxRedisUnavailable": HealthComponentRedis,

	// Object store dependency (MinIO ArtifactStore, checkpoint storage).
	"MinIOUnavailable":               HealthComponentObjectStore,
	"CheckpointStorageUnavailable":   HealthComponentObjectStore,
	"MinIOArtifactReplicationFailed": HealthComponentObjectStore,

	// cert-manager dependency (mTLS certificate renewal).
	"CertExpiryImminent": HealthComponentCertManager,

	// Gateway subsystem health.
	"GatewayNoHealthyReplicas":    HealthComponentGateway,
	"GatewaySubsystemCircuitOpen": HealthComponentGateway,
	"GatewayLatencyHigh":          HealthComponentGateway,
	"GatewayQueueDepthHigh":       HealthComponentGateway,
	"GatewayActiveStreamsHigh":    HealthComponentGateway,
	"KMSSigningUnavailable":       HealthComponentGateway,

	// Circuit-breaker cache freshness (the §11.6 cb:* snapshot).
	"CircuitBreakerStale": HealthComponentCircuitBreakerCache,
}

// HealthComponentFor returns the §25.3 health component an alert derives,
// and whether the alert is mapped at all. An unmapped alert (ok=false)
// does not contribute to any component's health verdict.
func HealthComponentFor(alertName string) (component string, ok bool) {
	c, ok := healthComponents[alertName]
	return c, ok
}
