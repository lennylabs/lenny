# Billing resource attribution

Status: design for owner review, revision 1, 2026-11-05. It records the owner's settled decisions on pricing by actual resources, resource classes held in configuration, idle billing for owned pools, and storage usage. Nothing in this document is built, staged, or validated, and no test, spike, or benchmark has run for it. It reads against `tenant-derived-runtimes-design.md` revision 53 and `forensics-evidence-preservation-design.md` revision 22.

## Problem

§11.2.1 describes its billing event stream as suitable for invoice-grade billing, and an operator cannot price a tenant's usage from it today.

- No billing event carries pod time. §11.2.1 lists `pod_minutes` and does not say which event carries it, and the gateway sets it on no event it emits.
- A pod's cost depends on its CPU, memory, and storage, and no event states them. The operator also redefines a resource class at any time, so a class name alone does not state what a pod held.
- The code holds the class values. §5.2 names `small`, `medium`, and `large` and gives no quantities, the controller carries built-in quantities, and the `SandboxTemplate` CRD refuses any other class name.
- A tenant's owned pool (`tenant-derived-runtimes-design.md` DR-5) keeps warm and reserved pods that serve no session, and those pods appear only in a per-pool metric with no tenant.
- Stored bytes have a quota (§11.2) and no billing event. The `artifact_store` ledger holds every size and lifetime the bill needs, and nothing compares it with the object store, which holds bytes the ledger does not count.

## Scope

This design covers resource classes as deployment configuration, the resource fields on session pod usage, a periodic `pool.idle_usage` event for owned pools, a periodic `storage.usage` event computed from the `artifact_store` ledger, and a daily reconciliation of that ledger against the object store.

The following are outside it:

- Prices, currencies, invoices, and rating. Lenny records quantities, and the operator's billing system prices them.
- Token metering, which §11.2 already covers.
- Activity evidence bytes, which `forensics-evidence-preservation-design.md` meters as `evidence.usage`.
- Extended resources such as GPUs in a class. A class here holds CPU, memory, and ephemeral storage.
- Node-level costs that no pod requests, such as DaemonSets, the kubelet reserve, and idle node capacity.
- The tmpfs size caps the pod builder applies (§6.4). The class check reads the same values the builder uses, and those values are unchanged.

Relationship to other designs:

- `tenant-derived-runtimes-design.md` supplies `ownerTenantId` on the pool row (DR-5), the `spec.reserved` mark (DR-10), and the warm clone storage request (`maxWarmCloneBytes`). `pool.idle_usage` depends on DR-5, and its "Not verified" bullet on idle billing now points here. Its DR-10 **Reporting** paragraph states that `lenny_warmpool_idle_pod_minutes` makes a reserved idle cost attributable. That metric carries no tenant, so the attribution is `pool.idle_usage`, and the next revision of that document should say so.
- `forensics-evidence-preservation-design.md` decision 16 makes the operator pay for quarantined pods and held bytes. `pool.idle_usage` leaves out quarantined pods, and `storage.usage` leaves out security-hold escrow copies.
- `egress-and-git-design.md` places `lenny-git-mirror` on persistent volumes in `lenny-system`. Those volumes are outside the artifact ledger.

## Current state

Each row was checked against the named source at the working tree of branch `design/egress-and-git` on 2026-11-05. "Read" means the evidence comes from reading the code, and nothing was run.

