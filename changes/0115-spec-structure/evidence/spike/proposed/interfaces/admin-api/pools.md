---
code: APOOLS
type: interface
summary: >-
  The admin API's pool operations create, read, update, delete, drain, and resize pools, report a
  pool's reconciliation state, and manage which tenants may use a pool. This page states each
  operation's path, request and response fields, errors, and authorization.
owns: [operations, errors]
depends-on: [ADMIN, POOL, CLAIM, APOD, DERIVED, ERR]
---

# Admin API: pools

The admin API's pool operations create, read, update, delete, drain, and resize pools, report a pool's reconciliation state, and manage which tenants may use a pool. This page states each operation's path, request and response fields, errors, and authorization.

## Terms {#terms}

| Term | Gloss |
| :-- | :-- |
| Pool | A warmable deployment target for one runtime ([Warm pools](../../capabilities/runtimes-and-pools/warm-pools.md#pool)). |
| Pool name | The primary key of a pool record ([Warm pools](../../capabilities/runtimes-and-pools/warm-pools.md#pool-name)). |
| Owned pool | The pool of a tenant-derived runtime ([Tenant-derived runtimes](../../capabilities/runtimes-and-pools/tenant-derived-runtimes.md#owned-pool)). |
| Sync status | Whether the pool's latest configuration has reached Kubernetes ([Warm pools](../../capabilities/runtimes-and-pools/warm-pools.md#pool-r23)). |

## Overview {#overview}

A pool is a record the gateway stores in Postgres. The operations on this page change that record; the controllers carry each change into Kubernetes afterwards, which is why most responses carry a sync status. An owned pool is written through the derived-runtime operations; on an owned pool the operations on this page are `platform-admin` paths only.

## Conventions {#conventions}

The [admin API conventions](index.md#conventions) apply. Every write on this page requires `platform-admin`. A `tenant-admin` may call the read operations and sees only pools its tenant has access to. <!-- src: DR-1 -->

## The pool object {#pool-object}

The request body of a create or a full update, and the `pool` member of a read, carry these fields. Each field's meaning, default, and range are owned by the page in the last column.

| Field | Type | On write | Owner of the meaning |
| :-- | :-- | :-- | :-- |
| `name` | String | Required on create; absent on update | [Warm pools, POOL-R2](../../capabilities/runtimes-and-pools/warm-pools.md#pool-r2) |
| `runtimeRef` | String | Required | [Runtime registry](../../capabilities/runtimes-and-pools/runtime-registry.md#runtime-name) |
| `isolationProfile` | Enum | Required | [Isolation profiles](../../capabilities/runtimes-and-pools/isolation-profiles.md) |
| `resourceClass` | String | Required | [Resource classes](../../capabilities/runtimes-and-pools/resource-classes.md) |
| `minWarm`, `maxWarm`, `scalePolicy`, `topologySpreadConstraints` | See owner | Optional | [Warm pools](../../capabilities/runtimes-and-pools/warm-pools.md#settings) |
| `sessionPolicy.onPoolExhausted`, `sessionPolicy.maxQueueWaitSeconds` | See owner | Optional | [Pod claims](../../capabilities/runtimes-and-pools/pod-claims.md#settings) |
| `sessionPolicy.maxConcurrentSessionsPerPod`, `sessionPolicy.recycle`, `terminationGracePeriodSeconds` | See owner | Optional | [Agent pod lifecycle](../../capabilities/runtimes-and-pools/agent-pod-lifecycle.md#settings) |
| `sessionPolicy.acknowledgeSharedPod` | Boolean | Required when `maxConcurrentSessionsPerPod` is above 1 | [Slots](../../capabilities/sessions/slot-assignment-and-failure.md#shared-pod) |
| `placement` | Object | Optional | [Placement](../../capabilities/runtimes-and-pools/placement.md) |
| `egressProfile`, `dnsPolicy` | Enums | Optional | [Egress](../../capabilities/network-and-egress/egress-policy.md) |

<!-- unresolved: §15.1 defines no pool request schema; the field list is assembled from §5.2, the `PodSet` spec, and the designs. The session-lifecycle, checkpoint, cleanup, and volume-size fields are owned by pages outside this slice and are not listed. -->

## Operations {#operations}

### Create a pool {#pools-create}

`POST /v1/admin/pools` · operation `admin-api#pools.create` · `platform-admin` · accepts `?dryRun=true`

**Request.** The [pool object](#pool-object).

**Response.** `201` with the pool, `syncStatus: "pending"`, and an `ETag`. A pool is usable by no tenant until access is granted ([`pools.grantTenantAccess`](#pools-granttenantaccess)).

**Errors.**

| Status and code | When | Behind it |
| :-- | :-- | :-- |
| `400 VALIDATION_ERROR`, `details.field: "name"` | The name does not match the pool-name pattern; the message states the pattern. | [POOL-R2](../../capabilities/runtimes-and-pools/warm-pools.md#pool-r2) |
| `400 VALIDATION_ERROR`, `details.field: "name"` | The name contains `--`. <!-- unresolved: DR "Names" reserves `--` without naming the code. --> | [POOL-R2](../../capabilities/runtimes-and-pools/warm-pools.md#pool-r2) |
| `400 VALIDATION_ERROR`, `details.field` naming the member | The body names a removed field, such as `executionMode` or `maxConcurrentRequestsPerPod`. | [Removed fields](index.md#removed-fields) |
| `409 RESOURCE_ALREADY_EXISTS` | A pool of that name exists or existed. | [POOL-R2](../../capabilities/runtimes-and-pools/warm-pools.md#pool-r2) |
| `422 INVALID_POOL_CONFIGURATION`, `details.message` | A configuration constraint fails, such as `maxConcurrentSessionsPerPod` above 1 without `acknowledgeSharedPod: true`, whose message lists what the slots of a shared pod still share. | The page that owns the constraint |
| `404 RESOURCE_NOT_FOUND` | `runtimeRef` names no runtime. <!-- unresolved: §15.1's dry-run table says the create "checks runtime reference" without naming the code. --> | [Runtime registry](../../capabilities/runtimes-and-pools/runtime-registry.md) |
| `403 FORBIDDEN` | The caller's role is not `platform-admin`. | [Tenant-derived runtimes, shared-record boundary](../../capabilities/runtimes-and-pools/tenant-derived-runtimes.md#shared-record-boundary) |

### List pools {#pools-list}

`GET /v1/admin/pools` · operation `admin-api#pools.list` · `platform-admin`, `tenant-admin`

**Response.** `200` with a paginated list of pool objects. A `platform-admin` sees every pool; a `tenant-admin` sees the pools its tenant has an access entry for and its tenant's owned pools.

### Get a pool {#pools-get}

`GET /v1/admin/pools/{name}` · operation `admin-api#pools.get` · `platform-admin`, `tenant-admin`

**Response.** `200` with an `ETag` and:

| Field | Meaning |
| :-- | :-- |
| `pool` | The pool object. |
| `syncStatus` | `pending` or `synced` ([POOL-R23](../../capabilities/runtimes-and-pools/warm-pools.md#pool-r23)). |
| `phase` | `active` or `draining`. |
| `activeSessions` | Sessions on the pool's pods, while draining. |
| `poolCondition` | `PoolWarmingUp` while the pool warms ([POOL-R18](../../capabilities/runtimes-and-pools/warm-pools.md#pool-r18)). |
| `idlePodCount`, `warmCount`, `availableCount` | The pool's unpinned idle pods, its warm inventory, and its claimable pods, summed over its pod sets. <!-- unresolved: §15.1 and §5.2 use `idlePodCount` and `availableCount` without defining their difference. --> |

**Errors.** `404 RESOURCE_NOT_FOUND` when the pool does not exist or the caller's tenant has no access to it.

### Update a pool {#pools-update}

`PUT /v1/admin/pools/{name}` · operation `admin-api#pools.update` · `platform-admin` · requires `If-Match` · accepts `?dryRun=true`

**Request.** The full [pool object](#pool-object) without `name`. A pool is never renamed.

**Response.** `200` with the pool, `syncStatus: "pending"`, and a new `ETag`. The change reaches pods created afterwards ([POOL-R1](../../capabilities/runtimes-and-pools/warm-pools.md#pool-r1)).

**Errors.** The create's validation errors, `428 ETAG_REQUIRED`, `412 ETAG_MISMATCH`, `404 RESOURCE_NOT_FOUND`, and, on an owned pool, a refusal of every field the owned pool takes from its template. <!-- unresolved: DR-5 "Tenant operations" states the refusal without its code. -->

### Delete a pool {#pools-delete}

`DELETE /v1/admin/pools/{name}` · operation `admin-api#pools.delete` · `platform-admin`

**Response.** `204`. The pool's name stays taken.

**Errors.** `409 RESOURCE_HAS_DEPENDENTS` with `details.dependents` while any session in the pool is running or suspended ([POOL-R26](../../capabilities/runtimes-and-pools/warm-pools.md#pool-r26)); drain the pool first. `404 RESOURCE_NOT_FOUND`.

### Drain a pool {#pools-drain}

`POST /v1/admin/pools/{name}/drain` · operation `admin-api#pools.drain` · `platform-admin`

**Response.** `200` with `{"status": "draining", "activeSessions": <n>, "estimatedDrainSeconds": <n>}`. While the pool drains, a session create that would select it answers `503 POOL_DRAINING` with `Retry-After` ([POOL-R25](../../capabilities/runtimes-and-pools/warm-pools.md#pool-r25); [`client-api#sessions.create`](../client-api/sessions.md#sessions-create)). A tenant drains its owned pool through the derived-runtime drain operation.

### Set warm counts {#pools-setwarmcount}

`PUT /v1/admin/pools/{name}/warm-count` · operation `admin-api#pools.setWarmCount` · `platform-admin` · requires `If-Match` · scope `tools:pool:scale`

**Request.** `{"minWarm": <int>, "maxWarm": <int>, "confirm": <bool>}`.

**Response.** `200` with the pool. A change of more than half the current `minWarm` without `"confirm": true` answers `200` with a preview and writes nothing ([Dry run and confirmation](index.md#dry-run)).

**Errors.** `400 VALIDATION_ERROR` when `minWarm` exceeds `maxWarm` or `maxWarm` is not positive; `428`, `412`, and `404` as for an update.

### Remove the bootstrap override {#pools-deletebootstrapoverride}

`DELETE /v1/admin/pools/{name}/bootstrap-override` · operation `admin-api#pools.deleteBootstrapOverride` · `platform-admin`

**Response.** `204`. The pool leaves bootstrap mode and its `minWarm` follows the scaling formula ([POOL-R12](../../capabilities/runtimes-and-pools/warm-pools.md#pool-r12)).

### Get the sync status {#pools-syncstatus}

`GET /v1/admin/pools/{name}/sync-status` · operation `admin-api#pools.syncStatus` · `platform-admin`, `tenant-admin`

**Response.** `200` with `postgresGeneration`, `crdGeneration`, `lastReconciledAt`, `lagSeconds`, and `inSync` (boolean) ([POOL-R23](../../capabilities/runtimes-and-pools/warm-pools.md#pool-r23)). <!-- unresolved: with several `PodSet` objects per pool, `crdGeneration` is taken to be the lowest generation among them. -->

### Resume reconciliation {#pools-resumereconciliation}

`POST /v1/admin/pools/{name}/resume-reconciliation` · operation `admin-api#pools.resumeReconciliation` · `platform-admin`

**Response.** `200` with `{"pool": "<name>", "denialCounterCleared": <n>}`, and the audit event `pool.reconciliation_resumed`. The PoolScalingController's next render retries the pool ([POOL-R24](../../capabilities/runtimes-and-pools/warm-pools.md#pool-r24)).

**Errors.** `404 RESOURCE_NOT_FOUND` when the pool has no stopped state to clear.

### Grant, list, and revoke tenant access {#pools-granttenantaccess}

| Operation | Path | Behavior |
| :-- | :-- | :-- |
| `admin-api#pools.grantTenantAccess` | `POST /v1/admin/pools/{name}/tenant-access` | Body `{"tenantId"}`. Idempotent; `200` whether or not the grant existed. |
| `admin-api#pools.listTenantAccess` | `GET /v1/admin/pools/{name}/tenant-access` | `200` with `[{"tenantId", "tenantName", "grantedAt", "grantedBy"}]`. |
| `admin-api#pools.revokeTenantAccess` | `DELETE /v1/admin/pools/{name}/tenant-access/{tenantId}` | `204`; `404 RESOURCE_NOT_FOUND` when no grant exists. |

All three require `platform-admin`. An access entry naming an owned pool is refused ([Tenant-derived runtimes](../../capabilities/runtimes-and-pools/tenant-derived-runtimes.md#tenant-operations)). <!-- unresolved: the refusal's code is not stated. -->

## Removed operations {#removed}

`PUT /v1/admin/pools/{name}/circuit-breaker` leaves with pre-launched runtimes, and the `/v1/admin/pools/{name}/upgrade/*` operations and `upgrade-status` leave for the runtime rollout operations ([Runtime revisions and rollouts](../../capabilities/runtimes-and-pools/runtime-revisions.md#rollout)). A request to a removed path answers `404`.

## Compatibility {#compatibility}

The admin API is unversioned within `/v1`. <!-- unresolved: the admin API's compatibility policy is owned outside this slice. -->
