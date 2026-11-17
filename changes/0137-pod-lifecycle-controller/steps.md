# Steps: 0137 Pod lifecycle controllers

## Validation owed

| Item | Why it matters | Status |
| :-- | :-- | :-- |
| Regenerate the CRD manifests in `charts/lenny/crds/` and `pkg/embedded/crds/` and confirm that each carries the `lenny.dev/schema-version` annotation and the printer columns of "PLC-2". | An earlier generator run dropped the manually added annotation, and preflight depends on it. | Open, for PLC-B. |
| Confirm that the `PodSet` status carve-out works under server-side apply: the PoolScalingController's field manager owns `status.scalingMode` and `status.bootstrapHoursOfData`, and the WarmPoolController's status writes do not take or clear them **[inference]**. | Two writers now share one status subresource, as they share the warm pool's today. | Open, for PLC-B's tier 2 test. |
| Confirm that no chart template, script, or deployer document reads a retired resource through `kubectl` or `jsonpath` outside the tests that PLC-B rewrites. | PLCR-11. A search found the tier 5 tests, the `tests/testinfra/k8s/` templates, and the documentation listed under "Current state" **[source]**. | Open, for PLC-B. |
| Confirm that no SQL outside `pkg/` and `migrations/` names `sandbox_warm_pools`, and that no table named `pools` exists. | PLCR-12. | Open, for PLC-B. A search of `migrations/` found no `pools` table **[source]**. |
| Confirm that the deletion guard's conditions carry over from the template to the `PodSet` unchanged. | The guard is fail-closed, and RV-3's quarantine retention relies on it. | Open, for PLC-B. |
| Confirm that the API server refuses a custom resource name holding `_`, as "Pool label" under "Current state" infers from the DNS subdomain rule. | "PLC-7" leaves `_` out of pool and runtime names for this reason. | Read for revision 4 in `k8s.io/apiextensions-apiserver` v0.31.4, whose custom resource validator checks `metadata.name` with `NameIsDNSSubdomain` **[external]**. Not run. Open for PLC-D's tier 2 test. |
| Confirm that no test fixture, bootstrap example, or documented example names a pool that fails the pattern of PLCR-15, or a runtime that fails it under PLCR-16, including the `runtimestore` tests of today's 128-character and `_` cases. | A fixture with such a name would fail at create after PLC-D. The reference runtime catalog's names fit **[source]**. | Open, for PLC-D. |
| Confirm that no test, script, or document reads the `<runtime>-pool` name `lenny runtime apply` gives its pool today, including `tests/component/controllers/embedded_runtime_apply_test.go` and `pkg/embedded/stack/runtimeapply_envtest_test.go`. | PLCR-16 names the pool after the runtime. | Open, for PLC-D. |
| Confirm that the tenant-derived runtime create of [DR "API"](../0147-tenant-derived-runtimes/change.md#api) calls `runtimestore.ValidateName` on the whole name. The tree has no `derived-runtimes` route yet **[source]**, and DR "Names" states the prefix grammar and the 63-character cap without a suffix grammar. | PLCR-16. | Open, for PLC-D with DR-1 and DR-4. |
| State how a `PodSet` is named during a rollout, and confirm that every `PodSet` name fits the `lenny.dev/pod-set` label. The label carries the `PodSet` name, and a label value holds at most 63 characters, while a `PodSet` name, as a custom resource name, may hold 253 **[external]**. No design states the name. "PLC-2" says only that outside a rollout the `PodSet` may carry the pool's name, [RV-3](../0140-runtime-revisions/change.md#rv-3-pods) keeps one `PodSet` per pool and pod-set key "labelled with the key" without naming it, and the tier 2 test under "Tests" uses `pool-a-r1`. A name formed as the pool name plus a suffix of n characters exceeds 63 for any pool name longer than 63 − n, and PLCR-15 admits pool names of 63. RV-3's label carrying the pod-set key has the same limit, and a SHA-256 digest written in hexadecimal is 64 characters. The names derived from a `PodSet` fit: an `AgentPod` name comes from `generateName`, whose base the API server truncates to 58 characters before adding 5 random ones **[external]**, and the PDB name `<podSet>-warm` is an object name of up to 253 characters. | A `PodSet` name over 63 characters fails every `AgentPod` and pod create of that pod set at the `lenny.dev/pod-set` label, so the pod set never warms. A rule that bounds the `PodSet` name to 63 characters, for example a pool name truncated to leave room for a fixed-length suffix, closes it. | Closed by revision 5 (D12). "PLC-2" **Pod-set names.** names every `PodSet` `<pool>.<key16>`, at most 80 characters as an object name, and puts the 16-character `key16` in the label rather than the name, and [`0140-runtime-revisions`](../0140-runtime-revisions/change.md#rv-3-pods) revision 23 defines the key's encoding. |
| Confirm that "Changing the list" under [DR-11](../0147-tenant-derived-runtimes/change.md#warm-stores) keeps an earlier pod set claimable only where its pods hold what a session on the new key needs. It argues claimability for warm stores, and the pod-set key also covers a tenant-derived runtime's `inline` and `artifact` assets, so a pod of an earlier key can hold assets a later write replaced. | PLCR-18 claims from every `PodSet` of the revision's pod hash, which includes an earlier key's pod set. If an asset change has to retire the earlier pod set from the claim, the claim set narrows to `PodSet` objects whose key covers the current assets. | Open. [`0147-tenant-derived-runtimes`](../0147-tenant-derived-runtimes/change.md) owns the rule, and "Instructions for other designs" asks it. |
| Confirm that the gateway's claim path can read `PodSet` objects with its existing `get`, `list`, and `watch` on the pool resources, and that the claim's added `PodSet` list adds no measurable latency to `session.claim_pod` with a cached client. | PLCR-18. | Open, for PLC-B. |
| Confirm that `lenny runtime apply` in the embedded stack can compute the pod-set key of the `PodSet` it applies from the `Runtime` resource alone, with no registry revision. | PLCR-17 names the verb's `PodSet` `<runtime>.<key16>`. The pool label still carries the runtime name, so the arithmetic of "PLC-7" **Runtime names.** is unchanged. | Open, for PLC-B with RV-3. |
| Confirm that a drain during a rollout evicts idle pods of both pod sets once each pod set has its own PDB. | PLCR-13. Two PDBs on one pod make the eviction API refuse the eviction **[external]**. | Open, for PLC-B's tier 5 test. |

## Steps

### 0137.S1 Specification changes
- status: Planned
- aliases: [PLC-A, PLC-1, PLC-2 (part), PLC-3, PLC-4 (part)]
- lanes: [specification, documentation]
- depends-on: [0137.S5, 0119.S2, 0118.S3, 0145.S1, 0121.S1]

The specification changes of PLC-1, PLC-3, and PLC-4, and the label and claim text of PLC-2 (§4.6.1 **Disruption protection for agent pods:**, §17.2 items 5 and 9), with the documentation. The kind names and PLC-5's type names land earlier with 0137.S5, at today's fields.

The step rests on D6 and D7, which settled O3 and O4. It follows the designs' own §4.6.1 and §5.2 edits for the fields PLC-4 names: AR-2's removals in [0119.S2](../0119-agent-runtimes-only/steps.md#steps), PL-9's rename in [0118.S3](../0118-agent-pod-layout/steps.md#steps), SI-4's slot count in [0145.S1](../0145-session-isolation/steps.md#steps), and BR-1's class configuration in [0121.S1](../0121-billing-resource-attribution/steps.md#steps). 0137.S1 lands with or after them, so §4.6.1's new text describes the final fields, and the later pass of "Instructions for other designs" renames their staged text before they land.

### 0137.S2 Pod-set names, labels, claim scope, and slot fields
- status: Planned
- aliases: [PLC-B, PLC-2 (part), PLC-4 (part)]
- lanes: [code, documentation]
- depends-on: [0137.S5, 0137.S1, 0118.S3, 0145.S1, 0140.S1]

The pool and pod-set labels with the per-`PodSet` PDB and the label checks of PLCR-13 and PLCR-14, the `PodSet` names of PLCR-17, the claim scope and the `agent_pod_state.pod_set` column of PLCR-18, and `spec.slotCount` and the pod set's `maxConcurrentSessionsPerPod` of PLC-4, with their tests. The kinds, the merge, and the table rename land earlier with 0137.S5. The label rules come after that merge, because the scoping fault exists only once a pool has several `PodSet` objects.

The step needs 0137.S1 for the specification text that names the fields. It needs PL-9's code in [0118.S3](../0118-agent-pod-layout/steps.md#steps) for `maxConcurrentSessionsPerPod`, and SI-4's code in [0145.S1](../0145-session-isolation/steps.md#steps) for the slot count's reader. It needs RV-3's code in [0140.S1](../0140-runtime-revisions/steps.md#steps) for `spec.podHash` and `spec.podSetKey`, which the names of PLCR-17 and the claim of PLCR-18 read, and RV-2's revision selection in the same step, which gives the claim its pod hash. 0137.S2 and RV-3 land together or RV-3 first.

### 0137.S3 Go type renames
- status: Cancelled
- lanes: [code, documentation]
- depends-on: [0137.S1]

The Go type renames of PLC-5. Cancelled on 2026-11-16: 0137.S5 carries the Go type renames with the kind renames, under the aliases PLC-C and PLC-5.

### 0137.S4 Pool and runtime name rule
- status: Planned
- aliases: [PLC-D, PLC-7]
- lanes: [specification, code, documentation]

The pool-name and runtime-name rule of "PLC-7": the §5.2 **Pool name.** and §5.1 **Runtime name.** paragraphs, the §15.1 refusals, `poolstore.ValidateName`, `runtimestore.ValidateName`, the tenant-derived runtime create's name check, the pool name of `lenny runtime apply`, the `docs/api/admin.md` and runtime configuration reference rows, and the tests of PLCR-15 and PLCR-16.

The step depends on nothing. It fixes today's defects, a pool or a runtime that is stored while its pool never warms, under today's kind names, so it can land before 0137.S1. Its §5.2 paragraph names the pod-set label only once 0137.S1 has landed, and states the pool label alone if it lands first. Its §5.1 paragraph names the tenant-derived runtime create only once DR-4 ([0147.S4](../0147-tenant-derived-runtimes/steps.md#steps)) has landed, as "Specification changes" states. If 0137.S4 lands first, DR-4's route calls the new `runtimestore.ValidateName` when it lands. It and DR-1 ([0147.S1](../0147-tenant-derived-runtimes/steps.md#steps)) each limit runtime names to 63 characters, and whichever lands first closes F-5.1.31.

### 0137.S5 Kind renames and the PodSet merge
- status: Planned
- aliases: [PLC-C, PLC-5, PLC-2 (part)]
- lanes: [specification, code, documentation]
- ruled-ready: 2026-11-16, names only, and no decision of 0137 is open

The "Names." table and "Identifiers that change." of PLC-2: the Go types and files, `pkg/controller/sandbox` to `agentpod`, the lower-case identifiers, the CRD files, `LennyCRDNames`, RBAC, webhooks, metrics, alerts, the readiness gate, the field-manager prefix, runbooks, and the table rename from `sandbox_warm_pools` to `pools`. It also carries `podSetRef` and `agentPodRef`, the removal of `templateRef`, the merge of the template and the warm pool into one `PodSet` per pool named after the pool, and the Go type renames of PLC-5. It renames the kinds in the current specification text, row by row in the sections "Specification changes" lists, at today's fields. The tests are PLCR-3, PLCR-4, PLCR-5, PLCR-11, and PLCR-12.

The step depends on no other step, so it runs once, tree-wide, in milestone M1 of the [v1 program](../programs/v1.md), before any area migrates. The merge lands with the renames because `PodSet` is the only name this design gives the template and the warm pool. Until 0137.S2 lands, a `PodSet` is named after its pool (D13). It costs less after 0116 retires `tests/spec-map.json`, which holds many of the old kind names, but it does not wait for 0116.

### Notes on the steps

0137.S5 lands first, in milestone M1 of the [v1 program](../programs/v1.md). 0137.S1 and 0137.S2 follow it, and 0137.S4 lands at any point. None of them depends on [`0125-decision-records`](../0125-decision-records/change.md). 0137.S1 and that design's REC-1 edit (0125.S1) edit one §4.6.1 paragraph and the same §19 and §23.2 rows in separate phrases, so either lands first: 0137.S1 renames the kind in the paragraph's label and REC-1 drops its "ADR-007: " prefix ("PLC-4"), and 0137.S1 restates the license criterion that names upstream while REC-1 restates the phrases that name ADR-008.
