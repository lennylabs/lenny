# Non-spec changes — Session-capability and delegation child tokens are never minted, signed, or recorded

## Design (implementation-facing)

The code implements these rules that spec-changes.md **Design (as the spec must state it)** names:

- **Parent-terminal fence rule** (§12.8 "Lifecycle fence and drain", SPEC-3). CODE-2 adds it to the creation fence in `sessionstore`, `pgstore`, and `memstore`. The fence's existing `*sessionstore.CreationRefusal` carries it to `lenny/delegate_task`, which already surfaces a refusal's code and details.
- **Session-creation caller check** (§10.2 "Signing topology", SPEC-1 Edit (b)). CODE-3 adds one function that both `sessionserver.requireActiveUser` and the `lenny/delegate_task` handler call.
- **Child identity** (§8.2, SPEC-2). It needs no new code. CODE-1 deletes the unsigned child-token minter and every path that only it fed.

CODE-4 regenerates the alert description that restates the §16.5 `TokenStoreUnavailable` row. Every sketch locates code by symbol and file. The line numbers in the problem statement are evidence and drift as other proposals land.

## Staged code changes

### CODE-1 · Delete the unsigned child-token minter and the audit-contention path

Targets:
- `pkg/gateway/mcpfabric/delegation/childtoken/` (`childtoken.go`, `childtoken_test.go`): delete the package.
- `pkg/gateway/mcpfabric/delegation/mint.go`: delete `ParentToken`, `ChildTokenParams`, `ChildToken`, `ActClaim`, `ChildTokenMinter`, `ChildTokenAudience`, `DefaultChildTokenTTL`, `ErrParentRevoked`, and `ErrAuditContention`. Delete the file when nothing remains in it.
- `pkg/gateway/mcpfabric/delegation/mint_wiring_test.go`: delete.
- `pkg/gateway/mcpfabric/delegation/service.go`: delete `Request.ParentToken`, `Result.ChildToken`, `Options.ChildTokenMinter`, the `tokenMinter` field, the mint block in `buildChildSession` (which then returns no `*ChildToken`), and the `child_token_jti`, `act_chain`, and `delegation_token_scope` detail keys in `emitSpawnedAudit`.
- `pkg/gateway/mcpfabric/mcptools/mcptools_register.go`: delete the `parentToken` construction in the `delegate_task` handler and the `ErrParentRevoked` and `ErrAuditContention` branches of its error mapping.
- `pkg/gateway/externalapi/errorclassify/errorclassify.go`: delete the `DELEGATION_AUDIT_CONTENTION` entry. Keep `DELEGATION_PARENT_REVOKED`.
- `cmd/lenny-gateway/mcpsurface.go`: delete the `childTokenMinter` construction and the option that passes it. Keep `revCache`, which the revocation propagator and the auth middleware share.
- `pkg/gateway/middleware/auth/auth.go`: rewrite the `Principal.JTI` comment so it no longer names the delegation `ParentToken`; it states that the field is the bearer's `jti` and is empty on the pod path.
- Comments and the served description that restate removed spec text. Every comment, `spec:` annotation (test annotations included), and error-string suffix under `pkg/`, `cmd/`, and `charts/` that quotes or paraphrases the §10.2 sentence SPEC-1 Edit (f) deletes, or cites §10.2 for the signature, expiry, audience, or TokenReview check of the projected SA token a pod presents on `LNK-GWCONTROL`, cites §10.3 "Projected SA token" instead; grep `cannot forge`, `TokenReview`, `SA token`, and `10.2 line 227` near `10.2` to find them. Every comment and `spec:` annotation that cites §8.2 for the child-token exchange or minter, its actor-token re-read, or a playground bearer as a delegation's `subject_token` stops citing it (grep `child-token exchange`, `child-token minter`, and `delegation from a playground session`; `8.2` in `pkg/tokenservice`, `pkg/gateway/storage/issuedtokenstore`, and `tests/tier3_contract/oauth_token`; and `13.3` and `8.2` on one line elsewhere under `tests/`): the `ErrParentNoUser` comments cite §8.2 "Child identity", the `revCache` comments name only the auth middleware and the revocation propagator, and the token-service and `issuedtokenstore` comments and the token-exchange test annotations cite §13.3 alone. The `/internal/runtimes/{name}/meta/{key}` bullet in `cmd/lenny-gateway/authposture.go` and the `MetadataVisibility` doc comment in `pkg/gateway/runtime/runtimestore/runtimestore.go` follow SPEC-5 Edit (m) and name an authenticated caller rather than a session JWT. The `upload-to-session` operation description in `pkg/gateway/externalapi/openapi/openapi.json` and the `UploadToSessionRequest` doc comment in `pkg/gateway/sessionserver/upload_to_session.go` follow SPEC-5 Edit (o) and name the caller's bearer. Regenerate `pkg/embedded/manifests/manifests.yaml` with `make generate`.
- `tests/spec-map.json`: in the §8.2 entry, remove `"pkg/gateway/mcpfabric/delegation/childtoken"`, `"pkg/gateway/storage/issuedtokenstore"`, and `"pkg/tokenservice"` from `"packages"`, and every test whose name ends in `_spec_13_3` from `"tests"`. The §13.3 entry already lists each of them.
- `tests/registers/change-graph-coverage.yaml`: remove the `pkg/gateway/mcpfabric/delegation/childtoken/` entry.

