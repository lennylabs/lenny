# Idempotency keys scoped by principal

Status: `Draft`, revision 1, 2026-11-25.

## Problem

The gateway keys each stored idempotent response by tenant and key alone, so a second user of the same tenant who sends the same key with the same body receives the first user's stored response. That response can hold the first user's session id and, since change [0173](../archive/2026-11-25-0173-idempotency-stored-credentials/change.md), an upload token that the replay rebuilds from its redaction reference.

[§11.5](../../spec/11_policy-and-controls.md#115-idempotency) states the scope: the table `idempotency_keys` is "keyed by `(tenant_id, idempotency_key)`", and keys "are scoped per tenant". The code follows it:

- `migrations/0005_idempotency_keys.up.sql`: the primary key is `(tenant_id, idempotency_key)`.
- `pkg/idempotency/idempotencymw/idempotency.go`: the `Store` interface's `Get`, `Claim`, and `Release` take `(tenantID, key)`, and the middleware resolves the tenant through `Options.TenantFromRequest`, which reads the `X-Lenny-Tenant-ID` header that the auth middleware sets.
- `pkg/idempotency/idempotency.go`: `idempotency.Key` holds `TenantID` and `Value`.
- `pkg/gateway/platformtools/mcp/idempotency.go`: the MCP hook resolves the tenant through `IdempotencyConfig.TenantFromRequest`, which `cmd/lenny-gateway/httpsurface.go` wires to the same header, and claims under the tool-namespaced key `mcp/<tool>/<key>`.
- `pkg/idempotency/idempotencymw/credentials.go`: the 0173 wrapper passes `(tenantID, key)` through.

The body hash guards against a different request with the same key (`422 IDEMPOTENCY_KEY_REUSED`), and it does not guard against the same request from a different user. A request body for `POST /v1/sessions` names a runtime and options that two users of one tenant can send identically, and a client library that derives keys deterministically makes a collision likely. The review of 0173 found the gap.

The owner settled the remedy as decision 1: keys are scoped by principal, the tenant together with the user, through a small change to §11.5 and the store.

## Scope

The change covers the idempotency records of the §11.5 critical operations on the REST `Idempotency-Key` path and the MCP `idempotencyKey` path: the store's key, the table's primary key, the principal the middleware and the MCP hook resolve, and §11.5.

It leaves out:

- The `lenny-ops` idempotency records of [§25.4](../../spec/25_agent-operability.md#254-the-lenny-ops-service) (`ops_idempotency_keys`, `pkg/ops/opsidem`).
- Deleting a user's idempotency records during user erasure. The subject column makes a `DeleteByUser` possible, and the records expire after 24 hours today; adding them to the §12.8 deletion sequence is a separate question.
- The metrics of the middleware, which keep the tenant as their only identity label.

## Terms

| Term | Meaning |
| :-- | :-- |
| Idempotency record | A row of `idempotency_keys`, or an entry of the in-memory store: the scope, the key, the request hash, and the stored response ([§11.5](../../spec/11_policy-and-controls.md#115-idempotency)). |
| Principal | The authenticated caller the auth middleware attaches to the request (`authmw.Principal`): its `TenantID`, its `Subject` (the bearer's `sub` claim, or `X-Lenny-User-ID` on the development header path), and, for a bearer bound to a session, its `SessionID`. |
| Session owner | The user a new session belongs to: the principal's subject, under [§10.2](../../spec/10_gateway-internals.md#102-authentication) **Session ownership.** rule 1 (`ownerFromPrincipal` in `pkg/gateway/core/session/sessionserver/request_identity.go`). |
| Record scope | The values that, with the key, identify an idempotency record. Today the tenant; after this change the tenant and the subject, and the session under open decision O1. |

## High-level design

The gateway files each idempotency record under the caller's tenant and the caller's subject, the same subject that becomes the owner of a session the request creates. A retry by the same user finds the record. The same key from another user of the tenant finds nothing and runs the operation. A request that carries a key and no subject is refused rather than filed under an empty subject.

## Requirements

None by identifier. §11.5 is content of the placeholder page APICONV.

## Current state

As "Problem" states, on `main` at `6bc1c2859`. The auth middleware wraps the idempotency middleware (`cmd/lenny-gateway/httpsurface.go`: `idempotencymw.Wrap` is applied before `authmw.Wrap`, so it runs inside it), so the principal is in the request context when the idempotency middleware and the MCP hook run. The session handlers already refuse a create from a principal with no subject with `401 UNAUTHORIZED`, reason `principal_subject_required` (`writeNoPrincipalSubject`).

## Detailed design

### IP-1 The record scope

`idempotency.Key` gains `Subject`, and `Validate` refuses an empty subject as it refuses an empty tenant. The `Store` methods `Get`, `Claim`, and `Release` take an `idempotency.Key` in place of `(tenantID, key)`, and `Put` reads the key from the record as it does today. `MemoryStore`, the Postgres store, and the 0173 credential wrapper follow. The subject is the principal's `Subject`, the value `ownerFromPrincipal` returns, so the scope of a create record is the owner of the session it creates.

### IP-2 Resolving the principal

`idempotencymw.Options.TenantFromRequest` is replaced by `PrincipalFromRequest func(*http.Request) (tenantID, subject string)`, and `mcp.IdempotencyConfig.TenantFromRequest` by the same field. `cmd/lenny-gateway` wires both to one function that reads `authmw.FromContext`, so the REST and MCP paths resolve one scope, and the MCP package receives the values without importing the auth middleware. The resolver has no default that reads a header. A request with an idempotency key whose principal has an empty tenant is refused as today. A request with an idempotency key whose principal has an empty subject is refused with `401 UNAUTHORIZED`, reason `principal_subject_required`, the envelope the session surfaces already write, before any claim.

### IP-3 The table

A new migration deletes every row of `idempotency_keys`, adds `subject TEXT NOT NULL`, and replaces the primary key with `(tenant_id, subject, idempotency_key)`. The rows are a 24-hour cache and no deployment exists, so the migration discards them rather than backfilling a subject, and it keeps no compatibility path for the old key. The tenant guard trigger, the row-level security policy, and the `(tenant_id, stored_at)` index of migration 0082 are unchanged. The Postgres store's queries name the subject.

### IP-4 The specification

§11.5 **Mechanism.** states that the table is keyed by `(tenant_id, subject, idempotency_key)`, that the subject is the authenticated principal's subject, which is the session owner of [§10.2](../../spec/10_gateway-internals.md#102-authentication), and that the same key string from different principals is treated independently, within a tenant and across tenants. It states the refusal of a key with no subject.

## Worked example

`alice` and `bob`, two users of tenant `acme`, each send `POST /v1/sessions` with `Idempotency-Key: k1` and the same body. Today `bob` receives the 201 response stored for `alice`, with her session id and her upload token. After this change, `alice`'s request is filed under `(acme, alice, k1)` and `bob`'s under `(acme, bob, k1)`, so `bob`'s request creates his own session. When `alice` retries with `k1`, she receives her stored response.

## Limits, and alternatives rejected

- The scope follows the bearer's subject, so two bearers with the same subject share records, whatever else differs between them. Open decision O1 asks whether a bearer bound to a session narrows the scope to that session.
- Option (b) of decision 1, keeping per-tenant scoping and documenting it, was not taken.

## Specification and non-specification changes

- `spec/11_policy-and-controls.md`: §11.5, **Mechanism.**
- `migrations/`: a new migration for `idempotency_keys`.
- `pkg/idempotency`: `Key.Subject` and its validation.
- `pkg/idempotency/idempotencymw` and `pkg/idempotency/idempotencymw/pgstore`: the store interface, the stores, the credential wrapper, and the resolver.
- `pkg/gateway/platformtools/mcp`: `IdempotencyConfig` and `dispatchIdempotent`.
- `cmd/lenny-gateway`: the resolver wiring in `httpsurface.go`.

## Documentation

- `docs/api/rest.md` (client developer): the idempotency section states that a key is scoped to the calling user within the tenant, and that a key sent with no authenticated subject is refused.
- `docs/api/mcp.md` (client developer): the same for `idempotencyKey`.

## Tests

- Unit, `pkg/idempotency`: a key with an empty subject fails validation. Cites §11.5 (Idempotency).
- Unit, `pkg/idempotency/idempotencymw`: two subjects of one tenant with the same key and body each run the operation and each replay their own response; the same subject replays; a request with a key and no subject is refused with `401` before any claim; the credential wrapper passes the scope through. Cites §11.5 (Idempotency).
- Unit, `pkg/gateway/platformtools/mcp`: the MCP hook claims under the principal's scope, and two subjects with the same `idempotencyKey` do not share a result. Cites §11.5 (Idempotency).
- Component, `tests/component/stores`: the Postgres store keys rows by tenant, subject, and key, and the migration leaves the table empty with the new primary key. Cites §11.5 (Idempotency).
- Contract, `tests/contract/rest_idempotency`: `bob`'s `POST /v1/sessions` with `alice`'s key and body creates a second session. Cites §11.5 (Idempotency).
- Load_local: concurrent claims of one key by two subjects both succeed, and concurrent claims by one subject collapse to one execution, under `-race`. Cites §11.5 (Idempotency).
- Security, `tests/security`: a user of the same tenant who replays another user's key and body receives neither that user's session id nor an upload token. Cites §11.5 (Idempotency).
