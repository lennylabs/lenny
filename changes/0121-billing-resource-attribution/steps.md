# Steps: 0121 Billing resource attribution

## Steps

No proposal has been started for any of these.

### 0121.S1 Resource classes in configuration
- status: Planned
- aliases: [BR-1]
- lanes: [specification, code, documentation]
- depends-on: [0145.S1, 0118.S4]
- closes: [F-5.2.70, F-5.2.71, F-5.2.72, F-6.4.21]

Resource classes in configuration: the ConfigMap, the checks at load, write, and reconcile, the CRD enum's removal, the listing route, the embedded stack check, decision 7's mapping onto the adapter, `netagent`, and the slot containers, and the slot memory floor.

The step needs the slot containers of [`0145-session-isolation`](../0145-session-isolation/steps.md#steps) (0145.S1) and the volumes of [`0118-agent-pod-layout`](../0118-agent-pod-layout/steps.md#steps) (0118.S4) for the mapping and the ephemeral storage request.

### 0121.S2 Pod usage events
- status: Planned
- aliases: [BR-2]
- lanes: [specification, code, documentation]
- depends-on: [0121.S1]
- closes: [F-11.2.37]

`session.pod_usage`: `session_pod_windows`, the effective values, the emitter and its read-back progress, `usage_window_key` with the target-less insert on both paths, the erasure and teardown steps, the `usagestore.Record` call, and the `GET /v1/usage` fields.

The step needs 0121.S1 for `resource_class`. The envelope carries `routing` once the second proposal of [`0141-runtime-routing`](../0141-runtime-routing/steps.md#steps) (0141.S2) lands, and `runtime_revision` once [`0140-runtime-revisions`](../0140-runtime-revisions/steps.md#steps) (0140.S1) lands.

### 0121.S3 Pool idle usage
- status: Planned
- aliases: [BR-3]
- lanes: [specification, code, documentation]
- depends-on: [0121.S2, 0147.S5, 0140.S1]

`pool.idle_usage`: `pool_usage_accrual` with its conditional updates, gap cap, and revision key, the subjects with `pool_ownership` `owned`, `owned_rollout`, and `shared_pinned`, empty slots on owned concurrent pods, and the emitter.

The step needs 0121.S2 for the emitter and key, and [`0147-tenant-derived-runtimes`](../0147-tenant-derived-runtimes/change.md) [DR-5](../0147-tenant-derived-runtimes/change.md#dr-5-owned-pools) (0147.S5) for the owner of an `owned` subject. The `owned_rollout` subject also waits on the pod sets per revision of [`0140-runtime-revisions`](../0140-runtime-revisions/change.md#rv-3-pods) (0140.S1). The `shared_pinned` subject does not wait on DR-5.

### 0121.S4 Artifact storage usage
- status: Planned
- aliases: [BR-4]
- lanes: [specification, code, documentation]
- depends-on: [0121.S2, 0131.S4]

`storage.usage` for `store: artifact`: the ledger computation, the tombstone watermark, and the billing of packs once per pack with the `platform_chunks` artifact type and the platform tenant for platform-owned sets, with a tenant's uploaded `artifact` assets billed through the packs of its tenant-owned set.

The step needs 0121.S2 for the emitter and key, and the pack catalog and platform chunk sets of [IC-4](../0131-incremental-checkpoint/change.md#ic-4-catalog-reference-closure-and-quota) (0131.S4) for the pack rows.

### 0121.S5 Storage reconciliation
- status: Planned
- aliases: [BR-5]
- lanes: [specification, code, documentation]
- depends-on: [0121.S4]

Storage reconciliation: the listing and inventory sources, the snapshot margin, the `HEAD` confirmation, the abort rules, the metrics and alerts, `STORAGE_RECONCILIATION`, the corrections, the operator guide's abort rule for multipart uploads, and the `lenny_postgres_tenant_bytes_estimate` gauge.

### 0121.S6 Escrow storage usage
- status: Planned
- aliases: [BR-6]
- lanes: [specification, code, documentation]
- depends-on: [0121.S4]

`storage.usage` for `store: escrow`: `escrow_object_bytes` on `legal_hold_escrow_records`, the emission after Phase 4, and the escrow watermark.

### 0121.S7 Dedicated node capacity
- status: Planned
- aliases: [BR-7]
- lanes: [specification, code, documentation]
- depends-on: [0121.S2, 0121.S3, 0136.S3, 0136.S4]

Dedicated node capacity: `node.dedicated_usage`, `dedicated_node_accrual`, and the WarmPoolController's node sampling (decision 17).

The step needs 0121.S2 for the emitter and key, 0121.S3 for the accrual rules, and the placement design's node classes and node tenancy ([PM-1](../0136-placement/change.md#pm-1-node-classes-and-presets) in 0136.S3, [PM-2](../0136-placement/change.md#pm-2-node-tenancy) in 0136.S4).

## Validation owed

- **Listing rate (decision 22).** Measure the listing time and request count of storage reconciliation at `billing.storageReconciliation.requestsPerSecond` 50 on a bucket at the artifact volume of capacity Tier 3, at test Tier 7, against S3 and MinIO, and confirm that the rate stays within each store's request limits and finishes within the daily run.
- **Class ephemeral storage request (decision 18).** Confirm on Kind that a pod of a shipped class reports an effective ephemeral storage request of the volume sum plus 1 GiB, and that the scheduler reserves it.