| Claim | Status | Source |
| :-- | :-- | :-- |
| The code holds the resource classes | True. `DefaultRegistry` returns `small` (requests 250m and 1Gi, limits 1 and 1Gi), `medium` (500m and 2Gi, 2 and 2Gi), and `large` (1 and 4Gi, 4 and 4Gi). `DefaultClass` is `medium`. | `pkg/controller/sandbox/resourceclass/resourceclass.go` |
| An operator can override a class | In part. The repeatable `--resource-class name=requests.cpu:…,limits.memory:…` flag replaces or adds a class on top of the built-in table. The chart's `controller.resourceClasses` is `{}` and renders a flag only for a class the operator sets. `buildResourceClasses` starts from `DefaultRegistry`, so no override removes a built-in class. | `cmd/lenny-controller/main.go`, `cmd/lenny-controller/setup.go`, `charts/lenny/values.yaml`, `charts/lenny/templates/controller-deployment.yaml` |
| The controller checks each class against the tmpfs reservation | True. `Registry.Validate` refuses a class whose memory limit is unset or does not exceed `TmpfsReservationMiB` (576Mi), and the controller exits at startup on that error. | `resourceclass.go`, `setup.go` |
| A deployer-defined class can be used | False. `SandboxTemplateSpec.ResourceClass` carries `+kubebuilder:validation:Enum=small;medium;large`, so the API server refuses a template naming any other class, although the flag accepts one and the package comment names "a deployer-defined class". The PoolScalingController copies the pool row's class onto the template. | `pkg/apis/lenny/v1alpha1/sandboxtemplate_types.go`, `charts/lenny/crds/lenny.dev_sandboxtemplates.yaml`, `pkg/controller/poolscaling/poolstoresource.go` |
| A pool write naming an undefined class is refused | False. `POST` and `PUT /v1/admin/pools` and the bootstrap path store `resourceClass` without a check against any class table, and the gateway holds no class table. | `pkg/gateway/externalapi/admin/pools.go`, `bootstrap_resources.go`, `pkg/gateway/runtime/poolstore/poolstore.go` |
| An undefined class at pod creation fails closed | False. `resolveResources` returns no requirements for a class the registry lacks, so the pod's containers carry no CPU or memory requests or limits. | `pkg/controller/sandbox/controller.go` |
| A class states a pod's resources | False. `applyResources` copies the class onto every container and init container, and a sidecar pod has an adapter and a runtime container, so the pod requests the class once per container. | `pkg/controller/sandbox/podspec/resources.go`, `sidecar.go`, `embedded.go` |
| The embedded stack carries its own class values | False. `lenny up` applies manifests pre-rendered from the chart under `presets/dev.yaml`, the rendered controller arguments carry no `--resource-class`, and the controller therefore uses the built-in table. The embedded catalog names `small`, `medium`, and `large`. | `pkg/embedded/manifests/manifests.go`, `pkg/embedded/manifests/manifests.yaml`, `pkg/embedded/stack/catalog.go`, `agentplacement.go` |
| The specification defines classes | False. §5.2 **Pool dimensions:** lists "Resource class (small / medium / large)", §6.4 requires class definitions to account for tmpfs, and §4.6.1 **CRD validation:** checks "resource class values within the allowed set", which nothing defines. No section gives quantities or an override. | §4.6.1, §5.2, §6.4 |
| A billing event carries pod time | False. `session.completed` is appended in `afterTerminalRelease` and in the watchdog's `recordCompleted` with no `PodMinutes`. `token_usage.checkpoint` carries tokens alone. Only a `billing_correction` sets `PodMinutes`. | `pkg/gateway/sessionserver/usage.go`, `pkg/gateway/runtime/watchdog/watchdog.go`, `pkg/gateway/billing/billingfanout/billingfanout.go`, `pkg/gateway/externalapi/admin/billing_corrections.go` |
| `GET /v1/usage` reports pod minutes | False. The field exists, and the only `usagestore.Record` call sets `Sessions` and no `PodMinutes`, so `totalPodMinutes` sums zeros. | `usage.go`, `pkg/gateway/billing/usagestore/pgstore/pgstore.go` |
| A billing event states a pod's resources | False. `billingstore.Event` and `Conditional` hold no class, resource, or isolation field for usage. | `pkg/gateway/billing/billingstore/billingstore.go` |
| Idle warm pods reach a tenant's bill | False. `lenny_warmpool_idle_pod_minutes` is labelled by `pool` and `resource_class`, counts ready and pinned idle pods, and has no tenant label. The class label reads `unspecified` when the template names none. | `pkg/controller/warmpool/idle_meter.go`, `pkg/controller/warmpool/controller.go` |
| The idle integral survives a controller restart | False. `idleMeter` keeps its last sample in memory, and the first observation after a restart or leader change only sets a baseline, so the gap is not counted. | `idle_meter.go` |
| The ledger holds what a storage bill needs | True. `artifact_store` has `tenant_id`, `session_id`, `artifact_type` (`workspace`, `eviction_context`, `checkpoint`, `export`, and `session_log`), `artifact_size_bytes`, `state`, `legal_hold`, `created_at`, and `soft_deleted_at`. §11.2 and §12.5 name the deletion column `deleted_at`. | migrations `0049`, `0063`, and `0066` |
| Ledger rows outlive their deletion | For `gc.tombstoneRetentionSeconds` only, 24 hours by default, after which the tombstone sweep deletes them. | §12.5 **Tombstone hard-prune sweep.**, `pkg/blobstore/artifactcatalog/catalog.go` |
| Anything compares the ledger with the object store | False. No code lists object versions or multipart uploads for that purpose. | Search of `pkg/` for `ListObjectVersions`, `ListMultipartUploads`, and inventory readers |
| The bucket keeps noncurrent versions | True for MinIO. The chart's lifecycle Job enables versioning and expires noncurrent versions and delete markers. It installs no rule that aborts incomplete multipart uploads. Cloud stores are configured by the operator. | §12.5 **Delete-marker lifecycle:**, `charts/lenny/templates/minio-bucket-lifecycle-job.yaml` |
| A correction can replace any billed quantity | False. §11.2.1 **Correction semantics** defines replacement values for `tokens_input`, `tokens_output`, and `pod_minutes` alone. | §11.2.1, `billing_corrections.go` |
| The metering API uses the §11.2.1 field names | False. The wire uses camelCase (`podMinutes`, `sequenceNumber`), and §11.2.1 uses snake_case. This design names fields as §11.2.1 does, and the wire renders them as it renders the existing ones. | `pkg/gateway/sessionserver/metering.go` |

