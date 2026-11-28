# Idempotency keys scoped by principal

Status: `In progress`, revision 13, 2026-11-27.

## Problem

The gateway keyed each stored idempotent response by tenant and key alone, so a second user of the same tenant who sent the same key with the same body received the first user's stored response. That response can hold the first user's session id and, since change [0173](../archive/2026-11-25-0173-idempotency-stored-credentials/change.md), an upload token that the replay rebuilds from its redaction reference.

Steps 0178.S1 and 0178.S2 landed the scope by tenant, subject, and bound session, and the deletion of a user's records by user erasure. The reviews of open decisions O4 and O8 then found further gaps:

- The platform-admin impersonation bearer and the playground bearer carry no `act` claim and no `session_id` claim, so they share the user's records, including a create response that restores the upload token ([APICONV-I3](../../issues/APICONV/APICONV-I3-impersonation-and-playground-bearers-share-the-users-idempotency-scope.md)).
- A token that another caller narrows for the user carries nothing the scope separates ([APICONV-I7](../../issues/APICONV/APICONV-I7-token-narrowed-by-another-caller-shares-the-subjects-idempotency-scope.md)).
- An IdP token keeps its `act` claim through normalization, so an IdP can name an acting party that no Lenny mint wrote ([AUTHN-I2](../../issues/AUTHN/AUTHN-I2-idp-act-claim-survives-normalization.md)).
- The REST key ignores the route, so one key with equal bodies on two routes or two sessions replays one operation's response for the other ([APICONV-I5](../../issues/APICONV/APICONV-I5-rest-idempotency-key-ignores-the-route.md)).
- A replay returns a stored response without the checks the operation runs before it executes ([APICONV-I6](../../issues/APICONV/APICONV-I6-idempotency-replay-skips-the-handler-gates.md)), and the middleware stores refusals whose condition can clear ([APICONV-I8](../../issues/APICONV/APICONV-I8-idempotency-stores-refusals-that-can-clear.md)).
- The idempotency middleware wraps the client listener's mux, so a replay returns before the request pipeline runs its permission gate and its ownership check, on every stored route ([APICONV-I13](../../issues/APICONV/APICONV-I13-idempotency-replay-skips-the-pipelines-permission-and-ownership-checks.md)).
- `/mcp` is a stored path, so a JSON-RPC call with an `Idempotency-Key` header stores the result of any edge MCP tool, including read tools such as `download_artifact`, and replays it to another credential of the subject without the tool's own checks ([APICONV-I14](../../issues/APICONV/APICONV-I14-mcp-read-tool-results-are-stored-and-replayed-without-the-tools-checks.md)).
- The replay of a session-creating route skips the create's own authorization checks beyond the creating-caller check, such as the permission gate, the §10.6 environment admission, and the playground's runtime boundary, and the replay of `lenny/delegate_task` runs only the creating-caller check.

Decision 8 scoped each record by the rotation chain of the bearer, so that only the credential that stored a response, or its self-rotation, replays it. The chain exists to protect one value, the `uploadToken` in a create response. Building it showed its costs: a retry after a re-login, a re-narrowing, or `exp` runs the operation again (decision 9 and step 0178.S5); an IdP token's chain names a client application rather than a party (decisions 10 and 11, and the open decisions O11 and O12 they raised); and a deny-list of client claims cannot be complete, because it omits `nonce` and any claim an identity provider lets a client set.

Change [0182](../0182-client-chosen-upload-secret/change.md) removes the credential from the create response: the client chooses the upload secret and sends its digest. With no credential to protect, this change scopes a record by who calls and what the call is, without the credential's identity, which is design C. The owner chose design C for both changes on 2026-11-27 (decision 1 of change 0182, which records the owner's words that led to the redesign). Decision 12 here states what that answer withdraws from or restores to the settled decisions, decisions 13 to 17 settle the decisions that design C narrows or withdraws, decision 18 settles the replay admission, and decision 19 adds step 0178.S6 for the `lenny-ops` records.

## Scope