Stop condition: run grep for `ChildToken`, `childtoken`, `ParentToken`, `child_token_jti`, `act_chain`, `delegation_token_scope`, `ErrParentRevoked`, `ErrAuditContention`, `DELEGATION_AUDIT_CONTENTION`, `child-token exchange`, and `child-token minter` across `pkg`, `cmd`, `sdks`, `tests` (including `tests/spec-map.json` and `tests/registers`), and `schemas`. Stop when only the sessionstore `CreationRefusal` constant `DELEGATION_PARENT_REVOKED` (CODE-2) and unrelated `a2a_delegation` code in `pkg/tokenservice` remain.

**Ordering.** CODE-1 lands after CODE-2 and in the same proposal merge as SPEC-2, SPEC-4, and CODE-2, so `lenny/delegate_task` never runs in a state where §8.2 still requires the exchange or `DELEGATION_PARENT_REVOKED` has no producer.

### CODE-2 · Creation fence refuses a delegated child of a terminal or absent parent

Targets:
- `pkg/gateway/session/sessionstore/creation_fence.go`
- `pkg/gateway/session/sessionstore/pgstore/creation_fence.go` and `pgstore.go` (`create`)
- `pkg/gateway/session/sessionstore/memstore/fence.go`
- `pkg/gateway/sessionserver/usage.go` (`cascadeToChildren`)
- `pkg/gateway/mcpfabric/mcptools/mcptools.go` (`cancelSubtree`) and its call in the `cancel_child` handler in `mcptools_register.go`