### Not verified

- Whether the gateway's object store client uses multipart uploads for large workspace uploads was not traced. The reconciliation counts incomplete uploads either way.
- Whether `agent_pod_state` records the time of each state change was not established. Decision 9 does not depend on it.
- Whether `nodes/stats` is granted and served on each managed Kubernetes offering was not tested.
- MinIO's default for `stale_uploads_expiry` was not checked against the MinIO version the chart pins.
- How legal-hold escrow copies under `pkg/legalholdescrow` are sized and listed was not traced.

## Design

### Resource classes in configuration

**Source.** The binaries hold no class. The chart's `values.yaml` ships a top-level `resourceClasses` map and a `defaultResourceClass`, with the values the code holds today:

```yaml
resourceClasses:
  small:
    requests: { cpu: 250m, memory: 1Gi }
    limits:   { cpu: "1",  memory: 1Gi }
  medium:
    requests: { cpu: 500m, memory: 2Gi }
    limits:   { cpu: "2",  memory: 2Gi }
  large:
    requests: { cpu: "1",  memory: 4Gi }
    limits:   { cpu: "4",  memory: 4Gi }
defaultResourceClass: medium
```

An operator changes a class's values, adds a class, or removes one. Helm merges maps, so an operator removes a shipped class by setting it to `null`. The chart renders the map into one ConfigMap, `lenny-resource-classes`, which the controller and the gateway mount and read at startup (decision 8). The `--resource-class` flag and `controller.resourceClasses` go, and no deployment exists that needs them.

**Fields.** A class holds `requests.cpu`, `requests.memory`, `limits.cpu`, and `limits.memory`, all required, and optional `requests.ephemeral-storage` and `limits.ephemeral-storage`. Its name is a DNS label of at most 63 characters, because it appears as a metric label value and in billing events.

**Checks at load.** The controller and the gateway each refuse to start, with `CONFIG_INVALID` naming the class and the field, when the file is absent, the map is empty, a required field is missing, a request exceeds its limit, `defaultResourceClass` names no class, or a class's memory limit does not exceed the tmpfs reservation. The controller computes the reservation from the size caps the pod builder applies, as `TmpfsReservationMiB` does today, and the comparison follows decision 7.

**Checks at write.** The gateway refuses a write that names an undefined class with `400 VALIDATION_ERROR`, naming the field and listing the defined classes. This covers a pool's `resourceClass` on `POST` and `PUT /v1/admin/pools` and on bootstrap, a runtime's `allowedResourceClasses` and `defaultPoolConfig.resourceClass`, and the equivalent fields on a tenant-derived runtime and its owned pool. The `SandboxTemplate` CRD loses its enum, because the API server cannot read deployment configuration, and §4.6.1's **CRD validation:** sentence changes to name the gateway check.

**Checks at reconcile.** A template can name a class that a later configuration change removed. The controller creates no pod for it, sets the pool condition `ResourceClassUndefined` with the class name, and increments `lenny_resource_class_undefined_total{pool}`, and the alert `ResourceClassUndefined` fires. Running pods keep their resources. The fallback to unconstrained containers goes.

**Changing a class.** A change applies to pods created after the controller restarts with the new file. A pod keeps the resources it was created with until it ends, which is why every usage event records the pod's actual resources.

**Listing.** `GET /v1/admin/resource-classes` returns the loaded table to `platform-admin` and `tenant-admin`, so a tenant writing a derived runtime can see the names it may use.

**Embedded stack.** `lenny up` applies manifests rendered from the chart under `presets/dev.yaml`, so the embedded stack receives the same ConfigMap and the same defaults through that render, and `TestEmbeddedManifestsMatchDevProfileRender` holds the copy to the chart. The embedded catalog and agent placement name `small`, and a test checks that every class name in `pkg/embedded/stack` is defined in the rendered ConfigMap.

### Resource fields on usage events

**Carrier.** A new event type, `session.pod_usage`, carries a session's pod time, one event per pod-binding window (decision 6). A window opens when the session binds a pod and closes at the first of the release of that binding (suspend, terminal state, eviction, or a resume onto another pod) or the next boundary of `billing.usageIntervalSeconds`, default 3600 and minimum 300, aligned to UTC. A session that moves between pods produces separate windows for each pod. `session.completed` carries no `pod_minutes`.

**Fields.** `session.pod_usage` carries the common envelope (`tenant_id`, `user_id`, `session_id`, `environment_id`, `experiment_id`, `variant_id`, and labels) and these fields:

| Field | Type | Meaning |
| :-- | :-- | :-- |
| `pod_minutes` | float64 | The session's bound time on the pod within the window |
| `window_start`, `window_end` | RFC 3339 | The window's bounds |
| `window_reason` | string | `interval` or `release` |
| `pod_uid` | string | The pod's Kubernetes UID, so a consumer can group a shared pod's windows |
| `pool_name` | string | The pool the pod belongs to |
| `runtime_name` | string | The runtime the session runs |
| `resource_class` | string | The class the pod was created with |
| `isolation_profile` | string | `standard`, `sandboxed`, or `microvm` |
| `cpu_request_millicores`, `cpu_limit_millicores` | uint64 | The pod's effective CPU request and limit |
| `memory_request_bytes`, `memory_limit_bytes` | uint64 | The pod's effective memory request and limit |
| `ephemeral_storage_request_bytes` | uint64 | The pod's effective ephemeral storage request, which includes a warm clone's `warmCloneBytes` |
| `ephemeral_storage_limit_bytes` | uint64 | Absent when no container sets one |
| `ephemeral_storage_used_max_bytes` | uint64 | The largest sampled use in the window, absent when no sample was taken |
| `max_concurrent_sessions` | uint32 | The pool's `sessionPolicy.maxConcurrentSessions` when the pod was bound |
| `usage_window_key` | string | The idempotency key described under "Writing periodic events" |

**Effective values.** The gateway reads the pod object at bind and computes each effective value as Kubernetes does for scheduling and quota: the sum over regular containers and native sidecar init containers, or the largest regular init container if that is larger, plus `spec.overhead` from the RuntimeClass. It stores the values on the binding, so the release path does not read the pod again. The values are what the pod holds, whatever decision 7 settles about how a class maps to containers.

**Shared pods.** On a pod with `maxConcurrentSessions` above 1, each session's window reports its own bound time and the pod's full resources. The operator divides the pod's cost, for example by `max_concurrent_sessions` or by the number of windows sharing a `pod_uid` at each moment.

**Ephemeral storage.** The bill uses the request, which the scheduler reserves on the node. A sampler reads `/stats/summary` for each node with agent pods through the API server under `nodes/stats` (decision 10) every `billing.ephemeralStorageSampleSeconds`, default 300, and records each pod's `ephemeral-storage.usedBytes`. Memory-backed tmpfs counts against memory and is absent from this figure. The sample is informative and bills nothing by itself.

**Durability.** The gateway writes each open window to a `session_pod_windows` row (`tenant_id`, `session_id`, `pod_uid`, `opened_at`, the resource fields, and `closed_at`) in the transaction that records the binding, and sets `closed_at` in the transaction that records the release. A leader-elected task under `lenny-gateway-leader` emits each window whose interval has passed or whose `closed_at` is set, and it closes a window whose session is terminal or whose pod no longer exists at the later of the session's last state change and the pod's deletion time. A gateway crash therefore delays an event and loses none.

**`GET /v1/usage`.** `totalPodMinutes` and a new `podMinutes` on each `byTenant` and `byRuntime` entry sum `session.pod_usage`. The report gains `idlePodMinutes` from `pool.idle_usage` and `storageByteHours` from `storage.usage`.

### `pool.idle_usage`

**What it counts.** For each owned pool, the event reports pod minutes that serve no session, per window of `billing.usageIntervalSeconds`, attributed to the pool's `ownerTenantId`. `tenant_id` is the owner, and `user_id` and `session_id` are absent. Its fields are `pool_name`, `runtime_name`, `window_start`, `window_end`, `usage_window_key`, the resource fields of `session.pod_usage` without the session ones, and these counts:

| Field | Pods counted |
| :-- | :-- |
| `warming_pod_minutes` | Pods created and not yet `idle` |
| `idle_pod_minutes` | Pods in `idle`, claimable and pinned |
| `hold_pod_minutes` | Pods whose claim is in the `reserved` hold between sessions (§4.6.1 **Reserved hold**) |
| `draining_pod_minutes` | Pods draining with no session |
| `reserved_pod_minutes` | The part of the preceding counts on pods marked `spec.reserved` under DR-10 |
| `pod_count_max` | The largest number of such pods at one sample |

A pod that holds a session is billed through `session.pod_usage` and is absent here. A pod in `failed` and a quarantined pod are absent, because the platform and the operator bear those (forensics decision 16). A pool whose pods carry different resources in one window, after a class change, produces one event per distinct set of resource values.

**Accrual.** The WarmPoolController already integrates idle pods on each reconcile. It gains a durable accrual in a `pool_usage_accrual` table, keyed by pool, window start, and a hash of the resource values, holding minutes per state and the last sample's time and counts. Each reconcile adds the minutes since the last persisted sample, using the counts at that sample, so a restart or leader change continues the integral from the persisted sample. The controller already holds a Postgres connection for the `agent_pod_state` mirror. The gateway's leader-elected task converts each closed window into one `pool.idle_usage` event (decision 9). `lenny_warmpool_idle_pod_minutes` stays as it is.

**Ownership.** Until DR-5 lands, no pool has an owner and no event is emitted. Pinned idle pods on shared pools are decision 11.

### `storage.usage`

**What it counts.** For each tenant and each `artifact_type` with a live row in the window or a row deleted in it, the event reports, per window of `billing.usageIntervalSeconds`:

