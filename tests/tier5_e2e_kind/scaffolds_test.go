// SPDX-License-Identifier: MIT

//go:build e2e_kind

// Tier-5 e2e Kind tests that need infrastructure the
// control-plane-only dev-mode install does not provide. Each test
// calls kind.InstallLenny to confirm the control plane is up (and to
// skip cleanly when install.sh has not run), then records a skip whose
// message names the exact missing infrastructure.
//
// The e2e values overlay (tests/testinfra/kind/e2e-values.yaml)
// installs the control plane in dev mode: the gateway, controller,
// token service, ops, and admission webhooks run, but no agent-pod
// workload is scheduled and no external Postgres, Redis, or MinIO is
// wired. The behaviours below need one of those, so they skip with a
// precise diagnosis rather than asserting against absent
// infrastructure.
//
// The converted tier-5 tests live in sibling files:
//   - admission_test.go            §13.7 admission policy, inventory,
//                                  label immutability, pod security.
//   - admission_featuregated_test.go  feature-gated admission webhooks.
//   - crd_test.go                  §13.6 warm-pool and sandbox-claim
//                                  CRD plane.
//   - lifecycle_test.go            §13.7 lenny-ops deploy, bootstrap.
//   - mtls_test.go                 §10.3 mTLS PKI.
//   - etcd_encryption_test.go      §13.11 etcd encryption at rest.

package tier5_e2e_kind_test

import (
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/kind"
)

// §13.6 Phase 3 — the pod-lifecycle critical path.

// spec: 13.6
// diagnosis: the §13.6 pod lifecycle (a pod warmed by the pool,
// claimed, assigned to a tenant, drained, and returned) cannot be
// exercised. The dev-mode install runs the control plane but schedules
// no agent-pod workload: there is no runtime image, no SandboxTemplate
// seeded, and no Sandbox driving pod creation. The pod-lifecycle
// behaviour is covered by the tier-2/3 component suites against a
// faked pod registry; a tier-5 run needs a real agent runtime image
// loaded onto the cluster and a SandboxTemplate referencing it.
func TestPodLifecycle(t *testing.T) {
	kind.InstallLenny(t)
	t.Skip("§13.6 pod lifecycle needs a running agent-pod workload: a runtime image " +
		"loaded onto the Kind cluster and a seeded SandboxTemplate. The control-plane-only " +
		"dev-mode install schedules no agent pods.")
}

// §13.19 Phase 8 — checkpoint/resume + node drain.

// spec: 13.19
// diagnosis: the §13.19 node-drain checkpoint/resume path cannot be
// exercised. A drain test cordons a worker node and evicts the agent
// pods on it, then asserts their workspace state is checkpointed and
// resumed on a new node. The dev-mode install schedules no agent pods,
// so there is nothing on a worker node to drain, and no MinIO artifact
// store to checkpoint workspace state into. A tier-5 run needs a
// running agent-pod workload and a reachable artifact store.
func TestNodeDrain(t *testing.T) {
	kind.InstallLenny(t)
	t.Skip("§13.19 node drain needs running agent pods on a worker node and a reachable " +
		"artifact store for checkpoint/resume. The dev-mode install schedules no agent pods.")
}

// §13.27 Phase 12c — concurrent execution modes.

// spec: 13.27
// diagnosis: the §13.27 concurrent execution modes cannot be
// exercised. The test runs sync, async, and streaming sessions
// against the same gateway and asserts mode isolation. It needs a
// running agent-pod workload to host the sessions; the dev-mode
// install schedules no agent pods. Concurrent-mode isolation is
// covered by the tier-2/3 component suites against the gateway's
// in-memory stores.
func TestConcurrentModes(t *testing.T) {
	kind.InstallLenny(t)
	t.Skip("§13.27 concurrent execution modes need a running agent-pod workload to host the " +
		"sessions. The control-plane-only dev-mode install schedules no agent pods.")
}

// §13.28 Phase 13 — observability + audit + backup.

// spec: 13.28
// diagnosis: the §13.28 audit pipeline cannot be exercised end to end.
// The audit pipeline writes audit events to Postgres and ships them to
// the configured sink. The dev-mode install runs the gateway with
// in-memory stores and wires no external Postgres, so there is no
// durable audit table to assert against and no audit sink configured.
// A tier-5 run needs the chart installed with an external Postgres and
// an audit sink.
func TestAuditPipeline(t *testing.T) {
	kind.InstallLenny(t)
	t.Skip("§13.28 audit pipeline needs an external Postgres for the durable audit table and a " +
		"configured audit sink. The dev-mode install runs the gateway with in-memory stores.")
}

// spec: 13.28
// diagnosis: the §13.28 backup/restore path cannot be exercised. A
// backup test triggers a backup of the Postgres state and the MinIO
// artifact store, then restores it into a fresh namespace. The
// dev-mode install wires no external Postgres and no MinIO, so there
// is no durable state to back up. A tier-5 run needs the chart
// installed with external Postgres and MinIO and the backups feature
// configured.
func TestBackupRestore(t *testing.T) {
	kind.InstallLenny(t)
	t.Skip("§13.28 backup/restore needs an external Postgres and a MinIO artifact store to back " +
		"up and restore. The dev-mode install wires neither; the gateway uses in-memory stores.")
}

// §13.32 Phase 15 — cross-environment delegation.

// spec: 13.32
// diagnosis: the §13.32 cross-environment delegation path cannot be
// exercised. Delegation across environments needs a running parent
// agent pod that recursively delegates to a child pod in a second
// environment, plus the DelegationPolicy CRD and the controller sync.
// The dev-mode install schedules no agent pods and seeds no
// environments. A tier-5 run needs a running agent-pod workload and at
// least two configured environments.
func TestCrossEnvironmentDelegation(t *testing.T) {
	kind.InstallLenny(t)
	t.Skip("§13.32 cross-environment delegation needs a running parent agent-pod workload and at " +
		"least two configured environments. The dev-mode install schedules no agent pods.")
}
