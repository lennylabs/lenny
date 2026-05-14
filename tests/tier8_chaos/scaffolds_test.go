// SPDX-License-Identifier: MIT

//go:build chaos

// Tier-8 chaos test scaffolds. Each test corresponds to a TESTING.md-
// named chaos scenario that requires the production stack plus a
// chaos-injection harness (Chaos Mesh, kube-monkey, or equivalent).
// Today each calls t.Skip with a diagnosis pointing at the spec
// section and the missing infrastructure.

package tier8_chaos_test

import "testing"

// §13.7 Phase 3.5 — admission webhook outage chaos.
func TestAdmissionWebhookOutage(t *testing.T) {
	t.Skip("not implemented: §17.2 admission webhook failurePolicy=Fail chaos — requires Kind cluster + Helm-rendered webhooks + chaos harness to take a webhook deployment down mid-traffic")
}

// §13.7 Phase 3.5 — concurrent claim chaos (ADR-007).
func TestConcurrentClaim(t *testing.T) {
	t.Skip("not implemented: §4.6.1 ADR-007 concurrent-claim chaos — requires the WarmPoolController + the lenny-sandboxclaim-guard webhook running against 50+ concurrent goroutines on a Kind cluster")
}

// §13.19 Phase 8 — MinIO outage during checkpoint.
func TestMinIOOutageDuringCheckpoint(t *testing.T) {
	t.Skip("not implemented: §4.4 eviction fallback — requires MinIO ArtifactStore, the checkpoint pipeline, and a chaos harness that drops MinIO mid-checkpoint")
}

// §13.19 Phase 8 — node drain during MinIO outage (joint chaos).
func TestNodeDrainDuringMinIOOutage(t *testing.T) {
	t.Skip("not implemented: §10.4 reliability chaos — requires the drain-readiness webhook + MinIO ArtifactStore + chaos harness for the joint outage scenario")
}

// §13.14 Phase 5.75 — Redis down during policy check.
func TestRedisDownDuringPolicyCheck(t *testing.T) {
	t.Skip("not implemented: §11.2 + §12.4 quota fail-open — requires Redis-backed quota counters + the §12.4 fail-open / fail-closed transition + chaos harness that drops Redis")
}
