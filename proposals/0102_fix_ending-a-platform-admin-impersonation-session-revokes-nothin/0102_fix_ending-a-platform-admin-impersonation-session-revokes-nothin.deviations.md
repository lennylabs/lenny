# Deviations — 0102_fix_ending-a-platform-admin-impersonation-session-revokes-nothin

The implementor owns this file. It stays empty until an implementation records a departure from what the proposal states.

## Proposed: Impersonation store methods live in a separate file

**Status:** accepted (2026-10-27, orchestrator under the owner's delegation)

**Reported by:** step S9 (CODE-2: issuedtokenstore impersonation queries, fenced record, step (1a) refusal, playground check).

**What the proposal says.** The non-spec changes for this step place the impersonation queries and the fenced record in `pkg/gateway/storage/issuedtokenstore/issuedtokenstore.go`.

**What landed instead.** The code is in a new file in the same package, `pkg/gateway/storage/issuedtokenstore/impersonation.go`. That file holds `impersonationPredicate`, `NotActiveError` and `ErrNotActive`, `UserInvalidatedError`, `RecordImpersonation`, `EndImpersonation`, `ListActiveImpersonations`, and `RevokeImpersonationsByActor`. `issuedtokenstore.go` receives only doc-comment updates.

**Why.** The step reports that `code-best-practices.md` asks for one concern per file, and `issuedtokenstore.go` was already 816 lines. The package API and behavior match what the proposal specifies.

**What a later reader would otherwise get wrong.** A reader following the non-spec changes would look for these symbols in `issuedtokenstore.go` and would not find them there.

## Proposed: selectRevokedAt renamed to selectSourceStates

**Status:** accepted (2026-10-27, orchestrator under the owner's delegation)

**Reported by:** step S9 (CODE-2: issuedtokenstore impersonation queries, fenced record, step (1a) refusal, playground check).

**What the proposal says.** The step (1a) item states that `selectRevokedAt` also reads the impersonation predicate per jti.

**What landed instead.** `selectRevokedAt` in `pkg/gateway/storage/issuedtokenstore/recheck.go` is renamed to `selectSourceStates`. It returns `revoked_at` and the impersonation predicate result for each jti. `recheckSources` consumes both values: it checks revocation across all sources first and then checks impersonation.

**Why.** The step reports that the function now returns more than the revocation time, so the old name would mislead. No code outside `recheck.go` referenced the function.

**What a later reader would otherwise get wrong.** A reader searching for `selectRevokedAt` from the proposal text would find no such function and could conclude that the step (1a) predicate read was never implemented.

## Proposed: RecordImpersonation refuses an empty act_sub or a set parent

**Status:** accepted (2026-10-27, orchestrator under the owner's delegation)

**Reported by:** step S9 (CODE-2: issuedtokenstore impersonation queries, fenced record, step (1a) refusal, playground check).

**What the proposal says.** `RecordImpersonation(ctx, tok, callerTenantID)` returns an error and writes nothing when `callerTenantID` is empty. The proposal states no other input check.

**What landed instead.** `RecordImpersonation` in `pkg/gateway/storage/issuedtokenstore/impersonation.go` also returns an error without writing when `tok.ActSubject` is empty or `tok.ParentJTI` is set.

**Why.** The step reports this as a fail-closed check. A row without `act_sub` (which `NullString` would write as NULL) or with a parent would not match the impersonation predicate, so `EndImpersonation`, `ListActiveImpersonations`, and the actor cascade could never reach it. Such a row would recreate the unrevocable-bearer defect the proposal fixes.

**What a later reader would otherwise get wrong.** A reader of the proposal would expect `RecordImpersonation` to accept any token with a non-empty caller tenant, and would not anticipate errors for a token with an empty `ActSubject` or a set `ParentJTI`.

## D4 · step S13 · 2026-10-27 · accepted
**Status:** accepted
**Proposal says:** The TEST-2 bullet in `0102_fix_ending-a-platform-admin-impersonation-session-revokes-nothin.non-spec-changes.md` (line 226) states: "After it is ended, the same exchange returns reason `subject_token_revoked`." The same exchange is the self-rotation, in which the ended impersonation bearer is both the caller and the `subject_token`.
**Implemented instead:** The self-rotation subtest of `TestImpersonationBearerRefusals_spec_13_3` in `tests/tier9_security/ops_revocation_enforcement_test.go` asserts two refusals after the session ends. `requireCallerRevoked(t, a)` (helper at lines 531-542, called at line 593) asserts that the self-rotation of the ended bearer is refused at caller authentication with 401 `TOKEN_REVOKED`. `requireExchangeRefused(t, b, a, "subject_token_revoked")` (line 594) presents the ended bearer as `subject_token` under the target's live user bearer `b` as caller, and asserts `invalid_grant` with reason `subject_token_revoked`.
**Why:** The judges established that §13.3 makes the proposal's sentence unreachable. Rule 5 under "Platform-admin impersonation" requires every gateway replica to refuse a revoked impersonation bearer through its revocation cache, and the "Client authentication on /v1/oauth/token" paragraph authenticates the caller bearer before the exchange runs. Step (1a) of "Write-before-issue ordering", which returns `subject_token_revoked` for a revoked `subject_token` jti, runs only inside the exchange transaction after the caller has authenticated. An ended bearer used as the caller is therefore refused with 401 `TOKEN_REVOKED` before step (1a). Changing the test to match the proposal's sentence would assert behavior that contradicts §13.3 and would fail against correct code. The fixer may not edit proposal files, so no legal code or test change closes the finding.
**Consequence if the proposal is not corrected:** A later reader or implementor following TEST-2 would expect `invalid_grant` `subject_token_revoked` for a self-rotation of an ended bearer. That reader would treat the 401 `TOKEN_REVOKED` the test asserts as a defect, and could change the gateway or the test away from §13.3.
**Suggested next step:** correct the proposal. Done 2026-10-27: the TEST-2 bullet now states both refusals.
**Evidence:** Commit 649d6d49c (round 2) changed only `tests/tier9_security/ops_revocation_enforcement_test.go` (+35/-13). It added `requireCallerRevoked` and moved the `subject_token_revoked` assertion under the live caller `b`. Rounds 3, 4, and 5 made no commit, and each review re-raised the finding with the framing that the code is right and the proposal text is wrong. Three judges, each at high confidence, classified the finding as unresolvable within the loop. They found the landed test consistent with §13.3, found that the remaining gap is a proposal-record defect that only a deviation entry or a correction to the TEST-2 bullet closes, and forecast that further rounds would repeat without a commit.

## Proposed: Nested impersonation subtest asserts no added session and provisions the target through the admin API

**Status:** accepted (2026-10-27, orchestrator under the owner's delegation)

**Reported by:** step S13 (TEST-2: two-replica integration and lenny-ops security tests).

**What the proposal says.** In the TEST-2 nested impersonation case, the GET listing of active impersonation sessions returns only the outer session. The target user is provisioned with a platform-managed mapping that lists `platform-admin`.

**What landed instead.** The nested subtest of `TestImpersonationBearerRefusals_spec_13_3` in `tests/tier9_security/ops_revocation_enforcement_test.go` asserts that the refused nested POST adds no session to the listing and that the outer session appears in it. The subtest does not assert that the outer session is the only entry. It provisions the target through `POST /v1/admin/users` with the `platform-admin` role rather than through a platform-managed mapping. This subtest came from a prior attempt at the step, and the current fix left it unchanged.

**Why.** The step reports that the subtests share one stack, so sessions created by earlier subtests also appear in the listing and an "only the outer session" assertion cannot hold. The verifier recorded this departure in the prior attempt.

**What a later reader would otherwise get wrong.** A reader following TEST-2 would expect the nested subtest to assert a listing that holds exactly the outer session and to provision the target through a platform-managed mapping. That reader would find a weaker listing assertion and a different provisioning path, and could conclude that the nested case was not implemented as staged.

## Proposed: Invalidate section also states the tenant-admin caller case

**Status:** accepted (2026-10-27, orchestrator under the owner's delegation)

**Reported by:** step S14 (DOCS-1: operator guide, admin API reference, CMP-058 trims, BUILD-GAPS, tier-11 test).

**What the proposal says.** DOCS-1 (c) extends the `POST /v1/admin/users/{userId}/invalidate` section of `docs/api/admin.md` with one sentence. The sentence states that in `full_revoke` mode a platform administrator's invalidation also revokes, in every tenant, the impersonation bearers the user started, and counts them in the revoked-token total.

**What landed instead.** The invalidate section of `docs/api/admin.md` carries the specified sentence and a second sentence after it. The second sentence states that when the caller is a tenant-admin, the call revokes only the user's own tokens and reports, in the response's token revocation error, that the revocation of impersonation bearers the user started was skipped.

**Why.** The step reports that §13.3 "Platform-admin impersonation" rule 6 states this behavior for a caller without platform-admin, and that the invalidate section names tenant-admin as an allowed caller. The step reports that without the second sentence the section would cover only the platform-admin case, which `doc-content.md` "Cover the page's whole subject" does not permit.

**What a later reader would otherwise get wrong.** A reader comparing the page against DOCS-1 (c) would find two sentences where the proposal stages one, and could treat the tenant-admin sentence as unstaged content to remove.

## Proposed: POST impersonation 403 entry states which refusals write audit events

**Status:** accepted (2026-10-27, orchestrator under the owner's delegation)

**Reported by:** step S14 (DOCS-1: operator guide, admin API reference, CMP-058 trims, BUILD-GAPS, tier-11 test).

**What the proposal says.** DOCS-1 (b) has the `POST /v1/admin/impersonation` section state the 403 `FORBIDDEN` cases and the request, response, and errors of the §15.1 row. It does not say which refusals write audit events.

**What landed instead.** The 403 entry of `POST /v1/admin/impersonation` in `docs/api/admin.md` states that a refusal caused by the caller's bearer writes no audit event. It also states that a refusal caused by the hard-disable or full-revoke fence writes no rejection audit event, and that the started event written before that refusal has no ended event.

**Why.** The step reports that under §13.3 rule 1, step (a) appends the started event before the step (c) fence runs, so a blanket statement that 403 refusals write no audit event would be wrong for the fence case.

**What a later reader would otherwise get wrong.** A reader following DOCS-1 (b) would not expect audit-event statements in the 403 entry, and could treat them as unstaged content to remove. A reader who assumed that no 403 refusal writes an audit event would not expect an orphaned started event after a fence refusal.
