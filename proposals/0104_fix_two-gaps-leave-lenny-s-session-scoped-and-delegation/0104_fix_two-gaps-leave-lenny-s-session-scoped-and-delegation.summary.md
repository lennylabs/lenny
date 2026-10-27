# Summary: Pods hold no Lenny-issued JWT: drop the session-creation mint and the internal child-token exchange, and fence a delegated child against a terminal parent

## Summary

**Problem statement.** The specification states two Lenny-issued tokens that nothing consumes, and it contradicts itself on whether pods hold Lenny-issued JWTs. §10.2 states a `session_capability` mint on session creation that pods present back to the gateway, while the §10.2 boundary table and §13.3 give pods mTLS and a projected SA token only. The code follows the boundary table and mints nothing. §8.2 routes delegation through an internal token exchange whose code returns an unsigned token, records nothing, and checks no parent freshness on the pod path. A child admitted just before its parent's terminal write can therefore escape the §8.10 cascade, and `DELEGATION_PARENT_REVOKED` never fires. Proposal 0102 attaches its impersonation refusal to the session-creation mint that does not exist.

**What changes.**
- §10.2 drops the session-creation mint and the pod-presented-token claims and anchors the 0102 step (1a) caller check on session creation itself, and §10.3 states that the gateway validates the projected SA token's signature (SPEC-1). CODE-3 builds the check in one function called by `requireActiveUser` and client-facing `lenny/delegate_task`.
- §8.2 replaces the child-token exchange with child-row identity and a parent-freshness rule (SPEC-2). CODE-1 deletes the unsigned minter, the audit-contention path, and their registry entries.
- The §12.8 creation fence gains a parent-terminal rule for delegated children (SPEC-3), built in CODE-2 for pgstore and memstore.
- Every other restatement of the removed mints is dropped or retargeted (SPEC-4, SPEC-5). CODE-4 regenerates the alert description.
- Tests across tiers (TEST-1), published docs (DOCS-1), and the build and test records (REC-1) follow. REC-1 also records the forwarded-call session binding the code lacks as a BUILD-GAPS finding (D11).

