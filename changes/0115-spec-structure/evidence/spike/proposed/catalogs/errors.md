---
code: CAT-ERRORS
type: catalog
summary: >-
  Every error code and admission refusal with the interface that returns it and its owner page.
  Generated from the error definition blocks on owner pages; this slice's rows only.
---

<!-- GENERATED from `kind: error` and `kind: condition-reason` definition blocks. Do not edit by hand. In the spike this file was written by hand to show what the generator would produce, and holds the warm pools and pod lifecycle slice's rows only. Rows marked "outside the slice" point at owners this slice did not write; the generator would read their blocks there. -->

# Errors catalog

## Defined on the slice's pages

| Code or refusal | Interface | Status | Owner |
| :-- | :-- | :-- | :-- |
| PodClaim concurrent claim refusal, `"PodClaim already exists for AgentPod <name>; concurrent claim rejected"` | Kubernetes | 403 | [`PodClaim` admission](../interfaces/kubernetes/podclaim.md#admission) |
| `tenant_label_immutable` | Kubernetes | 403 | [`lenny.dev/tenant-id`](../interfaces/kubernetes/labels-and-annotations.md#lenny-dev-tenant-id) |
| `PodSetKeyCollision` (condition reason on `Degraded`) | Kubernetes | Not an HTTP error | [`PodSet` conditions](../interfaces/kubernetes/podset.md#conditions) |
| `PODSET_POOL_LABEL_INVALID`, `PODSET_POD_SET_LABEL_INVALID`, `PODSET_NAME_MISMATCH`, `PODSET_LABEL_IMMUTABLE` (proposed) | Kubernetes | 403 | [`PodSet` admission](../interfaces/kubernetes/podset.md#admission) |
| `AGENTPOD_LABEL_MISMATCH`, `LABEL_IMMUTABLE`, `QUARANTINE_DELETE_DENIED` (proposed) | Kubernetes | 403 | [`AgentPod` admission](../interfaces/kubernetes/agentpod.md#admission) |

## Returned by the slice's operations, defined elsewhere

| Code | Status | Returned by | Owner |
| :-- | :-- | :-- | :-- |
| `VALIDATION_ERROR` | 400 | [`pools.create`](../interfaces/admin-api/pools.md#pools-create), [`pools.update`](../interfaces/admin-api/pools.md#pools-update), [`pools.setWarmCount`](../interfaces/admin-api/pools.md#pools-setwarmcount) | [Error model](../cross-cutting/error-model.md#validation_error) (outside the slice) |
| `RESOURCE_ALREADY_EXISTS` | 409 | [`pools.create`](../interfaces/admin-api/pools.md#pools-create) | [Error model](../cross-cutting/error-model.md#resource_already_exists) (outside the slice) |
| `RESOURCE_NOT_FOUND` | 404 | Every pool operation | [Error model](../cross-cutting/error-model.md#resource_not_found) (outside the slice) |
| `RESOURCE_HAS_DEPENDENTS` | 409 | [`pools.delete`](../interfaces/admin-api/pools.md#pools-delete) | [Error model](../cross-cutting/error-model.md#resource_has_dependents) (outside the slice) |
| `INVALID_POOL_CONFIGURATION` | 422 | [`pools.create`](../interfaces/admin-api/pools.md#pools-create), [`pools.update`](../interfaces/admin-api/pools.md#pools-update) | [Error model](../cross-cutting/error-model.md#invalid_pool_configuration) (outside the slice) |
| `ETAG_REQUIRED`, `ETAG_MISMATCH` | 428, 412 | Every pool `PUT` | [Error model](../cross-cutting/error-model.md#etag_required) (outside the slice) |
| `FORBIDDEN`, `SCOPE_FORBIDDEN` | 403 | Every pool write | [Error model](../cross-cutting/error-model.md#forbidden) (outside the slice) |
| `WARM_POOL_EXHAUSTED` | 503 | [`client-api#sessions.create`](../interfaces/client-api/sessions.md#sessions-create), under [CLAIM-R12](../capabilities/runtimes-and-pools/pod-claims.md#claim-r12) | [Client API sessions](../interfaces/client-api/sessions.md#errors) (outside the slice) |
| `RUNTIME_UNAVAILABLE` (pool warming) | 503 | [`client-api#sessions.create`](../interfaces/client-api/sessions.md#sessions-create), under [POOL-R18](../capabilities/runtimes-and-pools/warm-pools.md#pool-r18) | [Error model](../cross-cutting/error-model.md#runtime_unavailable) (outside the slice) |
| `POOL_DRAINING` | 503 | [`client-api#sessions.create`](../interfaces/client-api/sessions.md#sessions-create), under [POOL-R25](../capabilities/runtimes-and-pools/warm-pools.md#pool-r25) | [Client API sessions](../interfaces/client-api/sessions.md#errors) (outside the slice) |
| `SESSION_CREATION_FAILED`, `STARTING_FAILED` (failed pin read) | 503 | Session create and start, under [CLAIM-R6](../capabilities/runtimes-and-pools/pod-claims.md#claim-r6) | [Client API sessions](../interfaces/client-api/sessions.md#errors) (outside the slice) |
| `SEED_VALIDATION` (entry error) | 207 | Bootstrap seed pool entries, under [POOL-R2](../capabilities/runtimes-and-pools/warm-pools.md#pool-r2) | [Admin API bootstrap](../interfaces/deployment-configuration/bootstrap-seed.md#errors) (outside the slice) |
