# Deviations — 0104_fix_two-gaps-leave-lenny-s-session-scoped-and-delegation

The implementor owns this file. It stays empty until an implementation records a departure from what the proposal states.

## Proposed: Delegated child with an empty parent session ID is refused as parent-terminal

**Status:** accepted (2026-10-28, orchestrator under the owner's delegation)

**Reported by:** step S6 (CODE-2: creation fence parent-terminal rule and primary-read cascade walks).

**What the proposal says.** CODE-2 items 3 and 4 in the non-spec changes state that when `delegatedChild` is true, `readCreationMarkers` runs `SELECT state FROM sessions WHERE tenant_id = $1 AND id = $2::uuid FOR SHARE`. A missing row sets `ParentTerminal`, and any other read error fails the insert closed. `Reserve` passes an empty parent ID and reads no parent row. The proposal does not state what the insert path does for a delegated child (`delegation_depth > 0`) whose parent session ID is empty.

**What landed instead.** The fence takes a separate `checkParent` flag, which is false only for `Reserve`. On the insert path, a delegated child with an empty `parentSessionID` counts as parent-terminal without a query and is refused with `DELEGATION_PARENT_REVOKED`, with details `{parentSessionId: ""}`. The rule is in `readParentTerminal` in `pkg/gateway/session/sessionstore/pgstore/creation_fence.go` and in `parentTerminalLocked` in `pkg/gateway/session/sessionstore/memstore/fence.go`. It landed in commit `f180369d1` and is kept. The failing sessionserver eval fixture was corrected to seed a running parent chain.

**Why.** The proposal leaves this case open. §12.8 states that the insert is refused when the parent has no row, and an empty ID names no row. Without the rule, Postgres inserts the row with a NULL `parent_session_id`, which the foreign key does not reject, so the child escapes the parent rule and the fence fails open. Running the proposal's literal query with an empty ID fails the `''::uuid` cast, which fails closed with a wrapped error instead of the catalogued refusal. The verifier read this rule as going beyond the step text, so a human should confirm it.

**What a later reader would otherwise get wrong.** A reader of CODE-2 alone would expect `checkParent` to be inferred from an empty parent ID, and would expect an empty-parent delegated insert either to proceed without a parent check or to fail with a uuid cast error. The tree refuses that insert with `DELEGATION_PARENT_REVOKED`, and `Reserve` is distinguished by the `checkParent` flag rather than by the empty parent ID.

## Proposed: CODE-1 grep stop condition matches two absence regression tests

**Status:** accepted (2026-10-28, orchestrator under the owner's delegation)

**Reported by:** step S7 (CODE-1: delete the unsigned child-token minter and audit-contention path).

**What the proposal says.** The CODE-1 stop condition in the non-spec changes states that a grep for `ChildToken`, `child_token_jti`, `act_chain`, `delegation_token_scope`, `DELEGATION_AUDIT_CONTENTION`, and the other removed identifiers matches only the sessionstore `DELEGATION_PARENT_REVOKED` constant and unrelated `a2a_delegation` code in `pkg/tokenservice`.

**What landed instead.** Two regression tests added in this step still contain removed strings, because each one asserts that the removed surface is absent. `pkg/gateway/externalapi/errorclassify/errorclassify_test.go` holds `TestDelegationAuditContentionIsNotAClassifiedCode`, which checks that `DELEGATION_AUDIT_CONTENTION` is no longer a known code. `pkg/gateway/mcpfabric/delegation/child_identity_test.go` checks that the spawned audit record carries no `child_token_jti`, `act_chain`, or `delegation_token_scope` key.

**Why.** The step rules require a regression test for each corrected outcome, and a test that asserts a removed code or key is absent has to name it. The step reports that no production code, registry, or spec-map entry still carries these strings.

**What a later reader would otherwise get wrong.** A reader who runs the stop-condition grep and finds matches in these two test files would conclude that CODE-1 left the removed surface in place. The matches are absence assertions in tests, and the production tree carries none of the identifiers.

## Proposed: SA-token citation sweep also renamed test function suffixes

**Status:** accepted (2026-10-28, orchestrator under the owner's delegation)

**Reported by:** step S7 (CODE-1: delete the unsigned child-token minter and audit-contention path).

**What the proposal says.** The CODE-1 comment sweep retargets SA-token citations of §10.2 to §10.3 "Projected SA token". It does not mention test function names.

**What landed instead.** The test functions in `leasecontrol/satoken_verify_test.go` with the suffix `_spec_10_2_227` were renamed to the suffix `_spec_10_3`. In `serviceidentity_test.go`, `TestResolverScreensTheAudienceBeforeTheTokenReview_spec_10_2` was renamed to `TestResolverScreensTheAudienceBeforeTheTokenReview_spec_10_3`. The step reports that `spec-map.json` references none of these names.

**Why.** The name suffix is a spec annotation that cited §10.2, and in the first file it also carried a spec line number (227). Keeping the old suffix would have kept the stale citation the sweep exists to remove.

**What a later reader would otherwise get wrong.** A reader who searches for the old test names from the proposal era, or who reads the sweep as limited to comments, would not find these functions and would not know that the sweep renamed them.

## Proposed: Session-server caller check is wired in sessionsrv.go

**Status:** accepted (2026-10-28, orchestrator under the owner's delegation)

**Reported by:** step S8 (CODE-3: session-creation caller check).

**What the proposal says.** The implementation checklist and the CODE-3 targets state that the sessionserver dependencies are wired in `cmd/lenny-gateway/httpsurface.go` and `cmd/lenny-gateway/mcpsurface.go`.

**What landed instead.** The `sessionserver.Options` are built in `buildSessionServer` in `cmd/lenny-gateway/sessionsrv.go`, so the sessionserver `CallerCheck` field is set there. The shared constructor `sessionCallerCheck` lives in `httpsurface.go`, and `mcpsurface.go` sets the mcptools field from it.

**Why.** `httpsurface.go` receives a `*sessionserver.Server` that is already constructed. Setting the option where the server is built avoids adding a post-construction setter whose only purpose is to place the wiring line in `httpsurface.go`.

**What a later reader would otherwise get wrong.** A reader who follows the CODE-3 targets would look for the sessionserver wiring in `httpsurface.go` and not find it. The sessionserver field is set in `sessionsrv.go`, and `httpsurface.go` holds only the shared constructor.

## Proposed: Caller-check audit write goes through the audit validator

**Status:** accepted (2026-10-28, orchestrator under the owner's delegation)

**Reported by:** step S8 (CODE-3: session-creation caller check).

**What the proposal says.** CODE-3 item 1 marks the audit adapter as the implementor's choice and gives as an example a production `Audit` adapter that calls `auditstore.AcquireLockInTx` and then `auditstore.AppendInTx` in one transaction. The step task text names the same two calls.

**What landed instead.** `Audit` is `w.auditValidator`, an `auditscope.Validator` over the durable `auditstore.Store`. Its `Append` takes the §11.7 per-tenant advisory lock and commits in one transaction before it returns. The wiring is in `sessionCallerCheck` in `cmd/lenny-gateway/httpsurface.go`.

**Why.** The step reports that this meets the stated constraint, which is that the lock is taken and the row is committed before the call returns, and that it reuses the store's shard routing, lock-timeout retry, and ops-stream escalation. A second transaction written by hand over the raw pool would bypass the audit shard router. The proposal gives the two calls only as an example.

**What a later reader would otherwise get wrong.** A reader who searches the caller-check path for `AcquireLockInTx` or `AppendInTx` would find neither call and could conclude that the audit write takes no lock. The lock and the commit happen inside the validator's `Append`.

## Proposed: Caller-check dependencies are grouped in one CallerCheck field

**Status:** accepted (2026-10-28, orchestrator under the owner's delegation)

**Reported by:** step S8 (CODE-3: session-creation caller check).

**What the proposal says.** CODE-3 adds two new dependencies, `IssuedTokens` and `Audit`, to the sessionserver options and to the mcptools `Deps`.

**What landed instead.** Each struct gains one field, `CallerCheck callercheck.Deps`, which carries both `IssuedTokens` and `Audit`. The fields are `Options.CallerCheck` in `pkg/gateway/sessionserver/sessionserver.go` and `Deps.CallerCheck` in `pkg/gateway/mcpfabric/mcptools/mcptools.go`.

**Why.** The mcptools `Deps` already has an unrelated `Audit` field. Grouping the two dependencies in the check's own `Deps` type avoids the name clash and keeps the two wiring sites identical.

**What a later reader would otherwise get wrong.** A reader of CODE-3 would expect top-level `IssuedTokens` and `Audit` fields on both structs, and could mistake the existing mcptools `Audit` field for the caller-check audit dependency. Both dependencies sit under `CallerCheck`.

## Proposed: KMS independence test sits in the tier 8 chaos suite

**Status:** accepted (2026-10-28, orchestrator under the owner's delegation)

**Reported by:** step S10 (TEST-1: parent freshness, caller check, and KMS independence tests, narrowed to the unlanded entries plus an audit).

**What the proposal says.** The step header names `tests/tier2_component/tokensigning/kms_outcome_test.go` as the location of the KMS independence test. It allows `tests/tier8_chaos/jwt_keyset_chaos_test.go` only when the session surface is reachable only from tier 8, citing the §10.2 "KMS signing failure mode" paragraph.

**What landed instead.** The KMS independence test is in `tests/tier8_chaos/jwt_keyset_chaos_test.go`. An earlier attempt of this step placed it there, and the later attempt left it in place.

**Why.** The step reports that the earlier attempt chose tier 8 and that the verifier confirmed the test passes there in all three signer states. The later attempt changed only what was needed to clear a lint finding.

**What a later reader would otherwise get wrong.** A reader who follows the step header would look for the test in `tests/tier2_component/tokensigning/kms_outcome_test.go` and not find it. The step report does not state that the session surface is reachable only from tier 8, so a reader could also assume the header's condition for using tier 8 was established when the step does not record that.
