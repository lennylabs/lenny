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
- `tests/spec-map.json`: remove `"pkg/gateway/mcpfabric/delegation/childtoken"` from the §8.2 `"packages"` list.
- `tests/registers/change-graph-coverage.yaml`: remove the `pkg/gateway/mcpfabric/delegation/childtoken/` entry.

The change is a pure deletion. Stop condition: run grep for `ChildToken`, `childtoken`, `ParentToken`, `child_token_jti`, `act_chain`, `delegation_token_scope`, `ErrParentRevoked`, `ErrAuditContention`, and `DELEGATION_AUDIT_CONTENTION` across `pkg`, `cmd`, `sdks`, `tests` (including `tests/spec-map.json` and `tests/registers`), and `schemas`. Stop when only the sessionstore `CreationRefusal` constant `DELEGATION_PARENT_REVOKED` (CODE-2) and unrelated `a2a_delegation` code in `pkg/tokenservice` remain.

**Ordering.** CODE-1 lands after CODE-2 and in the same proposal merge as SPEC-2, SPEC-4, and CODE-2, so `lenny/delegate_task` never runs in a state where §8.2 still requires the exchange or `DELEGATION_PARENT_REVOKED` has no producer.

### CODE-2 · Creation fence refuses a delegated child of a terminal or absent parent

Targets:
- `pkg/gateway/session/sessionstore/creation_fence.go`
- `pkg/gateway/session/sessionstore/pgstore/creation_fence.go` and `pgstore.go` (`create`)
- `pkg/gateway/session/sessionstore/memstore/fence.go`
- `pkg/gateway/sessionserver/usage.go` (`cascadeToChildren`)

Changes:
1. `sessionstore.CreationMarkers` gains `ParentSessionID string` and `ParentTerminal bool`. Add the constant `RefusalDelegationParentRevoked = "DELEGATION_PARENT_REVOKED"`.
2. `CreationMarkers.Refusal` gains rule 5, evaluated after the four existing rules (§12.8 refusal order, SPEC-3 Edit (b)): when `m.IsDelegatedChild && m.ParentTerminal`, return a `*CreationRefusal` with `Code: RefusalDelegationParentRevoked` and `Details: {"parentSessionId": m.ParentSessionID}`. Its HTTP status is 403, as for every fence refusal.
3. pgstore: `readCreationMarkers` and `fenceInsert` take the parent session ID next to `delegatedChild`. When `delegatedChild` is true, `readCreationMarkers` runs, after the tenant and user reads, `SELECT state FROM sessions WHERE tenant_id = $1 AND id = $2::uuid FOR SHARE`. A terminal state sets `ParentTerminal`. No row sets `ParentTerminal`, so the refusal comes before the `parent_session_id` foreign-key error. Any other read error fails the insert closed with a wrapped error.
4. `create()` in `pgstore.go` passes `sess.ParentSessionID`. `Reserve` passes an empty parent ID and reads no parent row: the reservation runs only the tenant and user rules (SPEC-3 Edit (c)).
5. memstore makes the same check under its mutex in `fence.go`, next to the existing `Refusal` call, reading the parent from its own map. The reservation path in memstore also skips the parent rule.
6. The code comment on the parent read states that the `parent_session_id` foreign key already takes `FOR KEY SHARE` on the parent row at INSERT, so the `FOR SHARE` read keeps the existing lock order (tenant, user, parent), and that the terminal writer's `SELECT ... FOR UPDATE` in `pgstore.Update` serializes against it. Cite `// spec: §12.8 (Lifecycle fence and drain), §8.2 (Parent freshness)`.
7. `cascadeToChildren` lists the tenant's sessions with `ListFilter{Primary: true}`. Cite `// spec: §12.8 (Lifecycle fence and drain), §8.10`.

**Trap: the terminal set.** Use the store's existing terminal-state predicate. Do not compare against `running`: a parent in another live state must not refuse its child (summary decision D7).

**Trap: the early gate.** `requireActiveDelegator` in `mcptools.go` builds markers for its early check. It must leave `ParentTerminal` unset, so the insert transaction stays the only producer of `DELEGATION_PARENT_REVOKED`. Admission's `ErrParentNotRunning` stays as it is.