**Decisions.**
- **D1.** Pods hold no Lenny-issued JWT. The boundary table, §10.3, and the §13.3 credential flow are canonical, and the code already follows them. Open points (1), (2), (4), and (5) of the problem statement and pod-token renewal need no answer.
- **D2.** Session creation mints no JWT, because no implemented endpoint consumes one: MCP clients and the WebSocket upgrade use their own bearer or the playground bearer. The one stated pod-side consumer, runtime self-submission of evals through §10.7, has no implementation and is removed (D12). `session_capability` stays as the playground bearer's `typ`. The KMS failure mode on session creation and the §17.8.2 `session_creation_rate` term go with it.
- **D3.** The 0102 D20/D22 refusal survives, re-anchored from the mint onto session creation (SPEC-1 Edit (b)). One function is called by every root-creating handler and by client-facing `lenny/delegate_task`, so derive, replay, and edge delegation cannot bypass it. Without it, an impersonated session and its children would outlive DELETE of the impersonation, on which 0102 rests "no impersonated session exists".
- **D4.** The D3 rejection row keeps `exchange_type: session_mint`. §16.7 redefines the value as the session-creation caller check, which mints no JWT, so the enum does not change.
- **D5.** Internal delegation mints no token. The child's identity is its session row, and the delegation lease (§8.3) enforces narrowing. The external `a2a_delegation` exchange with `actor_token` is unchanged and keeps its §13.3 rules.
- **D6.** Parent freshness is keyed on the parent session row. The insert transaction of a delegated child reads the parent `FOR SHARE` and refuses a terminal or absent parent with the existing `CreationRefusal` carrying `DELEGATION_PARENT_REVOKED`. Terminal writers take `FOR UPDATE` on the row, so either the insert sees the terminal parent or the post-commit cascade sees the child. `delegate_task` already surfaces any `CreationRefusal`, so the `ErrParentRevoked` sentinel is deleted rather than re-wired.
- **D7.** The fence refuses only a terminal parent. The cascade race exists only at the terminal transition, and a parent that moves from `running` to another live state mid-admission keeps its child.
- **D8.** Revocation reaches a delegated tree through session-tree mechanisms: a §11.4 full revoke cancels every session of the user, the creation fence refuses a child of a hard-disabled or fully revoked owner, and the §8.10 cascade cancels descendants. A single-bearer revocation does not stop a running session from delegating, as for every other in-session action.
- **D9.** `DELEGATION_AUDIT_CONTENTION`, the §8.2 audit-contention retry paragraph, and `ErrAuditContention` are deleted. They exist only for the deleted exchange, and production never wired the audit lock.
- **D10.** 0104 lands after 0102's spec steps and after 0102 CODE-2 item 7 (see **Impacts on other proposals**).
- **D11.** The forwarded-call session binding is split out of 0104. SPEC-1 Edit (f) deletes the §10.2 paragraph ("Pods cannot forge or extend this token. The gateway validates the signature on every pod→gateway request."), moves its signature check to the §10.3 projected SA token, and adds no binding rule. Specifying the binding whole needs the `pod_assignment` invariant at every state edge (suspend, resume, recycle, and terminal), the set of `LNK-GWCONTROL` RPCs it covers, a posture for the dev plaintext listener that carries no SPIFFE identity, the timing of assignment relative to the first forwarded call, and the refusal outcome. That work is disproportionate to 0104. The code enforces no binding today: the platform-tool bridge dispatches under the session ID the adapter presents. Removing the paragraph therefore regresses nothing. REC-1 records the gap as a Medium BUILD-GAPS finding routed to its own proposal in the remediation plan §10.2. SPEC-2 "Child identity" states that a pod-path child's parent is the session ID the adapter presents. Rejected: the one-sentence binding the review loop wrote and patched in rounds 3 and 8, which left the dev listener, assignment timing, RPC coverage, the refusal outcome, and `pod_assignment` across suspend, resume, and recycle unaddressed.
- **D12.** Runtime self-submission of evals is dropped. §10.7 lists "the agent runtime itself (via its session credential)" as an eval caller of `POST /v1/sessions/{id}/eval`, and §23.1 item 12 repeats it. A pod holds no Lenny-issued credential (D1), and no code implements a pod-side eval path, so SPEC-5 Edits (q) and (r) remove both statements. A later proposal may add runtime eval submission through a platform tool.

**Watch out for.**
- SPEC-1 Edit (b) and CODE-3 apply only after the 0102 steps they depend on (see **Impacts on other proposals**, row 0102).
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

## Defects in the shipped tree that this proposal does not stage

- **`ErrParentNotRunning` is unmapped in `lenny/delegate_task`.** Admission's not-running sentinel reaches the tool caller without a catalogued code. It concerns non-terminal parent states, outside the parent-terminal race. REC-1 files it once confirmed.
- **The Token Service's external `a2a_delegation` record omits `parent_jti` and `act_sub`.** `RevokeCascade` then cannot reach an external delegation child. It concerns the external path, outside the validated problem. REC-1 files it once confirmed.

## Impacts on other proposals

