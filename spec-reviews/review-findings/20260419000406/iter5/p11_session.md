## 11. Session Lifecycle & State Management

### SES-019 (carry-forward from iter4) — `POST /v1/sessions/{id}/start` precondition/result table still omits `resume_pending`
**Severity:** Low
**Location:** `spec/15_external-api-surface.md` §15.1 line ~618
**Finding:** The endpoint row for `POST /v1/sessions/{id}/start` still lists only `starting → running` as the resulting transition and does not enumerate the `starting → resume_pending` outcome that occurs when a checkpoint is available and workspace rehydration is required. §7.2 and §6.2 both document `starting → resume_pending` as a valid edge, but the external-API surface table remains inconsistent with the internal state machine.
**Impact:** Clients consulting only the external API reference will not know that `POST /start` can legitimately leave the session in `resume_pending` awaiting workspace rehydration, leading to confused client state machines and incorrect assumptions that `202 Accepted` from `/start` always transitions toward `running`.
**Recommendation:** Update the §15.1 row for `POST /v1/sessions/{id}/start` to list the resulting transitions as `starting → running | starting → resume_pending`, with a note that the choice depends on whether a checkpoint is present and workspace rehydration is required.
**Status vs iter4:** Carry-forward — issue identified in iter4 as SES-019; iter5 spec unchanged at this location.

---

### SES-020 (carry-forward from iter4) — SSE reconnect/replay has no rate limit or per-reconnect replay cap
**Severity:** Low
**Location:** `spec/07_session-lifecycle.md` §7.2 SSE reconnection policy (lines ~349–363); `spec/10_gateway-internals.md` §10.4 event replay buffer
**Finding:** The replay window is specified as `max(periodicCheckpointIntervalSeconds × 2, 1200s)` with a bounded-error `OutboundChannel` policy, but there is no per-session or per-principal reconnect rate limit (a client can reconnect with `Last-Event-ID` arbitrarily often) and no cap on the per-reconnect replay volume (a long-idle client reconnecting at the 1200s horizon will replay the full window on every attempt). A malicious or buggy client can thereby impose amplification load on the gateway replay path.
**Impact:** Resource-exhaustion vector (CPU + bandwidth amplification) against the gateway's event replay subsystem; no denial-of-service guardrail at the session-event API surface. Not a correctness bug, but a missing reliability control.
**Recommendation:** Add a per-session reconnect rate limit (e.g., token bucket with configurable burst/refill) in `spec/11_policy-and-controls.md` alongside the existing `sessionEventReplayBufferDepth` policy, and specify a per-reconnect replay byte cap beyond which the gateway emits `gap_detected` and requires the client to re-snapshot.
**Status vs iter4:** Carry-forward — issue identified in iter4 as SES-020; iter5 spec text at §7.2 lines 349–363 is unchanged.

---

### SES-021 (carry-forward from iter4) — `awaiting_client_action → expired` trigger is ambiguous across three timers
**Severity:** Low
**Location:** `spec/07_session-lifecycle.md` §7.2 line ~202
**Finding:** The edge `awaiting_client_action → expired (lease/budget/deadline exhausted while awaiting client action)` collapses three distinct timers into a single free-text trigger: (a) `maxAwaitingClientActionSeconds` (900s default, a state-specific timer from §11), (b) session budget exhaustion, and (c) absolute session TTL / deadline. The spec does not disambiguate which timer fires the transition or which `terminal_reason` is recorded for each case.
**Impact:** Operators cannot distinguish "client failed to respond in time" from "budget exceeded while waiting" from "absolute deadline hit" in audit/observability. Incident triage and billing reconciliation both suffer, and clients cannot programmatically distinguish the three cases from the terminal event payload.
**Recommendation:** Split the §7.2 transition row into three rows (or enumerate three sub-triggers) with distinct `terminal_reason` codes — e.g., `awaiting_client_action_timeout`, `budget_exhausted`, `session_deadline_exceeded` — and reference the relevant policy knob from §11 for each.
**Status vs iter4:** Carry-forward — issue identified in iter4 as SES-021; iter5 spec at §7.2 line 202 is unchanged.

---

### Convergence assessment (Perspective 11)

**Counts:**
- Critical: 0
- High: 0
- Medium: 0
- Low: 3
- Info: 0

**Per-finding status vs. iter4:**
- SES-015 (`resuming → cancelled/completed` missing from §6.2): **Fixed in iter5** — §6.2 lines 129–135 now enumerate both terminal edges with snapshot-close semantics.
- SES-016 (pre-attach terminal collapse `resume_pending → cancelled/completed` unspecified): **Fixed in iter5** — §7.2 lines 193–194 and §6.2 lines 137–139 now enumerate the pre-attach collapse edges.
- SES-017 (generation-counter bookkeeping for mid-resume terminals unspecified): **Fixed in iter5** — §7.2 "Mid-resume terminal transitions — snapshot-close semantics" step 4 covers generation bookkeeping; §4.2 line 156 formalizes `recovery_generation` / `coordination_generation` invariants.
- SES-018 (`created → failed` derive-failure edge + atomicity + reachability): **Fixed in iter5** — §7.1 line 28 conditions atomicity on `persistDeriveFailureRows: false`; §7.2 line 164 enumerates the edge; §15.1 lines 647–663 provide the audit-row reachability table.
- SES-019 (`POST /start` precondition/result table missing `resume_pending`): **Carry-forward** — still open in iter5.
- SES-020 (SSE reconnect rate limit + per-reconnect replay cap missing): **Carry-forward** — still open in iter5.
- SES-021 (`awaiting_client_action → expired` trigger disambiguation): **Carry-forward** — still open in iter5.

**New findings in iter5:** None. No new correctness or reliability bugs were identified in §6.2, §7, §10.1–10.4, §11, or §15.1 session-lifecycle scope beyond the three iter4 carry-forwards.

**Verdict:** **Not converged.**

Rationale: Four of seven iter4 findings are closed (including all Medium/High items), and no new findings were introduced. However, three Low items (SES-019, SES-020, SES-021) remain unaddressed in iter5. Each is a documentation/policy completeness gap with clear, bounded fixes; none blocks deployment individually, but convergence requires zero open findings against the prior iteration. Recommend closing all three in iter6 via the targeted edits above.

---
