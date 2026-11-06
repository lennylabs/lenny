# Billing resource attribution

Status: design for owner review, revision 2, 2026-11-05. Revision 2 applies the independent review recorded under "Review of revision 1". It records the owner's settled decisions on pricing by actual resources, resource classes held in configuration, idle billing for owned pools, and storage usage. Nothing in this document is built, staged, or validated, and no test, spike, or benchmark has run for it. It reads against `tenant-derived-runtimes-design.md` revision 53 and `forensics-evidence-preservation-design.md` revision 24.

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
| `GET /v1/usage` reports pod minutes | False. The field exists, and the only `usagestore.Record` call sets `Sessions` and no `PodMinutes`, so `totalPodMinutes` sums zeros. The handler reads `usagestore` and never the billing ledger. `GET /v1/sessions/{id}/usage` and `GET /v1/admin/environments/{name}/usage` report `podMinutes` from the billing ledger totals, which also sum zeros. | `usage.go`, `pkg/gateway/billing/usagestore/pgstore/pgstore.go`, `pkg/gateway/sessionserver/session_subresources.go`, `pkg/gateway/externalapi/admin/environment_usage.go` |
| The ledger row follows the object | True. `cataloging.Put`, `RecordPut`, and `Copy` write or confirm the object before inserting the `artifact_store` row, and `SoftDelete` tags the object before changing the row. A listing taken between the two steps sees an object the ledger does not yet record, or the reverse. | `pkg/blobstore/cataloging/cataloging.go` |
| A duplicate billing insert is a no-op | In part. `Append` has no `ON CONFLICT` clause. `InsertFromStream` names the conflict target `(tenant_id, stream_entry_id)`, so it absorbs a duplicate on that index alone. `flushEntries` stops at the first entry `InsertFromStream` rejects and leaves it pending. The failover pipeline routes any `Append` error from the primary to the Redis stream. Both draw `nextval` before the insert. | `pkg/gateway/billing/billingstore/pgstore/pgstore.go`, `pkg/gateway/billing/billingstore/failover/redisstream/redisstream.go`, `pkg/gateway/billing/billingstore/failover/failover.go` |
| An appended event is durable | False on the fallback paths. The pipeline returns success once an event is in the Redis stream or the Tier 2 in-memory buffer, and a replica crash loses the in-memory buffer. | `failover.go` |
| Erasure purges staged billing events | True. `PurgeStagedByUser` removes the user's events from the Redis stream and the in-memory buffer, which §12.8 step 5 requires before step 15 pseudonymizes the ledger. | `failover.go`, §12.8 |
| A class ConfigMap change restarts its readers | False today. The gateway and controller Deployment templates carry no `checksum/` annotation, and the chart uses one only on the OpenTelemetry collector. | `charts/lenny/templates/gateway-deployment.yaml`, `controller-deployment.yaml`, `otel-collector.yaml` |
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
- Whether each managed Kubernetes offering lets a pod reach its own node's kubelet on port 10250 and authorizes `/stats/summary` under `nodes/stats` was not tested.
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

An operator changes a class's values, adds a class, or removes one. Helm merges maps, so an operator removes a shipped class by setting it to `null`. The chart renders the map into one ConfigMap, `lenny-resource-classes`, which the controller and the gateway mount and read at startup (decision 8). The gateway and controller Deployments carry a `checksum/resource-classes` pod annotation computed from the rendered ConfigMap, so a `helm upgrade` that changes a class rolls both. The `--resource-class` flag and `controller.resourceClasses` go, and no deployment exists that needs them.

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
| `max_concurrent_sessions` | uint32 | The pod's slot capacity: the pool's `sessionPolicy.maxConcurrentSessions` when the pod was created, recorded on the pod so that a later pool change does not alter it |
| `usage_window_key` | string | The idempotency key described under "Writing periodic events" |

**Effective values.** The gateway reads the pod object at bind and computes each effective value as Kubernetes does for scheduling and quota: the sum over regular containers and native sidecar init containers, or the largest regular init container if that is larger, plus `spec.overhead` from the RuntimeClass. It stores the values on the binding, so the release path does not read the pod again. The values are what the pod holds, whatever decision 7 settles about how a class maps to containers.

