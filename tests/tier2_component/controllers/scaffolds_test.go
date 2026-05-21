// SPDX-License-Identifier: MIT

//go:build component

// Tier-2 component scaffolds for §12.2.4 Controllers. Each controller
// suite reconciles against a fake client-go lister first, then
// against envtest for the higher-fidelity scenarios.

package controllers_test

// TestWarmPoolController is implemented in warmpool_test.go: it runs
// the reconciler against an envtest API server.

// TestPoolScalingController is implemented in
// poolscaling_admission_test.go, which exercises the §4.6.2 / §16.5
// admission-retry harness and the PoolScalingAdmissionStuck alert
// wiring. The §4.6.2 scaling-formula evaluator and the §6.1 circuit
// breaker carry their own unit suites under pkg/controller/poolscaling
// and pkg/controller/poolscaling/strategy.

// TestTokenServiceController is implemented in tokenservice_test.go,
// which drives pkg/tokenservice.GRPCServer over an in-process bufconn
// link and exercises AssignCredentials, RotateCredentials, and
// RevokeCredentials against a registered §4.9 credential pool. The
// §4.3 gateway-side cutover is exercised by
// tokenservice_client_test.go: the gateway-side credassign.Client
// rides the same bufconn link and mirrors materialized leases into
// the gateway's local credleasestore and credcache so the §4.9 LLM
// proxy hot path is satisfied without an extra Token Service
// round-trip per upstream call.
//
// The scaffold's original mention of "multi-replica leader election"
// is deliberately not exercised: per §4.3 line 209 the Token Service
// is fully stateless and "any replica can handle any request with no
// affinity requirements", so AssignCredentials / RotateCredentials /
// RevokeCredentials need no leader-election coordination at the v1
// scope. KMS-envelope encryption is covered by pkg/credentialstore
// against KMS-backed secret storage.
