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

// TestTokenServiceController is implemented in tokenservice_test.go,
// which drives pkg/tokenservice.GRPCServer over an in-process bufconn
// link and exercises AssignCredentials, RotateCredentials, and
// RevokeCredentials against a registered §4.9 credential pool.
//
// The scaffold's original mention of "multi-replica leader election"
// is deliberately not exercised: per §4.3 line 209 the Token Service
// is fully stateless and "any replica can handle any request with no
// affinity requirements", so AssignCredentials / RotateCredentials /
// RevokeCredentials need no leader-election coordination at the v1
// scope. KMS-envelope encryption is covered by pkg/credentialstore
// against KMS-backed secret storage; the §4.3 gateway↔Token-Service
// trust boundary plus the eventual switch from in-process MintLease
// to the gRPC client is recorded as a follow-on in BUILD-GAPS.md.