Changes:
1. `sessionstore.CreationMarkers` gains `ParentSessionID string` and `ParentTerminal bool`. Add the constant `RefusalDelegationParentRevoked = "DELEGATION_PARENT_REVOKED"`.
2. `CreationMarkers.Refusal` gains rule 5, evaluated after the four existing rules (§12.8 refusal order, SPEC-3 Edit (b)): when `m.IsDelegatedChild && m.ParentTerminal`, return a `*CreationRefusal` with `Code: RefusalDelegationParentRevoked` and `Details: {"parentSessionId": m.ParentSessionID}`. Its HTTP status is 403, as for every fence refusal.
3. pgstore: `readCreationMarkers` and `fenceInsert` take the parent session ID next to `delegatedChild`. When `delegatedChild` is true, `readCreationMarkers` runs, after the tenant and user reads, `SELECT state FROM sessions WHERE tenant_id = $1 AND id = $2::uuid FOR SHARE`. A terminal state sets `ParentTerminal`. No row sets `ParentTerminal`, so the refusal comes before the `parent_session_id` foreign-key error. Any other read error fails the insert closed with a wrapped error.
4. `create()` in `pgstore.go` passes `sess.ParentSessionID`. `Reserve` passes an empty parent ID and reads no parent row: the reservation runs only the tenant and user rules (SPEC-3 Edit (c)).
5. memstore makes the same check under its mutex in `fence.go`, next to the existing `Refusal` call, reading the parent from its own map. The reservation path in memstore also skips the parent rule.
6. The code comment on the parent read states that the `parent_session_id` foreign key already takes `FOR KEY SHARE` on the parent row at INSERT, so the `FOR SHARE` read keeps the existing lock order (tenant, user, parent), and that the terminal writer's `SELECT ... FOR UPDATE` in `pgstore.Update` serializes against it. Cite `// spec: §12.8 (Lifecycle fence and drain), §8.2 (Parent freshness)`.
7. Both §8.10 cascade walks, `cascadeToChildren` and the `lenny/cancel_child` walk `cancelSubtree`, read from the primary (`ListFilter{Primary: true}`) once per tree level. Each lists after the parent's terminal commit and again after each level's terminal writes return, and queues a cancelled descendant's children only from a listing taken after that descendant's terminal write committed (SPEC-3 Edit (a)). In `cancelSubtree` the parent is the `cancel_child` target, and the handler's pre-write `ListByRoot` keeps serving only authorization and `treeRoot`. Cite `// spec: §12.8 (Lifecycle fence and drain), §8.10`.

**Trap: the terminal set.** Use the store's existing terminal-state predicate. Do not compare against `running`: a parent in another live state must not refuse its child (summary decision D7).

**Trap: the early gate.** `requireActiveDelegator` in `mcptools.go` builds markers for its early check. It must leave `ParentTerminal` unset, so the insert transaction stays the only producer of `DELEGATION_PARENT_REVOKED`. Admission's `ErrParentNotRunning` stays as it is.

**Trap: undo on refusal.** `commitChild` in `delegation/service.go` undoes the file export on a `CreationRefusal` only on the reserved path. Confirm that the unreserved path writes nothing under the child ID before the insert. If it does, add the same undo there.

### CODE-3 · Session creation applies the step (1a) caller check

This code applies §10.2 "Signing topology" (SPEC-1 Edit (b)). It re-anchors the refusal that proposal 0102 D20 and D22 approved; see the summary's **Impacts on other proposals**, row 0102.

Targets:
- `pkg/gateway/callercheck/callercheck.go` (new) and its test.
- `tests/change-graph.json`: a glob key `pkg/gateway/callercheck` whose `unit` list holds `pkg/gateway/callercheck/...`, `pkg/gateway/sessionserver/...`, and `pkg/gateway/mcpfabric/mcptools/...`, and whose `contract` list holds `tests/tier3_contract/rest_mcp_consistency/...`.
- `pkg/gateway/sessionserver/user_gate.go` (`requireActiveUser`).
- `pkg/gateway/sessionserver/sessionserver.go` (the two new dependencies).
- `pkg/gateway/mcpfabric/mcptools/mcptools_register.go` (the `delegate_task` handler) and the mcptools `Deps` that carries the same two dependencies.
- `cmd/lenny-gateway/httpsurface.go` and `cmd/lenny-gateway/mcpsurface.go` (wiring).

Do not edit `create.go`, `create_and_start.go`, `derive.go`, or `replay.go`. Each already calls `requireActiveUser`. MCP `lenny/create_session` reaches it because it runs `handleCreate` through `CreateSessionService`, and the translator single-shot path reaches it through `sessionSingleShotBinder.BindSingleShot` → `CreateAndStartService`.

