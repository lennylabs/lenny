// SPDX-License-Identifier: MIT

//go:build e2e_kind

// Tier-5 e2e Kind tests that need infrastructure the e2e install does
// not provide. Each test calls kind.InstallLenny to confirm the
// control plane is up (and to skip cleanly when install.sh has not
// run), then records a skip whose message names the exact missing
// infrastructure.
//
// install.sh stands up the control plane, the in-cluster Postgres,
// Redis, and MinIO data stores, and the two-pool agent-pod workload.
// The behaviours below need infrastructure beyond that — the gateway
// driving live sessions onto the warm pods, a seeded pair of
// environments, or a clock-injection harness — so they skip with a
// precise diagnosis rather than asserting against absent
// infrastructure.
//
// The converted tier-5 tests live in sibling files:
//   - admission_test.go            §13.7 admission policy, inventory,
//                                  label immutability, pod security.
//   - admission_featuregated_test.go  feature-gated admission webhooks.
//   - crd_test.go                  §13.6 warm-pool and sandbox-claim
//                                  CRD plane.
//   - pod_lifecycle_test.go        §13.6 warm-pool pod lifecycle and
//                                  the two §4.7 deployment models.
//   - lifecycle_test.go            §13.7 lenny-ops deploy, bootstrap.
//   - mtls_test.go                 §10.3 mTLS PKI.
//   - etcd_encryption_test.go      §13.11 etcd encryption at rest.
//   - ingress_test.go              §13.2 NET-038 gateway-ingress
//                                  NetworkPolicy.
//   - audit_test.go                §13.28 audit pipeline to Postgres.
//   - backup_test.go               §13.28 / §25.11 backup subsystem.

package tier5_e2e_kind_test

import (
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/kind"
)

// §13.6 Phase 3 — the pod-lifecycle critical path — is converted in
// pod_lifecycle_test.go against the live agent-pod workload.

// §13.19 Phase 8 — checkpoint/resume + node drain.

// spec: 13.19
// diagnosis: the §13.19 node-drain checkpoint/resume path is not
// fully exercised. The install runs an agent-pod workload and an
// in-cluster MinIO artifact store, so warm pods exist on a worker
// node. The remaining gap is a live session with workspace state on
// one of those pods: a drain test cordons the node and evicts the
// pod, then asserts the §12.5 drain-readiness webhook checkpointed
// the session workspace to MinIO and resumed it elsewhere. Evicting
// a shared agent pod also disrupts the other tier-5/8/9 tests.
func TestNodeDrain(t *testing.T) {
	kind.InstallLenny(t)
	t.Skip("§13.19 node drain needs a live session with workspace state on an agent pod so " +
		"the checkpoint has state to capture; the e2e overlay does not drive gateway sessions " +
		"onto the warm pods, and evicting a shared agent pod would disrupt the other tier-5/8/9 tests.")
}

// §13.27 Phase 12c — concurrent execution modes.

// spec: 13.27
// diagnosis: the §13.27 concurrent execution modes are not exercised
// at the e2e tier. The install now runs an agent-pod workload, so warm
// pods exist to host sessions. The remaining gap is the gateway
// driving sync, async, and streaming sessions onto those pods: the
// e2e values overlay installs the control plane but does not seed the
// runtime registration or drive the session API, so there is no
// concurrent session traffic to assert mode isolation against.
// Concurrent-mode isolation is covered by the tier-2/3 component
// suites against the gateway's in-memory stores.
func TestConcurrentModes(t *testing.T) {
	kind.InstallLenny(t)
	t.Skip("§13.27 concurrent execution modes need the gateway driving sync, async, and " +
		"streaming sessions onto the warm pods; the e2e overlay installs the control plane but " +
		"does not drive the session API, so there is no concurrent session traffic to assert.")
}

// §13.28 Phase 13 — observability + audit + backup. The audit pipeline
// (§13.28) and the backup subsystem (§13.28 / §25.11) are converted in
// audit_test.go and backup_test.go against the live in-cluster Postgres
// and MinIO.

// §13.32 Phase 15 — cross-environment delegation.

// spec: 13.32
// diagnosis: the §13.32 cross-environment delegation path is not
// exercised. The install now runs an agent-pod workload, so a parent
// agent pod exists. The remaining gaps are the §10.6 cross-environment
// delegation resolver, which Phase 15 has not built, and a seeded pair
// of environments with a bilateral cross-environment declaration for a
// parent pod to delegate across.
func TestCrossEnvironmentDelegation(t *testing.T) {
	kind.InstallLenny(t)
	t.Skip("§13.32 cross-environment delegation needs the §10.6 cross-environment delegation " +
		"resolver (Phase 15, not built) and a seeded pair of environments with a bilateral " +
		"cross-environment declaration.")
}