**Shared pods.** On a pod with `maxConcurrentSessions` above 1, each session's window reports its own bound time and the pod's full resources. The apportioning rule is per slot: a session's share is its `pod_minutes` times the pod's resources divided by `max_concurrent_sessions`, so the shares of the sessions on one pod never sum above the pod's cost. On an owned pool, the emitter computes the empty slots of each pod that serves at least one session from the pod's `session_pod_windows` rows and reports them in `pool.idle_usage` as `idle_slot_pod_minutes`. On a shared pool, empty slots stay with the operator.

**Ephemeral storage.** The bill uses the request, which the scheduler reserves on the node. Under decision 10, a node-local reader, `lenny-node-stats`, runs as a DaemonSet on agent nodes and reads `/stats/summary` from its own node's kubelet every `billing.ephemeralStorageSampleSeconds`, default 300. Its ServiceAccount holds `get` on `nodes/stats` and nothing else, and the reader holds no database credential. It records each agent pod's `ephemeral-storage.usedBytes` and sends the samples to the gateway, which keeps the largest per window on `session_pod_windows`, and BR-2 fixes the transport. The design grants no component `nodes/proxy`, because reading the same path through the API server's node proxy requires it, and it reaches the whole kubelet API, including exec into any pod on the node. Memory-backed tmpfs counts against memory and is absent from this figure. The sample is informative and bills nothing by itself.

**Durability.** The gateway writes each open window to a `session_pod_windows` row (`tenant_id`, `session_id`, `pod_uid`, `opened_at`, the resource fields, `closed_at`, and `emitted_at`) in the transaction that records the binding, and sets `closed_at` in the transaction that records the release. A leader-elected task under `lenny-gateway-leader` emits each window whose interval has passed or whose `closed_at` is set. When no release recorded a close, the task closes the window at the earliest of the session's terminal or suspend transition, the same session's next binding, the time the pod was quarantined, and the pod's deletion time. A window therefore never covers quarantine, which the operator pays for under forensics decision 16 and which that design keeps out of `pod_minutes` by stopping it at the terminal transition. A resumed session's window on its old pod also ends before its window on the new pod opens.

**Emitter progress.** The emitter sets `emitted_at` only after it reads the window's `usage_window_key` back from `billing_events` in Postgres. An `Append` that returns after the event landed in the Redis stream or the in-memory buffer leaves the window pending, because a replica crash loses the in-memory buffer, and the next cycle appends the event again under the idempotent insert in "Writing periodic events". Each emitter persists its progress per subject: `emitted_at` on each `session_pod_windows` row, an emitted mark on each `pool_usage_accrual` row, and, for `storage.usage`, a watermark per tenant and `artifact_type`. A gateway crash therefore delays an event and loses none.

**Erasure and teardown.** §12.8 step 5 closes and emits every pending window of the erased user's sessions, so step 15 pseudonymizes those events with the rest of the ledger. An emitter that writes a window for a user whose erasure has started writes the §12.8 pseudonym in place of `user_id`, which covers a window that closes after step 5. The `DeleteByUser` sequence deletes the user's `session_pod_windows` rows after emission. Tenant deletion Phase 4 deletes the tenant's `session_pod_windows` and `pool_usage_accrual` rows and its `storage.usage` watermarks. The emitters write events for a tenant in `disabling` as usual and write no event once `TenantState` is `deleting`, because Phase 4 deletes the tenant's billing events.

**`GET /v1/usage`.** The handler reads `usagestore`, so the emitter also calls `usagestore.Record` after each committed `session.pod_usage`, `pool.idle_usage`, and `storage.usage`, keyed by `usage_window_key` so that a retried window records once. `totalPodMinutes` and a new `podMinutes` on each `byTenant` and `byRuntime` entry sum the recorded `session.pod_usage` minutes. The report gains `idlePodMinutes` from `pool.idle_usage` and `storageByteHours` from `storage.usage`. `GET /v1/sessions/{id}/usage` and `GET /v1/admin/environments/{name}/usage` sum `pod_minutes` from the billing ledger, so they include `session.pod_usage` with no change to either handler.

### `pool.idle_usage`

**What it counts.** For each owned pool, the event reports pod minutes that serve no session, per window of `billing.usageIntervalSeconds`, attributed to the pool's `ownerTenantId`. `tenant_id` is the owner, and `user_id` and `session_id` are absent. Its fields are `pool_name`, `runtime_name`, `window_start`, `window_end`, `usage_window_key`, the resource fields of `session.pod_usage` without the session ones, and these counts:

| Field | Pods counted |
| :-- | :-- |
| `warming_pod_minutes` | Pods created and not yet `idle` |
| `idle_pod_minutes` | Pods in `idle`, claimable and pinned |
| `hold_pod_minutes` | Pods whose claim is in the `reserved` hold between sessions (§4.6.1 **Reserved hold**) |
| `draining_pod_minutes` | Pods draining with no session |
| `reserved_pod_minutes` | The part of the preceding counts on pods marked `spec.reserved` under DR-10 |
| `idle_slot_pod_minutes` | The empty slots of concurrent pods that serve at least one session, in pod minutes, under "Shared pods" |
| `unsampled_minutes` | Minutes of the window that no sample covered within `billing.idleAccrualMaxGapSeconds`, which bill nothing |
| `pod_count_max` | The largest number of such pods at one sample |

A pod that holds a session is billed through `session.pod_usage` and is absent here. A pod in `failed` and a quarantined pod are absent, because the platform and the operator bear those (forensics decision 16). A pool whose pods carry different resources in one window, after a class change, produces one event per distinct set of resource values.

**Accrual.** The WarmPoolController already integrates idle pods on each reconcile. It gains a durable accrual in a `pool_usage_accrual` table, keyed by pool, window start, and a hash of the resource values, holding `owner_tenant_id`, minutes per state, `unsampled_minutes`, and the last sample's time and counts. The controller copies the owner from the pool row when it creates the accrual row, so the last window of a deleted pool still has a tenant. Each reconcile adds the minutes since the last persisted sample, using the counts at that sample, so a restart or leader change continues the integral from the persisted sample. The following rules hold the integral under failure:

- Each write is an update conditional on the `last_sample_at` the controller read. During a leader overlap, the second writer matches no row, rereads, and adds nothing for the interval the first writer counted.
- An interval that crosses a window boundary is split at the boundary, and each part accrues to its own window.
- An interval longer than `billing.idleAccrualMaxGapSeconds`, default 600, accrues its first `billing.idleAccrualMaxGapSeconds` at the earlier counts, and the rest goes to `unsampled_minutes`, so counts that went stale during an outage do not bill the outage.
- A window is ready for emission only after a sample at or after its end, or after the pool's deletion, which the controller records as a final sample.

The controller already holds a Postgres connection for the `agent_pod_state` mirror. The gateway's leader-elected task converts each closed window into one `pool.idle_usage` event (decision 9). `lenny_warmpool_idle_pod_minutes` stays as it is.

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

**Rows the sweep would remove.** The tombstone sweep deletes a row 24 hours after its deletion by default, and a stalled emitter would then lose a deleted row's last hours. The sweep therefore also requires `soft_deleted_at` to precede the watermark for the row's tenant and `artifact_type`, which is the end of the last window whose `storage.usage` event the emitter has read back from `billing_events` in Postgres. The emitter advances the watermark only after that read, so an event held in the Redis stream or the in-memory buffer does not release a tombstone. A stalled emitter keeps tombstones, and `StorageUsageEmitterStalled` fires when the watermark is older than 2 windows.

**Daily reconciliation.** A leader-elected gateway task compares the ledger with the object store once a day, from `billing.storageReconciliation.source`:

- `listing`, the default: `ListObjectVersions` and `ListMultipartUploads` under each `/{tenant_id}/` prefix, rate-limited by `billing.storageReconciliation.requestsPerSecond`.
- `inventory`: the provider's inventory report (S3 Inventory, Cloud Storage inventory reports, or Azure Blob inventory), read from the location the operator configures. MinIO uses `listing`.

For each tenant and object type it reports, under `lenny_storage_reconciliation_difference_bytes{tenant_id, kind}` and `..._objects`, the kinds `missing_object` (a live row with no current object), `orphan_object` (a current object with no row), `tombstoned` (a current object whose row is soft-deleted), `size_mismatch`, `noncurrent_version`, `delete_marker`, and `incomplete_multipart`. `StorageLedgerDrift` fires when `missing_object`, `orphan_object`, or `size_mismatch` exceeds `billing.storageReconciliation.alertFraction` of the tenant's ledger bytes, default 0.01, on consecutive runs. The task writes `storage.reconciliation_completed` to the audit log with the totals.

