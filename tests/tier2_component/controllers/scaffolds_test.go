// SPDX-License-Identifier: MIT

//go:build component

// Tier-2 component scaffolds for §12.2.4 Controllers. Each controller
// suite reconciles against a fake client-go lister first, then
// against envtest for the higher-fidelity scenarios.

package controllers_test

import "testing"

// TestWarmPoolController is implemented in warmpool_test.go: it runs
// the reconciler against an envtest API server.

// TestPoolScalingController — scaling-formula computation,
// admission-denied retry-with-backoff, PoolScalingAdmissionStuck alert
// wiring.
func TestPoolScalingController(t *testing.T) {
	t.Skip("not implemented: §12.2.4 Pool Scaling Controller — requires the scaling-formula evaluator + the admission-denied retry harness + Prometheus alert assertions")
}

// TestTokenServiceController — AssignCredentials, RevokeCredentials,
// RotateCredentials, multi-replica leader election, KMS-envelope
// encryption.
func TestTokenServiceController(t *testing.T) {
	t.Skip("not implemented: §12.2.4 Token Service controller — Phase 12a ships the HTTP surface; this suite requires the leader-election loop + the AssignCredentials/RevokeCredentials/RotateCredentials gRPC RPCs + KMS-envelope writer")
}