1. **`callercheck.Check(ctx, deps Deps, p auth.Principal, at time.Time) error`.** `Deps` holds `IssuedTokens`, a consumer-defined interface with `Get(ctx, tenantID, jti string) (issuedtokenstore.IssuedToken, error)`, and `Audit`, a consumer-defined interface with one method that writes a `token.exchanged` row for a tenant. The rules, in order:
   1. A nil `IssuedTokens` returns nil. No impersonation route is registered without the pool (0102 D17).
   2. An empty `p.JTI` returns nil. This is the pod path over `LNK-GWCONTROL`.
   3. `issuedtokenstore.ErrNotFound` returns nil. Any other read error returns a wrapped internal error.
   4. When `row.SourceRefusal(issuedtokenstore.SourceSubjectToken)` is non-nil, write the rejection row with `exchange_type: session_mint`, `subject_token_sub` = `p.Subject`, `subject_token_jti` = `p.JTI`, `policy_result: rejected:<Reason()>`, and no `issued_token_jti`. When the write succeeds, return `*callercheck.Refused{Reason}`. When it fails, or `Audit` is nil, return a wrapped internal error carrying the intended reason.
   5. Otherwise return nil.

   **IMPLEMENTOR'S CHOICE:** the production `Audit` adapter. It takes the §11.7 per-tenant audit advisory lock and commits the row before it returns, as the Token Service `Auditor` behind `rejectRevokedSource` does (for example `auditstore.AcquireLockInTx` then `auditstore.AppendInTx` in one transaction).
2. **`requireActiveUser`.** Call `callercheck.Check` first, before the `s.users == nil` early return and before the §12.8 early gate, so the refusal precedes any claim or store write. Map `*Refused` to `403 FORBIDDEN` and any other error to `500 INTERNAL_ERROR`. Do not add the call to `requireActiveUserForAction`, which interaction resolution also uses.
3. **`delegate_task`.** Call `callercheck.Check` with the request principal before `delegation.Service.Delegate`, so no admission, reservation, export, or pod claim runs for a refused bearer. Map `*Refused` to the `FORBIDDEN` tool error and any other error to `INTERNAL_ERROR`. A pod-path call carries no JTI and passes by rule 1.2.
4. **Wiring.** Set `IssuedTokens` to `issuedtokenstore.New(w.pgPool)` and `Audit` to the adapter when the pool is non-nil, on both the sessionserver and the mcptools `Deps`.

**Sequencing trap.** `IssuedToken.SourceRefusal` is added by 0102 CODE-2 item 7, so this code does not compile before that item lands. The check refuses nothing until 0102 CODE-3 item 3, which writes the impersonation bearer's `issued_tokens` row through `RecordImpersonation`, lands, because every lookup before then returns `ErrNotFound`.

### CODE-4 · Alert description drops delegation minting

Target: `pkg/alerting/rules/rules.go`, the `TokenStoreUnavailable` `Description`. Replace "session creation, delegation minting, and credential leasing are all failing" with "session creation and credential leasing are both failing", mirroring SPEC-5 Edit (h). Regenerate with `make generate`, and confirm that `charts/lenny/files/alerting-rules.yaml`, `docs/alerting/rules.yaml`, and `pkg/embedded/manifests/manifests.yaml` carry the new text and that the generator drift check passes.

## Staged schema, chart, and migration changes

None beyond comments. CODE-1 rewrites comments in `charts/lenny/templates/gateway-deployment.yaml` and `charts/lenny/tests/gateway-deployment_test.yaml`, and CODE-4 regenerates the bundled alert rule file. No chart value or rendered resource changes.

## Staged docs changes

### DOCS-1 · Published docs follow the spec

Each table row stays one physical line. No page cites a spec section number.

