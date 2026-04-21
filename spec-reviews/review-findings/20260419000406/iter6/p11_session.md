# Perspective 11 — Session Lifecycle & State Management (iter6)

**Scope:** Verify iter5 SES carry-forwards (SES-019, SES-020, SES-021) and cross-cutting iter5 fixes touching session lifecycle — specifically CNT-015 (`gitClone.ref` pinning to `sources[<n>].resolvedCommitSha` at session creation, with per-session immutability on retry/resume/checkpoint-restore materializations) — for correctness, reachability, and reliability regressions.

**Numbering:** SES-022+ (iter5 ended at SES-021). SES-015–SES-018 closed in iter5; SES-019–SES-021 remain open as Low-severity documentation/policy completeness gaps that carry-forward unchanged.

**Spec surfaces under review:** `spec/06_warm-pod-model.md` §6.2, `spec/07_session-lifecycle.md` §§7.1–7.4, `spec/10_gateway-internals.md` §§10.1, 10.4, `spec/11_policy-and-controls.md`, `spec/14_workspace-plan-schema.md` (CNT-015 surface), `spec/15_external-api-surface.md` §15.1.

**Severity anchoring:** iter1–iter5 rubric preserved per `feedback_severity_calibration_iter5.md`. Low-severity documentation/policy completeness gaps stay Low.

---

## 1. Iter5 carry-forward verification

### SES-019 (carry-forward from iter4/iter5) — `POST /v1/sessions/{id}/start` precondition/result table still omits `resume_pending`
**Severity:** Low
**Location:** `spec/15_external-api-surface.md` §15.1 line 618
**Finding:** The `POST /v1/sessions/{id}/start` row still lists the resulting transition as `starting → running` only. The session state machine in `spec/07_session-lifecycle.md` §7.2 line 166 (`starting → resume_pending (pod crash / gRPC error during agent runtime launch, retryCount < maxRetries)`) and `spec/06_warm-pod-model.md` §6.2 line 103 both document `starting → resume_pending` as a valid edge that can materialize during the `/start` call (post-attached visibility of a mid-launch pod crash). The §15.1 table remains inconsistent with both authoritative state tables.
**Impact:** Clients consulting only the external API reference will not know that `POST /start` can legitimately leave the session in `resume_pending` awaiting replacement-pod allocation. Incorrect client state machines will treat any non-`running` state after `/start` as an error. No runtime/correctness impact — the underlying state machine is unambiguous — but the API reference is out of sync with the authoritative §7.2/§6.2 tables.
**Recommendation:** Update the §15.1 row for `POST /v1/sessions/{id}/start` resulting-transition cell to `starting → running | starting → resume_pending`, with a note (in the same row or an adjacent paragraph) that the latter fires when the runtime launch encounters a retryable error with retries still remaining, and that the client should listen for `status_change(state: "running")` or `session.resumed` on the event stream rather than assuming the synchronous response covers all outcomes.
**Status vs iter5:** Carry-forward — issue verified still open in iter6 at the same location; no spec/15 edits to the `/start` row landed in the iter5 fix commit (`c941492`). The iter5 fix commit touched spec/15 line 597 (`GET /v1/sessions/{id}` description augmented with `resolvedCommitSha` audit visibility — CNT-015) but did not edit the precondition table.

---

