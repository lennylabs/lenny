# Review of the pod-set label

Date: 2026-11-16. Scope: the `lenny.dev/pod-set` label and the selectors that revision 4 of [`pod-lifecycle-controller-design.md`](../../pod-lifecycle-controller-design.md) scopes with it, in "PLC-2" **Pool and pod-set labels.** and PLCR-13 and PLCR-14. The review read the design, [`runtime-revisions-design.md`](../../runtime-revisions-design.md) revision 22, and the tree at commit `1c71c405a` on branch `design/v1-cleanup`, and asked whether each selector has the right scope during a rollout, whether every label value fits, and whether anything guards the label. It ran nothing against a cluster. The design's author re-read each cited path before revision 5 cited it, and this record cites code by file and function.

The kind names below are those of the design (`PodSet`, `AgentPod`, `PodClaim`). Today's code names the same objects `SandboxWarmPool` and `SandboxTemplate`, `Sandbox`, and `SandboxClaim`.

## Findings

### Issue 1 (high): the gateway's claim is scoped to the whole pool

Revision 4 lists the gateway's claim among the selectors scoped to the whole pool, "so a session reaches the pods of every pod set of its pool". [RV-2](../../runtime-revisions-design.md#rv-2-selection) **Claim.** states the opposite: the gateway claims from the pod set for the chosen revision's pod hash and does not substitute a pod set of another pod hash. [RV-6](../../runtime-revisions-design.md#rv-6-sessions-over-time) states the same for a retried or resumed session. "Changing the list" under "Warm stores" in [`tenant-derived-runtimes-design.md`](../../tenant-derived-runtimes-design.md#warm-stores) keeps an earlier pod set's pods claimable after a change to a tenant's contributions, so the pod sets one claim covers can be more than one `PodSet`.

Today `Claim` in `pkg/gateway/podlifecycle/podclaim/claimer.go` and `ClaimSlot` in `slotclaimer.go` list `Sandbox` objects with `client.MatchingLabels{warmpool.LabelPool: req.Pool}` alone, and `ClaimRequest` carries the pool and no revision. The Postgres fallback, `fallbackClaim` in `pkg/gateway/podlifecycle/podsession/fallbackclaim.go`, calls `agentpodstate.Store.ClaimIdle` with the pool, and an `agent_pod_state` row records the pool and no pod set. Under revision 4, during a 5% canary a session that chose revision 8 would bind to an idle revision-7 pod whenever one came first in the list.

Fix proposed: the claim, the reserved-hold rebind that runs on the same list, and the Postgres fallback move into the per-pod-set scope. The claimer resolves the pool's `PodSet` objects whose pod hash is the chosen revision's, under any rule of "Changing the list", and selects `lenny.dev/pool=<pool>, lenny.dev/pod-set in (...)`. Each mirror row records its pod set. Recycle's policy lookup, topology spread, `lenny-ctl diagnose pool`, the runbooks, and the operator's `kubectl` commands stay pool-scoped where the pool is the right scope. Tests cover the canary case and a resumed session.

### Issue 2 (medium): no `PodSet` naming rule, and the pod-set label value can exceed 63 characters

No design names a `PodSet` during a rollout. Revision 4 says only that outside a rollout the `PodSet` "may carry the pool's name", RV-3 keeps one `PodSet` per pool and pod-set key "labelled with the key" without naming the object or the label, and the design's tier 2 test uses `pool-a-r1`. `lenny.dev/pod-set` carries the `PodSet` name, a label value holds at most 63 characters, and the pool-name rule of "PLC-7" admits pool names of 63, so a name formed as the pool name plus any suffix can exceed the limit. A suffix scheme such as `web-1a2b3c4d` also produces a name another pool can hold. A full SHA-256 digest in hexadecimal is 64 characters, so RV-3's key label cannot carry the whole key either.

Fix proposed: name each `PodSet` `<pool>.<key16>`, where `key16` is the first 16 hexadecimal characters of the pod-set key, inside and outside a rollout. Pool names hold no `.` under "PLC-7", so no `PodSet` name equals a pool name or another pool's `PodSet` name. The `lenny.dev/pod-set` value is `key16`, always paired with `lenny.dev/pool` in a selector, and the PDB selects `{lenny.dev/pool, lenny.dev/pod-set, lenny.dev/state: idle}` under the name `<podSet>-warm`. RV-3 defines the key's encoding and its one label.

### Issue 3 (low): the pod-set label is mutable on a false premise

Revision 4 leaves `lenny.dev/pod-set` mutable because "a principal that can write an agent pod's labels can already delete the pod". The gateway's Role in `charts/lenny/templates/gateway-deployment.yaml` grants `get`, `list`, `watch`, and `patch` on pods and no `delete`. A pod whose label is removed leaves its PodDisruptionBudget, and `tenantLabelledPods` in `pkg/controller/warmpool/pinned.go` no longer finds it, so a tenant-pinned pod counts as general warm inventory. Nothing restores the label: the WarmPoolController copies the `AgentPod`'s labels onto the pod once, at create, through `podLabels` in `pkg/controller/sandbox/podspec/base_pod.go`.

Fix proposed: add `lenny.dev/pod-set` to the strictly immutable keys of `DecideImmutableLabels` in `pkg/admission/label_immutability/label_immutability.go`, for pod and `AgentPod` updates, and make `AgentPod.spec.podSetRef` immutable with the CEL rule `self == oldSelf`.

### Issue 4 (low): no carrier of pod-set membership is authoritative

A pod's `PodSet` is recorded three times: in `lenny.dev/pod-set`, in `AgentPod.spec.podSetRef`, and in the `AgentPod`'s owner reference. Revision 4 names none of them as authoritative.

Fix proposed: `spec.podSetRef` and the owner reference are authoritative. The reconcile lists `AgentPod` objects by owner reference or by a field index on `spec.podSetRef`. The label is a copy, and a webhook requires it to equal the value derived from `spec.podSetRef` on `AgentPod` `CREATE`. Mirror recovery takes the pool from the `AgentPod`'s `lenny.dev/pool`, so it still works after the `PodSet` is deleted.

## Checked and ruled out

- **A new label is needed.** No pod or `AgentPod` carries a pod-hash, revision, or pod-set-key label today: no file under `pkg/`, `cmd/`, or `charts/` names `lenny.dev/pod-hash`, `lenny.dev/revision`, or `lenny.dev/pod-set`. RV-3 labels only the `PodSet`. A PodDisruptionBudget selector cannot use an owner reference. The key alone does not identify a pod set across pools, because two pools on one revision with no contributions of their own compute the same key.
- **No two PDBs select one pod.** Each pod carries one `lenny.dev/pod-set` value, and each PDB names one value together with the pool.
- **`minAvailable` is never zero or negative.** `reconcilePDB` in `pkg/controller/warmpool/pdb.go` creates no PDB when `minWarm` is below 2 and deletes an existing one. A canary pod set at RV-3's floor of one pod is unprotected, as a pool with `minWarm` 1 is today.
- **The idle-to-claimed transition is unchanged.** The PDB selects idle pods by `lenny.dev/state`, which the WarmPoolController re-syncs on each phase change.
- **The weaker drain guarantee during a rollout is stated.** Revision 4's "PLC-2" says that during a rollout each pod set admits its own eviction, so a drain can take one idle pod from each pod set at once.
- **Deleting a `PodSet` is safe.** `reconcilePDB` sets the warm pool, the future `PodSet`, as the PDB's controller owner, so the PDB is garbage-collected with it, and RV-3 keeps the `PodSet` while a quarantined `AgentPod` exists.
- **No selector collides.** No code matches a `lenny.dev/` prefix. `DecideImmutableLabels` checks a fixed list of keys. The pool and pod-set labels reach a pod only at create, from `sandboxLabels` in `pkg/controller/warmpool/controller.go` through `podLabels`. After create, the WarmPoolController re-syncs `lenny.dev/state` (`syncPodStateLabel`) and `lenny.dev/host-schedulable` (`reconcileHostSchedulable`), and the gateway stamps `lenny.dev/tenant-id`, so nothing writes or restores the pod-set label. No chart template, NetworkPolicy, or metrics relabel rule names `lenny.dev/pool` or the pod-set label.
- **No back-fill is needed.** Nothing is deployed.

## Owner's answer

The owner answered "yes, apply all four" on 2026-11-16 (decision D12 of the design).

## Notes from applying the fixes

Revision 5 of the design and revision 23 of `runtime-revisions-design.md` apply the fixes. These points differ from the review's wording or add to it:

- **The stateless endpoint lister.** Revision 4 listed `cmd/lenny-gateway/statelessrouting_wiring.go` among the pool-scoped selectors. It serves service mode, which [AR-2](../../agent-runtimes-only-design.md#ar-2-service-mode) removes together with the `/v1/stateless/{pool}` route, so revision 5 drops it rather than rescoping it.
- **Recycle.** The recycle paths in `pkg/gateway/session/recycle/scrubreporter_seams.go` (`maxConcurrentSessions`, `InspectForRecycle`) and `recycleboundary.go` read `pod.Labels[warmpool.LabelPool]` and look up the pool record's recycle policy and cleanup timeout by that name. Those settings belong to the pool record, so the pool scope is correct, and recycle returns a pod to idle inventory in its own pod set without moving it.
- **The rebind.** `Claim` rebinds a same-tenant `reserved` pod from the same list before it acquires an idle pod, so the rebind moves into the per-pod-set scope with the claim.
- **The claim set.** RV-2 said "under the pool's current pod-set key", and "Changing the list" keeps an earlier pod set's pods claimable. Revision 23 of `runtime-revisions-design.md` defines the claim set as every `PodSet` of the pool whose pod hash is the chosen revision's, with the claim-time refusals of "Changing the list" applied per pod. "Validation owed" in the design records that "Changing the list" argues claimability for warm stores, while the pod-set key also covers a tenant's `inline` and `artifact` assets.
- **The `AgentPod` name.** The `AgentPod`, and so the pod, keeps the generated name `<pool>-<random>`, from `generateName` in `createSandbox` in `pkg/controller/warmpool/controller.go`, which today is the warm pool object's name plus `-`. A `PodSet` name holds a `.`, and a generated name based on it would carry the `.` into every pod name, so the generated name takes the pool name from the `PodSet`'s `lenny.dev/pool` and keeps the form it has today.
- **The equality webhook.** `PodSet` names are `<pool>.<key16>`, so the label values of an `AgentPod` follow from `spec.podSetRef` with no API read. `lenny-label-immutability`, which already reviews pod `CREATE` and `UPDATE` and gains `AgentPod` `UPDATE` under PLCR-14, also reviews `AgentPod` `CREATE` and checks both labels against `spec.podSetRef`. `lenny-pool-config-validator` checks the `PodSet`'s own name and labels against its pod-set key.
- **Collisions of `key16`.** Within one pool, two distinct pod-set keys share a 64-bit prefix with probability below n²/2⁶⁵ for n keys, about 3 × 10⁻¹⁴ for 1,000 keys over a pool's life. The WarmPoolController still compares the full key on the `PodSet` it finds under a computed name and refuses to adopt one whose key differs.