| Field | Type | Meaning |
| :-- | :-- | :-- |
| `store` | string | `artifact` for the ledger, and `postgres` under decision 13 |
| `artifact_type` | string | The ledger's `artifact_type` value |
| `bytes_held_start`, `bytes_held_end` | uint64 | Sum of `artifact_size_bytes` over rows live at each bound |
| `byte_hours` | float64 | Sum over rows of `artifact_size_bytes` times the hours of the row's life inside the window |
| `legal_hold_byte_hours` | float64 | The part of `byte_hours` on rows with `legal_hold` set |
| `bytes_written`, `bytes_deleted` | uint64 | Sizes of rows created, and of rows soft-deleted, in the window |
| `object_count_end` | uint64 | Live rows at the window's end |
| `estimated` | boolean | `true` only for `store: postgres` |
| `window_start`, `window_end`, `usage_window_key` | | As for the other periodic events |

A row's life runs from `created_at` to `soft_deleted_at`, or to the window's end when it is live. Checkpoint reservations without an `artifact_store` row are quota reservations and are absent. `user_id` and `session_id` are absent.

**Rows the sweep would remove.** The tombstone sweep deletes a row 24 hours after its deletion by default, and a stalled emitter would then lose a deleted row's last hours. The sweep therefore also requires `soft_deleted_at` to precede the end of the last window the emitter has written, a watermark the emitter stores. A stalled emitter keeps tombstones, and `StorageUsageEmitterStalled` fires when the watermark is older than 2 windows.

**Daily reconciliation.** A leader-elected gateway task compares the ledger with the object store once a day, from `billing.storageReconciliation.source`:

- `listing`, the default: `ListObjectVersions` and `ListMultipartUploads` under each `/{tenant_id}/` prefix, rate-limited by `billing.storageReconciliation.requestsPerSecond`.
- `inventory`: the provider's inventory report (S3 Inventory, Cloud Storage inventory reports, or Azure Blob inventory), read from the location the operator configures. MinIO uses `listing`.

For each tenant and object type it reports, under `lenny_storage_reconciliation_difference_bytes{tenant_id, kind}` and `..._objects`, the kinds `missing_object` (a live row with no current object), `orphan_object` (a current object with no live row), `size_mismatch`, `noncurrent_version`, `delete_marker`, and `incomplete_multipart`. `StorageLedgerDrift` fires when `missing_object`, `orphan_object`, or `size_mismatch` exceeds `billing.storageReconciliation.alertFraction` of the tenant's ledger bytes, default 0.01, on consecutive runs. The task writes `storage.reconciliation_completed` to the audit log with the totals.

**Corrections.** A `missing_object` means the ledger billed bytes the store did not hold. The task soft-deletes the row through the guarded path in §12.5, so the quota counter falls, and emits a `billing_correction` with reason `STORAGE_RECONCILIATION` against each `storage.usage` event since the last run that saw the object, with the row's bytes removed. A `size_mismatch` with the ledger larger is corrected the same way. Reconciliation never raises a tenant's bill, so an orphan or a larger object is reported and left to the operator (decision 12). `STORAGE_RECONCILIATION` is a Category 1 code, and a correction of a periodic event carries the full replacement set of the corrected event's quantity fields.

**Cases the ledger misses.** The ledger counts the current object of each artifact it records. The object store bills for more, and the reconciliation reports what it can see:

- **Versioning.** A deleted or overwritten object leaves a noncurrent version for the expiry period, 1 day on MinIO under §12.5, and a delete marker. The ledger stops counting at deletion. The reconciliation reports both kinds, and the operator prices them as overhead or sets a shorter expiry.
- **Lifecycle rules and manual deletes.** An operator's lifecycle rule or a manual delete removes an object the ledger still counts. The reconciliation reports it as `missing_object` and corrects it.
- **Abandoned multipart uploads.** A gateway that crashes during a multipart upload leaves parts the ledger never recorded, and the provider bills them until they are aborted. The chart's lifecycle Job installs no abort rule. MinIO removes stale uploads under its own `stale_uploads_expiry` setting, and a cloud store needs an `AbortIncompleteMultipartUpload` lifecycle rule or its equivalent, which the operator guide names with a 1-day period. The reconciliation reports what remains.
- **Activity evidence.** `EvidenceStore` batches are metered as `evidence.usage` by the forensics design and are absent from `storage.usage`.
- **Git mirror stores.** `lenny-git-mirror` holds repositories on persistent volumes shared across tenants. Their bytes are reported per volume by `kubelet_volume_stats_used_bytes` and reach no tenant's bill.
- **Postgres-held data.** Session messages, session logs, the session tree archive, the audit log, and agent memory (`session_messages`, `session_logs`, `session_tree_archive`, `audit_log`, and `agent_memory`) are rows in Postgres. Their size per tenant can only be estimated, because TOAST, compression, indexes, WAL, and replicas do not divide by tenant. Decision 13 covers whether that estimate is billed.
- **Pod ephemeral disk.** Billed as the request on `session.pod_usage` and `pool.idle_usage`, with sampled use beside it.
- **Forensic escrow copies.** Security-hold copies in the escrow bucket and held evidence are paid by the operator (forensics decision 16), so they are absent from tenant events and reported by the forensics design's metrics. Legal-hold escrow copies are decision 14.