### SES-020 (carry-forward from iter4/iter5) — SSE reconnect/replay has no rate limit or per-reconnect replay cap
**Severity:** Low
**Location:** `spec/07_session-lifecycle.md` §7.2 lines 349–363 (Reconnect / back-pressure semantics); `spec/10_gateway-internals.md` §10.4 line 389 (event replay buffer)
**Finding:** The replay window is defined as `max(periodicCheckpointIntervalSeconds × 2, 1200s)` and the `OutboundChannel` back-pressure policy bounds a single slow client's memory footprint at the per-connection level, but there is still no (a) per-session or per-principal reconnect rate limit (a client may reconnect with `Last-Event-ID` or `resumeFromSeq` arbitrarily often) and (b) no per-reconnect replay volume cap (a client reconnecting near the 1200s horizon on every attempt will replay the full window each time). Neither `spec/11_policy-and-controls.md` nor `spec/10_gateway-internals.md` §10.4 introduces a rate limit keyed by `(tenant_id, session_id)` or `(principal, session_id)` for reconnect attempts, nor a byte cap beyond which the gateway short-circuits to a `gap_detected` frame and forces the client to re-snapshot via a synthesized `status_change`.
**Impact:** Amplification-style resource-exhaustion vector against the gateway's event replay subsystem (CPU + egress). A malicious or buggy client can impose O(N) replay cost per reconnect, and a script that reconnects every second over a 1200s window amplifies gateway egress by ~1200× relative to an in-protocol client. Not a correctness defect — the data emitted is still correct — but a missing reliability/defense-in-depth control. Consistent with iter3 SES-012 / iter4 SES-020 framing.
**Recommendation:** Add a per-session reconnect rate limit (token bucket with configurable burst/refill, e.g., `gateway.sessionReconnectRateLimit.perSessionPerMinute` default 12 and `.burstCapacity` default 6) in `spec/11_policy-and-controls.md` alongside the existing `sessionEventReplayBufferDepth` policy, with a `RECONNECT_RATE_LIMITED` error code (HTTP 429 on the streaming attach). Additionally specify a per-reconnect replay byte cap (e.g., `gateway.sessionEventReplayMaxBytesPerReconnect` default 8 MiB) beyond which the gateway emits a single `gap_detected` frame plus a synthesized `status_change` from durable Postgres state and requires the client to re-snapshot rather than continuing to drain the full historical replay.
**Status vs iter5:** Carry-forward — issue verified still open in iter6; no spec changes landed in iter5 at §7.2 lines 349–363 or §10.4 line 389. The iter5 fix commit touched `spec/10_gateway-internals.md` only by ±2 lines (likely cross-referencing); the event replay buffer prose is unchanged.

---

### SES-021 (carry-forward from iter4/iter5) — `awaiting_client_action → expired` trigger is ambiguous across three timers
**Severity:** Low
**Location:** `spec/07_session-lifecycle.md` §7.2 line 202 (state-machine edge); §7.3 lines 421–428 (`awaiting_client_action` semantics)
**Finding:** The edge `awaiting_client_action → expired (lease/budget/deadline exhausted while awaiting client action)` collapses three distinct timers into a single free-text trigger: (a) `maxAwaitingClientActionSeconds` (900s default, state-specific timer from §11 line 191), (b) session budget exhaustion (delegation tree budget counters; see §11 Crash Recovery for Delegation Budget Counters paragraph at line 48), and (c) absolute session TTL / deadline (`retryPolicy.maxSessionAgeSeconds`, default 7200s). The spec does not disambiguate which timer fires this specific edge, nor record a distinct `terminal_reason` code per trigger. Related edges elsewhere in §7.2 also collapse multiple triggers into a single `expired` terminus without reason disambiguation (`suspended → expired` at line 188 uses "delegation lease perChildMaxAge wall-clock expiry"; `running → expired` at line 175 uses "lease/budget/deadline exhausted"; `input_required → expired` at line 178 uses "deadline reached"). No canonical `terminal_reason` enum is defined in §7 or §16.
**Impact:** Operators cannot distinguish "client failed to respond in time" from "budget exceeded while waiting" from "absolute deadline hit" in audit/observability output. Incident triage, billing reconciliation, and automated client resume-vs-fork decisioning all suffer — a CI pipeline polling `GET /v1/sessions/{id}` and receiving `state: "expired"` cannot programmatically distinguish the three cases. Billing teams cannot reconcile pod-hours consumed during a long `awaiting_client_action` wait vs. a short budget-exhaustion terminus. Not a correctness bug; a documentation/policy completeness gap.
**Recommendation:** Introduce a canonical `terminal_reason` enum (sibling to `failureClass` at §7.1 line 102) covering at minimum `awaiting_client_action_timeout`, `budget_exhausted`, `session_deadline_exceeded`, `perChildMaxAge_expired`, and `delegation_lease_expired`. Split the §7.2 transition row for `awaiting_client_action → expired` into three rows (or enumerate three sub-triggers) each carrying a distinct `terminal_reason`. Reference the relevant policy knob from §11 (`maxAwaitingClientActionSeconds`, session budget counters, `maxSessionAgeSeconds`) for each. Surface the field on `GET /v1/sessions/{id}` and on the terminal event payload (`status_change(state: "expired", reason: "...")`). Apply the same disambiguation to `suspended → expired`, `running → expired`, and `input_required → expired` so the enum is exhaustive.
**Status vs iter5:** Carry-forward — issue verified still open in iter6 at §7.2 line 202; the `awaiting_client_action` semantics paragraph at §7.3 lines 421–428 was unchanged in iter5. No `terminal_reason` enum exists in `spec/07`, `spec/11`, or `spec/15`.