**Trap: undo on refusal.** `commitChild` in `delegation/service.go` undoes the file export on a `CreationRefusal` only on the reserved path. Confirm that the unreserved path writes nothing under the child ID before the insert. If it does, add the same undo there.

### CODE-3 · Session creation applies the step (1a) caller check

This code applies §10.2 "Signing topology" (SPEC-1 Edit (b)). It re-anchors the refusal that proposal 0102 D20 and D22 approved; see the summary's **Impacts on other proposals**, row 0102.

Targets:
- `pkg/gateway/callercheck/callercheck.go` (new) and its test.
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

   **IMPLEMENTOR'S CHOICE:** the production `Audit` adapter — it takes the §11.7 per-tenant audit advisory lock and commits the row before it returns, as the Token Service `Auditor` behind `rejectRevokedSource` does (for example `auditstore.AcquireLockInTx` then `auditstore.AppendInTx` in one transaction).
2. **`requireActiveUser`.** Call `callercheck.Check` first, before the `s.users == nil` early return and before the §12.8 early gate, so the refusal precedes any claim or store write. Map `*Refused` to `403 FORBIDDEN` and any other error to `500 INTERNAL_ERROR`. Do not add the call to `requireActiveUserForAction`, which interaction resolution also uses.
3. **`delegate_task`.** Call `callercheck.Check` with the request principal before `delegation.Service.Delegate`, so no admission, reservation, export, or pod claim runs for a refused bearer. Map `*Refused` to the `FORBIDDEN` tool error and any other error to `INTERNAL_ERROR`. A pod-path call carries no JTI and passes by rule 1.2.
4. **Wiring.** Set `IssuedTokens` to `issuedtokenstore.New(w.pgPool)` and `Audit` to the adapter when the pool is non-nil, on both the sessionserver and the mcptools `Deps`.