### Writing periodic events

`session.pod_usage`, `pool.idle_usage`, and `storage.usage` are appended through the `EventStore` with the per-tenant sequence of §11.2.1 and its Redis and in-memory fallbacks. Each carries `usage_window_key`, a SHA-256 over the event type, tenant, subject (session and pod, pool, or store and artifact type), window start, and resource hash. The billing table gains a unique index on `(tenant_id, usage_window_key)`, and the insert uses `ON CONFLICT DO NOTHING`, as the flusher does for `stream_entry_id`. A leader that re-runs a window after a failover therefore inserts nothing twice, and the table stays insert-only. A wrong event is changed only by a `billing_correction`.

The §11.2.1 null/absent contract extends to the new fields, each present only on the event types above. §11.2.1's description of `tenant_id` as the tenant "that owns the session" becomes the tenant the event is billed to, because `pool.idle_usage` and `storage.usage` have no session.

An operator-initiated correction of a new event type always takes the dual-control path, because the `billing.dualControlThreshold` sum adds tokens and pod minutes and has no unit for bytes.

## Specification sections affected

- §4.6.1 **Idle cost visibility and scale-to-zero:**: the durable accrual and `pool.idle_usage`. **CRD validation:**: the class check moves to the gateway write, and the `SandboxTemplate` enum goes.
- §4.6.3: the controller's writes to `pool_usage_accrual`.
- §5.1 **Standalone Runtime**, **Inheritance Rules**, and **Minimal Configuration**: `allowedResourceClasses` and `defaultPoolConfig.resourceClass` name classes defined in configuration, and the default class is `defaultResourceClass`.
- §5.2 **Pool dimensions:**: resource classes as deployment configuration, the chart defaults with their values, the override and removal, the checks at load, write, and reconcile, and the mapping of a class to containers under decision 7. **Pinned idle inventory.**: billing under decision 11. The concurrent-sessions paragraphs: apportioning a shared pod.
- §6.4: the tmpfs check under decision 7, and ephemeral storage in a class.
- §11.2 **Storage quota enforcement mechanism.**: the ledger as the source of `storage.usage`.
- §11.2.1: the event types `session.pod_usage`, `pool.idle_usage`, and `storage.usage`, their fields, `usage_window_key`, the null/absent contract, the meaning of `tenant_id`, the statement that `session.completed` carries no `pod_minutes`, correction replacement sets for the new types, `STORAGE_RECONCILIATION`, and dual control for their operator corrections.
- §12.5 **Tombstone hard-prune sweep.**: the billing watermark. **HA topology requirements:**: abandoned multipart uploads and the abort rule on each provider. A new paragraph for the daily reconciliation and the cases the ledger misses.
- §12.6: `session_pod_windows` and `pool_usage_accrual`.
- §15.1: `GET /v1/admin/resource-classes`, the `VALIDATION_ERROR` on pool and runtime writes, the new fields of the `GET /v1/usage` schema, the `GET /v1/metering/events` fields, and the `POST /v1/admin/billing-corrections` body's replacement fields.
- §16.1 and §16.5: `lenny_resource_class_undefined_total`, `lenny_storage_reconciliation_difference_bytes`, `lenny_storage_reconciliation_difference_objects`, and the alerts `ResourceClassUndefined`, `StorageLedgerDrift`, and `StorageUsageEmitterStalled`, with label cardinality stated.
- §17.4: the embedded stack's classes come from the chart render.
- §17.6 and §17.8.2: `resourceClasses`, `defaultResourceClass`, the `billing.usageIntervalSeconds`, `billing.ephemeralStorageSampleSeconds`, and `billing.storageReconciliation.*` values, and the controller's `nodes/stats` grant.

## Proposals

No proposal has been started for any of these.

| Label | Contents | Depends on |
| :-- | :-- | :-- |
| BR-1 | Resource classes in configuration: the ConfigMap, the checks at load, write, and reconcile, the CRD enum's removal, the listing route, the embedded check, and decision 7 | None |
| BR-2 | `session.pod_usage`: `session_pod_windows`, the effective values, the emitter, `usage_window_key`, the ephemeral storage sampler, and the `GET /v1/usage` fields | BR-1 for `resource_class` |
| BR-3 | `pool.idle_usage`: `pool_usage_accrual` and the emitter | BR-2 for the emitter and key, and `tenant-derived-runtimes-design.md` DR-5 for the owner |
| BR-4 | `storage.usage`: the ledger computation and the tombstone watermark | BR-2 for the emitter and key |
| BR-5 | Storage reconciliation: the listing and inventory sources, the metrics and alerts, `STORAGE_RECONCILIATION`, the corrections, and the operator guide's abort rule | BR-4 |
| BR-6 | The Postgres estimate, if decision 13 adopts it | BR-4 |