---

## 2. Cross-cutting iter5 fix verification — CNT-015 (gitClone.ref pinning) and its session-lifecycle implications

The iter5 fix commit (`c941492`) introduced the `sources[<n>].resolvedCommitSha` field to `WorkspacePlan` via CNT-015. Session-lifecycle implications of this fix (per-session immutability of cloned repo contents across retries, resumes, and checkpoint restores) are relevant to §7.3 resume flow. The following checks verify the fix integrates cleanly with the session state machine.

### CNT-015 integration check 1 — §14 resolution language is consistent with §7.3 resume flow
`spec/14_workspace-plan-schema.md` line 102 declares `resolvedCommitSha` is persisted alongside the stored `WorkspacePlan` and is re-used for "retries within `retryPolicy.maxResumeWindowSeconds`, resumes after pod eviction, checkpoint restores". `spec/07_session-lifecycle.md` §7.3 lines 399–411 enumerate the resume flow as: `resume_pending` → claim new pod → replay checkpoint → restore session file → resume. The §14 reconciliation-loop prose at `spec/14_workspace-plan-schema.md` line 324 explicitly states that replayed `gitClone` entries use `resolvedCommitSha` rather than re-resolving `ref`. These references are aligned; no new finding.

### CNT-015 integration check 2 — §15 `GET /v1/sessions/{id}` surfaces the pinned SHA for audit
`spec/15_external-api-surface.md` line 597 was updated in iter5 to document that `GET /v1/sessions/{id}` includes `sources[<n>].resolvedCommitSha` in the response's `workspacePlan`. This provides the audit and debugging visibility SES-heavy operators need. No new finding.

### CNT-015 integration check 3 — Independent-session boundary (delegation children, retry-beyond-resume-window) is explicit
`spec/14_workspace-plan-schema.md` line 102 calls out explicitly that "Sessions that reference the same `WorkspacePlan` template but are independent sessions (recursive delegation children, session-from-session retries initiated after the original session's `maxResumeWindowSeconds` has elapsed, or any `POST /v1/sessions` that reuses the plan body) re-resolve `ref`". This correctly scopes the immutability guarantee as **per-session**, not **per-plan**, avoiding the failure mode where a derived or retried session silently pins to the parent's SHA. No new finding.

### CNT-015 residual gap observation (not a finding)
`spec/07_session-lifecycle.md` §7.3 step 3e ("Replay latest workspace checkpoint") does not explicitly cross-reference the `resolvedCommitSha` re-use. An editorial nit — a parenthetical `(see §14 gitClone.ref resolution for gitClone re-materialization from pinned SHA)` would make the per-session immutability property self-contained from §7.3 — but the chain of references via §14's "live consumer" paragraph at line 324 is complete and not ambiguous. Raising this as an editorial suggestion in the recommendation below rather than as a new finding.

