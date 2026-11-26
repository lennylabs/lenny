# Steps: 0121 Billing resource attribution

## Validation owed

- **Listing rate (decision 22).** Measure the listing time and request count of storage reconciliation at `billing.storageReconciliation.requestsPerSecond` 50 on a bucket at the artifact volume of capacity Tier 3, against S3 at the `load_cloud` tier and against MinIO at the `load_kind` tier, and confirm that the rate stays within each store's request limits and finishes within the daily run. 0121.S5 runs it.
- **Class ephemeral storage request (decision 18).** Confirm at the `e2e_kind` tier that a pod of a shipped class reports an effective ephemeral storage request of the volume sum plus 1 GiB, and that the scheduler reserves it. 0121.S1 runs it.
- **Escrow object size.** Trace whether the escrow migrator in `pkg/legalholdescrow` knows the byte count of each object it writes, which `escrow_object_bytes` (under "Legal-hold escrow copies" in `change.md`) needs. 0121.S6 waits on it.
- **Inserts for a deleted tenant.** Trace whether the `billing_events` tenant guard and the per-tenant sequence accept an insert for a tenant whose `TenantState` is `deleted`, which the `store: escrow` emitter needs after Phase 4. §12.8 Phase 4 keeps the sequence. 0121.S6 waits on it.

## Steps

No proposal has been started for any of these.

### 0121.S1 Resource classes in configuration
- status: Planned
- aliases: [BR-1]
- lanes: [specification, code, documentation]
- depends-on: [0145.S1, 0118.S4]
- closes: [F-5.2.70, F-5.2.71, F-5.2.72, F-6.4.21]