BR-1 also closes F-5.2.70, F-5.2.71, F-5.2.72, and F-6.4.21. BR-2 closes F-11.2.37.

## Tests

- **Tier 0, static.** A lint that finds no class name or quantity literal under `pkg/` or `cmd/` outside tests, and a chart schema check on `resourceClasses`.
- **Tier 1, unit.** Class file parsing and every refusal at load. The effective-value computation with native sidecars, init containers, and overhead. Window splitting at release and at interval boundaries. Byte-hour arithmetic for rows created, deleted, and live across a window. The accrual's continuation from a persisted sample. Each reconciliation kind and the correction amounts. `usage_window_key` stability.
- **Tier 2, component.** The gateway's pool and runtime writes refusing an undefined class. The controller setting `ResourceClassUndefined` and creating no pod. The emitter's idempotent insert under a duplicate key.
- **Tier 3, contract.** The `GET /v1/metering/events` and `GET /v1/usage` schemas, the null/absent contract for the new fields, and the correction body.
- **Tier 4, integration.** A session suspended and resumed on another pod produces separate windows with each pod's resources. A class change between pods shows in the events. A tombstone is kept until the watermark passes it. A missing object yields a soft delete and a correction.
- **Tier 5, end to end on Kind.** `lenny up` and the chart install both start with the shipped classes, an operator's added class creates pods, and a removed class blocks new pods on its pools.
- **Tier 6, end to end on cloud.** The `inventory` source on S3 and Cloud Storage, and `nodes/stats` on managed clusters.
- **Tier 7, load.** Emitter throughput at the Tier 3 session count, and listing time and request count on a bucket at the Tier 3 artifact volume.
- **Tier 8, chaos.** Gateway and controller leader loss mid-window, with no lost and no duplicated event.
- **Tier 9, security.** A tenant cannot read another tenant's usage events, and a tenant-admin's pool write cannot name an undefined class.
- **Tier 11, documentation.** The operator guide's class override and pricing examples match the chart.

## Residual risks

- **Sampling.** Ephemeral storage use is sampled, so a burst between samples is missed. Idle accrual changes only at reconciles, and a change between them is approximated by the earlier count.
- **Provider bills differ.** `byte_hours` from the ledger leaves out noncurrent versions, delete markers, minimum storage durations on cold storage classes, request charges, and egress. The reconciliation reports noncurrent versions and delete markers, and the operator prices the rest.
- **Apportioning shared pods.** `max_concurrent_sessions` states a pod's capacity and its occupancy varies. Any division the operator chooses charges some sessions more than their share.
- **Unattributed costs.** Node-level costs and the share of a node that no pod requests reach no event.
- **Stalled emitter.** The tombstone watermark holds rows while the emitter is stalled, so `artifact_store` grows until the alert is handled.
- **Reconciliation cost.** Listing a large bucket takes time and requests, and an inventory report lags by up to a day or more, depending on the provider, so a difference can persist for that long.
- **Visibility to tenants.** A tenant's billing viewer reads the class sizes, pod UIDs, and pool names in its own events.
- **Estimates.** A Postgres estimate under decision 13 can be far from the storage cost, and a tenant can dispute it.
- **Owned pools first.** `pool.idle_usage` has no owner until DR-5 lands, and idle cost on shared pools stays the operator's unless decision 11 adopts the pinned case.

## Defects found while verifying

These exist in today's tree independent of this design and are filed in `BUILD-GAPS.md` under the F-id in the "Filed as" column. "Read" means the evidence comes from reading the code, and nothing was run.

| ID | Filed as | Severity | Defect | Evidence | Closed by |
| :-- | :-- | :-- | :-- | :-- | :-- |
| DF-1 | F-5.2.70 | Medium | The specification names `small`, `medium`, and `large` and defines no class, no quantities, and no override, while §4.6.1 validates class values "within the allowed set". | §4.6.1, §5.2, §6.4. Read. | BR-1 |
| DF-2 | F-5.2.71 | Medium | The class values, the default class, and the allowed names are built into the code and the CRD: `DefaultRegistry`, `DefaultClass`, and the `SandboxTemplate` enum, so a deployer-defined class is accepted by the flag and refused by the API server. | `resourceclass.go`, `sandboxtemplate_types.go`, `main.go`. Read. | BR-1 |
| DF-3 | F-5.2.72 | Medium | A class the registry lacks yields pod containers with no CPU or memory requests or limits, and no pool write checks the class. | `resolveResources` in `controller.go`, `pools.go`. Read. | BR-1 |
| DF-4 | F-6.4.21 | Medium | `applyResources` copies the class onto every container, so a sidecar pod requests twice the class, while §6.4 treats a class as the pod's memory limit. | `podspec/resources.go`, `sidecar.go`. Read. | BR-1, under decision 7 |
| DF-5 | F-11.2.37 | Medium | No billing event carries `pod_minutes`, §11.2.1 does not say which event should, and `GET /v1/usage` reports a `totalPodMinutes` that sums zeros. | `usage.go`, `watchdog.go`, `billingfanout.go`, `usagestore/pgstore`. Read. | BR-2 |