The change covers the idempotency records of the §11.5 critical operations on the REST `Idempotency-Key` path and the MCP `idempotencyKey` path: the store's key and the table, the scope the middleware and the MCP hook resolve, the principal source that the verifier records, the replay admission, the responses that are stored and those that are not, and the attribution of the credential that stored a record. It also covers the deletion of a user's records by user erasure (steps 0178.S1 and 0178.S2), and the `lenny-ops` idempotency records of [§25.4](../../spec/25_agent-operability.md#254-the-lenny-ops-service) (`ops_idempotency_keys`, `pkg/ops/opsidem`), which store and replay credentials and compare neither the endpoint nor the body ([APICONV-I16](../../issues/APICONV/APICONV-I16-ops-idempotency-replays-the-impersonation-bearer-by-subject.md), step 0178.S6, decision 19).

It leaves out:

- The upload credential itself, which change [0182](../0182-client-chosen-upload-secret/change.md) changes.
- The effective audience and the operation check of 0138's narrowing ("RO-4" of [0138](../0138-rest-only-client-api/change.md)), which do not exist in the tree. The scope carries an effective audience that is empty for a request that is not narrowed, so step 0138.S2 sets it without changing the record scope's layout, and 0138.S2 adds the operation check to the replay admission as a hook that runs on every route.
- The lifetime and size of stored responses ([APICONV-I15](../../issues/APICONV/APICONV-I15-stored-idempotency-responses-outlive-what-they-describe-and-have-no-size-limit.md)).

## Terms

| Term | Meaning |
| :-- | :-- |
| Idempotency record | A row of `idempotency_keys`, or an entry of the in-memory store: the scope, the key, the request hash, the stored response, and the attribution of the storing credential ([§11.5](../../spec/11_policy-and-controls.md#115-idempotency)). |
| Principal | The authenticated caller the auth middleware attaches to the request (`authmw.Principal`): its tenant, its subject, its source, its session id, and its token identifiers. |
| Principal source | The verifier path that authenticated the principal: `lenny` (a token the Lenny key set verified), `idp` (a token the upstream IdP's keys verified), `agent` (a platform tool call from a session's pod over `LNK-GWCONTROL`), `service` (a platform service account's token), or `dev` (the development header path). The verifier member that accepted the token sets it. |
| Session dimension | The session a record is filed under: the calling session of an `agent` call, or the `session_id` claim of any other bearer that carries one, and empty otherwise (decision 3). A token exchanged from another carries the `session_id` of its subject token, so an `a2a_delegation` token exchanged from a user's token carries none. |
| Record scope | The values that identify an idempotency record: the tenant, the subject, the principal source, the session dimension, the effective audience, the method, the mux route pattern, the path parameters, and the key. |
| Scope digest | The SHA-256 digest of the canonical encoding of the record scope without the tenant and the subject. The table keys a row by the tenant, the subject, and the digest. |
| Storing credential | The credential whose request stored a record: its `jti`, and the `azp` or `client_id` of an IdP token. The row records it as data, outside the scope. |
| Stored route | A route or MCP tool whose responses the gateway stores: the §11.5 critical operations on REST, and, on `/mcp`, the `tools/call` requests of the tools of the storage allow-list. |
| Replay admission | The checks the gateway runs before it returns a stored response. |

## High-level design

The gateway files each idempotency record under the caller's tenant and subject, the verifier path that authenticated it, the session the bearer is bound to, the operation's route with its path parameters, and the key. It does not file a record under the identity of the credential, so a retry after any token refresh replays the stored response. No response that the gateway stores carries a credential: change 0182 removes the upload token from the create response, and a response that carries any other credential is not stored. What a replay returns is therefore state that the caller could read through the session's own read operations. The `lenny-ops` service keeps idempotency records of its own, which step 0178.S6 brings under the same rules: it compares the endpoint and the body, stores no response that carries a credential, and runs the MCP bridge's scope gate before a replay (APICONV-I16).

Before a replay, the gateway runs the authorization checks of the matched route: the permission gate of every stored route, the ownership check of a session route, the checks of the caller that a session-creating route or tool runs, and the user gate and the restriction of processing. A caller that the operation would refuse receives the refusal. On `/mcp`, only the session-creating tools are stored. The record notes which credential stored it, and a metric counts replays by another credential, so a replay across credentials leaves a trace. Refusals that can clear, `401` responses, upload-token refusals, and the refusal of an upload digest in use are not stored.

## Requirements

None by identifier. §11.5 is content of the placeholder page APICONV, and §13.3, §10.2, and §27 are numbered sections.

## Current state

On `main` at `1ce7a1941`, steps 0178.S1 and 0178.S2 have landed: migration 0206 keys `idempotency_keys` by `(tenant_id, subject, session_id, idempotency_key)`, and user erasure deletes a user's records. The branch `change/0178.S3-acting-party` holds the build of decision 8 and of decisions 10 and 11 (revisions 8 to 10): the `rch` claim at every Lenny mint, `authmw.Principal.Source` and `Chain`, the client claim setting, the scope digest with migration 0208, the route resolution, the replay admission of decision 8, and the refusals that are not stored. None of it is on `main`.

The auth middleware wraps the idempotency middleware (`cmd/lenny-gateway/httpsurface.go`), so the principal is on the request context when the idempotency middleware and the MCP hook run. The idempotency middleware wraps the client listener's mux and replays before the mux runs (`pkg/idempotency/idempotencymw/idempotency.go`), so the request pipeline's `guard` (`pkg/gateway/api/pipeline/access.go`), which runs the permission gate and `AuthorizeSession` with `r.PathValue("id")`, does not run on a replay. `AllowedPaths` lists `/mcp` (`cmd/lenny-gateway/httpsurface.go`), and `idempotencymw.DoNotStoreCredential` has one caller, `lenny/vcs_token` (`pkg/gateway/platformtools/mcptools/mcptools.go`). `refusalCanClear` (`pkg/idempotency/idempotencymw/refusal.go`) stores `401 UPLOAD_TOKEN_INVALID`, `403 UPLOAD_TOKEN_MISMATCH`, and `410 UPLOAD_TOKEN_CONSUMED`.

## Detailed design

### IP-1 The record scope

`idempotency.Key` holds the tenant, the subject, the source, the session dimension, the method, the route, the path parameters, the effective audience, and the key string. `Validate` refuses an empty tenant, subject, source, or key. `Digest` returns the SHA-256 digest of the length-prefixed encoding of the source, the session dimension, the method, the route, the path parameters sorted by name, the audience, and the key. The `Store` methods take an `idempotency.Key`, and the stores key a record by the tenant, the subject, and the digest. The scope names no credential (decision 12).

The source is a scope column although it identifies no credential: a Lenny token that carries a session id files under `lenny` and the session, and the pod's own platform tool calls under the same session file under `agent`, so a holder of such a token cannot replay or pre-claim the pod's templated keys. A token exchanged from another carries the `session_id` of its subject token (`pkg/tokenexchange`), and boundary normalization clears `session_id` from an IdP token. An `a2a_delegation` token that a third-party agent holds therefore carries a session id only when it was exchanged from a session-bound token; one exchanged from a user's token carries none, and its records file under the user's top-level scope with the source `lenny`, beside the user's own Lenny tokens.

### IP-2 Resolving the scope

`idempotencymw.Options.PrincipalFromRequest` returns the tenant, subject, source, and session dimension of the request's principal, which `cmd/lenny-gateway` reads from `authmw.FromContext` through GWSEAM-RequestCaller. The session dimension is the calling session for the `agent` source and `Principal.SessionID` for every other source, so any bearer that carries a session id is filed under it, as decision 3 states. `Options.Route` returns the request's mux route pattern and path parameters, resolved from `mux.Handler(r)` and, for a handler that exposes its own mux, such as the request pipeline's handler of the session paths, from that mux. The MCP hook resolves the same principal and maps a tool call to the method `POST` and the route `mcp:<tool>`. A call through the explicit-environment endpoint `/mcp/environments/{name}` carries the path parameter `environment` with the environment's name, as a REST path parameter does, so one key sent to two environments files two records; any other tool call has no path parameters. A keyed request whose principal has no subject is refused with `401 UNAUTHORIZED`, reason `principal_subject_required`, before any claim.

### IP-3 The table

Migration 0208 empties `idempotency_keys`, drops `session_id`, adds `scope_digest BYTEA NOT NULL DEFAULT '\x'`, `stored_by_jti TEXT NOT NULL DEFAULT ''`, and `stored_by_client TEXT NOT NULL DEFAULT ''`, and makes the primary key `(tenant_id, subject, scope_digest)`. The `idempotency_key` column stays as data. The rows are a 24-hour cache and no deployment exists, so the migration discards them, which also discards every stored response that holds a 0173 redaction reference. Its down migration restores the key of migration 0206. The tenant guard trigger, the row-level security policy, and the `(tenant_id, stored_at)` index of migration 0082 are unchanged. Migration 0208 has not merged, so this revision rewrites it in place.

### IP-4 Attribution of the storing credential

The gateway writes the storing credential's `jti` to `stored_by_jti`, and the `azp`, else the `client_id`, of an IdP token to `stored_by_client`; a principal without the value writes the empty string. On a replay, a request whose `jti` differs from `stored_by_jti`, or whose client differs from `stored_by_client`, increments `lenny_idempotency_replay_other_credential_total`, labelled by `tenant_id` and `source`, and the replay is logged at info level with the record's route and both pairs of values. A principal with neither value, such as an IdP token with no `jti` and no client claim, a principal of the development header path, a service account's token without a `jti`, or a pod-path call, compares the empty string with the empty string, so a replay between two such credentials is not counted; §11.5 and the operator page state that limit. The columns are not part of the scope, and erasure deletes them with the row. §16.1 lists the metric.

### IP-5 The specification

- §11.5 **Mechanism.** states the record scope, the session dimension per source, the refusal of a key with no subject, the MCP mapping, the table key, the attribution columns, and that the effective audience separates records without protecting one credential of a subject from another.
- §11.5 states the stored routes, the replay admission (IP-7), the checks a replay does not run again, that a replay of a session route whose session was deleted or is hidden from the caller since the original answers the `404` of the ownership check, the responses that are not stored (IP-8), and the limit of the attribution metric (IP-4).
- §10.2 states that boundary normalization of an IdP token clears `act`, so an actor that the identity provider names reaches neither `CallerIdentity.Act` nor a token exchanged from the IdP token; the clearing grants no access.
- §12.8 keeps the deletion of idempotency records by `(tenant_id, subject)`, whatever their scope digest.
- §16.1 lists `lenny_idempotency_replay_other_credential_total`.
- `GWSEAM-RequestCaller` names the source and the session dimension.
- A new seam operation, GWSEAM-AdmitRouteReplay, served by the session core, through which the request pipeline runs the replay checks of IP-7 that live in the session core; GWSEAM-AdmitToolReplay gains `lenny/create_and_start_session` and the checks IP-7 lists for `lenny/delegate_task`.
- The branch's edits for the rotation chain are reverted: §13.3 **Rotation chain.** and the `rch` claim row, the §10.2 client claim and **Client claim of an identity-provider token.**, the §17.4 chain of the development header path, the §17.8.1 `auth.oidc.clientIdClaim` row, the §25.4 chain of a service account's call, the §27.3.1 playground chain, and §11.5 **Retry across a credential refresh.**

### IP-6 Erasure of a user's records

§12.8 **Storage backends in erasure scope:** has a row for idempotency records in Postgres, whose erased data is the user's records, found by `(tenant_id, subject)` with the subject equal to the erased user's id. The **`DeleteByUser` dependency-ordered deletion sequence** has a step that deletes them, and tenant deletion Phase 4 deletes the tenant's records by `tenant_id`. The Postgres store and `MemoryStore` carry `DeleteByUser(ctx, tenantID, subject)` and `DeleteByTenant(ctx, tenantID)`, which the erasure wiring in `cmd/lenny-gateway` registers under the `IdempotencyStore` name of `pkg/storage/erasure/order.go`. The 24-hour garbage-collection sweep stays. A record names only its own subject, so erasure deletes every record that holds the erased user's identity. Step 0178.S2 built this section, and it is unchanged.

### IP-7 Replay admission (step 0178.S4)

Before a replay, the gateway runs the authorization checks of the matched route, and the checks of the caller that the route runs, and no other check (decision 18).

**Where the lookup runs.** Every stored REST route is a route of the request pipeline: the session-creating routes `POST /v1/sessions`, `POST /v1/environments/{name}/sessions`, and `POST /v1/sessions/start`, and the session routes `/v1/sessions/{id}/finalize`, `start`, `resume`, `derive`, and `tool-use/{toolCallId}/approve` and `deny`. The idempotency lookup of each runs inside the pipeline after `guard`, so a replay passes the route's permission gate (the `manage_own_sessions` permission of `AccessSessionCreate` on a create route) and, on a session route, its ownership check (`AuthorizeSession` on the path's session id), with the path values the pipeline's mux populates. The middleware outside the mux keeps only `/mcp`. The pipeline builds one gate per listener; the lookup is installed in the client listener's gate only, so the holder forward listener, which copies only `Content-Type` and `Accept` from the forwarded request, never claims a key.

**What a replay of each route runs:**

- **Session routes.** The permission gate and the ownership check, by construction, and the §11.4 gate and the Article 18 restriction, which the handlers of finalize, start, and resume do not run today.
- **Session-creating REST routes.** The permission gate by construction, the creating-caller check of §10.2 **Signing topology**, the §11.4 gate, the Article 18 restriction, the §12.8 lifecycle fence of the caller, the §10.6 environment admission of the caller for the environment the stored request names, and, for a caller whose origin is the playground, the §27.5 runtime boundary (`allowedRuntimes`) for the runtime the stored request names. The replayed request carries the body the record was stored for, so the checks that read the environment and the runtime read them from it.
- **Not run again on a create replay:** the §4.8 policy chain, the §11.1 quota, concurrency, and rate limits, pool and capacity checks, and every other check of a resource's state, as for every route.
- **`lenny/create_session` and `lenny/create_and_start_session`.** The checks of the session-creating REST route the tool reaches, through GWSEAM-AdmitToolReplay.
- **`lenny/delegate_task`.** The creating-caller check, the §11.4 gate and the Article 18 restriction of the user, the ownership check of the parent session (the calling session of an `agent` call, else the session the call names), and the §10.6 admission of the caller for the target runtime. The §8.2 delegation policy, the tree's budget, and the isolation checks are checks of the parent's policy and state, and are not run again.
- **Narrowed requests.** The operation check of 0138's narrowing, as a hook that 0138.S2 adds on every route.

Checks of a resource's state, quota checks, body-dependent capability checks other than those listed, and header-dependent checks are not run again. A finalize replay therefore does not check the upload secret, and the stored finalize response holds nothing secret. A replay of a session route whose session was deleted, or is hidden from the caller, since the original answers the `404` of the ownership check. A refused replay returns the check's refusal, is not stored, and leaves the record in place. A creating-caller refusal at replay writes its `token.exchanged` rejection row with the `exchange_type` `session_mint_replay`.

**How the pipeline reaches the checks.** The §11.4 gate, the Article 18 restriction, the §12.8 fence, the §10.6 admission, and the playground boundary live in the session core (`activeUserRefusal` and the create handler's admission in `pkg/gateway/core/session/sessionserver`). The pipeline reaches them through a new seam operation, GWSEAM-AdmitRouteReplay, keyed by the mux pattern, which the session core serves beside GWSEAM-AdmitToolReplay; the gateway wiring lint requires a seam for a call from the pipeline into the session core.

**The move from the outer middleware.** Step 0178.S4 moves the lookup of the REST routes into the pipeline in one build. The outer middleware and the pipeline compute the same record scope, because IP-2 resolves the route pattern and the path parameters from the pipeline's mux in both places, so during a rolling upgrade a record that one replica's outer middleware claimed is found by another replica's pipeline lookup, and the reverse, and a pending claim from either answers the other with `409 IDEMPOTENCY_KEY_IN_FLIGHT`. A unit test pins that both compute one scope digest for each stored route.

### IP-8 Stored routes, and responses that are not stored (step 0178.S4)

- **Storage allow-list.** On `/mcp`, the middleware and the MCP hook store only a JSON-RPC `tools/call` request whose tool has an admit hook: `lenny/create_session`, `lenny/create_and_start_session`, and `lenny/delegate_task`. A keyed `tools/call` of any other tool, and every other JSON-RPC method, such as `initialize` and `tools/list` (whose result is filtered per credential), runs and is not stored. `lenny/create_and_start_session` is stored because its REST twin `POST /v1/sessions/start` is. The REST stored routes are the §11.5 critical operations of `AllowedPaths`.
- **No stored credential, static.** A static test reads a map, kept in the test, from each stored route and tool to the Go type its handler encodes as the response, and fails when a stored route or tool of `AllowedPaths` or the allow-list has no entry. It walks each type recursively and fails on a JSON field whose name, compared without case, ends in `token`, `secret`, `password`, `apikey`, or `credential`, or is on its list of capability URL names, or whose Go type is on its list of credential types. A field of type `json.RawMessage`, `map[string]any`, or `any`, and a response built as text, is opaque to the walk: the test fails on it unless the map lists the field with the reason it holds no credential, such as `workspacePlan` and `runtimeOptions`, which echo the request.
- **No stored credential, contract.** A contract test replays each stored route and tool and fails when the stored body holds a JSON key that the static test's name rule matches, which covers the opaque fields and the responses built as maps.
- A handler whose response carries a credential calls `idempotencymw.DoNotStoreCredential`, and its route or tool is left off the stored routes.
- **Refusals that are not stored.** The gateway does not store any `401` response; a response whose code is `UPLOAD_TOKEN_INVALID`, `UPLOAD_TOKEN_EXPIRED`, `UPLOAD_TOKEN_MISMATCH`, or `UPLOAD_TOKEN_CONSUMED`; a `400 VALIDATION_ERROR` whose `details.reason` is `digest_in_use` (change 0182, US-2); a `403` response whose code is `FORBIDDEN`, `USER_INVALIDATED`, or `ERASURE_IN_PROGRESS`; a response whose code is `OPERATION_NOT_PERMITTED_FOR_RESOURCE`; or a `429` response whose code is `QUOTA_EXCEEDED`. It releases the pending claim, as it does for a `5xx`, so a retry after the condition clears runs the operation, and a refusal that another credential of the subject provokes with a predicted key cannot occupy the key. An MCP tool error is not stored already.

### IP-9 The rework of the branch

The branch's build of decisions 8, 10, and 11 is reworked mostly by deletion:

- Deleted: the `rch` claim and the self-rotation decision at the mints (`pkg/tokenservice`, `pkg/gateway/api/admintoken`, `pkg/credential/impersonation`, `pkg/gateway/playground`), `jwt.ChainOf`, `pkg/auth/jwt/clientclaim.go` and `jwt.ClientClaimVerifier`, `authmw.Principal.Chain`, the `--oidc-client-id-claim` flag, the `auth.oidc.clientIdClaim` chart value and schema, the reason `principal_chain_required`, and their tests (`idp_client_claim_test.go`, the rotation-chain tests of the token service, the playground, the contract tier, and the security tier).
- Kept: `authmw.Principal.Source` and the source each verifier reports, the clearing of `act` in `normalizeUserBearer`, the route resolution, the scope digest and migration 0208, the per-route admit hooks, GWSEAM-AdmitToolReplay, and the refusals that are not stored.
- Added: the session dimension from `Principal.SessionID`, the explicit environment of an MCP call kept as a path parameter, the attribution columns and the metric, the lookup inside the pipeline after `guard` for every stored REST route, GWSEAM-AdmitRouteReplay with the checks of IP-7, the checks of `lenny/delegate_task` and `lenny/create_and_start_session` in GWSEAM-AdmitToolReplay, the `/mcp` storage allow-list of `tools/call`, the static and contract tests of stored responses, and the refusals of IP-8 that decision 8 did not list.

### IP-10 The `lenny-ops` records (step 0178.S6)

`lenny-ops` keeps idempotency records of its own in `ops_idempotency_keys` through `pkg/ops/opsidem`, keyed by the key and the caller's subject (§25.4 **Idempotency**). Decision 19 brings them under the rules this change states for the gateway:

- **Match.** A record is matched on the key, the caller, the endpoint (`method path`), and a SHA-256 of the request body. A request with a stored key whose endpoint or body hash differs answers `422 IDEMPOTENCY_KEY_REUSED`, and is not executed.
- **No stored credential.** A response that carries a credential is not stored. The impersonation proxy (`admin.start_impersonation`, which returns `bearerToken`) and every tool or route whose response carries a token mark the response, and `opsidem` releases the claim instead of storing it, so a retry executes again and writes its own audit row.
- **Replay through the MCP bridge.** A replay through `/mcp/management` runs the bridge's scope gate for the stored tool before it returns the stored response, so a token without the tool's scope is refused.
- §25.4 states the three rules.

An in-depth review of `pkg/ops/opsidem` against the gateway's idempotency design, which the owner asked about with decision 19 and which started on 2026-11-27, completes this section's scope. Step 0178.S6 starts after it reports.

## Ordering

Step 0178.S6 depends on no other step and waits only on the review of IP-10.

Step 0178.S3 merges to `main` only with or after step 0182.S1. Without 0182.S1, a create response still carries the upload token that 0173 restores on replay, and the scope of IP-1 returns it to every credential of the subject that knows the key and the body, including an `a2a_delegation` token that a third-party agent holds and a playground bearer. That is `main`'s behaviour today, so the order adds no regression, but it leaves the intent of decision 5 unmet. Step 0178.S4 can be built on the branch beside the rework, and it merges with 0178.S3.

## Overlaps with other changes

- **0182.** Change 0182 removes the upload token from every response and retires 0173's restore. This change relies on it for the absence of a credential in stored create responses (see "Ordering").
- **0166 DP-1.** A `session.create` that a runtime calls with an idempotency key under DP-1 is deduplicated by the child row the gateway stores under the parent's session (DPR-9), and files no §11.5 record. A pod-path call to a §11.5 operation, such as `lenny/delegate_task` over `LNK-GWCONTROL` today, files a §11.5 record under the `agent` source and the calling session. The two mechanisms do not read each other's records, so a parent that sends one key through both runs the operation once on each. DP-1 gives `session.send` an `idempotencyKey` as well, which 0166 dedupes by the first send's outcome; a pod-path send that reaches the REST message route with the `Idempotency-Key` header files a §11.5 record under the `agent` source and the calling session.
- **0138's removal of the MCP `idempotencyKey`.** When 0138 removes the MCP API, the `idempotencyKey` tool argument, the MCP hook, and the `/mcp` storage allow-list leave with it, and the pod-path platform tools lose §11.5 deduplication. A runtime then deduplicates child creation through DP-1's `session.create` key, and a client through the REST `Idempotency-Key`. The `agent` source stays for any pod-path call that reaches a REST route with the header.
- **0138 R5.** R5, the "Idempotency [design]" paragraph, and the §11.5 row of 0138's table of specification changes state the scope of decision 8, with the rotation chain. Under decision 12, the scope in all three becomes the record scope of IP-1: the bound session of decision 3 stays, R5 keeps its effective audience and its operation check, and the chain sentences and the `clientRef` note about a change of chain leave R5. Revision 18 of 0138 makes that edit.
- **0182.** Decision 1 of change 0182 chose design C for both changes; decisions 12 to 17 here state what it means for this change.

## Worked example

`alice` and `bob`, two users of tenant `acme`, each send `POST /v1/sessions` with `Idempotency-Key: k1` through their own Lenny tokens; `alice`'s body carries `uploadTokenDigest` `d-a` and `bob`'s carries `d-b`. `alice`'s record is filed under `acme`, `alice`, and the digest of `(lenny, "", POST, POST /v1/sessions, {}, "", k1)`, with `stored_by_jti` her token's `jti`; `bob`'s under his own subject, so he creates his own session. `alice`'s token expires, she obtains a new one by any means, and her retry with `k1` and the same body replays her stored response, which holds the session id and no credential; the metric counts the replay under another `jti`. A delegation child minted for `alice`'s session `s0` carries `session_id` `s0`, so its records are filed under `lenny` and `s0` and never meet hers. A platform admin's impersonation bearer for `alice` that sends `k1` with her body passes the scope and is refused at replay by the creating-caller check. A playground bearer of `alice` whose playground does not list the runtime of her body is refused at replay by the §27.5 runtime boundary. `alice` then sends `POST /v1/sessions/s1/finalize` and `POST /v1/sessions/s2/finalize` with one key and empty bodies, and each runs, because the path parameter differs. A day later `alice` deletes `s1`, and a replay of its finalize answers `404` from the ownership check. An hour later a tenant admin erases `alice`; the erasure job deletes every record under `(acme, alice)` and leaves `bob`'s.

## Limits, and alternatives rejected

- Every credential of one subject that knows a key and the exact body shares a record: a second device, a narrowed token for the same audience, a service account's refreshed token, and the development header path. What it receives is the stored response, which holds no credential, after the route's authorization checks. The attribution columns and the metric record that it happened.
- A credential of the same subject that knows or predicts a key can claim it first, which answers the owner's request with `422 IDEMPOTENCY_KEY_REUSED` (the body differs, if only by the upload digest), `409 IDEMPOTENCY_KEY_IN_FLIGHT`, or a pending row that blocks the key ([APICONV-I9](../../issues/APICONV/APICONV-I9-a-stuck-idempotency-claim-blocks-its-key-for-24-hours.md)). Keys that 0138's adapters derive, such as an A2A `messageId` or a digest of a provider request, are predictable. The same credential can already cancel or message the user's sessions.
- Two applications of one user that send an identical request through the same adapter with a key derived from the request collapse into one record. The effective audience keeps different adapters apart and protects no credential of the subject from another: under `audienceRule: resourceOrRest`, a `rest` token enters an adapter's scope with the `Lenny-Resource` header.
- Stored responses outlive what they describe and have no size limit ([APICONV-I15](../../issues/APICONV/APICONV-I15-stored-idempotency-responses-outlive-what-they-describe-and-have-no-size-limit.md)).
- The attribution metric does not see a replay between two credentials that carry neither a `jti` nor a client claim (IP-4).
- Clearing `act` at boundary normalization drops an actor that the identity provider names. Lenny never trusted that claim for access, so nothing is lost but the record of it.
- Until step 0178.S6 lands, the `lenny-ops` records of §25.4 still store and replay credentials ([APICONV-I16](../../issues/APICONV/APICONV-I16-ops-idempotency-replays-the-impersonation-bearer-by-subject.md), decision 19).
- Decision 8's rotation chain was the design of revisions 7 to 10. Decision 12 withdrew it under design C, which the owner chose over designs A, A2, and V1, each of which kept the chain (decision 1 of change 0182).
- Restoring the upload token on replay only to the storing credential's `jti` until 0182 lands, which the security review offered in place of the merge order, is not proposed: "Ordering" keeps the chain's purpose by holding the merge instead of building a mechanism that 0182 deletes.
- Option (b) of decision 1, keeping per-tenant scoping, was not taken.

## Specification and non-specification changes

- `spec/11_policy-and-controls.md`: §11.5, **Mechanism.**, the stored routes, the replay admission, the responses that are not stored, and the attribution; §11.5 **Retry across a credential refresh.** removed.
- `spec/13_security-model.md`: §13.3, **Rotation chain.** and the `rch` row of the claim table removed.
- `spec/10_gateway-internals.md`: §10.2, the `user_bearer` normalization clears `act`, and what that drops; **Client claim of an identity-provider token.** removed.
- `spec/17_deployment-topology.md`: §17.4, the chain of the development header path, and §17.8.1, the `auth.oidc.clientIdClaim` row, removed.
- `spec/25_agent-operability.md`: §25.4 "Calling the Gateway", the chain of a service account's call removed; §25.4 **Idempotency**, the match on the endpoint and the body hash, the responses that are not stored, and the scope gate before a replay through the MCP bridge (step 0178.S6).
- `spec/27_web-playground.md`: §27.3.1, the playground chain removed.
- `spec/12_storage-architecture.md`: §12.8, the erasure-scope row and `DeleteByUser` step 17.
- `spec/16_observability.md`: §16.1, `lenny_idempotency_replay_other_credential_total`; §16.7, the `session_mint_replay` value of `exchange_type`.
- `spec/interfaces/gateway-seams.md`: the summary of GWSEAM-RequestCaller, GWSEAM-AdmitToolReplay, and the new GWSEAM-AdmitRouteReplay.
- `migrations/`: 0206 (steps 0178.S1 and 0178.S2), and 0208 for the scope digest and the attribution columns.
- `pkg/auth/jwt`, `pkg/auth`, and `pkg/auth/authmw`: the source each verifier reports, `Principal.Source`, and the normalization; the chain code removed.
- `pkg/tokenservice`, `pkg/gateway/api/admintoken`, `pkg/credential/impersonation`, and `pkg/gateway/playground`: the `rch` claim removed.
- `pkg/idempotency`, `pkg/idempotency/idempotencymw`, and `pkg/idempotency/idempotencymw/pgstore`: the scope, its digest, the stores, the attribution, the route resolution, the admit hook, the storage allow-list, and the refusals that are not stored.
- `pkg/gateway/api/pipeline`: the idempotency lookup after `guard` in the client listener's gate, and the call of GWSEAM-AdmitRouteReplay.
- `pkg/gateway/platformtools/mcp`: the MCP scope, the allow-list, and the admit hook.
- `pkg/gateway/seam`: the caller's source and session dimension, and GWSEAM-AdmitRouteReplay.
- `pkg/auth/callercheck` and `pkg/gateway/core/session/sessionserver`: the replay checks of IP-7, served through GWSEAM-AdmitRouteReplay and GWSEAM-AdmitToolReplay.
- `cmd/lenny-gateway`: the verifier tags, the resolvers, the admit hooks, and the wiring of the lookup inside the pipeline; the `--oidc-client-id-claim` flag removed.
- `charts/lenny` and `pkg/chart/values`: the `auth.oidc.clientIdClaim` value and its schema removed.
- `pkg/ops/opsidem` with its Postgres store, `pkg/ops/opsserver`, and `pkg/ops/mcp`: the match on the endpoint and the body hash, the credential mark and the release of the claim, and the scope gate before a replay (step 0178.S6); a migration of `ops_idempotency_keys` for the body hash, numbered when the step is built.

## Documentation

- `docs/api/rest.md` (client developer): the idempotency section states the scope by subject, source, bound session, route, and path parameters; that a retry after any token refresh replays; the refusal `principal_subject_required`; the authorization checks a replay runs; the stored routes; the responses that are not stored; and that another credential of the same user that knows the key and body receives the stored response, which holds no credential.
- `docs/api/mcp.md` (client developer): the same for `idempotencyKey`, that only the `tools/call` requests of `lenny/create_session`, `lenny/create_and_start_session`, and `lenny/delegate_task` are stored, and that the explicit-environment endpoint separates records by environment.
- `docs/client-guide/authentication.md` (client developer): the retry-cost text and the rotation example of revisions 8 to 10 removed; a refresh by any means keeps replay; an IdP token's `act` claim is cleared and names no actor in Lenny.
- `docs/operator-guide/security.md` (operator): the `idempotency_keys` key and the attribution columns, migration 0208 and its upgrade window, the erasure of a user's records, the metric `lenny_idempotency_replay_other_credential_total`, what a rise means, and the replays it cannot see, the clearing of an IdP token's `act` claim, that the `lenny-ops` records are outside these rules, that the effective audience protects no credential of a subject from another, and the limits of "Limits, and alternatives rejected"; the rotation-chain and client-claim text removed.
- `docs/reference/configuration.md` (operator): the `auth.oidc.clientIdClaim` row removed.
- `docs/reference/metrics.md` (operator): the new metric with its labels.
- `docs/operator-guide/agent-operability.md` (operator, and the author of an agent that calls `lenny-ops`): the "Idempotency" section states that a key is matched with the endpoint and the body, the `422 IDEMPOTENCY_KEY_REUSED` on a mismatch, that a response carrying a credential, such as an impersonation bearer, is not stored and a retry executes again, and that a replay through the MCP bridge needs the tool's scope (step 0178.S6).
- No reader-facing behavior changes: GWSEAM.

## Tests

- Unit, `pkg/idempotency`: validation refuses an empty source; the digest separates every scope dimension and is stable under path-parameter order. Cites §11.5 (Idempotency).
- Unit, `pkg/auth/jwt` and `pkg/auth/authmw`: normalization clears `act`; each verifier and `MultiVerifier` report the source. Cites §10.2 and §11.5.
- Unit, `pkg/idempotency/idempotencymw`, `pkg/gateway/platformtools/mcp`, and `cmd/lenny-gateway`: a retry after a refresh, a re-narrowing, a new login, and an IdP refresh replays; a delegation child with a session id and the pod's own call under that session file apart; one key on two routes, two sessions, or two explicit environments of `/mcp` runs twice; a keyed request with no subject is refused with `401` before any claim; the route resolver reaches the inner mux; the attribution columns are written, and the metric counts a replay by another `jti` or another client and not one between two credentials with neither. Cites §11.5.
- Unit, `pkg/gateway/api/pipeline`: a replay on a session route by a caller whom the permission gate or the ownership check refuses receives the refusal and leaves the record; a replay of a create route by a caller without `manage_own_sessions` is refused; the lookup sees the path values; the forward listener's gate does no lookup; the outer middleware and the pipeline compute one scope digest for each stored route. Cites §11.5 and §10.2.
- Unit, `pkg/gateway/core/session/sessionserver`: GWSEAM-AdmitRouteReplay refuses a create replay for a caller outside the stored environment, for a playground caller whose `allowedRuntimes` omits the stored runtime, and for a fenced caller; GWSEAM-AdmitToolReplay refuses a `lenny/delegate_task` replay by a caller that does not own the parent. Cites §11.5, §10.6, and §27.5.
- Unit, `pkg/gateway/platformtools/mcp`: a keyed call to a tool outside the allow-list, and a keyed `tools/list`, run twice and store nothing. Cites §11.5.
- Static, `tests/static`: every stored route and tool has an entry in the test's map, and no response type has a credential field or an unlisted opaque field. Cites §11.5.
- Component, `tests/component/stores` and `tests/component/migrations`: the Postgres store keys rows by tenant, subject, and scope digest and writes the attribution columns; migration 0208 empties the table, drops `session_id`, keys the table by the digest, adds the columns, and rolls back to the key of 0206. Cites §11.5.
- Contract, `tests/contract/rest_idempotency`: through the auth middleware's bearer path, a replay across a token refresh, a replay refused by the ownership check of a session route, a replay of a deleted session's finalize answering `404`, a refusal that is not stored for each code of IP-8, the stored bodies of every stored route and tool free of credential keys, and the `/mcp` allow-list. Cites §11.5.
- Security, `tests/security`: an impersonation bearer that reuses the user's key and body is refused at replay; a credential that lost access to a session receives the ownership refusal on a replay of its finalize; a stored `/mcp` read-tool result does not exist to replay; a predicted key with a missing upload secret does not store the `401`. Cites §11.5.
- Unit, contract, and security (step 0178.S4): an invalidated, restricted, or refused bearer's replay is refused on every stored route and leaves the record; a refused replay writes the `session_mint_replay` row; a retry after a quota refusal runs; an admitted replay returns the stored response. Cites §11.5.
- Unit and component, `pkg/ops/opsidem` and its Postgres store, and contract, the `lenny-ops` server (step 0178.S6): the same key on another path, and the same key with another body, answer `422 IDEMPOTENCY_KEY_REUSED`; a replay of `admin.start_impersonation` executes again and writes its `admin.impersonation_started` audit row; a token without a tool's scope is refused on a replay through `/mcp/management`; an admitted replay returns the stored response. Cites §25.4.
- Each isolation case has a mutation check: the test fails when the dimension or the check that separates it is removed.