**Sequencing trap.** `IssuedToken.SourceRefusal` is added by 0102 CODE-2 item 7, so this code does not compile before that item lands. The check refuses nothing until 0102 CODE-3 item 3 (`RecordImpersonation`, which writes the impersonation bearer's `issued_tokens` row) lands, because every lookup before then returns `ErrNotFound`.

### CODE-4 · Alert description drops delegation minting

Target: `pkg/alerting/rules/rules.go`, the `TokenStoreUnavailable` `Description`. Replace "session creation, delegation minting, and credential leasing are all failing" with "session creation and credential leasing are both failing", mirroring SPEC-5 Edit (h). Regenerate with `make generate`, and confirm that `charts/lenny/files/alerting-rules.yaml`, `docs/alerting/rules.yaml`, and `pkg/embedded/manifests/manifests.yaml` carry the new text and that the generator drift check passes.

## Staged schema, chart, and migration changes

None. CODE-4 regenerates the bundled alert rule file in the chart and changes no chart template or value.

## Staged docs changes

### DOCS-1 · Published docs follow the spec

Each table row stays one physical line. No page cites a spec section number.

- `docs/reference/error-catalog.md`, the `DELEGATION_PARENT_REVOKED` row: the parent session was terminal or absent when the child session row was inserted. No child session is created and no pod is allocated. Only `details.parentSessionId` is included; drop `revocationReason`. Not retryable. The catalog has no `DELEGATION_AUDIT_CONTENTION` row, so nothing else changes there.
- `docs/client-guide/authentication.md`, the paragraph that begins "For token lifecycle operations inside Lenny": remove ", or any internal delegation child-token issuance" and keep the list grammatical.
- `docs/api/admin.md`, under `POST /v1/oauth/token`: delete the "Gateway (internal)" caller row. In the sentence "For delegation child-token minting, additionally supply `actor_token=<parent_session_token>` and a narrowed `scope` string.", retarget to `a2a_delegation` child-token minting with `actor_token` set to the delegating caller's token.
- `docs/getting-started/architecture.md`, the paragraph that begins "Externally, token issuance": retarget "delegation child-token minting" and the "parent session token in the `actor_token` field" sentence to the external `a2a_delegation` exchange. Remove "and the session-capability tokens of the session-creation path" and keep the list grammatical. Add that internal delegation through `lenny/delegate_task` mints no token.
- `docs/operator-guide/security.md`, item 3 "**Bearer tokens**": change "delegation child tokens" to "`a2a_delegation` child tokens" and "(credential leasing, delegation minting)" to "(credential leasing)". Remove the session-capability clause from the in-process list. Add: "An impersonation bearer cannot create a session; the refusal is audited."
- `docs/client-guide/sdk-examples/curl.md`, the sentence that begins "For delegation child-token minting": retarget to `a2a_delegation` child-token minting with `actor_token` set to the delegating caller's token.
- `docs/reference/configuration.md`: in the `tokenExchange.rateLimitPerUser` row, replace "Rotation + delegation minting share this budget." with a sentence naming rotation and `a2a_delegation` child-token minting. In the `tokenExchange.requireActorToken` row, retarget "delegation child-token minting" to `a2a_delegation` child-token minting. Leave the external `actor_token` lifetime paragraph unchanged.
- `docs/tutorials/user-credentials.md`: replace "including `actor_token` for delegation minting" with "including `actor_token` for `a2a_delegation` child-token minting".
- `docs/operator-guide/observability.md`, the `TokenStoreUnavailable` row, and `docs/runbooks/token-store-unavailable.md`, the front-matter symptom `"delegation minting fails"` and the body paragraph that begins "The Postgres-backed token store": remove delegation minting. Keep "session creation", because the CODE-3 check fails closed on an `issued_tokens` read error.

### REC-1 · Records

Targets: `BUILD-GAPS.md` F-10.2.25, F-8.2.31, and one new finding; `TEST-GAPS.md` T-8.2.7 and T-10.2.2; `gateway-runtime-comms-remediation.md` §10.2, the F-10.2.25 and F-8.2.31 queue entries, the "session capability and delegation child token proposal" routing text, and one new queue entry; `scripts/seed-claim-register.py` and `tests/claim-map.json`, one new claim-register row. CODE-1 owns the `tests/spec-map.json` and `tests/registers/change-graph-coverage.yaml` removals.

- Close F-10.2.25. The session-creation mint is removed from §10.2, and the renewal half is moot under D1. Close F-8.2.31. Child identity is the session row, and the §12.8 fence enforces parent freshness. Each closure points to 0104 and replaces the stale **Suggested resolution:** text.
- Close T-8.2.7 against the Testing section's **Fence rule model**, **Fence race**, and **Delegation flow** entries.
- Close T-10.2.2. Its throttle and breaker half was already covered by 0097. Its `POST /v1/sessions` half has no spec basis after SPEC-1, and the Testing section's **KMS independence** entry pins that session creation succeeds while the breaker is open.
- In the remediation plan, mark both entries resolved by 0104.
- File, each only when the implementor confirms it, the findings in the summary's **Defects in the shipped tree that this proposal does not stage**.
- The §28.4 claim register (`tests/claim-map.json`) carries no row for either removed mint, so no existing claim row changes. One row is added for the forwarded-call session binding (summary decision D11). The register is generator-produced, so add the row to the hand-written row list in `scripts/seed-claim-register.py`, with a comment above it in the style of its neighbours, and regenerate the register with `scripts/seed-claim-register.py --out tests/claim-map.json`. Do not edit `tests/claim-map.json` by hand: the tier-0 generator gate requires the committed file to match the generator output byte for byte. The row is:

  ```
  {
      "claim": "Gateway binds a CH-MCP-PLATFORM or CH-MCP-CONNECTOR call forwarded on LNK-GWCONTROL to a session assigned to the calling pod",
      "status": "ABSENT",
      "spec_anchor": "#2853-intra-pod",
      "deferral_id": "R14",
      "surface": "`pkg/gateway/gatewaycontrol/platformtools/platformtools.go` `Bridge.CallPlatformTool`, `pkg/gateway/connectors/connectortools/connectortools.go` `Bridge.CallConnectorTool`",
      "note": "the gateway does not bind a forwarded call to a session assigned to the calling pod; it dispatches under the session id the adapter presents; BUILD-GAPS.md records the gap, routed to a dedicated proposal in gateway-runtime-comms-remediation.md §10.2; R14 supplies the agent-pod client identity such a binding keys on"
  }
  ```

  The validator requires a row that is not `WIRED` to name a plan step that the remediation plan declares. No step owns the binding. R14 is the step whose pod client identity the binding needs, and the seeding script's own deferral rules map SPIFFE and client identity to R14. The comment above the row states that the row records a gap that §28 and §10.2 do not specify after SPEC-1 Edit (f), and that the dedicated proposal owns the specification.
- File a new Medium finding in `BUILD-GAPS.md` for the absent binding, under the §10.2 group with the next free `F-10.2.N` identifier, following the BUILD-GAPS conventions for a new open finding (title line, **Gap:**, evidence, and **Suggested resolution:**). **Gap:** the gateway serves a `CH-MCP-PLATFORM` or `CH-MCP-CONNECTOR` call forwarded on `LNK-GWCONTROL` under the session ID the adapter presents, with no check that the session is assigned to the calling pod, so a compromised adapter can act for another session. Evidence: `pkg/gateway/gatewaycontrol/platformtools/platformtools.go` (package comment and `Bridge.CallPlatformTool`) and `pkg/gateway/connectors/connectortools/connectortools.go` (`Bridge.CallConnectorTool`). **Suggested resolution:** a dedicated proposal specifies the binding, covering the `pod_assignment` invariant at every state edge, the RPCs it covers, the dev plaintext listener with no SPIFFE identity, assignment timing relative to the first forwarded call, and the refusal outcome. Owner: that proposal. Add an open queue entry for that proposal to `gateway-runtime-comms-remediation.md` §10.2, citing the new finding and the claim-register row.

## Testing

### TEST-1 · Tests for parent freshness, the caller check, and KMS independence

TEST-1 extends the existing harnesses named below. It adds no full-revoke race, because 0104 does not change that behavior. Every test carries a `// spec:` annotation, and every tier-2-and-higher test carries a `// diagnosis:` comment. Each entry asserts what its rule's home states and does not restate the rule.

- **Fence rule model** (tier 1; `pkg/gateway/session/sessionstore/creation_fence_test.go`; §12.8). Add `ParentTerminal` to the `expected` reference model and to `TestCreationRefusalCrossProduct_spec_12_8`, so rule 5 is checked against every combination of the other markers and ordered after them. Add a focused test: a missing parent refuses with `details.parentSessionId`, and a depth-0 row with `ParentTerminal` set admits. The discriminating assertion is the depth-0 admit.
- **memstore parity** (tier 1; `memstore` `fence_test.go`; §12.8). The same cases as the fence rule model, plus a reservation that admits while the parent is terminal.
- **Postgres fence** (tier 2; `tests/tier2_component/stores/sessionstore_creation_fence_test.go`; §12.8). Add the parent-row case to the in-transaction refusal test and to the serialization test. Add a fault-injected parent-row read error that fails the insert and writes no row.
- **Fence race** (tier 7a; `tests/testinfra/lifecyclefence` and `tests/tier7a_load_local/creation_fence_race_test.go`; §12.8, §8.10). Add a "parent terminal" `fenceMarker` that moves the parent to a terminal state through store `Update`, raced against `PathDelegate` creates on memstore and pgstore. Invariant: every delegated child is either refused with `DELEGATION_PARENT_REVOKED` or committed before the parent's terminal commit, so a post-commit list of children finds it. Only `PathDelegate` creates are subject to the marker.
- **Cascade read source** (tier 1; `pkg/gateway/sessionserver/cascade_on_expire_test.go`; §12.8, §8.10). A recording store asserts that the `cancel_all` cascade lists with `Primary: true`.
- **Delegation flow** (tier 1; `pkg/gateway/mcpfabric/delegation` service tests and `mcptools` `delegate_task` tests; §8.2, §12.8). A store hook moves the parent to a terminal state between admission and `CreateReserved`. Expect the `DELEGATION_PARENT_REVOKED` tool error with `parentSessionId`, no child row, the export undone, and no pod allocation. Assert that `requireActiveDelegator` leaves `ParentTerminal` unset.
- **Caller check** (tiers 1 and 3; `pkg/gateway/callercheck` and `pkg/gateway/sessionserver` tests; §10.2 "Signing topology"). One table drives the REST create, create-and-start, derive, and replay routes, the MCP `lenny/create_session` tool, the single-shot binder, and client-facing `lenny/delegate_task`. Cases: an impersonation bearer gets 403 (`FORBIDDEN` tool error on MCP) with a durable `session_mint` rejection row; a revoked bearer gets the same with reason `subject_token_revoked`; a failed audit write gets 500 and no session; a missing row or an empty JTI passes; a read error gets 500; a nil `IssuedTokens` skips the check. The bypass guard is the table covering every root-creating entry point. A pod-path `delegate_task` with no JTI passes without a store read.
- **KMS independence** (tier 2 or 8; extend `tests/tier2_component/tokensigning/kms_outcome_test.go` or `tests/tier8_chaos/jwt_keyset_chaos_test.go`; §10.2 "KMS signing failure mode"). `POST /v1/sessions` succeeds while the signer fails and the breaker is open, and the signer records no call from session creation.
- **Catalog code** (tier 1; `pkg/gateway/externalapi/errorclassify/errorclassify_test.go`; §15.1). Add `DELEGATION_PARENT_REVOKED` to `TestCreationFenceCodesArePolicyNonRetryable`.
- **Spec map** (TEST-1 owns these entries). Add the new tests to the `tests/spec-map.json` entries for §8.2, §10.2, §12.8, and §15.1.
- **Docs** (tier 11). The alert-to-runbook and docs consistency checks pass after CODE-4 and DOCS-1.

## Edge cases and accepted failure modes

- **Caller check before 0102 lands.** The check passes every bearer until 0102 writes impersonation rows (CODE-3 **Sequencing trap**).
- **Parent read contention.** The `FOR SHARE` read waits for an in-flight terminal write on the parent. The wait is bounded by that write's transaction and adds no new lock cycle.
- **Single-bearer revocation.** It does not stop an already running session from delegating (summary decision D8).

## Files touched on application (non-spec)

- `pkg/gateway/mcpfabric/delegation/childtoken/` (deleted), `pkg/gateway/mcpfabric/delegation/mint.go`, `mint_wiring_test.go`, `service.go` (CODE-1)
- `pkg/gateway/mcpfabric/mcptools/mcptools_register.go` (CODE-1, CODE-3), `pkg/gateway/mcpfabric/mcptools/mcptools.go` (CODE-3 `Deps`)
- `pkg/gateway/externalapi/errorclassify/errorclassify.go` (CODE-1)
- `cmd/lenny-gateway/mcpsurface.go` (CODE-1, CODE-3), `cmd/lenny-gateway/httpsurface.go` (CODE-3)
- `pkg/gateway/middleware/auth/auth.go` (CODE-1)
- `tests/spec-map.json` (CODE-1, Testing), `tests/registers/change-graph-coverage.yaml` (CODE-1)
- `pkg/gateway/session/sessionstore/creation_fence.go`, `pgstore/creation_fence.go`, `pgstore/pgstore.go`, `memstore/fence.go`, `sessionserver/usage.go` (CODE-2)
- `pkg/gateway/callercheck/` (new), `pkg/gateway/sessionserver/user_gate.go`, `sessionserver.go` (CODE-3)
- `pkg/alerting/rules/rules.go`, `charts/lenny/files/alerting-rules.yaml`, `docs/alerting/rules.yaml`, `pkg/embedded/manifests/manifests.yaml` (CODE-4)
- `docs/reference/error-catalog.md`, `docs/client-guide/authentication.md`, `docs/api/admin.md`, `docs/getting-started/architecture.md`, `docs/operator-guide/security.md`, `docs/client-guide/sdk-examples/curl.md`, `docs/reference/configuration.md`, `docs/tutorials/user-credentials.md`, `docs/operator-guide/observability.md`, `docs/runbooks/token-store-unavailable.md` (DOCS-1)
- `BUILD-GAPS.md`, `TEST-GAPS.md`, `gateway-runtime-comms-remediation.md`, `scripts/seed-claim-register.py`, `tests/claim-map.json` (REC-1)
- Test files named in the Testing section (TEST-1)
