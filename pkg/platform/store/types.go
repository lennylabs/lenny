// SPDX-License-Identifier: MIT

// Package store is the §12.6 shared-type home for the storage and
// event-bus extension interfaces. The §12.6 "shared type definitions"
// block declares the common ID types, the RedisConcern / StoreType
// enums, and the Subscription interface as a single block "used by the
// five scaling extension interfaces". This package owns that block so
// pkg/storerouter, pkg/podregistry, and pkg/gateway/eventbus share one
// import path rather than each redefining the ID types and casting
// between string and the typed forms at every boundary.
//
// The package is a leaf: it imports nothing, so the storerouter (pgx),
// podregistry (controller-runtime), and eventbus (Redis pubsub)
// packages can all depend on it without an import cycle and without
// pulling each other's heavy dependencies in transitively.
//
// spec: §12.6 lines 363-415 (shared type definitions block).
package store

// ID types are typed strings, so a tenant id cannot be transposed with
// a pool, session, pod, or cluster id at a call site. spec: §12.6
// lines 369-373.
type (
	// PodID identifies an agent pod. The CRD-backed registry maps it to
	// the Sandbox metadata.name; the Postgres-backed registry maps it to
	// agent_pod_state.pod_id.
	PodID string

	// PoolID identifies the warm pool a pod belongs to.
	PoolID string

	// TenantID is the platform-issued tenant identifier.
	TenantID string

	// SessionID is the §12.6 UUIDv8 session identifier.
	SessionID string

	// ClusterID identifies a Kubernetes cluster in a multi-cluster
	// topology. It is always nil on ClaimOpts in v1 (single cluster); the
	// §12.6 ClusterRegistry populates it when routing a claim to a remote
	// cluster.
	ClusterID string
)

// RedisConcern selects which Redis instance StoreRouter returns. In v1
// every concern maps to the same Redis instance; the names are
// load-bearing only when an operator splits concerns onto separate
// instances at Tier 3+ for isolation and independent scaling.
//
// spec: §12.6 lines 375-389.
type RedisConcern string

const (
	// RedisConcernCoordination backs session leases and generation
	// counters. session_data is co-located here in v1.
	RedisConcernCoordination RedisConcern = "coordination"
	// RedisConcernQuota backs token/rate counters, sliding windows, and
	// the billing stream. delegation is co-located here until Tier 3.
	RedisConcernQuota RedisConcern = "quota"
	// RedisConcernCachePubSub backs the routing cache, EventBus channels,
	// the semantic cache, and experiment sticky assignments.
	RedisConcernCachePubSub RedisConcern = "cache_pubsub"
	// RedisConcernDelegation backs delegation budget keys
	// ({root_session_id}:dlg:*).
	RedisConcernDelegation RedisConcern = "delegation"
	// RedisConcernSessionData backs the DLQ and durable inbox.
	RedisConcernSessionData RedisConcern = "session_data"
)

// StoreType identifies a Postgres store category for ShardCount.
// billing and audit share the same DSN in v1 but have separate routing
// methods to allow independent sharding later.
//
// spec: §12.6 lines 391-400.
type StoreType string

const (
	// StoreTypeSession covers sessions, session_messages, and
	// session_tree_archive.
	StoreTypeSession StoreType = "session"
	// StoreTypeTenant covers tenants, environments, runtime_definitions,
	// credential_pools, and delegation_policies.
	StoreTypeTenant StoreType = "tenant"
	// StoreTypeBilling covers billing_events (R-03).
	StoreTypeBilling StoreType = "billing"
	// StoreTypeAudit covers audit_log (R-03).
	StoreTypeAudit StoreType = "audit"
)

// Subscription is the handle EventBus.Subscribe returns. Unsubscribe
// detaches the subscriber and waits for its consume loop to exit. The
// error is in the signature so a future at-least-once backend (NATS,
// Kafka) can surface an unsubscribe failure without a caller-side
// rewrite.
//
// spec: §12.6 lines 411-414.
type Subscription interface {
	Unsubscribe() error
}
