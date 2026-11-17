---
code: ADMIN
type: interface
summary: >-
  The admin API is the REST API under /v1/admin through which operators and tenant administrators
  configure Lenny. This index states the conventions its operation pages rely on and lists the
  operation pages.
owns: [conventions]
depends-on: [ERR]
---

# Admin API

The admin API is the REST API under `/v1/admin` through which operators and tenant administrators configure Lenny. This index states the conventions its operation pages rely on and lists the operation pages.

<!-- stub: written by the slice with only the conventions `pools.md` needs. -->

## Conventions {#conventions}

### Authentication and authorization {#auth}

- Every request carries a bearer token. The token's role decides which operations a caller may call: `platform-admin` may call every operation, and `tenant-admin` may call the operations an operation page grants it, on its own tenant's records. <!-- unresolved: the authentication schemes and the role model are owned by pages outside this slice. -->
- A token may carry a `scope` claim of `tools:<domain>:<action>` values. When it does, an operation whose page names a scope is refused unless the claim grants it, with `403 SCOPE_FORBIDDEN`.
- A record the caller may not see answers as absent, with `404 RESOURCE_NOT_FOUND`, so the response reveals nothing about records of other tenants.

### Errors {#errors}

Every error answers with the shared envelope `{"error": {"code", "category", "message", "retryable", "details"}}` and the codes of the [error model](../../cross-cutting/error-model.md). An operation page lists the codes the operation returns and the condition behind each.

### Concurrency {#concurrency}

- A `GET` of a record returns an `ETag`.
- A `PUT` requires `If-Match`. A `PUT` without it answers `428 ETAG_REQUIRED`, and a `PUT` whose `If-Match` is stale answers `412 ETAG_MISMATCH` with `details.currentEtag`.

### Dry run and confirmation {#dry-run}

- A create or update that accepts `?dryRun=true` runs every check and writes nothing. It answers as the operation would, with `"dryRun": true`.
- An operation page that marks a change as needing confirmation answers a request without `"confirm": true` with `200` and a preview of the change, and writes nothing.

### Removed fields {#removed-fields}

A write that names a field Lenny has removed answers `400 VALIDATION_ERROR` with `details.field` naming the member, whatever its value.

### Pagination {#pagination}

Every collection `GET` returns a cursor-paginated envelope. <!-- unresolved: the envelope's fields are owned by the client API conventions page. -->

### Audit {#audit}

Every write emits an audit event naming the caller, the operation, and the record. An operation page names any event specific to it.

## Operation pages {#pages}

- [Pools](pools.md)
- Runtimes
- Derived runtimes
- Bootstrap
- Tenants
- Quarantines
- Diagnostics (served by the ops API)