- `docs/reference/error-catalog.md`, the `DELEGATION_PARENT_REVOKED` row: the parent session was terminal or absent when the child session row was inserted. No child session is created and no pod is allocated. Only `details.parentSessionId` is included; drop `revocationReason`. Not retryable. The catalog has no `DELEGATION_AUDIT_CONTENTION` row. In the `UPLOAD_TOKEN_CONSUMED` row, replace "use the session bearer credential" with "use the caller's bearer token".
- `docs/client-guide/authentication.md`, the paragraph that begins "For token lifecycle operations inside Lenny": remove ", or any internal delegation child-token issuance" and keep the list grammatical.
- `docs/api/admin.md`, under `POST /v1/oauth/token`: delete the "Gateway (internal)" caller row. In the sentence "For delegation child-token minting, additionally supply `actor_token=<parent_session_token>` and a narrowed `scope` string.", retarget to `a2a_delegation` child-token minting with `actor_token` set to the delegating caller's token. In the paragraph that begins "The caller token", delete ", as it does when a playground session delegates".
- `docs/getting-started/architecture.md`, the paragraph that begins "Externally, token issuance": retarget "delegation child-token minting" and the "parent session token in the `actor_token` field" sentence to the external `a2a_delegation` exchange. Remove "and the session-capability tokens of the session-creation path" and keep the list grammatical. Add that internal delegation through `lenny/delegate_task` mints no token.
- `docs/operator-guide/security.md`, item 3 "**Bearer tokens**": change "delegation child tokens" to "`a2a_delegation` child tokens" and "(credential leasing, delegation minting)" to "(credential leasing)". Remove the session-capability clause from the in-process list. Add: "An impersonation bearer cannot create a session; the refusal is audited." In the same change, move "session-capability tokens of the session-creation path" from `contains` to `notContain` in the `3. **Bearer tokens**` case of `tests/tier11_docs/shared_jwt_signing_docs_test.go`.
- `docs/client-guide/sdk-examples/curl.md`, the sentence that begins "For delegation child-token minting": retarget to `a2a_delegation` child-token minting with `actor_token` set to the delegating caller's token.
- `docs/reference/configuration.md`: in the `tokenExchange.rateLimitPerUser` row, replace "Rotation + delegation minting share this budget." with a sentence naming rotation and `a2a_delegation` child-token minting. In the `tokenExchange.requireActorToken` row, retarget "delegation child-token minting" to `a2a_delegation` child-token minting. Leave the external `actor_token` lifetime paragraph unchanged.
- `docs/tutorials/user-credentials.md`: replace "including `actor_token` for delegation minting" with "including `actor_token` for `a2a_delegation` child-token minting".
- `docs/api/internal.md`, under "Identity verification": replace the bullet that begins "Pods cannot forge or extend session JWTs" with "The gateway validates the signature, expiry, and audience of the pod's projected ServiceAccount token on every pod-to-gateway request."
- `docs/runbooks/jwt-key-rotation.md`, the "KMS Sign quota" section: in Step 1, replace "a burst of session creation on the gateway" with "a burst of playground-bearer or impersonation mints on the gateway". In Step 3, replace "the sum of the session creation rate, the delegation rate, the token renewal rate of every holder that renews before expiry, and infrequent operator mints" with "the sum of the token renewal rate of every holder that renews before expiry and infrequent operator mints".
- `docs/operator-guide/observability.md`, the `TokenStoreUnavailable` row, and `docs/runbooks/token-store-unavailable.md`, the front-matter symptom `"delegation minting fails"` and the body paragraph that begins "The Postgres-backed token store": remove delegation minting. Keep "session creation", because the CODE-3 check fails closed on an `issued_tokens` read error.

### REC-1 · Records

Targets: `BUILD-GAPS.md` F-10.2.25, F-8.2.31, and one new finding; `TEST-GAPS.md` T-8.2.7 and T-10.2.2; `tests/tier11_docs/build_gaps_jwt_signing_records_currency_test.go`; `gateway-runtime-comms-remediation.md` §10.2, the F-10.2.25 and F-8.2.31 queue entries, the "session capability and delegation child token proposal" routing text, and one new queue entry. CODE-1 owns the `tests/spec-map.json` and `tests/registers/change-graph-coverage.yaml` removals.