Resource classes in configuration: the ConfigMap, the checks at load, write, and reconcile, the CRD enum's removal, the listing route, the embedded stack check, decision 7's mapping onto the adapter, `netagent`, and the slot containers, and the slot memory floor. The step adds the chart value `runtimes.minSlotMemoryBytes`, which its checks read, and 0153.S7 of [`0153-workspace-materialization`](../0153-workspace-materialization/steps.md#steps), which depends on this step, uses that value.

The step needs the slot containers of [`0145-session-isolation`](../0145-session-isolation/steps.md#steps) (0145.S1) and the volumes of [`0118-agent-pod-layout`](../0118-agent-pod-layout/steps.md#steps) (0118.S4) for the mapping and the ephemeral storage request.

The step renders SI-9's fallback form, with each slot container's CPU limit equal to its request and no pod-level `spec.resources`, because the tree pins `k8s.io/api` v0.31.4, which has no pod-level field. 0145.S4, which depends on this step, renders the pod-level CPU limit together with SI-9's detection ("Container mapping" in `change.md`).

The mapping subtracts the requests of the platform containers present when the step lands. `netagent` arrives with 0127.S2 of [`0127-egress-and-git`](../0127-egress-and-git/steps.md#steps) and `keepalive` with 0128.S2 of [`0128-forensics-evidence-preservation`](../0128-forensics-evidence-preservation/steps.md#steps). Whichever of those steps lands after this one adds its container's request to the subtraction and to the check at load.

### 0121.S2 Pod usage events
- status: Planned
- aliases: [BR-2]
- lanes: [specification, code, documentation]
- depends-on: [0121.S1]
- closes: [F-11.2.37]

`session.pod_usage`: `session_pod_windows`, the effective values, the emitter and its read-back progress, `usage_window_key` with the target-less insert on both paths, the erasure and teardown steps, the keyed `usagestore.Record` call, and the `GET /v1/usage` field `podMinutes`. It also carries the correction surface of the new event types: their correction replacement sets in §11.2.1, the dual-control rule per event type, and the replacement fields of the `POST /v1/admin/billing-corrections` body.

The step needs 0121.S1 for `resource_class`. The close rule's quarantine time exists once 0128.S2 of [`0128-forensics-evidence-preservation`](../0128-forensics-evidence-preservation/steps.md#steps) lands, and until then no window is closed at quarantine. The step adds `k8s.io/component-helpers` to `go.mod` for the effective values, so its branch is reviewed under "Review changes to the merge gate" in `.claude/rules/git-workflow.md`.

Under decision 8 of [`0117-gateway-package-layout`](../0117-gateway-package-layout/decisions.md#decisions), this step changes no gateway seam operation: the window write joins the binding and release transactions inside the session core, which `GWSEAM-MaterializeChild` and `GWSEAM-TerminateUserSessions` reach with no change to their contracts, so the step edits neither `spec/interfaces/gateway-seams.md` nor `pkg/gateway/seam`.

Known conditions the step builds on: F-12.8.53 in `BUILD-GAPS.md` (open) reports that the per-replica billing buffer is erased only on the replica that runs the eraser, while the emitter runs on the leader, and `issues/closed/ERASE/ERASE-I1-billing-verification-fails-intermittently.md` (closed by 0176.S1 on 2026-11-26) reported the erasure job's billing verification failing intermittently. Open decision 24 decides whether periodic events reach the buffer at all. The envelope carries `routing` once the second proposal of [`0141-runtime-routing`](../0141-runtime-routing/steps.md#steps) (0141.S2) lands, and `runtime_revision` once [`0140-runtime-revisions`](../0140-runtime-revisions/steps.md#steps) (0140.S1) lands.

### 0121.S3 Pool idle usage
- status: Planned
- aliases: [BR-3]
- lanes: [specification, code, documentation]
- depends-on: [0121.S2, 0147.S5 (part), 0140.S1 (part), 0147.S9 (part)]

`pool.idle_usage`: `pool_usage_accrual` with its conditional updates, gap cap, and revision key, the subjects with `pool_ownership` `owned`, `owned_rollout`, and `shared_pinned`, empty slots on owned concurrent pods, the emitter, and the `GET /v1/usage` field `idlePodMinutes`.

The step needs 0121.S2 for the emitter and key. The `shared_pinned` subject, the accrual, and the emitter can land first. The `owned` and `owned_rollout` subjects wait on [`0147-tenant-derived-runtimes`](../0147-tenant-derived-runtimes/change.md) [DR-5](../0147-tenant-derived-runtimes/change.md#dr-5-owned-pools) (0147.S5) for the owner of an owned pool, the `owned_rollout` subject also waits on the pod sets per revision of [`0140-runtime-revisions`](../0140-runtime-revisions/change.md#rv-3-pods) (0140.S1), and `reserved_pod_minutes` waits on the `spec.reserved` mark of [DR-10](../0147-tenant-derived-runtimes/change.md#dr-10-capacity-reservation) (0147.S9).

### 0121.S4 Artifact storage usage
- status: Planned
- aliases: [BR-4]
- lanes: [specification, code, documentation]
- depends-on: [0121.S2, 0131.S4 (part)]

`storage.usage` for `store: artifact`: the ledger computation, the tombstone watermark in `storage_usage_watermarks` with the sweep's `pruned_at`, the `GET /v1/usage` field `storageByteHours`, and the billing of packs once per pack with the `platform_chunks` artifact type and the platform tenant for platform-owned sets, with a tenant's uploaded `artifact` assets billed through the packs of its tenant-owned set.

The step needs 0121.S2 for the emitter and key. The ledger computation and the tombstone watermark can land first, and the billing of packs waits on the pack catalog and platform chunk sets of [IC-4](../0131-incremental-checkpoint/change.md#ic-4-catalog-reference-closure-and-quota) (0131.S4) for the pack rows.

### 0121.S5 Storage reconciliation
- status: Planned
- aliases: [BR-5]
- lanes: [specification, code, documentation]
- depends-on: [0121.S4]

Storage reconciliation: the listing and inventory sources, the snapshot margin, the reconciliation state on the ledger row, the `HEAD` confirmation, the abort rules, the metrics and alerts, `STORAGE_RECONCILIATION`, the corrections, the operator guide's abort rule for multipart uploads, and the `lenny_postgres_tenant_bytes_estimate` gauge.

### 0121.S6 Escrow storage usage
- status: Planned
- aliases: [BR-6]
- lanes: [specification, code, documentation]
- depends-on: [0121.S4]

`storage.usage` for `store: escrow`: `escrow_object_bytes` on `legal_hold_escrow_records`, the emission after Phase 4, and the escrow watermark.

The step waits on the two traces under "Validation owed" that name it. Known conditions the step builds on, each open in `BUILD-GAPS.md`: F-12.8.52 (per-region escrow clients are not built), F-12.8.63 (the escrow object is written with no object lock), and F-12.8.65 (clearing a session hold deletes escrow objects with no check that a hold covered them, which would end escrow billing early).

### 0121.S7 Dedicated node capacity
- status: Planned
- aliases: [BR-7]
- lanes: [specification, code, documentation]
- depends-on: [0121.S2, 0121.S3, 0136.S3, 0136.S4]

Dedicated node capacity: `node.dedicated_usage`, `dedicated_node_accrual`, and the WarmPoolController's node sampling (decision 17).

The step needs 0121.S2 for the emitter and key, 0121.S3 for the accrual rules, and the placement design's node classes and node tenancy ([PM-1](../0136-placement/change.md#pm-1-node-classes-and-presets) in 0136.S3, [PM-2](../0136-placement/change.md#pm-2-node-tenancy) in 0136.S4).
