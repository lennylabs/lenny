// SPDX-License-Identifier: MIT

package operations

import "time"

// ExpectedCadence returns the §25.2 expected max inter-step cadence for
// an operation kind. An operation whose progress has not advanced for
// longer than this cadence is stalled (§25.2 line 396; the canonical
// progress envelope's stalledForSeconds is the overrun beyond it). The
// spec names two examples — "2 min per migration, 5 min per restore
// shard" (line 396) — which anchor the platform_upgrade and restore
// cadences; the remaining long-running kinds use the same 5-minute
// shard/dump cadence.
//
// Kinds that are not long-running operations with a progress envelope
// (remediation locks, escalations, idempotency keys) return 0, which
// disables stall detection for them — they are never reported stalled.
//
// spec: §25.2 line 391, line 396.
func ExpectedCadence(k Kind) time.Duration {
	switch k {
	case KindPlatformUpgrade:
		// §25.2 line 396: 2 min per migration step.
		return 2 * time.Minute
	case KindRestore:
		// §25.2 line 396: 5 min per restore shard.
		return 5 * time.Minute
	case KindBackup, KindBackupVerification:
		// A backup dump / verification advances per shard or per phase on
		// the same 5-minute cadence as a restore shard.
		return 5 * time.Minute
	case KindDriftReconciliation:
		// A drift reconcile advances one resource at a time; it shares the
		// 2-minute step cadence.
		return 2 * time.Minute
	case KindWebhookDeliveryPending:
		// A webhook backlog drain advances as the queue depth falls; the
		// 5-minute cadence catches a wholly stuck worker.
		return 5 * time.Minute
	default:
		return 0
	}
}