- Close F-10.2.25. The session-creation mint is removed from §10.2, and the renewal half is moot under D1. Close F-8.2.31. Child identity is the session row, and the §12.8 fence enforces parent freshness. Each closure points to 0104 and replaces the stale **Suggested resolution:** text.
- Close T-8.2.7 against the Testing section's **Fence rule model**, **Fence race**, and **Delegation flow** entries.
- Close T-10.2.2. Its throttle and breaker half was already covered by 0097. Its `POST /v1/sessions` half has no spec basis after SPEC-1, and the Testing section's **KMS independence** entry pins that session creation succeeds while the breaker is open. The closure names the **KMS independence** test as `file::Func`, and in the same change the T-10.2.2 case of `TestJWTSigningTestGapRecords_spec_10_2` in `tests/tier11_docs/build_gaps_jwt_signing_records_currency_test.go` becomes resolved, its "`POST /v1/sessions` half stays OPEN" fragment becomes `proposals/0104_fix_two-gaps-leave-lenny-s-session-scoped-and-delegation/`, and its diagnosis comment drops T-10.2.2 from the records that stay open.
- In the remediation plan, mark both entries resolved by 0104.
- File, each only when the implementor confirms it, the findings in the summary's **Defects in the shipped tree that this proposal does not stage**.
- The §28.4 claim register (`tests/claim-map.json`) carries no row for either removed mint, so no existing claim row changes. No row is added for the forwarded-call session binding, because no remediation-plan step closes it; BUILD-GAPS.md and the §10.2 queue entry carry that gap.
- File a new Medium finding in `BUILD-GAPS.md` for the absent binding, under the §10.2 group with the next free `F-10.2.N` identifier, following the BUILD-GAPS conventions for a new open finding (title line, **Gap:**, evidence, and **Suggested resolution:**). **Gap:** the gateway serves a `CH-MCP-PLATFORM` or `CH-MCP-CONNECTOR` call forwarded on `LNK-GWCONTROL` under the session ID the adapter presents, with no check that the session is assigned to the calling pod, so a compromised adapter can act for another session. Evidence: `pkg/gateway/gatewaycontrol/platformtools/platformtools.go` (package comment and `Bridge.CallPlatformTool`) and `pkg/gateway/connectors/connectortools/connectortools.go` (`Bridge.CallConnectorTool`). **Suggested resolution:** a dedicated proposal specifies the binding, covering the `pod_assignment` invariant at every state edge, the RPCs it covers, the dev plaintext listener with no SPIFFE identity, assignment timing relative to the first forwarded call, and the refusal outcome. Owner: that proposal. Add an open queue entry for that proposal to `gateway-runtime-comms-remediation.md` §10.2, citing the new finding.

## Testing

### TEST-1 · Tests for parent freshness, the caller check, and KMS independence

TEST-1 extends the existing harnesses named below. It adds no full-revoke race, because 0104 does not change that behavior. Every test carries a `// spec:` annotation, and every tier-2-and-higher test carries a `// diagnosis:` comment. Each entry asserts what its rule's home states and does not restate the rule.

