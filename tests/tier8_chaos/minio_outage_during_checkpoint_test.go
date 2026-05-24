// SPDX-License-Identifier: MIT

//go:build chaos

// Tier-8 chaos: MinIO outage during a checkpoint write.
//
// spec: §4.4 lines 254, 271, 277, 281 — when MinIO becomes unreachable
// mid-checkpoint, the gateway's eviction-fallback writer must:
//
//   1. Truncate `last_message_context` to ≤64 KB and write it inline
//      to Postgres (with `is_minio_key=false` and, when the original
//      payload exceeds the inline budget, `context_truncated=true`).
//   2. Run the §4.4 line 277 Postgres-fallback retry budget
//      (exponential backoff capped at 60s) before invoking
//      `driveTotalLoss`.
//   3. Increment `lenny_checkpoint_eviction_fallback_total` on the
//      fallback entry and `lenny_checkpoint_eviction_partial_keys_logged_total`
//      with the committed key list before total-loss.
//   4. Either succeed via the Postgres path (most outages) or, when
//      Postgres also fails inside the retry budget, emit
//      `session.lost` with `reason: "eviction_total_loss"` and bump
//      `lenny_session_eviction_total_loss_total{pool, had_prior_checkpoint}`.
//
// The composite live exercise drops MinIO mid-checkpoint via toxiproxy
// + a testcontainers MinIO sidecar so the eviction-fallback writer
// observes the same TCP-level failure mode an in-cluster MinIO outage
// would surface. The scenario is wired into the tier-8 ops backlog
// alongside the other live chaos runs (leader_election, pod_disruption,
// live_session); see runbook-map.yaml for the runbook contract.
//
// The unit-level coverage of the eviction-fallback writer (the Put-
// retry budget, the partial-keys WARN log, the total-loss path, the
// metric increments) is exercised by:
//
//   - pkg/gateway/evictionfallback/evictionfallback_test.go
//     · TestWriteRetriesPostgresFailover
//     · TestWriteExhaustsRetryBudgetThenDrivesTotalLoss
//     · TestWritePartialKeysCounterZeroLabel
//   - pkg/gateway/evictionfallback/eventbridge_test.go (session.lost)
//   - tests/tier2_component/stores/evictionfallback_test.go
//
// This file ensures the subset reference in tests/groups.subsets.yaml
// (the `minio-outage` chaos subset) resolves to a real test target so
// the §4.4 contract is part of the chaos catalog rather than a dead
// pointer.

package tier8_chaos

import (
	"testing"
)

// TestMinIOOutageDuringCheckpoint is the §4.4 live composite for the
// MinIO-outage-during-checkpoint scenario. The unit-level coverage
// referenced in the file doc-comment exercises every code path; this
// test reserves the chaos-subset target and is wired up alongside the
// other tier-8 live runs once the toxiproxy + MinIO sidecar overlays
// land (tier-8 ops backlog).
func TestMinIOOutageDuringCheckpoint(t *testing.T) {
	// spec: §4.4 lines 254, 271, 277, 281.
	t.Skip("tier-8 live exercise — requires toxiproxy + testcontainers MinIO; " +
		"unit coverage lives in pkg/gateway/evictionfallback/* and " +
		"tests/tier2_component/stores/evictionfallback_test.go")
}
