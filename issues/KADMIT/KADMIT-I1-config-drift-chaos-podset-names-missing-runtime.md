---
id: KADMIT-I1
status: open
kind: test-gap
severity: low
requirements: []
---

# The pool-config drift chaos test applies a PodSet that names a Runtime that does not exist

## Specification

§4.6.3 (CRD Field Ownership and Write Boundaries), **Validating webhook for Postgres-authoritative state (defense-in-depth):** the `lenny-pool-config-validator` webhook rejects writes to `PodSet.spec` on two rule sets. §4.6.1 (Warm Pool Controller (Pod Lifecycle)): the WarmPoolController maintains warm pods for each PodSet toward its `minWarm`.

## Evidence

- `tests/chaos/config_drift_test.go`, `TestPoolConfigDrift`: `warmPoolManifest` renders a PodSet with `runtimeRef: chaos-drift-runtime` and applies it with `minWarm` 1 and then 2. No Runtime of that name is created.
- Change 0137.S5 merged the template and the warm pool into one PodSet, and the merged CRD requires `runtimeRef`, so the fixture gained the reference. Before the merge the warm pool's controller stopped at the missing template and created no pod; the WarmPoolController now creates AgentPods for the PodSet, and the AgentPod reconciler cannot render their pods while the Runtime is missing.
- The behavior on Kind was not checked when the step was built.

## Gap

While the test runs, and until its cleanup deletes the PodSet, the WarmPoolController may keep AgentPods that never render a pod. Any test that waits on the managed agent pods or the AgentPods of `lenny-agents` during that window waits on them, and a killed run leaves them behind with the PodSet.

## Suggested resolution

Give the test a Runtime of its own, created before the PodSet and removed in cleanup, or apply the PodSet with `minWarm` and `maxWarm` of 0 where the test only exercises admission. Add a start-time sweep of the fixture's PodSets and AgentPods by its `lenny.dev/test: chaos-pool-drift` label, as `test-coverage.md` asks of cluster-scoped fixtures.
