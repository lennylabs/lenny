# Redesign of the lenny-ops idempotency records

Status: `Draft`, revision 2, 2026-11-28.

## Problem

`lenny-ops` keeps idempotency records of its own. [§25.4](../../spec/25_agent-operability.md#254-the-lenny-ops-service) "Idempotency" specifies them, and `pkg/ops/opsidem` implements them over the table `ops_idempotency_keys`. The gateway's records of [§11.5](../../spec/11_policy-and-controls.md#115-idempotency) are a second implementation of the same mechanism, in `pkg/idempotency` and `pkg/idempotency/idempotencymw`, which change [0178](../0178-idempotency-keys-by-principal/change.md) brings to design C. The two implementations have drifted apart.

When the owner approved step 0178.S6 for three `lenny-ops` fixes (0178 decision 19, IP-20 on the M3 readiness decision page, 2026-11-27), the owner added the note: "If lenny-ops has a completely separate implementation of idempotency, should we run an in depth review to make sure we're not missing anything else and that the design aligns as much as possible with the gateway's?" The in-depth review of `pkg/ops/opsidem` against the gateway's design ran on 2026-11-27 and found these defects, each filed as an issue and checked against `main` at `c173b3790`:

- A response that carries a credential is stored and replayed: the impersonation bearer of `admin.start_impersonation` through `/mcp/management`, and the plaintext webhook signing secret of the event-subscription create and rotate-secret routes. The record compares neither the endpoint nor the body, a replay through the MCP bridge skips the bridge's scope gate, and `ops_idempotency_keys` is copied into full backups ([APICONV-I16](../../issues/APICONV/APICONV-I16-ops-idempotency-replays-the-impersonation-bearer-by-subject.md), high).
- A request to an unauthenticated path is keyed, under a caller that the client names in `X-Lenny-Caller` ([APICONV-I17](../../issues/APICONV/APICONV-I17-lenny-ops-keys-unauthenticated-requests-by-a-client-supplied-caller.md)), and the ops Ingress routes those paths from outside the cluster ([LOPSKRES-I1](../../issues/LOPSKRES/LOPSKRES-I1-ops-ingress-routes-the-unauthenticated-paths-from-outside-the-cluster.md)).
- The management MCP tools whose REST call requires a key always fail at Tier 2 and Tier 3, because the bridge sends no key ([OPSMCP-I1](../../issues/OPSMCP/OPSMCP-I1-required-key-management-tools-fail-through-the-mcp-bridge.md)).
- A record is scoped by the bare subject, with no tenant and no verifier source, and the `403 IDEMPOTENCY_KEY_OWNED_BY_OTHER_CALLER` of §25.4 tells a caller that another caller holds a key ([APICONV-I18](../../issues/APICONV/APICONV-I18-lenny-ops-idempotency-scope-is-the-bare-subject.md)).
- A pending claim stays `in_progress` after a client disconnect or a handler panic, and blocks its key for 24 hours or 7 days ([APICONV-I19](../../issues/APICONV/APICONV-I19-lenny-ops-pending-idempotency-row-outlives-a-client-disconnect-or-panic.md)).
- Every POST and PUT body is cut at 1 MiB without an error, and a read error reaches the handler as an empty body ([APICONV-I21](../../issues/APICONV/APICONV-I21-lenny-ops-truncates-request-bodies-at-1-mib-without-an-error.md)).
- Refusals whose condition can clear are stored ([APICONV-I22](../../issues/APICONV/APICONV-I22-lenny-ops-idempotency-stores-refusals-that-can-clear.md)), a replay drops the stored headers and adds headers the specification does not define ([APICONV-I23](../../issues/APICONV/APICONV-I23-lenny-ops-idempotency-replay-drops-the-stored-headers.md)), a key of any length is accepted and a long one answers `500` ([APICONV-I25](../../issues/APICONV/APICONV-I25-lenny-ops-accepts-an-idempotency-key-of-any-length.md)), and a stored response has no size limit (the `lenny-ops` half of [APICONV-I15](../../issues/APICONV/APICONV-I15-stored-idempotency-responses-outlive-what-they-describe-and-have-no-size-limit.md)).
- User erasure deletes no `lenny-ops` record ([ERASE-I10](../../issues/ERASE/ERASE-I10-user-erasure-leaves-the-lenny-ops-idempotency-records.md)), the operations inventory has no source for the kind `idempotency_in_progress` ([OPSREM-I1](../../issues/OPSREM/OPSREM-I1-operations-inventory-has-no-source-for-idempotency-and-webhook-delivery-kinds.md)), and the cleanup runs every five minutes where §25.4 says daily at 02:30 UTC ([APICONV-I24](../../issues/APICONV/APICONV-I24-lenny-ops-idempotency-cleanup-runs-every-five-minutes.md)).
- The two implementations duplicate one mechanism, so every rule above is stated, built, and tested twice ([APICONV-I26](../../issues/APICONV/APICONV-I26-opsidem-duplicates-the-gateway-idempotency-middleware.md)).

The owner was then asked (IP-21) whether step 0178.S6 takes the review's full list, keeps its approved scope, or splits off an urgent fix, and answered on 2026-11-27: "No, let's target a redesign of lenny-ops idempotency as a new and separate change. We can tackle that later as part of M3, I think" (0178 decision 20). This change is that redesign. Step 0178.S6 is `Cancelled`, and its three fixes are part of this change.

## Scope

The change covers the `lenny-ops` idempotency records of §25.4 "Idempotency": which requests are keyed, the record scope and the match, the limits on the key, the request body, and the stored response, the responses that are not stored, the replay, the release of a pending claim, the `idempotencyKey` argument of the MCP bridge and its replay, the error codes, the erasure of a user's records, the cleanup schedule, the operations inventory kind `idempotency_in_progress`, the paths the ops Ingress routes, and the implementation that `lenny-ops` shares with the gateway. It takes over the three fixes of the cancelled step 0178.S6: the match on the endpoint and the body, no stored credential, and the bridge's scope gate before a replay.

It leaves out:

- The gateway's idempotency records, which change 0178 states. [APICONV-I20](../../issues/APICONV/APICONV-I20-gateway-idempotency-final-write-uses-the-request-context.md), the gateway's final write on the request context, belongs to 0178 rather than to this change: the defect is in `idempotencymw`, which steps 0178.S3 and 0178.S4 are rewriting on their branch, and a gateway fix should not wait for the `lenny-ops` redesign. Open decision O21 of 0178 asks whether step 0178.S4 takes it. When this change builds `lenny-ops` on `idempotencymw` (open decision O2, option (a)), `lenny-ops` inherits the fix.
- The lifetime half of APICONV-I15 and a size limit on the gateway's stored responses, which stay with APICONV-I15.
- The kind `webhook_delivery_pending` of OPSREM-I1, which is the delivery store's and has no idempotency record.
- The backups taken before the change lands. No deployment of Lenny exists outside the project, so no operator outside the project holds such a backup (open decision O4).

## Terms

| Term | Meaning |
| :-- | :-- |
| `lenny-ops` record | A row of `ops_idempotency_keys`, or an entry of the in-memory store of `pkg/ops/opsidem`: the scope, the key, the state, the stored response, and the expiry (§25.4 "Idempotency"). |
| Gateway record | A row of `idempotency_keys`, the gateway's §11.5 record, which change 0178 scopes. |
| Design C | The record design of change 0178 (its decision 12, after decision 1 of change [0182](../0182-client-chosen-upload-secret/change.md)): a record is filed under who calls and what the call is, without the identity of the credential, and no stored response carries a credential. |
| Principal source | The verifier path that authenticated the caller, as change 0178 defines it. `lenny-ops` admits Lenny-issued tokens (`lenny`) and, in dev mode, the dev trust hook (`dev`). |
| Record scope | The values that identify a `lenny-ops` record under this change: the tenant, the subject, the principal source, the method, the mux route pattern, the path parameters, and the key. |
| TTL class | The lifetime §25.4 assigns to a route's records: standard, 24 hours (`ops.idempotency.keyTTLSeconds`), or long-running, 7 days (`ops.idempotency.longRunningKeyTTLSeconds`), for the upgrade and restore routes. |
| Required-key route | A route that §25.4 refuses without a key at Tier 2 and Tier 3 with `400 IDEMPOTENCY_KEY_REQUIRED`: upgrade start, restore execute, and a full backup. |
| MCP bridge | The management MCP server at `/mcp/management` (§25.12), which maps each tool to a REST call on the `lenny-ops` mux (`invokeLocal`) or proxies it to the gateway (`invokeGateway`). |
| No-store gate | A mark that a handler sets so that its response is returned and not stored, and the claim is released: `idempotencymw.DoNotStore` and `idempotencymw.DoNotStoreCredential` in the gateway. |
| Refusal that can clear | A refusal whose condition can change before the record expires, such as a missing scope or a held lock, which is not stored so that a retry after the change runs the operation. |
| Fold | Building `lenny-ops`' records on `pkg/idempotency` and `idempotencymw`, so that both services run one implementation ([APICONV-I26](../../issues/APICONV/APICONV-I26-opsidem-duplicates-the-gateway-idempotency-middleware.md)). |

## High-level design

`lenny-ops` follows the gateway's design C, and runs the gateway's idempotency code wherever the two services need the same behaviour. A request is keyed only when a verified principal is on it, so the unauthenticated paths are never keyed. A record is filed under the caller's tenant, subject, and principal source, the route with its path parameters, and the key, and it keeps a SHA-256 digest of the request body. The same key on another route or with another body answers `422 IDEMPOTENCY_KEY_REUSED`, and the same key from another caller is a separate record that reveals nothing. A key holds at most 128 characters, and a body over the limit answers `413`.

A response that carries a credential is not stored, nor is a `401`, a refusal that can clear, or a response over the stored-response limit; a retry of any of them runs the operation again. A stored response keeps its headers. A pending claim is released when the handler panics, and the final write of a claim completes after a client disconnect.

The MCP bridge takes an `idempotencyKey` tool argument and forwards it as the `Idempotency-Key` header of the mapped REST call, so a tool's result is stored once, under its REST route, after the tool's scope and role checks have run. `/mcp/management` is not itself a stored path. User erasure deletes a user's records, and the operations inventory lists the claims in progress.

`lenny-ops` keeps what is its own: the TTL classes, the required-key routes, its error envelope, the `503` and the degradation warning of a store outage, and a platform-scoped table. The shared code takes each of these as a parameter.

## Requirements

None by identifier. §25.4, §25.5, §25.12, and §12.8 are numbered sections of pages that have not landed.

## Current state

On `main` at `c173b3790`, as the review and the issues record:

- `opsidem` wraps the whole `lenny-ops` mux (`New` in `pkg/ops/opsserver/opsserver.go`), and `withOpsAuth` passes the unauthenticated paths to that wrapped handler (`pkg/ops/opsserver/auth.go`). It keys every POST and PUT, `/mcp/management` included.
- A record is keyed by `(key, caller_id)`, where `caller_id` is `Principal.Subject`, or the `X-Lenny-Caller` header with no principal (`callerIdentity` in `pkg/ops/opsserver/backup.go`). The endpoint is stored and never compared, and no body hash is kept (`pkg/ops/opsidem/opsidem.go`, `pkg/ops/opsidem/pgstore/pgstore.go`). A live record of another caller answers `403 IDEMPOTENCY_KEY_OWNED_BY_OTHER_CALLER`, as the §25.4 error table requires.
- `bufferBody` cuts every POST and PUT body at 1 MiB, keyed or not (`pkg/ops/opsidem/opsidem.go`). The key is only trimmed.
- Every response below `500` is stored, `401` responses, tool errors, and credentials included. A replay sets `Content-Type` to JSON, drops the other stored headers, and adds `X-Lenny-Idempotent-Replay`; a degraded response carries `X-Lenny-Idempotency-Degraded` (`pkg/ops/opsidem/writers.go`). §25.4 defines neither header.
- `Complete` and `Fail` run with `r.Context()`, and no deferred release covers a panic.
- `invokeLocal` replays a tool's REST call through the wrapped mux with no `Idempotency-Key` (`pkg/ops/opsserver/mcp.go`), and the generated tools carry no `x-lenny-idempotency-key`, which §25.12 lists.
- The in-flight answer is `409 OPERATION_IN_PROGRESS` with `elapsed`; the gateway answers `409 IDEMPOTENCY_KEY_IN_FLIGHT` with `Retry-After: 1`.
- The reconciler deletes expired rows every five minutes (`pkg/ops/opsservice/reconcile.go`); §25.4 "Retention" says daily at 02:30 UTC.
- §12.8 deletes nothing in `ops_idempotency_keys`, and the operations inventory lists the kind `idempotency_in_progress` with no source (`pkg/ops/opsserver/operations.go`).
- The store outage behaviour matches §25.4: `503 IDEMPOTENCY_STORE_UNAVAILABLE` on a required-key route, and a degradation warning elsewhere.
- `idempotencymw` cannot serve `lenny-ops` as it stands: `Record.IsExpired` uses the fixed `idempotency.TTL`; errors go out in the §15.1 envelope through `errorclassify`; a store outage answers `500`; it has no required-key classification; and its store targets the tenant-scoped `idempotency_keys` table.

The review's finding F15, that §25.4 "Calling the Gateway" describes a key that `lenny-ops` forwards to the gateway, does not hold on `main`: that text exists only on the unmerged branch `change/0178.S3-acting-party`, whose text 0178 revision 13 removes. It is not filed and not in scope.

## Detailed design

Each section names the gateway rule it aligns with. The design below assumes the recommended answers to the open decisions, and names the decision where an answer changes it.

### OI-1 Keyed requests

`lenny-ops` keys a request only inside the authenticated chain, after the bearer verifier, the rate limiter, and the role and scope gates have run. A request to `/healthz`, `/readyz`, `/metrics`, or `/internal/v1/event-subscriptions/cache/invalidate` is never keyed. A keyed request that reaches the idempotency code with no verified principal is refused with `401 UNAUTHORIZED`, reason `principal_subject_required`, before any claim, as §11.5 states for the gateway. `X-Lenny-Caller` never names the caller of a record. Closes APICONV-I17.

The ops Ingress routes the public prefixes only, `/v1/` and `/mcp/management`, and §25.4 states which paths it routes; the invalidate RPC and `/metrics` stay on the in-cluster Service. Closes LOPSKRES-I1.

### OI-2 The record scope and the match

A record is filed under the record scope: the tenant, the subject, the principal source, the method, the mux route pattern, the path parameters, and the key. The store keys a row by the tenant, the subject, and the SHA-256 digest of the canonical encoding of the rest, the encoding `idempotency.Key.Digest` uses. The row keeps a SHA-256 digest of the request body (`idempotency.HashBody`). A request whose scope matches a record and whose body digest differs answers `422 IDEMPOTENCY_KEY_REUSED` and does not execute. The same key on another route or with other path parameters is another scope, and so a separate record.

The same key from another caller is a separate record. `403 IDEMPOTENCY_KEY_OWNED_BY_OTHER_CALLER` is removed from §25.4 and from the code (open decision O3).

A key holds at most 128 characters (`idempotency.MaxKeyLength`), and a longer or empty key answers `400 INVALID_IDEMPOTENCY_KEY` before any claim, as §11.5 states. Closes the match part of APICONV-I16, APICONV-I18, and APICONV-I25.

### OI-3 The request body

Only a keyed request on a stored route is buffered. A body over `ops.idempotency.maxRequestBodyBytes` (default 1 MiB, the limit the code applies today) answers `413`, and a read error answers `400`, as the gateway's `readBodyLimited` does. An unkeyed request reaches its handler with its body untouched. Closes APICONV-I21.

### OI-4 Responses that are not stored

These responses are returned without being stored, and their claim is released, so a retry runs the operation again:

- **A response that carries a credential.** The event-subscription create and rotate-secret handlers, which return the plaintext signing secret of §25.5, call the no-store gate for credentials, and so does every other `lenny-ops` handler whose response carries a credential. The impersonation bearer of `admin.start_impersonation` is no longer stored because `/mcp/management` is not a stored path and `lenny-ops` stores nothing for a tool that it proxies to the gateway (OI-7), so a retry of the tool executes again and writes its own `admin.impersonation_started` audit row. §25.4 states the rule, as §11.5 **Stored credentials.** does for the gateway. A static test lists every stored `lenny-ops` route and its response type and fails on a credential field, and a contract test replays each stored route and fails on a credential key in the stored body, as 0178's IP-8 does for the gateway.
- **A `401`, and a refusal that can clear.** The gateway's list of 0178 decision 8 and IP-8 (`FORBIDDEN`, `OPERATION_NOT_PERMITTED_FOR_RESOURCE`, `QUOTA_EXCEEDED`, and the others it names), extended with the `lenny-ops` codes whose condition can clear: a remediation lock held (`409`), `UPGRADE_NOT_IN_PROGRESS`, and any other code the build finds, each listed in §25.4.
- **A `5xx`**, as today.
- **A response over `ops.idempotency.maxStoredResponseBytes`** (default 1 MiB), counted with its reason in a `lenny-ops` metric of skipped stores, named when the step is built, as the gateway counts its skipped stores in `lenny_idempotency_cache_skipped_total`.

`/mcp/management` is not a stored path (OI-7). Closes the credential part of APICONV-I16, APICONV-I22, and the `lenny-ops` part of APICONV-I15.

### OI-5 The replay

A replay returns the stored status, headers, and body, as the gateway's `replayResponse` does. The headers `X-Lenny-Idempotent-Replay` and `X-Lenny-Idempotency-Degraded` are removed, and the degradation warning stays in `degradation.warnings` as §25.4 states (open decision O6). Closes APICONV-I23.

### OI-6 The pending claim

The final write of a claim, stored or released, runs with `context.WithoutCancel` and a bounded timeout, so a client disconnect does not leave the row `in_progress`. A deferred release covers a handler that panics. A claim in flight answers `409 IDEMPOTENCY_KEY_IN_FLIGHT` with `Retry-After` and the elapsed time in the error's details, which replaces `409 OPERATION_IN_PROGRESS` in §25.4, its error table, and its outage outcomes (open decision O8). Closes APICONV-I19.

### OI-7 The MCP bridge

Each management tool whose REST route is keyed takes an `idempotencyKey` argument, as §11.5 states for the gateway's MCP tools. The bridge removes the argument from the body it maps and forwards it as `Idempotency-Key` on the REST call of `invokeLocal`, so the record is filed under the REST route after the tool's scope and role checks run. A required-key tool without the argument answers the tool error of `IDEMPOTENCY_KEY_REQUIRED`. The generator emits `x-lenny-idempotency-key` for each tool from the §25.4 classification, `required`, `recommended`, or `ignored`, as §25.12 lists. `/mcp/management` itself is not keyed at the HTTP layer, so a tool error is never stored and a replay cannot skip the bridge's scope gate. A tool that `invokeGateway` proxies takes no key from `lenny-ops`: the gateway's own records of change 0178 apply to the gateway's stored routes, and `lenny-ops` stores nothing for it. Closes OPSMCP-I1 and the MCP part of APICONV-I16.

### OI-8 Erasure, cleanup, and the inventory

- **Erasure.** §12.8 **Storage backends in erasure scope:** gains `ops_idempotency_keys`, whose rows of the erased user, found by `(tenant_id, subject)`, the `lenny-ops` store deletes; tenant deletion deletes a tenant's rows by `tenant_id`. The gateway's user eraser cannot reach the platform-scoped table through a tenant transaction, so §12.8 names the `lenny-ops` store as the one that deletes them, and the step names how the erasure job reaches it. Closes ERASE-I10.
- **Cleanup.** §25.4 "Retention" states the schedule the code has, a delete of expired rows every five minutes, as the setting `ops.idempotency.cleanupInterval` (default `5m`) (open decision O5). Closes APICONV-I24.
- **Inventory.** The store gains a listing of the claims in progress, which the operations inventory reads for the kind `idempotency_in_progress`, as §25.4 "Operations Inventory" lists it (open decision O7). Closes the idempotency part of OPSREM-I1.

### OI-9 One implementation

`lenny-ops` runs `idempotencymw` (open decision O2). After step 0178.S4 lands, the shared code gains:

- an expiry carried by each record, so a store keeps the TTL class of the route;
- a required-key classification that the caller injects, with the tier rule of §25.4;
- an error writer that the caller injects, so `lenny-ops` keeps its `conventions` envelope;
- an outage policy that the caller injects, so `lenny-ops` keeps the `503` and the degradation warning of §25.4 and the gateway keeps its own;
- a store over `ops_idempotency_keys` through `PlatformPostgres()`, beside the gateway's tenant-scoped store;
- the no-store gates and the list of refusals that can clear in `pkg/idempotency`, so both services read one rule.

`pkg/ops/opsidem` is then removed. The gateway's behaviour does not change. Closes APICONV-I26.

### OI-10 The table

A new migration recreates `ops_idempotency_keys` with `tenant_id`, `subject`, `source`, `scope_digest`, `idempotency_key` as data, `request_hash`, the state, the stored status, headers, and body, `created_at`, and `expires_at`, keyed by `(tenant_id, subject, scope_digest)`, with the `expires_at` index. The rows are a cache with a lifetime of at most 7 days and no deployment exists outside the project, so the migration discards them, with no compatibility shim (design principles, "No compatibility machinery"). The table stays platform-scoped and stays in backups, so a restore keeps the records that match the restored state; once no stored response carries a credential, a backup holds none.

## Ordering

Under open decision O2, option (a), step 0183.S1 waits on step 0178.S4, because 0178.S3 and 0178.S4 rework `idempotency.Key`, its validation, and the stored routes on their branch. Step 0183.S4, the ops Ingress paths, depends on no other step. Under O2 option (b), every step but the fold starts without waiting on 0178, and the fold becomes the last step.

## Overlaps with other changes

- **0178.** This change applies 0178's design C to `lenny-ops`, takes over the three fixes of the cancelled step 0178.S6, and reuses the code 0178.S3 and 0178.S4 build. APICONV-I20 stays with 0178 (open decision O21 of 0178).
- **0182.** Change 0182 removes the upload token from the gateway's create response. No `lenny-ops` route stores an upload token.
- **0138.** Change 0138 removes the gateway's MCP API, with the gateway's MCP `idempotencyKey`. The management MCP server of `lenny-ops` is outside that removal, so OI-7 adds an `idempotencyKey` argument to the bridge while the gateway's argument leaves.

## Worked example

`ops-agent` is a service account of tenant `acme` with a token that carries the `platform-admin` role. It calls `lenny_platform_upgrade_start` through `/mcp/management` with `idempotencyKey` `k1`. The bridge runs the tool's scope and role checks, then calls `POST /v1/admin/platform/upgrade/start` with `Idempotency-Key: k1`. The record is filed under `acme`, the account's subject, the source `lenny`, and the digest of `(POST, POST /v1/admin/platform/upgrade/start, {}, k1)`, with the long-running TTL. The connection drops while the upgrade starts; the final write completes anyway, and the agent's retry with `k1` replays the stored response with its headers. A narrowed token of the same account without the upgrade scope that sends `k1` to the same tool is refused by the bridge's scope gate before the lookup. The same account then sends `k1` to `POST /v1/admin/event-subscriptions`: another route is another scope, so the create runs, and its response, which carries the signing secret, is returned and not stored. A retry with `k1` and the same body creates a second subscription, which the agent avoids by reading the subscription list first; §25.5 and the operator page state that a create whose response carries a secret is not replayed. A request with `k1` to `/internal/v1/event-subscriptions/cache/invalidate` from outside the cluster finds no Ingress route, and inside the cluster it is not keyed.

## Limits, and alternatives rejected

- A retry of a route whose response carries a credential, the event-subscription create and rotate-secret and `admin.start_impersonation`, executes again. A client that needs exactly one subscription checks for it before a retry.
- Every credential of one subject and source that knows a key and the body shares a record, as in the gateway under 0178 decision 13. What it receives holds no credential, and the route's role and scope gates run before the lookup.
- Keeping `opsidem` and fixing it in place, which the review recommended while the fixes were step 0178.S6 and could not wait on 0178.S4, is open decision O2, option (b). It keeps two implementations of one mechanism until a later fold.
- Keeping the owned-by-other `403` is open decision O3, option (b).
- An interim fix of the credential exposure ahead of the redesign, the urgent split that IP-21 offered and the owner did not choose, is open decision O4, option (c).
- Leaving `ops_idempotency_keys` out of backups by default was considered and not proposed: a restore without the records replays nothing for an operation that the restored state already holds, so a retry with the same key would execute it again. The no-store rule keeps credentials out of the backups instead.

## Specification and non-specification changes

- `spec/25_agent-operability.md`: §25.4 "Idempotency", the keyed requests, the record scope and the match with the body digest and the `422`, the key limit, the request body limit and the `413`, the responses that are not stored, the stored headers, the release of a pending claim, the in-flight answer, the error table without `IDEMPOTENCY_KEY_OWNED_BY_OTHER_CALLER`, the table, and "Retention"; §25.4 "Kubernetes Resources", the paths the ops Ingress routes; §25.4 "Operations Inventory", the source of `idempotency_in_progress`; §25.5, the create and rotate-secret responses are not stored; §25.12, the `idempotencyKey` argument and `x-lenny-idempotency-key`.
- `spec/12_storage-architecture.md`: §12.8, `ops_idempotency_keys` in the erasure scope and in tenant deletion.
- `spec/25_agent-operability.md`, §25.4 "Idempotency" and "Retention": the settings `ops.idempotency.maxRequestBodyBytes`, `ops.idempotency.maxStoredResponseBytes`, and `ops.idempotency.cleanupInterval` with their defaults, beside the TTL settings that §25.4 states today.
- `spec/16_observability.md`: §16.1, the `lenny-ops` metric of skipped stores.
- `migrations/`: the migration of OI-10, numbered when the step is built.
- `pkg/idempotency` and `pkg/idempotency/idempotencymw`: the parameters of OI-9, a store over the platform-scoped table, and the no-store gates and the refusal list moved into `pkg/idempotency`.
- `pkg/ops/opsserver` and `pkg/ops/opsservice`: the idempotency code inside the authenticated chain, the caller resolution, the no-store gates in the event-subscription handlers, the bridge's forwarding of `idempotencyKey` in `invokeLocal`, the erasure store, the cleanup setting in the reconciler, and the inventory source.
- `pkg/ops/mcp` and its generator: the `idempotencyKey` argument and `x-lenny-idempotency-key`.
- `pkg/ops/opsidem` and `pkg/ops/opsidem/pgstore`: removed.
- `cmd/lenny-ops`: the wiring and the flags of the new settings.
- `charts/lenny`: the ops Ingress paths, and the values of the new settings with their schema.
- `cmd/lenny-gateway` or the erasure job: the call that reaches the `lenny-ops` erasure store, as OI-8 states.

## Documentation

- `docs/operator-guide/agent-operability.md` (operator, and the author of an agent that calls `lenny-ops`): the "Idempotency" section states that only an authenticated request is keyed; the record scope; the `422 IDEMPOTENCY_KEY_REUSED` on another route or body; the key limit and `400 INVALID_IDEMPOTENCY_KEY`; the body limit and the `413`; the responses that are not stored, with the event-subscription create and rotate-secret and `admin.start_impersonation` named, and that their retry executes again; that a `401` and the refusals that can clear are not stored; the `409 IDEMPOTENCY_KEY_IN_FLIGHT` with `Retry-After`; the TTL classes; the required-key routes; and the `503` and the degradation warning of a store outage. The "MCP management catalog" section states the `idempotencyKey` argument, which tools require it at Tier 2 and Tier 3, and `x-lenny-idempotency-key` in the tool schema example.
- `docs/reference/error-catalog.md` (operator, and agent author): the `lenny-ops` idempotency codes, `IDEMPOTENCY_KEY_REQUIRED`, `IDEMPOTENCY_KEY_IN_FLIGHT`, `IDEMPOTENCY_STORE_UNAVAILABLE`, `IDEMPOTENCY_KEY_REUSED`, and `INVALID_IDEMPOTENCY_KEY` as `lenny-ops` returns them; the page lists none of the `lenny-ops` codes today.
- `docs/reference/configuration.md` (operator): `ops.idempotency.keyTTLSeconds`, `ops.idempotency.longRunningKeyTTLSeconds`, `ops.idempotency.maxRequestBodyBytes`, `ops.idempotency.maxStoredResponseBytes`, and `ops.idempotency.cleanupInterval`, with their defaults; the page lists none of them today.
- `docs/reference/metrics.md` (operator): the metric of skipped stores as `lenny-ops` emits it, with its labels.
- `docs/operator-guide/security.md` (operator): the `lenny-ops` records beside the gateway's, what they hold and do not hold, their erasure, and that a backup holds the live records, none of which carries a credential.
- `docs/operator-guide/ingress-and-tls.md` (operator): the paths the ops Ingress routes, and that the invalidate RPC and `/metrics` are reachable only inside the cluster.
- `docs/operator-guide/agent-operability.md`, "The operational event stream" (operator, and agent author): a keyed create or rotate-secret of an event subscription is not replayed, because its response carries the signing secret, so a retry creates or rotates again.

## Tests

- Unit, `pkg/idempotency` and `pkg/idempotency/idempotencymw`: the per-record expiry, the injected classification, error writer, and outage policy, and the gateway's behaviour unchanged under its defaults. Cites §11.5 and §25.4.
- Unit, the `lenny-ops` server: a keyed request to each unauthenticated path stores nothing; a keyed request with no principal is refused before any claim; the same subject under two tenants or two sources gets two records; the same key from another caller runs; the same key on another route or with another body answers `422`; a key at and over 128 characters; a body over the limit answers `413` and a failing reader `400`; each response of OI-4 is not stored and its retry runs; a replay returns the stored headers; a cancelled request context and a panicking handler leave no `in_progress` row; an in-flight claim answers `409 IDEMPOTENCY_KEY_IN_FLIGHT`; the cleanup interval; the inventory lists a claim in progress. Cites §25.4.
- Unit, `pkg/ops/mcp` and the bridge: a required-key tool with `idempotencyKey` runs and replays, and without it answers the required-key error at Tier 2; the argument reaches the REST call as the header; `x-lenny-idempotency-key` matches the classification for every tool; `/mcp/management` stores nothing. Cites §25.12 and §25.4.
- Static, `tests/static`: every stored `lenny-ops` route has an entry in the test's map of response types, and no response type has a credential field. Cites §25.4.
- Component, the `lenny-ops` Postgres store and `tests/component/migrations`: the store keys rows by tenant, subject, and scope digest, keeps the TTL class, deletes by user and by tenant, and lists the claims in progress; the migration recreates the table. Cites §25.4 and §12.8.
- Contract, the `lenny-ops` server: each required-key tool through `/mcp/management` with `IdempotencyKeyRequired: true`, with and without the argument; every stored route's stored body is free of credential keys; a replay of `admin.start_impersonation` executes again and writes its audit row. Cites §25.4 and §25.12.
- Security, `tests/security`: the invalidate path is not a write path into `ops_idempotency_keys`; a token without a tool's scope is refused on a retry through `/mcp/management`; a keyed event-subscription create stores no secret. Cites §25.4 and §25.5.
- Load_local: concurrent claims of one key on the Postgres store across two `lenny-ops` replicas run the operation once. Cites §25.4.
- Chart, `charts/lenny`: the rendered ops Ingress routes neither `/internal/` nor `/metrics`. Cites §25.4.
- Integration: a user erasure deletes the user's `lenny-ops` records. Cites §12.8.
- Each isolation case has a mutation check: the test fails when the scope dimension or the check that separates it is removed.
