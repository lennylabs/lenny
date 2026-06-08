// SPDX-License-Identifier: MIT

package v1alpha1

// Annotation keys the PoolScalingController stamps on the pool CRD pair
// (SandboxTemplate, SandboxWarmPool) so the gateway can compare the
// CRD-side reconciliation state against the Postgres source of truth.
// Exported here so both the writer (pkg/controller/poolscaling) and the
// reader (pkg/gateway/podsession sync-status lookup) share one literal
// and cannot drift.
const (
	// AnnotationConfigGeneration carries the §4.6.2 line 558
	// pool_config_generation the gateway-side sync-status / PoolConfigDrift
	// check compares against the Postgres counter.
	// spec: spec/04_system-components.md line 558.
	AnnotationConfigGeneration = "lenny.dev/config-generation"

	// AnnotationLastReconciledAt carries the RFC3339Nano instant at which
	// the PoolScalingController last reconciled a pool's config into its
	// CRD pair. It is stamped alongside AnnotationConfigGeneration whenever
	// the generation changes, so a steady-state pool does not rewrite the
	// CRD every tick. The gateway sync-status endpoint reports it as
	// lastReconciledAt and derives lagSeconds from it.
	// spec: spec/04_system-components.md line 560.
	AnnotationLastReconciledAt = "lenny.dev/last-reconciled-at"
)