**Stale views.** A listing or an inventory report is older than the ledger it is compared with. `cataloging.Put`, `RecordPut`, and `Copy` write the object before the row, and an inventory report lags by up to a day, so an artifact written near the snapshot appears in one source and not the other. Each run therefore records its snapshot time, which is the start of its listing or the generation time of the inventory report, and compares only rows whose `created_at` precedes the snapshot time less `billing.storageReconciliation.marginSeconds`, default 3600. A row becomes a confirmed `missing_object` or an overstated `size_mismatch` only when a direct `HEAD` on its key agrees on two consecutive runs. A run that receives any listing or inventory error for a tenant, or whose candidate `missing_object` bytes exceed `billing.storageReconciliation.maxMissingFraction` of the tenant's ledger bytes, default 0.05, aborts for that tenant, writes no correction, and fires `StorageReconciliationAborted`.

**Corrections.** A confirmed `missing_object` means the ledger billed bytes the store did not hold. The task emits a `billing_correction` with reason `STORAGE_RECONCILIATION` against each `storage.usage` event since the last run that saw the object, with the row's bytes removed, sets `reconciliation_missing_at` on the row so that later `storage.usage` events leave it out, and fires `StorageLedgerMissingObject` naming the tenant and the key. The task never deletes or soft-deletes a row. The operator decides whether to soft-delete the row through the guarded path in §12.5, which lowers the quota counter. A confirmed `size_mismatch` with the ledger larger is corrected and alerted the same way. Reconciliation never raises a tenant's bill, so an orphan or a larger object is reported and left to the operator (decision 12).

Each correction starts from the event's current values, which are those of its latest correction when one exists and the original's otherwise, because §11.2.1 applies corrections to one original in sequence order with the latest taking precedence, and a correction computed from the original would undo an earlier one. `STORAGE_RECONCILIATION` is a Category 1 code, and a correction of a periodic event carries the full replacement set of the corrected event's quantity fields. One run can correct every hourly event since the object was last seen, so `BillingCorrectionRateHigh` computes its rate over corrections with other reason codes, and `StorageLedgerDrift` and `StorageLedgerMissingObject` alert on `STORAGE_RECONCILIATION` corrections.

**Cases the ledger misses.** The ledger counts the current object of each artifact it records. The object store bills for more, and the reconciliation reports what it can see:

- **Versioning.** A deleted or overwritten object leaves a noncurrent version for the expiry period, 1 day on MinIO under §12.5, and a delete marker. The ledger stops counting at deletion. The reconciliation reports both kinds, and the operator prices them as overhead or sets a shorter expiry.
- **Lifecycle rules and manual deletes.** An operator's lifecycle rule or a manual delete removes an object the ledger still counts. The reconciliation reports it as `missing_object`, corrects the bill once confirmed, and leaves the row to the operator.
- **Soft-deleted objects.** A soft delete tags the object and leaves it in the bucket until the tombstone sweep's hard prune, 24 hours later by default, and the ledger stops counting at `soft_deleted_at`. The provider bills those bytes, and the reconciliation reports them as `tombstoned`.
- **Abandoned multipart uploads.** A gateway that crashes during a multipart upload leaves parts the ledger never recorded, and the provider bills them until they are aborted. The chart's lifecycle Job installs no abort rule. MinIO removes stale uploads under its own `stale_uploads_expiry` setting, and a cloud store needs an `AbortIncompleteMultipartUpload` lifecycle rule or its equivalent, which the operator guide names with a 1-day period. The reconciliation reports what remains.
- **Activity evidence.** `EvidenceStore` batches are metered as `evidence.usage` by the forensics design and are absent from `storage.usage`.
- **Git mirror stores.** `lenny-git-mirror` holds repositories on persistent volumes shared across tenants. Their bytes are reported per volume by `kubelet_volume_stats_used_bytes` and reach no tenant's bill.
- **Postgres-held data.** Session messages, session logs, the session tree archive, the audit log, and agent memory (`session_messages`, `session_logs`, `session_tree_archive`, `audit_log`, and `agent_memory`) are rows in Postgres. Their size per tenant can only be estimated, because TOAST, compression, indexes, WAL, and replicas do not divide by tenant. Decision 13 covers whether that estimate is billed.
- **Pod ephemeral disk.** Billed as the request on `session.pod_usage` and `pool.idle_usage`, with sampled use beside it.
- **Forensic escrow copies.** Security-hold copies in the escrow bucket and held evidence are paid by the operator (forensics decision 16), so they are absent from tenant events and reported by the forensics design's metrics. Legal-hold escrow copies are decision 14.

### Writing periodic events