---

## 3. New findings in iter6

None.

Scan of `spec/06` §6.2, `spec/07` §§7.1–7.4, `spec/10` §§10.1 and 10.4, `spec/11`, and `spec/15` §15.1 for correctness, reliability, or security defects introduced by iter5 fixes (CNT-015, CNT-014, BLD-012, POL-023, CRD-015, OBS-031/032, CMP-054/057/058, etc.) surfaced no new session-lifecycle issues. The CNT-015 fix is correctly scoped per-session with explicit cross-references to the resume flow. The iter5 fix commit did not modify `spec/07` (zero diff lines), so the session state machine and resume flow are unchanged from iter5 and all iter5-closed findings (SES-015 through SES-018) remain closed.

---

## 4. Per-finding status vs. iter5

- **SES-015** (`resuming → cancelled/completed` missing from §6.2): **Fixed in iter5** — §6.2 lines 134–135 enumerate both terminal edges with snapshot-close semantics cross-reference. Re-verified closed in iter6.
- **SES-016** (pre-attach terminal collapse `resume_pending → cancelled/completed` unspecified): **Fixed in iter5** — §7.2 lines 193–194 and §6.2 lines 138–139 enumerate the pre-attach collapse edges with explicit "no pod attached → no snapshot-close sequence" semantics. Re-verified closed in iter6.
- **SES-017** (generation-counter bookkeeping for mid-resume terminals unspecified): **Fixed in iter5** — §7.2 step 4 ("Generation bookkeeping") specifies `coordination_generation` advance and `recovery_generation` freeze, with partial-manifest tagging. §4.2 formalizes the monotonicity invariants. Re-verified closed in iter6.
- **SES-018** (`created → failed` derive-failure edge atomicity and reachability): **Fixed in iter5** — §7.1 line 28 conditions atomicity on `persistDeriveFailureRows: false`; §7.2 line 164 enumerates the edge as audit-only; §15.1 lines 647–663 provide the endpoint reachability table. Re-verified closed in iter6.
- **SES-019** (`POST /start` precondition table missing `resume_pending`): **Carry-forward** — open, Low severity.
- **SES-020** (SSE reconnect rate limit + per-reconnect replay cap missing): **Carry-forward** — open, Low severity.
- **SES-021** (`awaiting_client_action → expired` trigger disambiguation): **Carry-forward** — open, Low severity.

## 5. Counts

- Critical: 0
- High: 0
- Medium: 0
- Low: 3 (all carry-forward from iter4/iter5)
- Info: 0

## 6. Convergence assessment

**Verdict:** **Not converged.**

**Rationale:** No new findings surfaced in iter6 for Perspective 11. The four iter4/iter5-closed findings (SES-015 through SES-018) remain closed and all session-state-machine edits landed in iter5 are verified consistent. The CNT-015 gitClone.ref pinning fix introduced in iter5 is correctly scoped at the per-session boundary and cross-references the resume flow without introducing correctness defects in the session lifecycle. However, the three Low-severity carry-forwards (SES-019, SES-020, SES-021) remain unaddressed for the second consecutive iteration. Each has a clear, bounded fix — an API-reference table row edit for SES-019, a policy-knob addition in §11 plus a per-reconnect replay byte cap in §10.4 for SES-020, and a `terminal_reason` enum introduction plus three-row expansion at §7.2 for SES-021. None of the three blocks deployment individually, but convergence requires zero open findings against the prior iteration.

**Convergence blockers:** 3 Low findings (SES-019, SES-020, SES-021).

**Recommendation for iter7 fix cycle:** Close all three in the next fix iteration via the targeted edits above. Each is a self-contained documentation or policy edit with no cross-cutting implications beyond the described section and can be bundled in a single commit.

---