## Decisions

Decisions 1 to 5 are settled by the owner, and the design above is written to them. Decisions 6 to 14 are open, and each states a recommendation, its cost, and the main alternative.

- **Decision 1: pricing by actual resources.** Settled. Billing lets an operator price usage by actual resources, and events record the resource class name and the pod's actual resources, because an operator can redefine a class at any time.
- **Decision 2: idle billing for owned pools.** Settled. A periodic billing event reports a tenant's owned pools' idle and reserved pod minutes, attributed to the owning tenant. This is `pool.idle_usage`.
- **Decision 3: storage usage.** Settled. A periodic `storage.usage` event per tenant and artifact type from the `artifact_store` ledger, as bytes held and byte-hours, with a daily reconciliation against the object store that reports differences as a metric and an alert and emits `billing_correction` where appropriate. The design states the cases the ledger misses, bills pod ephemeral disk by its request with sampled use, and leaves forensic escrow copies to the operator under forensics decision 16.
- **Decision 4: resource classes in configuration.** Settled, in the owner's words "Ability to override the default classes is important. In fact, I think there shouldn't be any values hardcoded in the code." The code holds no class, the chart ships `small`, `medium`, and `large` as defaults an operator changes or replaces, the embedded stack carries the same defaults, a pool naming an undefined class is refused at write, the controller checks each class against the tmpfs reservation, and the specification states the classes as chart defaults and defines the override.
- **Decision 5: resource fields on session usage.** Settled. Session usage events carrying `pod_minutes` gain the class name, the pod's actual CPU and memory requests and limits at the time, the isolation profile, and, for a pod serving several sessions, its `maxConcurrentSessions`.

| # | Decision | Recommendation | Cost of the recommendation | Main alternative |
| :-- | :-- | :-- | :-- | :-- |
| 6 | Which event carries a session's pod time | A new `session.pod_usage` per pod-binding window, closed at release and at each interval boundary. | A new event type and table, and more events for long sessions. | `pod_minutes` on `token_usage.checkpoint` and `session.completed`, which cannot describe two pods with different resources in one window and bills nothing until a long session ends. |
| 7 | What a class states | A class states the pod's total. On a sidecar pod the adapter container takes `adapterResources`, a chart value, and the runtime container takes the class less that value. An embedded pod's single container takes the whole class. The tmpfs check runs against the runtime container's memory limit. | One more chart value, and a class must exceed the adapter's share. | Keep per-container semantics and state in §5.2 that a pod requests the class once per container, which doubles a sidecar pod's footprint against an embedded pod of the same class. |
| 8 | Where the gateway and controller read classes | One ConfigMap rendered from `resourceClasses`, mounted by both. | A file mount on both, and a restart of both to apply a change. | Flags rendered on both Deployments from the same value, which puts the table in each command line and admits drift if either template changes alone. |
| 9 | Which component writes `pool.idle_usage` | The WarmPoolController persists accrual, and the gateway's leader-elected task emits the events. | A new table, and an event that trails its window by up to one emitter cycle. | The controller appends billing events itself, which puts a second writer on the billing table outside the gateway's Redis fallback and sinks. |
| 10 | Source of sampled ephemeral storage use | A sampler in the controller reading `/stats/summary` through the API server under `nodes/stats`. | A cluster-wide read of node statistics, which also shows other pods' figures to the controller. | The adapter reporting its own volume use, which a compromised runtime on an embedded pod controls. |
| 11 | Pinned idle pods on shared pools | Report them under the pinned tenant in `pool.idle_usage` with `pool_ownership: shared_pinned`, so the operator chooses whether to price them. | More events, and a tenant sees a figure for pods it did not ask to keep. | Owned pools alone, which leaves the pinned cost with the operator. |
| 12 | Direction of reconciliation corrections | Lower a tenant's bill for `missing_object` and an overstated size, and never raise it. Orphans and larger objects are reported. | Bytes the platform leaked stay unbilled until the operator acts. | Bill orphans under the tenant prefix, which charges a tenant for a platform defect. |
| 13 | Postgres-held data | A daily `storage.usage` with `store: postgres` and `estimated: true`, computed on the read replica from `pg_column_size` over the listed tables. | Read-replica load, and a figure the tenant can dispute. | A per-tenant metric with no billing event, which leaves the operator to price it from monitoring. |
| 14 | Legal-hold escrow copies | A per-tenant metric with no billing event until the owner decides who pays. | Escrow bytes reach no bill. | A `storage.usage` row with `store: escrow` billed to the tenant, which is sound when the tenant asked for the hold and wrong when the operator set it. |