`session.pod_usage`, `pool.idle_usage`, and `storage.usage` are appended through the `EventStore` with the per-tenant sequence of §11.2.1 and its Redis and in-memory fallbacks. Each carries `usage_window_key`, a SHA-256 over the event type, tenant, subject (session and pod, pool, or store and artifact type), window start, and resource hash. The billing table gains a unique index on `(tenant_id, usage_window_key)`, partial on a present key.

`Append` and `InsertFromStream` both insert with `ON CONFLICT DO NOTHING` and no conflict target, which absorbs a duplicate on the `usage_window_key` index and on the `stream_entry_id` index alike. A named target absorbs a duplicate on its own index alone, and a duplicate on the other raises 23505. From `InsertFromStream`, `flushEntries` then stops at the rejected entry, and every later entry in the tenant's Redis billing stream waits behind it. From `Append`, the failover pipeline treats the error as a primary failure and routes the duplicate to the stream. A duplicate insert returns the committed row, and a Tier 2 test pins both paths with a duplicate key. The current code has no `usage_window_key` and therefore no second index to collide on, so this is a requirement of the design, and `BUILD-GAPS.md` carries no finding for it.

`nextval` runs before the insert, so a no-op insert would still consume a sequence number, and §11.2.1 states that a gap indicates lost events and should trigger replay. Both paths therefore look up `usage_window_key` in the insert transaction before drawing `nextval`, and a found key ends the transaction with no number drawn. Two concurrent inserts of one key can still both draw a number, and the loser leaves a gap. §11.2.1 gains the rule that a gap a replay leaves empty is benign, as a rollback gap is today. A leader that re-runs a window after a failover therefore inserts nothing twice, and the table stays insert-only. A wrong event is changed only by a `billing_correction`.

The §11.2.1 null/absent contract extends to the new fields, each present only on the event types above. §11.2.1's description of `tenant_id` as the tenant "that owns the session" becomes the tenant the event is billed to, because `pool.idle_usage` and `storage.usage` have no session.

An operator-initiated correction of a new event type always takes the dual-control path, because the `billing.dualControlThreshold` sum adds tokens and pod minutes and has no unit for bytes.

## Specification sections affected

- §4.6.1 **Idle cost visibility and scale-to-zero:**: the durable accrual and `pool.idle_usage`. **CRD validation:**: the class check moves to the gateway write, and the `SandboxTemplate` enum goes.
- §4.6.3: the controller's writes to `pool_usage_accrual`.
- §5.1 **Standalone Runtime**, **Inheritance Rules**, and **Minimal Configuration**: `allowedResourceClasses` and `defaultPoolConfig.resourceClass` name classes defined in configuration, and the default class is `defaultResourceClass`.
- §5.2 **Pool dimensions:**: resource classes as deployment configuration, the chart defaults with their values, the override and removal, the checks at load, write, and reconcile, and the mapping of a class to containers under decision 7. **Pinned idle inventory.**: billing under decision 11. The concurrent-sessions paragraphs: apportioning a shared pod.
- §6.4: the tmpfs check under decision 7, and ephemeral storage in a class.
- §11.2 **Storage quota enforcement mechanism.**: the ledger as the source of `storage.usage`.
- §11.2.1: the event types `session.pod_usage`, `pool.idle_usage`, and `storage.usage`, their fields, `usage_window_key`, the null/absent contract, the meaning of `tenant_id`, the statement that `session.completed` carries no `pod_minutes`, correction replacement sets for the new types, `STORAGE_RECONCILIATION`, corrections computed from the latest corrected values, the `BillingCorrectionRateHigh` rate rule, the target-less `ON CONFLICT DO NOTHING`, the benign gap a deduplicated insert leaves, and dual control for their operator corrections.
- §12.8: the step 5 emission of pending windows, the pseudonym for a window written after erasure starts, and `session_pod_windows`, `pool_usage_accrual`, and the `storage.usage` watermarks in the `DeleteByUser` and tenant deletion Phase 4 lists.
- §12.5 **Tombstone hard-prune sweep.**: the billing watermark per tenant and `artifact_type`. **HA topology requirements:**: abandoned multipart uploads and the abort rule on each provider. A new paragraph for the daily reconciliation, the `reconciliation_missing_at` column on `artifact_store`, and the cases the ledger misses.
- §12.6: `session_pod_windows` and `pool_usage_accrual`.
- §15.1: `GET /v1/admin/resource-classes`, the `VALIDATION_ERROR` on pool and runtime writes, the new fields of the `GET /v1/usage` schema, the `GET /v1/metering/events` fields, and the `POST /v1/admin/billing-corrections` body's replacement fields.
- §16.1 and §16.5: `lenny_resource_class_undefined_total`, `lenny_storage_reconciliation_difference_bytes`, `lenny_storage_reconciliation_difference_objects`, and the alerts `ResourceClassUndefined`, `StorageLedgerDrift`, `StorageLedgerMissingObject`, `StorageReconciliationAborted`, and `StorageUsageEmitterStalled`, with label cardinality stated, and the `BillingCorrectionRateHigh` rate rule.
- §17.4: the embedded stack's classes come from the chart render.
- §17.6 and §17.8.2: `resourceClasses`, `defaultResourceClass`, the `checksum/resource-classes` annotation on the gateway and controller Deployments, the `billing.usageIntervalSeconds`, `billing.ephemeralStorageSampleSeconds`, `billing.idleAccrualMaxGapSeconds`, and `billing.storageReconciliation.*` values, and the `lenny-node-stats` DaemonSet with its `nodes/stats` grant.