- **Fence rule model** (tier 1; `pkg/gateway/session/sessionstore/creation_fence_test.go`; §12.8). Add `ParentTerminal` to the `fenceOracle` and `fenceDetails` reference model, and its case count, and to `TestCreationRefusalCrossProduct_spec_12_8`, so rule 5 is checked against every combination of the other markers and ordered after them. Add a focused test: `ParentTerminal` on a delegated child refuses with `details.parentSessionId`, and a depth-0 row with `ParentTerminal` set admits. The discriminating assertion is the depth-0 admit.
- **memstore parity** (tier 1; `memstore` `fence_test.go`; §12.8). The same cases as the fence rule model, plus a reservation that admits while the parent is terminal, and the parent-row cases of the **Postgres fence** entry (CODE-2 item 5).
- **Postgres fence** (tier 2; `tests/tier2_component/stores/sessionstore_creation_fence_test.go`; §12.8). Add parent-row cases to the in-transaction refusal test: a terminal parent and an absent parent each refuse with `DELEGATION_PARENT_REVOKED`, and a parent in a live state other than `running` admits its child (CODE-2 item 3, **Trap: the terminal set**). The absent-parent refusal and the live-state admit are the discriminating assertions. Add the terminal-parent case to the serialization test. Add a fault-injected parent-row read error that fails the insert and writes no row.
- **Fence race** (tier 7a; `tests/tier7a_load_local/creation_fence_race_test.go`; §12.8). Add a parent-terminal race next to `runFenceRound` that reuses `memFenceStores` and `pgFenceStores` and runs no drain. Each round inserts one running depth-0 parent, races delegated `Create`s under it (`DelegationDepth: 1`, `ParentSessionID` set) against one `Update` that moves the parent to a terminal state, and lists the tenant with `ListFilter{Primary: true}` as soon as that `Update` returns. Assert that every admitted child is among that listing's children of the parent, and that every refused create carries `DELEGATION_PARENT_REVOKED` and left no row (SPEC-3 Edit (a)). The listing taken while creates are still in flight is the discriminating assertion.
- **Cascade read source** (tier 1; `pkg/gateway/sessionserver/cascade_on_expire_test.go` for `cascadeToChildren` and `pkg/gateway/mcpfabric/mcptools/cancel_release_test.go` for `lenny/cancel_child`; §12.8, §8.10). Each case runs against both walks. A recording store asserts that the `cancel_all` cascade lists with `Primary: true`. A second case nests a running child under the ended parent (the `cancel_child` target), and a store hook inserts a running grandchild under that child after the first listing returns; the grandchild ending `cancelled` is the discriminating assertion.
- **Delegation flow** (tier 1; `pkg/gateway/mcpfabric/delegation` service tests and `mcptools` `delegate_task` tests; §8.2, §12.8). A store hook moves the parent to a terminal state between admission and `CreateReserved`. Expect the `DELEGATION_PARENT_REVOKED` tool error with `parentSessionId`, no child row, the export undone, and no pod allocation. Assert that `requireActiveDelegator` leaves `ParentTerminal` unset.
- **Caller check** (tiers 1, 3, and 9; `pkg/gateway/callercheck` and `pkg/gateway/sessionserver` tests; §10.2 "Signing topology"). One table drives the REST create, create-and-start, derive, and replay routes, the MCP `lenny/create_session` tool, the single-shot binder, and client-facing `lenny/delegate_task`. Cases: one per rule and branch of CODE-3 item 1, plus a revoked bearer, each asserting the outcome that rule and CODE-3 item 2 or item 3 give. The bypass guard is the table covering every root-creating entry point. A pod-path `delegate_task` with no JTI passes without a store read. At tier 3, a new parity test in `tests/tier3_contract/rest_mcp_consistency` asserts that `POST /v1/sessions` and MCP `lenny/create_session` with an impersonation bearer return the same `FORBIDDEN` code and category. At tier 9, `tests/tier9_security/session_creation_impersonation_refusal_test.go` (new) reuses the `opsRevStack` of `ops_revocation_enforcement_test.go`: `POST /v1/sessions` with an impersonation bearer from `tenantAdmin` returns 403, the tenant's audit chain holds a committed `session_mint` row for the bearer's `jti`, and no session row exists. The committed row is the discriminating assertion.
- **KMS independence** (tier 2 or 8; extend `tests/tier2_component/tokensigning/kms_outcome_test.go` or `tests/tier8_chaos/jwt_keyset_chaos_test.go`; §10.2 "KMS signing failure mode"). `POST /v1/sessions` succeeds while the signer fails and the breaker is open, and the signer records no call from session creation.
- **Catalog code** (tier 1; `pkg/gateway/externalapi/errorclassify/errorclassify_test.go`; §15.1). Add `DELEGATION_PARENT_REVOKED` to `TestCreationFenceCodesArePolicyNonRetryable`.
- **Spec map** (TEST-1 owns these entries). Add the new tests to the `tests/spec-map.json` entries for §8.2, §10.2, §12.8, and §15.1, and add `tests/tier9_security/session_creation_impersonation_refusal_test.go` to the `security` list of the CODE-3 `tests/change-graph.json` key.

## Edge cases and accepted failure modes

- **Caller check before 0102 lands.** The check passes every bearer until 0102 writes impersonation rows (CODE-3 **Sequencing trap**).
- **Parent read contention.** The `FOR SHARE` read waits for an in-flight terminal write on the parent. The wait is bounded by that write's transaction and adds no new lock cycle.
- **Single-bearer revocation.** It does not stop an already running session from delegating (summary decision D8).

