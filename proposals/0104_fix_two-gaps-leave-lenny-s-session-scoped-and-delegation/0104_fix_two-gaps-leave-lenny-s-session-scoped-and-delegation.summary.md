# Summary: Pods hold no Lenny-issued JWT: drop the session-creation mint and the internal child-token exchange, and fence a delegated child against a terminal parent

## Summary

**Problem statement.** The specification states two Lenny-issued tokens that nothing consumes, and it contradicts itself on whether pods hold Lenny-issued JWTs. §10.2 states a `session_capability` mint on session creation that pods present back to the gateway, while the §10.2 boundary table and §13.3 give pods mTLS and a projected SA token only. The code follows the boundary table and mints nothing. §8.2 routes delegation through an internal token exchange whose code returns an unsigned token, records nothing, and checks no parent freshness on the pod path. A child admitted just before its parent's terminal write can therefore escape the §8.10 cascade, and `DELEGATION_PARENT_REVOKED` never fires. Proposal 0102 attaches its impersonation refusal to the session-creation mint that does not exist.

**What changes.**
- §10.2 drops the session-creation mint and the pod-presented-token claims, binds each forwarded tool call to a session on the calling pod, and anchors the 0102 step (1a) caller check on session creation itself (SPEC-1). CODE-3 builds the check in one function called by `requireActiveUser` and client-facing `lenny/delegate_task`.
- §8.2 replaces the child-token exchange with child-row identity and a parent-freshness rule (SPEC-2). CODE-1 deletes the unsigned minter, the audit-contention path, and their registry entries.
- The §12.8 creation fence gains a parent-terminal rule for delegated children (SPEC-3), built in CODE-2 for pgstore and memstore.
- Every other restatement of the removed mints is dropped or retargeted (SPEC-4, SPEC-5). CODE-4 regenerates the alert description.
- Tests across tiers (TEST-1), published docs (DOCS-1), and the build and test records (REC-1) follow.

**Decisions.**
- **D1.** Pods hold no Lenny-issued JWT. The boundary table, §10.3, and the §13.3 credential flow are canonical, and the code already follows them. Open points (1), (2), (4), and (5) of the problem statement and pod-token renewal need no answer.
- **D2.** Session creation mints no JWT, because no endpoint consumes one: MCP clients and the WebSocket upgrade use their own bearer or the playground bearer. `session_capability` stays as the playground bearer's `typ`. The KMS failure mode on session creation and the §17.8.2 `session_creation_rate` term go with it.
- **D3.** The 0102 D20/D22 refusal survives, re-anchored from the mint onto session creation (SPEC-1 Edit (b)). One function is called by every root-creating handler and by client-facing `lenny/delegate_task`, so derive, replay, and edge delegation cannot bypass it. Without it, an impersonated session and its children would outlive DELETE of the impersonation, on which 0102 rests "no impersonated session exists".
- **D4.** The D3 rejection row keeps `exchange_type: session_mint`. §16.7 redefines the value as the session-creation caller check, which mints no JWT, so the enum does not change.
- **D5.** Internal delegation mints no token. The child's identity is its session row, and the delegation lease (§8.3) enforces narrowing. The external `a2a_delegation` exchange with `actor_token` is unchanged and keeps its §13.3 rules.
- **D6.** Parent freshness is keyed on the parent session row. The insert transaction of a delegated child reads the parent `FOR SHARE` and refuses a terminal or absent parent with the existing `CreationRefusal` carrying `DELEGATION_PARENT_REVOKED`. Terminal writers take `FOR UPDATE` on the row, so either the insert sees the terminal parent or the post-commit cascade sees the child. `delegate_task` already surfaces any `CreationRefusal`, so the `ErrParentRevoked` sentinel is deleted rather than re-wired.
- **D7.** The fence refuses only a terminal parent. The cascade race exists only at the terminal transition, and a parent that moves from `running` to another live state mid-admission keeps its child.
- **D8.** Revocation reaches a delegated tree through session-tree mechanisms: a §11.4 full revoke cancels every session of the user, the creation fence refuses a child of a hard-disabled or fully revoked owner, and the §8.10 cascade cancels descendants. A single-bearer revocation does not stop a running session from delegating, as for every other in-session action.
- **D9.** `DELEGATION_AUDIT_CONTENTION`, the §8.2 audit-contention retry paragraph, and `ErrAuditContention` are deleted. They exist only for the deleted exchange, and production never wired the audit lock.
- **D10.** 0104 lands after 0102's spec steps and after 0102 CODE-2 item 7 (see **Impacts on other proposals**).