## Proposals

No proposal has been started for any of these.

| Label | Contents | Depends on |
| :-- | :-- | :-- |
| BR-1 | Resource classes in configuration: the ConfigMap, the checks at load, write, and reconcile, the CRD enum's removal, the listing route, the embedded check, and decision 7 | None |
| BR-2 | `session.pod_usage`: `session_pod_windows`, the effective values, the emitter and its read-back progress, `usage_window_key` with the target-less insert on both paths, the erasure and teardown steps, the node reader, the `usagestore.Record` call, and the `GET /v1/usage` fields | BR-1 for `resource_class` |
| BR-3 | `pool.idle_usage`: `pool_usage_accrual` with its conditional updates and gap cap, empty slots on owned concurrent pods, and the emitter | BR-2 for the emitter and key, and `tenant-derived-runtimes-design.md` DR-5 for the owner |
| BR-4 | `storage.usage`: the ledger computation and the tombstone watermark | BR-2 for the emitter and key |
| BR-5 | Storage reconciliation: the listing and inventory sources, the snapshot margin, the `HEAD` confirmation, the abort rules, the metrics and alerts, `STORAGE_RECONCILIATION`, the corrections, and the operator guide's abort rule for multipart uploads | BR-4 |
| BR-6 | The Postgres estimate, if decision 13 adopts it | BR-4 |

BR-1 also closes F-5.2.70, F-5.2.71, F-5.2.72, and F-6.4.21. BR-2 closes F-11.2.37.

## Tests

- **Tier 0, static.** A lint that finds no class quantity literal under `pkg/` or `cmd/` outside tests, and no class name literal there outside `pkg/embedded/stack`, whose catalog and agent placement name `small`, `medium`, and `large` and are held to the rendered ConfigMap by the check under "Embedded stack". A chart check that the gateway and controller Deployments carry the `checksum/resource-classes` annotation, and a chart schema check on `resourceClasses`.
- **Tier 1, unit.** Class file parsing and every refusal at load. The effective-value computation with native sidecars, init containers, and overhead. Window splitting at release and at interval boundaries, and the close rule for a quarantined pod and a resumed session. Byte-hour arithmetic for rows created, deleted, and live across a window. The accrual's continuation from a persisted sample, its split at window boundaries, and its gap cap. Empty-slot minutes on a concurrent pod. Each reconciliation kind, the snapshot margin, and the correction amounts computed from the latest correction. `usage_window_key` stability.
- **Tier 2, component.** The gateway's pool and runtime writes refusing an undefined class. The controller setting `ResourceClassUndefined` and creating no pod. `Append` and `InsertFromStream` each absorbing a duplicate `usage_window_key` with no error and no sequence number drawn, and `flushEntries` continuing past a duplicate entry. The accrual's conditional update refusing a second writer.
- **Tier 3, contract.** The `GET /v1/metering/events` and `GET /v1/usage` schemas, the null/absent contract for the new fields, and the correction body.
- **Tier 4, integration.** A session suspended and resumed on another pod produces separate windows with each pod's resources. A class change between pods shows in the events. A tombstone is kept until the watermark passes it, including while its event waits in the Redis stream. A missing object confirmed on two runs yields a correction and an alert and leaves its row in place. An object written during a listing yields no candidate, and a listing error aborts the run with no correction. A user erased with an open window has the window emitted under the pseudonym.
- **Tier 5, end to end on Kind.** `lenny up` and the chart install both start with the shipped classes, an operator's added class creates pods, and a removed class blocks new pods on its pools.
- **Tier 6, end to end on cloud.** The `inventory` source on S3 and Cloud Storage, and the node reader on managed clusters.
- **Tier 7, load.** Emitter throughput at the Tier 3 session count, and listing time and request count on a bucket at the Tier 3 artifact volume.
- **Tier 8, chaos.** Gateway and controller leader loss mid-window, a leader overlap on the accrual, and a replica crash with an event in the in-memory buffer, with no lost and no duplicated event.
- **Tier 9, security.** A tenant cannot read another tenant's usage events, a tenant-admin's pool write cannot name an undefined class, and the node reader's ServiceAccount is refused `nodes/proxy` and exec.
- **Tier 11, documentation.** The operator guide's class override and pricing examples match the chart.

