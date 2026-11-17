# Review of the pool label on `PodSet`

Date: 2026-11-16. Scope: one statement of revision 2 of [`0137-pod-lifecycle-controller`](../change.md), in "PLC-2" **The merge.**: every `PodSet` carries a `lenny.dev/pool` label naming its pool. The review read the tree at commit `b622d716c` on branch `design/v1-cleanup` and asked whether the label is correct during a rollout, whether anything guards it, and whether every pool name can be a label value. It ran nothing against a cluster. The design's author re-read each cited path with `grep` before revision 3 cited it.

The kind names below are those of the design (`PodSet`, `AgentPod`, `PodClaim`). Today's code names the same objects `SandboxWarmPool` and `SandboxTemplate`, `Sandbox`, and `SandboxClaim`.

## Findings

### Issue 1 (medium): one label value serves two scopes

Today's code treats the `lenny.dev/pool` label value and the warm pool object's name as one value:

- `pkg/controller/warmpool/controller.go` stamps `LabelPool: pool.Name`, the `SandboxWarmPool` object name, in `sandboxLabels`, and lists the pool's `Sandbox` objects with `client.MatchingLabels{LabelPool: pool.Name}`.
- `pkg/controller/warmpool/pdb.go` names the PDB `pdbName(pool.Name)`, which is `<pool>-warm`, and selects idle pods by `{lenny.dev/pool, lenny.dev/state: idle}` with `minAvailable` derived from `minWarm`.
- `pkg/controller/warmpool/pinned.go` `tenantLabelledPods` lists pods by the label.
- `pkg/controller/warmpool/mirror_recovery.go` groups `Sandbox` objects by the label and uses the value as the warm pool's and the template's name.
- `pkg/controller/warmpool/gc.go` `poolFromSandboxRef` falls back from the label to `spec.poolRef`.
- The gateway selects pods by the client's pool name: `pkg/gateway/podlifecycle/podclaim/slotclaimer.go`, `pkg/gateway/podlifecycle/podclaim/claimer.go`, and `cmd/lenny-gateway/statelessrouting_wiring.go`.
- Recycle looks the policy up by `pod.Labels[LabelPool]`: `pkg/gateway/session/recycle/scrubreporter_seams.go` and `recycleboundary.go`.

During a rollout a pool has several `PodSet` objects whose names differ from the pool name. If the pods carry the `PodSet` name, the claimer and recycle cannot find the new revision's pods. If they carry the pool name, each `PodSet`'s reconcile counts the other `PodSet`'s pods, which under-warms the pool, and two PDBs select the same idle pods. The Kubernetes eviction API refuses to evict a pod that more than one PDB selects, so node drains stall for the length of the rollout.

Fix proposed: the PoolScalingController sets `lenny.dev/pool` to the pool name on each `PodSet`, the WarmPoolController copies that value (not the `PodSet` name) onto each `AgentPod` and pod, everything scoped to one `PodSet` selects by `spec.podSetRef`, the owner reference, or a pod-set label, and everything scoped to the pool selects by `lenny.dev/pool`.

### Issue 2 (low): nothing guards the label

`lenny.dev/pool` is not in the strictly immutable label set of `pkg/admission/label_immutability/label_immutability.go`, which lists `lenny.dev/managed`, `lenny.dev/delivery-mode`, `lenny.dev/egress-profile`, `lenny.dev/dns-policy`, and `lenny.dev/runtime-process`. The claimer trusts the label alone. During a rollout no `PodSet` field names the pool, so the label is the only record of pool membership. A principal with write on `PodSet` objects could relabel one to pool B, which would make pool A's pods, built with pool A's isolation and egress settings, claimable for pool B. The tenant check at claim limits this on tenant-owned pools.

Fix proposed: the label is required and immutable on `PodSet`, `lenny-pool-config-validator` refuses a `PodSet` `CREATE` without it and an `UPDATE` that changes it, and the design decides whether the label joins the strictly immutable agent pod labels.

### Issue 3 (low): pool names that are not label values

`pkg/gateway/runtime/poolstore/poolstore.go` `ValidateName` accepts `^[a-z0-9][a-z0-9_-]{0,127}$`, with the comment "§5.2 pool-name shape": up to 128 characters, `_`, and a trailing `-` or `_`. A label value is at most 63 characters, begins and ends with an alphanumeric character, and holds `-`, `_`, and `.` between. A pool name that is not a valid label value is accepted today and never warms, because the create of the pool's per-pod objects fails. With the label on `PodSet`, the `PodSet` apply fails.

Fix proposed: the admin API refuses a pool name that is not a valid label value, with a pattern such as `^[a-z0-9]([a-z0-9_-]{0,61}[a-z0-9])?$`.

## Checked and ruled out

- **No consumer starts matching new objects.** Every Go consumer of the label lists one kind or selects pods. The CRDs declare no `categories`, so `kubectl get all` and category queries do not reach them. No chart template uses the label. The `kubectl -l lenny.dev/pool=...` commands in `docs/operator-guide/upgrades.md`, `docs/runbooks/nonce-only-mode.md`, and `scripts/cloud/*/run-load-legacy.sh` each name their kind. The `lenny.dev/pool` in the Kind test infrastructure (`tests/testinfra/kind/cluster.yaml`, `install-gvisor.sh`) is a Node label.
- **No duplicated field.** The `PodSet` of "PLC-2" has no pool field, so the label is not a second copy of a field.
- **No rename or reuse of a pool name.** `UpdatePoolRequest` has no name field, and pool names are the soft-deleted primary key of the pool record table, so a name is never reused.
- **No new tenant exposure.** The label carries the pool name, which the pool's pods already carry.
- **The label is needed.** The WarmPoolController needs the pool name to label pods during a rollout, and operators use `kubectl get podsets -l lenny.dev/pool=<pool>`.

## Owner's answer

The owner answered "ok" to adding all three fixes (decision D10 of the design).

## Notes from applying the fixes

Revision 3 of the design applies the fixes. Three points differ from the review's wording or add to it:

- **The PDB rule.** The review left the choice between one PDB per `PodSet` and one per pool to the design. RV-3 of [`0140-runtime-revisions`](../../0140-runtime-revisions/change.md#rv-3-pods) splits the pool's warm target across pod sets by the summed weight of their active revisions, with a floor of one pod, so `minWarm` is per pod set. The design chose one PDB per `PodSet`, named `<podSet>-warm`, selecting by a new `lenny.dev/pod-set` label and the idle state. §4.6.1 **Disruption protection for agent pods:** already states the PDB per template.
- **Issue 2's reach.** `pkg/admission/webhook/pool_config_validator.go` runs `DecideAuthorization` on every `CREATE` and `UPDATE` of the warm pool and template resources, and it refuses any principal other than the PoolScalingController's service account. A manual relabel of a `PodSet` is therefore already refused. The new check covers the PoolScalingController's own writes. The claimer lists `AgentPod` objects by the label, so the design also holds `lenny.dev/pool` immutable on `AgentPod` updates, and adds it to the strictly immutable agent pod labels.
- **Issue 3's pattern.** The design narrowed the suggested pattern to `^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`, leaving out `_`, because the `PodSet` and the PDB are named from the pool name and an object name of most kinds is a DNS subdomain, which admits no `_`. The refusal is the admin API's existing `400 VALIDATION_ERROR` with `details.field` set to `name`. §5.2 and §15.1 state no pool-name pattern today, so the design stages one in §5.2.