**Watch out for.**
- SPEC-1 Edit (b) locates its target by the wording 0102 SPEC-2 writes, and CODE-3 does not compile before 0102 adds `IssuedToken.SourceRefusal`. Apply both only after those 0102 steps land.
- `fenceInsert` is shared with `Reserve`. The reservation must not read the parent row (CODE-2 item 4).
- The `parent_session_id` foreign key already locks the parent row at INSERT, and an absent parent would otherwise surface as a foreign-key error. CODE-2 item 3 makes the refusal come first.
- `requireActiveUserForAction` is shared with interaction resolution, and `requireActiveUser` returns early when `s.users == nil`. The caller check goes in `requireActiveUser`, ahead of that return (CODE-3 item 2).
- The `requireActiveDelegator` early gate must never answer `DELEGATION_PARENT_REVOKED` (CODE-2 **Trap: the early gate**).
- `commitChild` undoes an export only on the reserved path (CODE-2 **Trap: undo on refusal**).
- Deleting the `childtoken` package without the `tests/spec-map.json` and change-graph edits breaks the registry gates (CODE-1 targets).
- `revCache` stays: the revocation propagator and the auth middleware share it.
- Every edited table row stays one physical line.

## Goals

- The specification states one answer on pod credentials: pods hold no Lenny-issued JWT.
- No specification text states a session-creation mint, an internal delegation mint, or a sign rate for either.
- A delegated child cannot be created under a terminal or absent parent, on the pod path and the client-facing path, and the refusal is `DELEGATION_PARENT_REVOKED`.
- An impersonation or revoked bearer cannot create a session through any root-creating entry point or through client-facing `lenny/delegate_task`, and every refusal is audited before the response.
- The code carries no unsigned child-token minter and no error path that only it produced.
- F-10.2.25, F-8.2.31, T-8.2.7, and T-10.2.2 close.

## Non-goals

- **Pods hold Lenny JWTs, so the §10.2 JWT-claims list wins.** This needs a delivery channel into pods, a `subject_token` the gateway does not hold on the pod path, renewal for 24-hour tokens, and redelivery after pod restart or coordinator handoff. It contradicts the boundary table and §13.3 and protects a credential no endpoint consumes.
- **Signing and recording session-creation and child tokens as gateway-held revocation anchors.** It costs one KMS `Sign` and one `issued_tokens` row per session and per delegation, plus a `sessions.token_jti` column or a `jti = session_id` convention. It adds a KMS dependency on session creation, and a 24-hour `exp` that would stop long-lived or suspended sessions from delegating. Revoking an unheld token gates nothing the session tree does not already stop.
- **Routing child minting through the Token Service** by HTTP to `/v1/oauth/token` or a new `TokenService` gRPC exchange RPC. This needs signed parent tokens the gateway does not store (`issued_tokens` keeps only the hash), internal-caller authentication, and new protocol surface.
- **Returning a session-creation token in the `POST /v1/sessions` response, or confining `session_capability` to client-facing session creation.** It adds a wire field and a mint with no consumer.
- **Binding a session to the creating bearer's `jti` and re-reading it at delegation.** Routine 15-minute bearer rotation would break delegation for every long-lived session.
- **Minting the root token lazily at the first delegation.** No token is minted.
- **Threading the user's JWT or the bearer `jti` through `LNK-GWCONTROL`** so the pod-path principal carries a JTI. That gives the pod path client-token material §13.3 forbids and checks the wrong authority.
- **A session-tree recursive-revocation endpoint, or making a §13.3 token-level recursive revocation terminate sessions.** §11.4 full revoke, session cancel, the creation fence, and the §8.10 cascade already end trees through the session row, and the validated problem shows no scenario they miss.
- **Making `RevokeCascade` and `RevokeBySubject` take the §11.7 per-tenant advisory lock.** That race exists only for token-level issuance into a tree, which this design no longer performs. The external `a2a_delegation` path keeps §13.3's existing window.
- **Stamping `parent_jti` and `act_sub` in the Token Service's external actor-token record.** It concerns the external path, outside the validated problem; it is listed under **Defects in the shipped tree that this proposal does not stage**.
- **Re-anchoring `DELEGATION_AUDIT_CONTENTION` on the `delegation.spawned` audit write.** No spec rule puts that write under the advisory lock with fail-closed semantics.
- **Deleting `DELEGATION_PARENT_REVOKED`.** The parent-terminal race is real, because the cascade reads children only after the terminal commit and orphan cleanup waits up to `cascadeTimeoutSeconds`. The code is reused for the fence refusal.
- **Refusing every non-running parent in the fence.** See D7.
- **Mapping the admission-time `ErrParentNotRunning` to `DELEGATION_PARENT_REVOKED`.** The validated problem does not require it, and admission's not-running case covers non-terminal states the catalog row does not describe.
- **A conditional `INSERT ... WHERE EXISTS` without a lock.** Under READ COMMITTED a concurrent terminal commit could slip between check and commit, and the cascade could miss the child.
- **Renaming `DELEGATION_PARENT_REVOKED`, or adding an `exchange_type` value for the session-creation rejection row.** Both churn the catalog and enums with no behavior gain (D4).
- **A separate "Pod credentials" paragraph under the boundary table.** The boundary table already states the rule, and SPEC-1's `session_capability` bullet carries the one sentence the claims list needs.
- **Keeping §18.40 item 7 as a placeholder to avoid renumbering.** A placeholder is not a prerequisite. The item is deleted and the later items are renumbered.
- **Keeping the gateway revocation-cache pre-check on the delegation path.** It belongs to the deleted minter, and the auth middleware already checks edge callers' bearers per request.
- **A gateway-to-pod renewal path, or moving pod tokens to `auth.capabilityTokenTtlSeconds`.** The problem statement and 0097 D13 scope these out, and D1 makes them moot.
- **Making `POST /v1/admin/issued-tokens/{jti}/revoke` cascade.** The validated problem does not require it.