## Residual risks

- **Sampling.** Ephemeral storage use is sampled, so a burst between samples is missed. Idle accrual changes only at reconciles, and a change between them is approximated by the earlier count.
- **Provider bills differ.** `byte_hours` from the ledger leaves out noncurrent versions, delete markers, minimum storage durations on cold storage classes, request charges, and egress. The reconciliation reports noncurrent versions and delete markers, and the operator prices the rest.
- **Apportioning shared pods.** The per-slot share charges a session for its slot whatever the session uses, and empty slots on shared pools reach no tenant.
- **Node reader grant.** A `nodes/stats` grant applies to every node's kubelet, so a stolen reader token reads the statistics of every node it can reach, and the reader adds a workload to each agent node.
- **Unsampled idle time.** Idle minutes beyond the gap cap bill nothing, so a long controller outage leaves idle cost with the operator.
- **Missing rows left to the operator.** A confirmed `missing_object` keeps its row and its quota charge until the operator acts on the alert.
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
| DF-5 | F-11.2.37 | Medium | No billing event carries `pod_minutes`, §11.2.1 does not say which event should, and `GET /v1/usage` reports a `totalPodMinutes` that sums zeros. `GET /v1/usage` reads `usagestore`, and the session and environment usage routes report ledger pod minutes that also sum zeros. | `usage.go`, `watchdog.go`, `billingfanout.go`, `usagestore/pgstore`, `session_subresources.go`, `environment_usage.go`. Read. | BR-2 |

## Review of revision 1

One independent review read revision 1 against the tree. It found one Critical, one High, five Majors, and a set of Minors. Each finding was checked against the tree before it was applied. Each holds, and m8f holds in part. The finding on `InsertFromStream` describes a requirement of this design, because today's tree has no second unique index for a duplicate to hit, so `BUILD-GAPS.md` gains no finding for it.