## Files touched on application (non-spec)

- `pkg/gateway/mcpfabric/delegation/childtoken/` (deleted), `pkg/gateway/mcpfabric/delegation/mint.go`, `mint_wiring_test.go`, `service.go`, `service_test.go` (CODE-1)
- `pkg/gateway/mcpfabric/mcptools/mcptools_register.go` (CODE-1, CODE-2, CODE-3), `pkg/gateway/mcpfabric/mcptools/mcptools.go` (CODE-2 `cancelSubtree`, CODE-3 `Deps`)
- `pkg/gateway/externalapi/errorclassify/errorclassify.go` (CODE-1)
- `cmd/lenny-gateway/mcpsurface.go` (CODE-1, CODE-3), `cmd/lenny-gateway/httpsurface.go` (CODE-3), `cmd/lenny-gateway/revocation.go` (CODE-1)
- `pkg/gateway/middleware/auth/auth.go` (CODE-1)
- `charts/lenny/templates/gateway-deployment.yaml`, `charts/lenny/tests/gateway-deployment_test.yaml`, `cmd/lenny-gateway/pod_lifecycle.go`, `cmd/lenny-gateway/authposture.go`, `cmd/lenny-gateway/gwcontrol_server.go`, `pkg/gateway/mcpfabric/delegationtree/leasecontrol/satoken.go`, `satoken_verify.go`, `satoken_verify_test.go`, `pkg/gateway/serviceidentity/serviceidentity.go`, `serviceidentity_test.go`, `pkg/gateway/runtime/runtimestore/runtimestore.go`, `pkg/gateway/externalapi/openapi/openapi.json`, `pkg/gateway/sessionserver/upload_to_session.go`, `pkg/tokenservice/tokenservice.go`, `caller_origin_test.go`, `rotation_test.go`, `source_recheck_test.go`, `tests/tier3_contract/oauth_token/oauth_token_caller_origin_test.go`, `oauth_token_source_revoked_test.go`, `oauth_token_test.go`, `pkg/gateway/storage/issuedtokenstore/issuedtokenstore.go`, `recheck.go`, `recheck_test.go`, `tests/tier2_component/issuedtokencascade/source_recheck_test.go`, `tests/tier7a_load_local/token_double_rotation_race_test.go`, `tests/tier9_security/token_source_revoked_refusal_test.go` (CODE-1)
- `tests/spec-map.json` (CODE-1, Testing), `tests/registers/change-graph-coverage.yaml` (CODE-1), `tests/change-graph.json` (CODE-3, Testing)
- `pkg/gateway/session/sessionstore/creation_fence.go`, `pgstore/creation_fence.go`, `pgstore/pgstore.go`, `memstore/fence.go`, `sessionserver/usage.go` (CODE-2)
- `pkg/gateway/callercheck/` (new), `pkg/gateway/sessionserver/user_gate.go`, `sessionserver.go` (CODE-3)
- `pkg/alerting/rules/rules.go`, `charts/lenny/files/alerting-rules.yaml`, `docs/alerting/rules.yaml` (CODE-4), `pkg/embedded/manifests/manifests.yaml` (CODE-1, CODE-4)
- `docs/reference/error-catalog.md`, `docs/client-guide/authentication.md`, `docs/api/admin.md`, `docs/getting-started/architecture.md`, `docs/operator-guide/security.md`, `docs/client-guide/sdk-examples/curl.md`, `docs/reference/configuration.md`, `docs/tutorials/user-credentials.md`, `docs/operator-guide/observability.md`, `docs/runbooks/token-store-unavailable.md`, `docs/api/internal.md`, `docs/runbooks/jwt-key-rotation.md`, `tests/tier11_docs/shared_jwt_signing_docs_test.go` (DOCS-1)
- `BUILD-GAPS.md`, `TEST-GAPS.md`, `gateway-runtime-comms-remediation.md`, `tests/tier11_docs/build_gaps_jwt_signing_records_currency_test.go` (REC-1)
- Test files named in the Testing section (TEST-1)