## Open decisions for human to make

None at drafting time. The review loops write this section.

## Defects in the shipped tree that this proposal does not stage

- **`ErrParentNotRunning` is unmapped in `lenny/delegate_task`.** Admission's not-running sentinel reaches the tool caller without a catalogued code. It concerns non-terminal parent states, outside the parent-terminal race. REC-1 files it once confirmed.
- **The Token Service's external `a2a_delegation` record omits `parent_jti` and `act_sub`.** `RevokeCascade` then cannot reach an external delegation child. It concerns the external path, outside the validated problem. REC-1 files it once confirmed.
- **§13.3 "Delegation-lifetime preservation" is not enforced on the external path.** `pkg/tokenservice` reads no delegation-lease budget (`maxTokenBudget`, `maxChildrenTotal`) when it mints an `a2a_delegation` child. It concerns the external exchange, which 0104 leaves unchanged. REC-1 files it once confirmed.

## Impacts on other proposals

| Proposal | Status | What this change does to it | What it must do |
|:--|:--|:--|:--|
| 0102 (impersonation revocation) | Approved | 0104 moves the anchor of 0102 SPEC-2 Edit (b) from the session-creation mint to session creation itself, and SPEC-1 Edit (b) replaces the sentences 0102 SPEC-2 Edit (b) writes. CODE-3 builds the check 0102 CODE-2 item 8 left unbuilt, and depends on 0102 CODE-2 item 7 (`SourceRefusal`) and CODE-3 item 3 (`RecordImpersonation`). 0102 non-spec (d) puts the "impersonation bearer cannot create a session" docs statement in a `POST /v1/admin/impersonation` section of `docs/api/admin.md` that does not exist; DOCS-1 puts it in `docs/operator-guide/security.md` instead. | 0102's spec steps and CODE-2 item 7 land before 0104 S1 and S8. 0102's files are not edited. |
| 0100 (terminal pipeline) | Approved | 0100 SPEC edits the *Drain* half of the §12.8 "Lifecycle fence and drain" paragraph; SPEC-3 edits the *Creation fence* half. 0100 also moves the post-terminal steps in `sessionserver/usage.go`, where the cascade runs. D6 needs the cascade to run after the parent's terminal commit, which 0100 keeps, and CODE-2 item 7 changes the cascade's listing in the same file. | Whichever lands second applies its §12.8 edits by quoted anchor with no change to content. 0104's implementor confirms the cascade still runs after the terminal commit and keeps CODE-2 item 7. |
| 0103 (production mode) | Approved | 0103 CODE-6 edits other alert descriptions in `pkg/alerting/rules/rules.go` and regenerates the same rule files as CODE-4. The descriptions are disjoint. | Whichever lands second re-runs `make generate`. |
| 0097 (shared JWT signing) | Implemented | 0097 D13 kept the session-creation token on its per-dialect cap; SPEC-1 removes that token. | Nothing. 0097 is not edited. |

## Deliverable index

- SPEC-1 — spec/10_gateway-internals.md — remove the session-creation mint and pod-presented-token claims; anchor the step (1a) caller check on session creation; bind forwarded tool calls to the calling pod
- SPEC-2 — spec/08_recursive-delegation.md — replace the child-token exchange with child identity and parent freshness
- SPEC-3 — spec/12_storage-architecture.md — the creation fence gains the parent-terminal rule
- SPEC-4 — spec/15_external-api-surface.md — error catalog, token-endpoint row, the `CallerIdentity.Act` and `ActorClaim` comments, and the MCP endpoint boundary
- SPEC-5 — spec/04, 05, 07, 10, 13, 16, 17, 18, 23, and 27 — drop internal delegation minting and the session mint from every restatement
- CODE-1 — pkg/gateway/mcpfabric/delegation/ — delete the unsigned child-token minter and the audit-contention path
- CODE-2 — pkg/gateway/session/sessionstore/ — the fence refuses a delegated child of a terminal or absent parent
- CODE-3 — pkg/gateway/callercheck/ — session creation and client-facing delegation apply the step (1a) caller check
- CODE-4 — pkg/alerting/rules/rules.go — the `TokenStoreUnavailable` description drops delegation minting
- TEST-1 — tests/ and package tests — parent freshness, the caller check, and KMS independence across tiers
- DOCS-1 — docs/ — published pages follow the spec
- REC-1 — BUILD-GAPS.md, TEST-GAPS.md, gateway-runtime-comms-remediation.md — close the findings and route the adjacent ones
