// SPDX-License-Identifier: MIT

//go:build chaos

// Tier-8 chaos: node drain while MinIO is unavailable.
//
// spec: §4.4 (spec/04_system-components.md, Total-loss path) — "the
// pre-drain MinIO health check webhook prevents most planned
// drain-triggered instances of this scenario by blocking drains when
// MinIO is unhealthy; the total-loss path is most likely to occur during
// spontaneous node failures (not planned drains) or when the forced-drain
// override (`lenny.dev/drain-force: "true"`) is used while both stores are
// degraded." When an agent pod is evicted by a node drain while MinIO is
// unreachable, the eviction checkpoint cannot land in MinIO, so the
// gateway's eviction-fallback writer degrades to the Postgres minimal
// state record; when Postgres is also unavailable it enters the
// total-loss path (session.lost with reason eviction_total_loss and the
// lenny_session_eviction_total_loss_total counter).
//
// The disposition this scenario resolves to — the eviction-fallback
// degradation and the total-loss orchestration — is exercised directly
// against real fault-injected stores by:
//
//   - tests/tier4_integration/eviction_fallback_outage_test.go
//     · TestEvictionFallbackMinIOOutageWritesPostgresMinimalState
//       (MinIO down, Postgres healthy: Postgres minimal-state fallback,
//       context truncated inline, workspace_lost/context_truncated set,
//       lenny_checkpoint_eviction_fallback_total fires).
//     · TestEvictionFallbackTotalLossWhenBothStoresDown
//       (MinIO and Postgres both down: session.lost with reason
//       eviction_total_loss, lenny_session_eviction_total_loss_total and
//       the partial-keys-logged counter fire, no durable row survives).
//   - pkg/gateway/storage/evictionfallback (chooser and total-loss
//     branches against in-memory fakes).
//   - tests/tier2_component/stores/evictionfallback_test.go (the writer
//     against a real Postgres store on the healthy path).
//
// The two halves of the live composite are covered independently:
//
//   - The drain driver is tests/tier5_e2e_kind/scaffolds_test.go
//     TestNodeDrainDuringActiveSession, which drains the node hosting a
//     bound agent pod and asserts the WarmPoolController replenishes.
//   - The §12.5 pre-drain gate (planned drains blocked while MinIO is
//     unhealthy) is asserted by tests/tier8_chaos/store_failure_test.go
//     TestMinIOUnavailable, which shows /internal/drain-readiness returns
//     503 during a MinIO outage.
//
// The combined live exercise — drain a node under a bound agent pod while
// MinIO is scaled to zero and observe the eviction disposition end to end
// — additionally needs the eviction-checkpoint trigger wired on the
// gateway. As documented in tests/tier5_e2e_kind/checkpoint_resume_test.go,
// checkpoint.TriggerEviction (pkg/checkpoint/checkpoint.go) is defined with
// its own §4.4 retry budget but is never referenced by any gateway code
// path: the only live checkpoint drivers are the periodic loop and the
// gateway's own preStop CheckpointBarrier fan-out, neither of which fires
// when an individual agent pod is evicted. Draining the node under a bound
// agent pod therefore does not currently produce an eviction checkpoint to
// observe, so the live composite has no disposition to assert until that
// gateway wiring lands. This file reserves the node-drain chaos-subset
// target (tests/groups.subsets.yaml) against a real file so the §4.4
// contract is part of the chaos catalog rather than a dead pointer.

package tier8_chaos

import (
	"testing"
)

// TestNodeDrainDuringMinIOOutage is the §4.4 live composite for the
// node-drain-during-MinIO-outage scenario. The eviction disposition it
// resolves to is exercised against real fault-injected stores by the
// tier-4 eviction-fallback tests referenced in this file's doc comment;
// this test reserves the chaos-subset target and is wired up alongside
// the other tier-8 live runs once the gateway eviction-checkpoint trigger
// is driven on individual agent-pod eviction.
//
// spec: §4.4 (Total-loss path most likely during spontaneous node failure
// or forced-drain override while both stores are degraded).
// diagnosis: a failure here means a node drain during a MinIO outage does
// not resolve to the §4.4 eviction-fallback / total-loss disposition, so
// an evicted session would neither fall back to the Postgres minimal-state
// record nor emit session.lost when both stores are down.
func TestNodeDrainDuringMinIOOutage(t *testing.T) {
	// spec: §4.4 (Total-loss path).
	t.Skip("tier-8 live composite — needs the gateway eviction-checkpoint " +
		"trigger driven on individual agent-pod eviction (checkpoint.TriggerEviction " +
		"is unreferenced by gateway code today; see tests/tier5_e2e_kind/checkpoint_resume_test.go). " +
		"The eviction-fallback / total-loss disposition is exercised against real " +
		"fault-injected stores in tests/tier4_integration/eviction_fallback_outage_test.go; " +
		"the drain driver is tier-5 TestNodeDrainDuringActiveSession and the §12.5 " +
		"pre-drain gate is tier-8 TestMinIOUnavailable.")
}
