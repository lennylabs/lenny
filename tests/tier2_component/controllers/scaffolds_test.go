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
//
// spec: 12.2.4
// diagnosis: pkg/controller/poolscaling has the §4.6.2 scaling-formula
// evaluator (strategy.go) and the §6.1 SDK-warm circuit breaker
// (circuitbreaker.go), but no admission-denied retry-with-backoff
// surface on the Reconciler. A test of "PoolScalingAdmissionStuck
// alert wiring" requires an admission-denial code path the controller
// retries against, and that path is not built.
func TestPoolScalingController(t *testing.T) {
	t.Skip("blocked: §12.2.4 Pool Scaling Controller admission-retry harness — the scaling-formula evaluator and the §6.1 circuit breaker exist, but the admission-denied retry-with-backoff loop and the PoolScalingAdmissionStuck alert-wiring surface are not built")
}

// TestTokenServiceController — AssignCredentials, RevokeCredentials,
// RotateCredentials, multi-replica leader election, KMS-envelope
// encryption.
//
// spec: 12.2.4
// diagnosis: pkg/tokenservice ships only the §13.3 HTTP
// token-exchange handler. The AssignCredentials, RevokeCredentials,
// and RotateCredentials gRPC RPCs are defined on the Adapter service
// (pkg/proto/adapter/v1) but pkg/tokenservice does not implement
// them, holds no leader-election loop for multi-replica coordination,
// and has no KMS-envelope writer for the per-tenant key material the
// Token Service controller would manage.
func TestTokenServiceController(t *testing.T) {
	t.Skip("blocked: §12.2.4 Token Service controller — the AssignCredentials/RevokeCredentials/RotateCredentials gRPC server, the leader-election loop for multi-replica coordination, and the KMS-envelope writer for managed credentials are not built; pkg/tokenservice ships only the §13.3 HTTP token-exchange handler")
}
