# Session creation applies the create permission and the admission gates on every surface

Status: `In progress`, revision 1, 2026-11-19.

## Problem

A session can be created through four calls: REST `POST /v1/sessions` and `POST /v1/sessions/start`, the MCP tools `lenny/create_session` and `lenny/create_and_start_session`, and the implicit session of a built-in adapter request (the single-shot path). The specification requires each of them to run the same checks. Two gaps break that parity. Issue [CAPISES-I1](../../issues/closed/CAPISES/CAPISES-I1-in-process-create-skips-permission-check.md) records the first and issue [CAPISES-I2](../../issues/closed/CAPISES/CAPISES-I2-create-and-start-skips-drain-and-degraded-gates.md) the second.

**The create permission is skipped in process.** Evidence from the tree:

- `pkg/gateway/api/pipeline/access.go`: the API front's request pipeline mounts `POST /v1/sessions` and `POST /v1/sessions/start` behind `seam.AccessSessionCreate`, which runs the permission gate for `manage_own_sessions` through `GWSEAM-AuthorizePermission`. A `tenant-viewer` or `billing-viewer` principal receives `403 FORBIDDEN` on both REST routes.
- `pkg/gateway/core/session/sessionserver/service.go`: `CreateSessionService` and `CreateAndStartService` call `handleCreate` and `handleCreateAndStart` directly behind `guardTenant`, which applies the tenant check alone. `ToolsSeam.CreateSession` (`tools_seam.go`) serves the MCP `lenny/create_session` tool through `CreateSessionService`, and `sessionSingleShotBinder.BindSingleShot` (`cmd/lenny-gateway/credentialsurface.go`) serves the single-shot path through `CreateAndStartService`. A `tenant-viewer` or `billing-viewer` principal creates a session it owns through either.
- The MCP `lenny/create_and_start_session` tool dispatches through `ServiceCall`, which runs the request pipeline, so it already refuses those roles.
- The security review of 0117.S8 raised the gap as decision 71 of [0117](../0117-gateway-package-layout/decisions.md) (gateway package layout). 0117 kept the behavior because its steps change no behavior.
- [§10.2](../../spec/10_gateway-internals.md#authorization-and-rbac) (Authorization and RBAC) maps "Create / cancel own sessions" to `platform-admin`, `tenant-admin`, and `user`, and **Session ownership.** rule 7 checks a session create against that row. The section does not name the creation surfaces the row governs.

**Create-and-start skips two admission gates.** Evidence from the tree:

- `pkg/gateway/core/session/sessionserver/create.go`: `createAdmissionGates` refuses a create with `503 PLATFORM_DEGRADED` and `Retry-After: 10` while `s.dualStore` reports both stores unavailable, and `validateAndBuildCreateRow` calls `requirePoolNotDraining` before the claim and again after the experiment router and the PreRoute segment above the pivot when either changed the runtime.
- `pkg/gateway/core/session/sessionserver/create_and_start.go`: `createAndStartGates` and `buildCreateAndStartRow` call neither gate. `pool_drain_gate.go` and `create.go` are the only sources of `POOL_DRAINING`, and the pod-claim code does not read the pool phase.
- `lenny session new` without `--workspace` creates its session through `lenny/create_and_start_session` since step 0160.S1 of [0160](../archive/2026-11-19-0160-cli-session-new-starts/change.md) (lenny session new starts the session before the prompt), so the CLI's default path reaches the handler without these gates. The review of 0160.S1 found the gap, and decision O2 of 0160 sent it to a change of its own.
- [§15.1](../../spec/15_external-api-surface.md#151-rest-api), the `POST /v1/admin/pools/{name}/drain` row, names only `POST /v1/sessions` and the `create_session` MCP call in its drain backpressure. [§10.1](../../spec/10_gateway-internals.md#1013-dual-store-unavailability) (Dual-Store Unavailability) item 2 names `session.create` requests. [§15](../../spec/15_external-api-surface.md#15-external-api-surface) **Built-in adapter single-shot compute model.** lists the gates the single-shot path shares with `POST /v1/sessions/start`, and names neither these two nor the create permission.

A read-only role can therefore start sessions through MCP and the single-shot path, which consumes pool capacity, credentials, and budget in its tenant. A `user` can keep creating sessions through create-and-start on a pool an operator is draining, and while Postgres and Redis are both unreachable each create-and-start claims a warm pod and runs setup and launch before it fails and rolls back.

## Scope

The change covers the in-process creation entry points `CreateSessionService` and `CreateAndStartService`, the create-and-start handler `handleCreateAndStart`, and the specification text that names the creation surfaces of the create permission, the drain backpressure, and the dual-store refusal.

It leaves out:

- The route through which the in-process entry points reach the handlers. They keep their direct handler call (decision 2).
- The `RegisterAdapterUnderTest` error-class matrix of §15.2.1, which exercises each session-creation rejection on `POST /v1/sessions` and `create_session`. The change adds no create-and-start rows to it.
- The `PLATFORM_DEGRADED` code, which the handler writes on a create refused while both stores are unavailable and which the §15.1 error code catalog does not list. The change keeps the code the two-step create returns.

## Terms

| Term | Meaning |
| :-- | :-- |
| Creation surface | A call that creates a session: REST `POST /v1/sessions` and `POST /v1/sessions/start`, the MCP tools `lenny/create_session` and `lenny/create_and_start_session`, and the implicit session of a built-in adapter request. |
| Single-shot path | The implicit session a built-in adapter request creates, as §15 **Built-in adapter single-shot compute model.** and **Open Responses conversation sessions.** state. `cmd/lenny-gateway` serves it through `sessionSingleShotBinder`, which calls `CreateAndStartService`. |
| `manage_own_sessions` | The permission of the §10.2 "Create / cancel own sessions" row, `auth.PermManageOwnSessions`. |
| In-process creation entry point | `CreateSessionService` or `CreateAndStartService` in `pkg/gateway/core/session/sessionserver`, which run a create handler against an in-process recorder rather than through the API front's request pipeline. |

## Requirements

None by identifier. The change edits §10.2 (Authorization and RBAC), §10.1 (Dual-Store Unavailability), §15 (Built-in adapter single-shot compute model), and §15.1 (REST API), which are content of the placeholder pages AUTHZ, CAPISES, and others that have no requirement identifiers yet.

## Detailed design

### CP-1 Create permission in the in-process entry points

`CreateSessionService` and `CreateAndStartService` wrap their handler in a permission guard for `manage_own_sessions`, applied before `guardTenant`. The guard decides through `permissionGranted`, the evaluator that `GWSEAM-AuthorizePermission` serves to the request pipeline, so it admits and refuses the same principals: it refuses a principal whose roles, built-in or custom, do not grant the permission, and a role-less principal in a multi-tenant deployment. It admits a request with no principal and a role-less principal in a single-tenant deployment, which the owner binding in the handler then treats as on REST. A refused call returns a `ServiceError` with HTTP status 403, code `FORBIDDEN`, and the message the pipeline writes, and it runs no gate, writes no row, and claims no pod.

`lenny/create_session` and the single-shot path therefore refuse a `tenant-viewer` or `billing-viewer` principal with the same 403 that REST returns, and the MCP `lenny/create_and_start_session` tool keeps the refusal it already had through the pipeline.

### CP-2 Admission gates on create-and-start

`createAndStartGates` runs the dual-store refusal first, before the active-user gate, as `createAdmissionGates` does: while both stores are unavailable the call is refused with `503 PLATFORM_DEGRADED`, `Retry-After: 10`, and `details.reason: dual_store_unavailable`. One helper writes this refusal for both handlers.

`buildCreateAndStartRow` calls `requirePoolNotDraining` at the points `validateAndBuildCreateRow` calls it: after the PreRoute segment below the experiment-router pivot has selected the runtime and before the isolation level resolves, and again after the experiment router and the PreRoute segment above the pivot when either changed the runtime. Both checks precede the claim in `mintClaimStartPersist`, so a refused call claims no pod and writes no row, and the refusal is `503 POOL_DRAINING` with `Retry-After` and `details.pool` and `details.estimatedDrainSeconds`, as on the two-step create.

Because `CreateAndStartService`, the MCP `lenny/create_and_start_session` tool, and the single-shot path all run `handleCreateAndStart`, each gains both gates.

### CP-3 Specification text

- §10.2, after the permission matrix: the "Create / cancel own sessions" row governs every creation surface, and a caller the row refuses receives `403 FORBIDDEN` on REST and MCP and the native 403 on a built-in adapter, with no session created.
- §10.1, Dual-Store Unavailability item 2: the refusal applies to every creation surface.
- §15.1, the drain row: the backpressure names `POST /v1/sessions/start` and `lenny/create_and_start_session` alongside `POST /v1/sessions` and `lenny/create_session`.
- §15, **Built-in adapter single-shot compute model.**: the gates the single-shot path shares with `POST /v1/sessions/start` include the create permission, the dual-store refusal, and the pool-drain gate.

## Specification and non-specification changes

- `spec/10_gateway-internals.md`: §10.1.3 item 2, and §10.2 Authorization and RBAC.
- `spec/15_external-api-surface.md`: §15 **Built-in adapter single-shot compute model.**, and the drain row of §15.1.
- `pkg/gateway/core/session/sessionserver`: `service.go`, `rbac_gate.go`, `create.go`, `create_and_start.go`, and `routes.go` (the `SetServiceHandler` comment).
- `tests/registers/spec-section-citations.yaml`: the section-citation counts of the packages whose annotations change.

## Documentation

- `docs/api/rest.md` (client developer): the key error codes of `POST /v1/sessions` gain `FORBIDDEN` (403), `POOL_DRAINING` (503), and the 503 while both stores are unavailable, and `POST /v1/sessions/start` states that it runs the same checks and returns the same codes.
- `docs/api/mcp.md` (client developer): the error codes of `create_session` gain `FORBIDDEN` and `POOL_DRAINING`, and `create_and_start_session` lists the codes it adds to them.

## Tests

Unit tier, in `pkg/gateway/core/session/sessionserver`:

- `CreateSessionService` and `CreateAndStartService` refuse a `tenant-viewer` and a `billing-viewer` principal with 403 `FORBIDDEN` and write no row, admit `user`, `tenant-admin`, and `platform-admin`, follow a custom role that grants or lacks `manage_own_sessions`, admit a role-less principal in a single-tenant deployment and refuse it in a multi-tenant one, and refuse a forbidden role before the dual-store refusal; the refusal matches the REST status and code. Cites §10.2 (Authorization and RBAC) and §10.2 (Session ownership).
- `POST /v1/sessions/start` and `CreateAndStartService` refuse a create with `503 POOL_DRAINING` and `Retry-After` while the selected pool drains, judge the runtime routing selected, claim no pod and write no row on refusal, and admit a create when no pool drains. Cites §15.1 (REST API).
- `POST /v1/sessions/start` and `CreateAndStartService` refuse a create with `503 PLATFORM_DEGRADED` and `Retry-After: 10` while both stores are unavailable, with the code and details the two-step create returns, and admit a create when the stores are available. Cites §10.1 (Dual-Store Unavailability).

Unit tier, in `pkg/gateway/platformtools/mcptools`: the MCP `lenny/create_session` and `lenny/create_and_start_session` tools over a session server refuse `tenant-viewer` and `billing-viewer` with `FORBIDDEN` and create no session, admit `user`, and refuse with `POOL_DRAINING` while the pool drains and with `PLATFORM_DEGRADED` while both stores are unavailable. Cites §10.2 and §15.1.

Unit tier, in `cmd/lenny-gateway`: the single-shot binder refuses `tenant-viewer` and `billing-viewer` with a 403 `FORBIDDEN` single-shot error and binds a session for `user`, and refuses with a retryable 503 `PLATFORM_DEGRADED` carrying a ten-second `Retry-After` while both stores are unavailable. Cites §10.2 and §15 (Built-in adapter single-shot compute model).