| Finding | Verdict | Change |
| :-- | :-- | :-- |
| C1, reconciliation soft-deletes live rows from a stale view | Holds. `cataloging.Put`, `RecordPut`, and `Copy` write the object before the row, and an inventory report lags. | "Stale views" adds the snapshot margin, a `HEAD` confirmation on two consecutive runs, and an abort on any listing error or past `maxMissingFraction`. "Corrections" corrects the bill and alerts, and the operator decides on deletion. |
| H2, a duplicate `usage_window_key` raises 23505 and blocks the tenant's Redis billing stream | Holds for the design. `InsertFromStream` names its conflict target, `flushEntries` stops at a rejected entry, and the pipeline routes an `Append` error to the stream. | Both paths use a target-less `ON CONFLICT DO NOTHING`, pinned by a Tier 2 test. No `BUILD-GAPS.md` finding, because the current code has no such index. |
| M3, an event lost after the emitter records it written, and the tombstone watermark advancing before commit | Holds. The pipeline returns success from the in-memory buffer, which a crash loses. | "Emitter progress" advances only after reading `usage_window_key` back from Postgres, and progress and the watermark are per subject. |
| M4, the window close bills quarantine and double-bills a resumed session | Holds. Forensics decision 16 makes the operator pay for quarantined pods, and that design stops `pod_minutes` at the terminal transition. | The close is the earliest of the terminal or suspend transition, the next binding, quarantine, and deletion. |
| M5, idle accrual under leader overlap, after an outage, and on a deleted pool | Holds. | Conditional updates on `last_sample_at`, splits at window boundaries, a gap cap, emission after a sample at the window's end, and `owner_tenant_id` on the accrual row. |
| M6, a deferred window writes a raw `user_id` after `PseudonymizeUser` | Holds. `PurgeStagedByUser` guards the Redis stream and the in-memory buffer, and nothing guards the new tables. | "Erasure and teardown" emits pending windows in step 5, writes the pseudonym after erasure starts, adds both tables to the erasure and Phase 4 lists, and stops emission at `deleting`. |
| M7, decision 10's sampler needs `nodes/proxy` | Holds. The API server authorizes its node proxy path under `nodes/proxy`, and that path reaches exec. | Decision 10 recommends a node-local reader under `nodes/stats` alone and states the reserved-disk alternative. |
| m8a, corrections from the latest corrected values, and the correction rate | Holds. §11.2.1 applies corrections latest-wins. | Corrections start from the current values, and `BillingCorrectionRateHigh` excludes `STORAGE_RECONCILIATION`. |
| m8b, `nextval` before the no-op leaves gaps | Holds. §11.2.1 says a gap should trigger replay. | Both paths look up the key before `nextval`, and §11.2.1 gains the benign-gap rule for the remaining race. |
| m8c, the Tier 0 lint against `pkg/embedded/stack` | Holds. `agentplacement.go` and `catalog.go` name classes. | The lint excludes that package. |
| m8d, soft-deleted objects stay until hard prune | Holds. | A new case and the `tombstoned` kind. |
| m8e, slot capacity on concurrent pods | Holds. | `max_concurrent_sessions` is recorded per pod, the share is per slot, and owned pools report `idle_slot_pod_minutes`. |
| m8f, decision 11 exceeds the settled scope | Holds in part. Decision 11 was already open. | It states that the owner settled owned pools alone. |
| m8g, F-11.2.37 omits two routes and the `usagestore` path | Holds. | F-11.2.37 and DF-5 name both handlers, and BR-2 adds a `usagestore.Record` call. |
| m8h, a class change does not roll the readers | Holds. Neither Deployment carries a `checksum/` annotation. | Both carry `checksum/resource-classes`. |

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
| 8 | Where the gateway and controller read classes | One ConfigMap rendered from `resourceClasses`, mounted by both, with a `checksum/resource-classes` annotation on both Deployments. | A file mount on both, and a rollout of both on each change, which the annotation triggers. | Flags rendered on both Deployments from the same value, which puts the table in each command line and admits drift if either template changes alone. |
| 9 | Which component writes `pool.idle_usage` | The WarmPoolController persists accrual, and the gateway's leader-elected task emits the events. | A new table, and an event that trails its window by up to one emitter cycle. | The controller appends billing events itself, which puts a second writer on the billing table outside the gateway's Redis fallback and sinks. |
| 10 | Source of sampled ephemeral storage use | A node-local DaemonSet, `lenny-node-stats`, reading `/stats/summary` from its own node's kubelet under `nodes/stats` alone, because decision 3 settles sampled use. Reading through the API server's node proxy needs `nodes/proxy`, which reaches the whole kubelet API including exec, and is excluded. | One more workload on each agent node, and a grant that applies to every node's kubelet. | Drop actual-use sampling and bill the reserved disk alone, which removes the reader and the grant and requires the owner to amend decision 3. The adapter reporting its own volume use is also rejected, because a compromised runtime on an embedded pod controls it. |
| 11 | Pinned idle pods on shared pools | Report them under the pinned tenant in `pool.idle_usage` with `pool_ownership: shared_pinned`, so the operator chooses whether to price them. The owner settled idle billing for owned pools alone (decision 2), so adopting this extends decision 2 to shared pools. | More events, and a tenant sees a figure for pods it did not ask to keep. | Owned pools alone, which is the scope the owner settled and leaves the pinned cost with the operator. |
| 12 | Direction of reconciliation corrections | Lower a tenant's bill for `missing_object` and an overstated size, and never raise it. Orphans and larger objects are reported. | Bytes the platform leaked stay unbilled until the operator acts. | Bill orphans under the tenant prefix, which charges a tenant for a platform defect. |
| 13 | Postgres-held data | A daily `storage.usage` with `store: postgres` and `estimated: true`, computed on the read replica from `pg_column_size` over the listed tables. | Read-replica load, and a figure the tenant can dispute. | A per-tenant metric with no billing event, which leaves the operator to price it from monitoring. |
| 14 | Legal-hold escrow copies | A per-tenant metric with no billing event until the owner decides who pays. | Escrow bytes reach no bill. | A `storage.usage` row with `store: escrow` billed to the tenant, which is sound when the tenant asked for the hold and wrong when the operator set it. |
