# Idempotency keys scoped by principal

Status: `Decided`, revision 3, 2026-11-26.

## Problem

The gateway keys each stored idempotent response by tenant and key alone, so a second user of the same tenant who sends the same key with the same body receives the first user's stored response. That response can hold the first user's session id and, since change [0173](../archive/2026-11-25-0173-idempotency-stored-credentials/change.md), an upload token that the replay rebuilds from its redaction reference.

[§11.5](../../spec/11_policy-and-controls.md#115-idempotency) states the scope: the table `idempotency_keys` is "keyed by `(tenant_id, idempotency_key)`", and keys "are scoped per tenant". The code follows it:

- `migrations/0005_idempotency_keys.up.sql`: the primary key is `(tenant_id, idempotency_key)`.
- `pkg/idempotency/idempotencymw/idempotency.go`: the `Store` interface's `Get`, `Claim`, and `Release` take `(tenantID, key)`, and the middleware resolves the tenant through `Options.TenantFromRequest`, which reads the `X-Lenny-Tenant-ID` header that the auth middleware sets.
- `pkg/idempotency/idempotency.go`: `idempotency.Key` holds `TenantID` and `Value`.
- `pkg/gateway/platformtools/mcp/idempotency.go`: the MCP hook resolves the tenant through `IdempotencyConfig.TenantFromRequest`, which `cmd/lenny-gateway/httpsurface.go` wires to the same header, and claims under the tool-namespaced key `mcp/<tool>/<key>`.
- `pkg/idempotency/idempotencymw/credentials.go`: the 0173 wrapper passes `(tenantID, key)` through.

The body hash guards against a different request with the same key (`422 IDEMPOTENCY_KEY_REUSED`), and it does not guard against the same request from a different user. A request body for `POST /v1/sessions` names a runtime and options that two users of one tenant can send identically, and a client library that derives keys deterministically makes a collision likely. The review of 0173 found the gap.

The owner settled the remedy as decision 1: keys are scoped by principal, the tenant together with the user, through a small change to §11.5 and the store. The owner then settled that a bearer bound to a session narrows the scope to that session (decision 3). [APICONV-I1](../../issues/APICONV/APICONV-I1-idempotency-key-scoped-per-tenant-only.md) records the gap.

User erasure does not delete the user's idempotency records ([ERASE-I2](../../issues/ERASE/ERASE-I2-user-erasure-leaves-idempotency-records.md)). The [§12.8](../../spec/12_storage-architecture.md#128-compliance-interfaces) table **Storage backends in erasure scope:** has no row for them, `canonicalRank` in `pkg/storage/erasure/order.go` names no idempotency store, and the records hold stored responses for 24 hours, which [§12.9](../../spec/12_storage-architecture.md#129-data-classification) classifies as T3, Confidential. A record names no user today, so it cannot be found by user. The subject this change adds makes the deletion possible, and the owner directed that it be added as the next step (decision 2).

## Scope

The change covers the idempotency records of the §11.5 critical operations on the REST `Idempotency-Key` path and the MCP `idempotencyKey` path: the store's key, the table's primary key, the principal the middleware and the MCP hook resolve, and §11.5. It also covers the deletion of a user's records by user erasure, in §12.8 and in the erasure job.

It leaves out:

- The `lenny-ops` idempotency records of [§25.4](../../spec/25_agent-operability.md#254-the-lenny-ops-service) (`ops_idempotency_keys`, `pkg/ops/opsidem`).
- The rest of the scope that R5 of [0138](../0138-rest-only-client-api/change.md) states: the effective audience, the token's `azp` or `client_id`, the method, the route template, and the path parameters. Step 0138.S1 adds them to the key that this change lands.
- The metrics of the middleware, which keep the tenant as their only identity label.

## Terms

| Term | Meaning |
| :-- | :-- |
| Idempotency record | A row of `idempotency_keys`, or an entry of the in-memory store: the scope, the key, the request hash, and the stored response ([§11.5](../../spec/11_policy-and-controls.md#115-idempotency)). |
| Principal | The authenticated caller the auth middleware attaches to the request (`authmw.Principal`): its `TenantID`, its `Subject` (the bearer's `sub` claim, or `X-Lenny-User-ID` on the development header path), and, for a bearer bound to a session, its `SessionID`. |
| Session owner | The user a new session belongs to: the principal's subject, under [§10.2](../../spec/10_gateway-internals.md#102-authentication) **Session ownership.** rule 1 (`ownerFromPrincipal` in `pkg/gateway/core/session/sessionserver/request_identity.go`). |
| Record scope | The values that, with the key, identify an idempotency record. Today the tenant; after this change the tenant, the subject, and the principal's session id, which is empty for a bearer not bound to a session (decision 3). |

## High-level design

The gateway files each idempotency record under the caller's tenant and the caller's subject, the same subject that becomes the owner of a session the request creates. When the caller's bearer is bound to a session, such as a session-capability bearer or the bearer of a delegated child, the record is also filed under that session, so one session's stored result does not reach another session of the same user. A retry by the same user, from the same session when the bearer is bound to one, finds the record. The same key from another user of the tenant finds nothing and runs the operation. A request that carries a key and no subject is refused rather than filed under an empty subject.

## Requirements

None by identifier. §11.5 is content of the placeholder page APICONV.

## Current state

As "Problem" states, on `main` at `6bc1c2859`. The auth middleware wraps the idempotency middleware (`cmd/lenny-gateway/httpsurface.go`: `idempotencymw.Wrap` is applied before `authmw.Wrap`, so it runs inside it), so the principal is in the request context when the idempotency middleware and the MCP hook run. The session handlers already refuse a create from a principal with no subject with `401 UNAUTHORIZED`, reason `principal_subject_required` (`writeNoPrincipalSubject`).

## Detailed design

### IP-1 The record scope

`idempotency.Key` gains `Subject` and `SessionID`, and `Validate` refuses an empty subject as it refuses an empty tenant. `SessionID` is the principal's `SessionID` when the bearer carries one, and empty for an ordinary user token (decision 3). The `Store` methods `Get`, `Claim`, and `Release` take an `idempotency.Key` in place of `(tenantID, key)`, and `Put` reads the key from the record as it does today. `MemoryStore`, the Postgres store, and the 0173 credential wrapper follow. The subject is the principal's `Subject`, the value `ownerFromPrincipal` returns, so the scope of a create record is the owner of the session it creates.

### IP-2 Resolving the principal

`idempotencymw.Options.TenantFromRequest` is replaced by `PrincipalFromRequest func(*http.Request) (tenantID, subject, sessionID string)`, and `mcp.IdempotencyConfig.TenantFromRequest` by the same field. `cmd/lenny-gateway` wires both to one function that reads `authmw.FromContext`, so the REST and MCP paths resolve one scope, and the MCP package receives the values without importing the auth middleware. The resolver has no default that reads a header. A request with an idempotency key whose principal has an empty tenant is refused as today. A request with an idempotency key whose principal has an empty subject is refused with `401 UNAUTHORIZED`, reason `principal_subject_required`, the envelope the session surfaces already write, before any claim.

### IP-3 The table

A new migration deletes every row of `idempotency_keys`, adds `subject TEXT NOT NULL` and `session_id TEXT NOT NULL DEFAULT ''`, and replaces the primary key with `(tenant_id, subject, session_id, idempotency_key)`. The session column holds the empty string for an ordinary user token, because a primary-key column cannot be NULL (decision 3). The rows are a 24-hour cache and no deployment exists, so the migration discards them rather than backfilling a subject, and it keeps no compatibility path for the old key. The tenant guard trigger, the row-level security policy, and the `(tenant_id, stored_at)` index of migration 0082 are unchanged. The Postgres store's queries name the subject and the session id.

### IP-4 The specification

§11.5 **Mechanism.** states that the table is keyed by `(tenant_id, subject, session_id, idempotency_key)`, that the subject is the authenticated principal's subject, which is the session owner of [§10.2](../../spec/10_gateway-internals.md#102-authentication), that the session id is the session a bearer is bound to and empty for a bearer bound to none, and that the same key string from different principals, or from different sessions of one principal, is treated independently, within a tenant and across tenants. It states the refusal of a key with no subject.

### IP-5 Erasure of a user's records

§12.8 **Storage backends in erasure scope:** gains a row for idempotency records in Postgres, whose erased data is the user's records, found by `(tenant_id, subject)` with the subject equal to the erased user's id, whatever their session id. The **`DeleteByUser` dependency-ordered deletion sequence** gains a step that deletes them. The records hold no foreign key to any other store and no store refers to them, so the step's position is free; it runs before `SessionStore`, with the other stores that hold data about the user's sessions.

The Postgres store gains `DeleteByUser(ctx, tenantID, subject)`, and `MemoryStore` the same method. The erasure wiring in `cmd/lenny-gateway` registers an eraser under a new `canonicalRank` name in `pkg/storage/erasure/order.go`. The validators refuse a name that a path neither wires nor exempts, so tenant deletion Phase 4 wires the same store to delete the tenant's records by `tenant_id`, which §12.8 Phase 4 states in the same edit. The 24-hour garbage-collection sweep stays, and the eraser removes a user's records before the sweep reaches them.

The §12.9 row for idempotency records is unchanged: the records stay T3, and the erasure deadline of T3 data applies to them.

## Worked example

`alice` and `bob`, two users of tenant `acme`, each send `POST /v1/sessions` with `Idempotency-Key: k1` and the same body. Today `bob` receives the 201 response stored for `alice`, with alice's session id and her upload token. After this change, their user tokens are bound to no session. `alice`'s request is filed under `(acme, alice, "", k1)` and `bob`'s under `(acme, bob, "", k1)`, so `bob`'s request creates his own session. When `alice` retries with `k1`, she receives her stored response. `alice`'s session `s1`, whose bearer is bound to `s1`, calls `lenny/delegate_task` with `idempotencyKey: "child-1"`, and the record is filed under `(acme, alice, s1, child-1)`. The same call with the same key and arguments from her session `s2` finds nothing and creates its own child. An hour later a tenant admin erases `alice`. The erasure job deletes every record filed under `(acme, alice, ...)`, including `k1` and `child-1`, and leaves `bob`'s record in place.

## Limits, and alternatives rejected

- The scope follows the bearer's subject and, for a bearer bound to a session, that session. Two bearers with the same subject and the same session, or two bearers with the same subject that are bound to no session, share records, whatever else differs between them.
- Option (b) of decision 3, keeping the scope at the tenant and the subject for a bearer bound to a session, was not taken.
- Option (b) of decision 1, keeping per-tenant scoping and documenting it, was not taken.

## Specification and non-specification changes

- `spec/11_policy-and-controls.md`: §11.5, **Mechanism.**
- `spec/12_storage-architecture.md`: §12.8, **Storage backends in erasure scope:**, the **`DeleteByUser` dependency-ordered deletion sequence**, and tenant deletion Phase 4 (IP-5).
- `migrations/`: a new migration for `idempotency_keys`.
- `pkg/idempotency`: `Key.Subject` and its validation.
- `pkg/idempotency/idempotencymw` and `pkg/idempotency/idempotencymw/pgstore`: the store interface, the stores, the credential wrapper, and the resolver.
- `pkg/gateway/platformtools/mcp`: `IdempotencyConfig` and `dispatchIdempotent`.
- `pkg/storage/erasure`: the new store name in `canonicalRank`.
- `cmd/lenny-gateway`: the resolver wiring in `httpsurface.go`, and the user and tenant erasers in the erasure wiring.

## Documentation

- `docs/api/rest.md` (client developer): the idempotency section states that a key is scoped to the calling user within the tenant, and that a key sent with no authenticated subject is refused.
- `docs/api/mcp.md` (client developer): the same for `idempotencyKey`.
- `docs/operator-guide/security.md` (operator): the GDPR erasure section states that user erasure deletes the user's idempotency records.

## Tests

- Unit, `pkg/idempotency`: a key with an empty subject fails validation. Cites §11.5 (Idempotency).
- Unit, `pkg/idempotency/idempotencymw`: two subjects of one tenant with the same key and body each run the operation and each replay their own response; the same subject replays; two bearers of one subject bound to different sessions each run the operation, and a bearer bound to no session does not share a record with either; a request with a key and no subject is refused with `401` before any claim; the credential wrapper passes the scope through. Cites §11.5 (Idempotency).
- Unit, `pkg/gateway/platformtools/mcp`: the MCP hook claims under the principal's scope, two subjects with the same `idempotencyKey` do not share a result, and two sessions of one subject with the same `idempotencyKey` on `lenny/delegate_task` do not share a child handle. Cites §11.5 (Idempotency).
- Component, `tests/component/stores`: the Postgres store keys rows by tenant, subject, session id, and key, and the migration leaves the table empty with the new primary key and the empty-string default for the session id. Cites §11.5 (Idempotency).
- Contract, `tests/contract/rest_idempotency`: `bob`'s `POST /v1/sessions` with `alice`'s key and body creates a second session. Cites §11.5 (Idempotency).
- Load_local: concurrent claims of one key by two subjects both succeed, and concurrent claims by one subject collapse to one execution, under `-race`. Cites §11.5 (Idempotency).
- Security, `tests/security`: a user of the same tenant who replays another user's key and body receives neither that user's session id nor an upload token. Cites §11.5 (Idempotency).
- Unit, `pkg/idempotency/idempotencymw` and its Postgres store: `DeleteByUser` removes every record of one subject in one tenant, under every session id, and leaves the records of other subjects and other tenants. Cites §12.8 (Compliance Interfaces).
- Unit, `pkg/storage/erasure`: the user and tenant erasure orders both wire the idempotency store, and the order validators accept them. Cites §12.8 (Compliance Interfaces).
- Integration, `tests/integration`: a real user erasure on the compose stack, after the erased user and another user of the tenant each stored a response, leaves no record of the erased user and keeps the other user's. The erasure completeness check, `TestErasureDeletesTheUsersRowsInEveryWiredStore_spec_12_8`, covers the new store. Cites §12.8 (Compliance Interfaces).