| Proposal | Status | What this change does to it | What it must do |
|:--|:--|:--|:--|
| 0102 (impersonation revocation) | Approved (2026-10-26, 0102 status record) | 0104 removes the session-creation `session_capability` mint that 0102 D20, D22, and SPEC-2 Edit (b) attach the step (1a) impersonation refusal to. SPEC-1 Edit (b) replaces the sentences 0102 SPEC-2 Edit (b) writes and moves the refusal onto session creation itself. The refusal, its audited rejection, and `exchange_type: session_mint` survive (D3, D4). The mint that D22 names, and the D20 condition "once the session-creation mint lands", lose their subject. CODE-3 builds the check that 0102 CODE-2 item 8 left unbuilt. It depends on 0102 CODE-2 item 7 (`SourceRefusal`) to compile, and on 0102 CODE-2 item 9 (`RecordImpersonation`), written through 0102 CODE-3 item 3, to have impersonation rows to refuse. DOCS-1 moves the docs statement from 0102 non-spec (d) to `docs/operator-guide/security.md`, and REC-1 replaces the F-10.2.25 **Suggested resolution:** text that 0102 non-spec (d) appends to. | 0102's spec steps and CODE-2 item 7 land before 0104 S1 and S8, and 0102 CODE-3 item 3 lands before S10. 0102's files are not edited. |
| 0100 (terminal pipeline) | Approved | 0100 SPEC edits the *Drain* half of the §12.8 "Lifecycle fence and drain" paragraph; SPEC-3 edits the *Creation fence* half. 0100 also moves the post-terminal steps in `sessionserver/usage.go`, where the cascade runs. D6 needs the cascade to run after the parent's terminal commit, which 0100 keeps, and CODE-2 item 7 changes the cascade's listing in the same file. | Whichever lands second applies its §12.8 edits by quoted anchor with no change to content. 0104's implementor confirms the cascade still runs after the terminal commit and keeps CODE-2 item 7. |
| 0103 (production mode) | Approved | 0103 CODE-6 edits other alert descriptions in `pkg/alerting/rules/rules.go` and regenerates the same rule files as CODE-4. The descriptions are disjoint. | Whichever lands second re-runs `make generate`. |
| 0097 (shared JWT signing) | Implemented (2026-10-26, 0097 status record) | 0097 D13 kept the session-creation `session_capability` token, which 0097 also names as the internal delegation `actor_token`, on its per-dialect cap. SPEC-1 Edit (b) removes the session-creation mint, SPEC-1 Edit (c) deletes the §10.2 sentence that carries the cap, and SPEC-2 removes the internal delegation mint, so that part of the D13 carve-out loses its subject. The `auth.capabilityTokenTtlSeconds` default and the no-default `requested_exp` rule for an external `a2a_delegation` child survive. | Nothing. 0097 is not edited. |

## Deliverable index

- SPEC-1 — spec/10_gateway-internals.md — remove the session-creation mint and pod-presented-token claims; anchor the step (1a) caller check on session creation; move the pod-request signature check to the §10.3 projected SA token
- SPEC-2 — spec/08_recursive-delegation.md — replace the §8.2 child-token exchange with child identity and parent freshness, and drop the Token Service from the §8.1 enforcement clause
- SPEC-3 — spec/12_storage-architecture.md — the creation fence gains the parent-terminal rule
- SPEC-4 — spec/15_external-api-surface.md — error catalog, token-endpoint row, the `CallerIdentity.Act` and `ActorClaim` comments, and the MCP endpoint boundary
- SPEC-5 — spec/04, 05, 07, 10, 13, 16, 17, 18, 23, and 27 — drop internal delegation minting and the session mint from every restatement
- CODE-1 — pkg/gateway/mcpfabric/delegation/ — delete the unsigned child-token minter and the audit-contention path
- CODE-2 — pkg/gateway/session/sessionstore/ and pkg/gateway/sessionserver/usage.go — the fence refuses a delegated child of a terminal or absent parent, and the cascade lists children from the primary
- CODE-3 — pkg/gateway/callercheck/ — session creation and client-facing delegation apply the step (1a) caller check
- CODE-4 — pkg/alerting/rules/rules.go — the `TokenStoreUnavailable` description drops delegation minting
- TEST-1 — tests/ and package tests — parent freshness, the caller check, and KMS independence across tiers
- DOCS-1 — docs/ — published pages follow the spec
- REC-1 — BUILD-GAPS.md, TEST-GAPS.md, gateway-runtime-comms-remediation.md — close the findings, route the adjacent ones, and record the absent forwarded-call binding
