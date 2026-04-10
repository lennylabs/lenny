# Technical Design Review Findings — 2026-04-07 (Iteration 7)

**Document reviewed:** `docs/technical-design.md`
**Review perspective:** Failure Modes & Resilience Engineering (FLR)
**Iteration:** 7
**Total findings:** 4 (all Medium)

## Findings Summary

| Severity | Count |
| -------- | ----- |
| Critical | 0     |
| High     | 0     |
| Medium   | 4     |

All 4 findings are new issues not previously identified. No regressions on previously Fixed findings. FLR-022 (Skipped) not re-reported.

---

## Detailed Findings

### FLR-023 GDPR Erasure Crash Window Between Transaction Commit and Verification Is Unaddressed [Medium]
**Section:** 12.8

The §12.8 GDPR erasure job flow describes an idempotent, resumable multi-phase process. Phase 4 ("Cryptographic erasure") is described as: (1) delete the user's `erasure_salt` and pseudonymize the last batch of records in a single database transaction, then (2) verify the salt is gone by re-querying `pg_stat_user_tables`. The idempotency/resumption paragraph states the controller "resumes from the last incomplete phase."

The gap: if the erasure job crashes in the window **after** the Phase 4 transaction commits (salt deleted) but **before** the verification query completes and the completion receipt is written, the job state is ambiguous:

- The salt is gone, so re-running pseudonymization is impossible (no key to re-hash with).
- `processing_restricted: true` remains set, blocking all new sessions for the user.
- No `gdpr.erasure_verification_success` receipt has been written, and no `gdpr.erasure_verification_failed` critical audit event has been emitted either.
- When the controller resumes "from the last incomplete phase," it encounters Phase 4 with a `NULL` erasure_salt and has no defined procedure: the pseudonymization cannot be re-run, and the verification query will trivially pass (salt is absent), but the spec does not state whether the job should treat a `NULL` salt at Phase 4 start as "Phase 4 already completed cleanly" or "Phase 4 failed and is unrecoverable."

The `POST /v1/admin/erasure-jobs/{job_id}/retry` endpoint and `DELETE /v1/admin/erasure-jobs/{job_id}/processing-restriction` (manual override) exist, but neither is documented as the resolution path for this specific crash window. An operator encountering this state has no runbook guidance.

The same crash window exists for the `POST /v1/admin/tenants/{id}/rotate-erasure-salt` flow (§15.1), which states "The old salt is **deleted immediately** upon rotation...a one-time re-hash migration job re-pseudonymizes historical billing records under the new salt before deletion." If the migration job crashes after the salt is deleted but before re-pseudonymization of all records is confirmed, the historical billing records are pseudonymized under a key that no longer exists, and the job has no way to recover without the old salt.

**Recommendation:** Add explicit crash-recovery semantics for Phase 4 of the erasure job: if the controller encounters Phase 4 with `erasure_salt IS NULL`, it should treat this as "Phase 4 transaction completed; proceed directly to verification." Add a corresponding runbook entry covering this state. For the salt rotation flow, ensure the migration job writes a durable "re-hash complete" marker before deleting the old salt (or retains the old salt in a soft-deleted state until re-pseudonymization is confirmed). Document the operator recovery path for both scenarios.

---

### FLR-024 `lenny_tenant_guard` Trigger Presence Check Does Not Detect Disabled-but-Present Trigger [Medium]
**Section:** 12.3, 17.6

The preflight check for cloud-managed pooler deployments (§17.6 preflight table, "Cloud-managed pooler sentinel defense") queries `pg_trigger` to verify the `lenny_tenant_guard` trigger **exists** on tenant-scoped tables. The gateway startup check (§12.3) performs the same existence query when `LENNY_POOLER_MODE=external`.

PostgreSQL supports `ALTER TABLE t DISABLE TRIGGER lenny_tenant_guard` — this marks the trigger as disabled in `pg_trigger.tgenabled = 'D'` while the row remains present. A disabled trigger never fires, so the RLS second-layer defense is completely inactive. Neither the preflight check nor the gateway startup check distinguishes between an enabled and a disabled trigger: both check for row presence only (`pg_trigger WHERE tgname = 'lenny_tenant_guard'`), not for `tgenabled != 'D'`.

This means:
- A superuser can disable the trigger post-deployment.
- The preflight check passes, the gateway starts, and all startup/periodic checks pass.
- No alert fires, no metric increments.
- The `lenny_tenant_guard` second-layer defense — which is the *sole* RLS enforcement mechanism for cloud-managed deployments that lack PgBouncer `connect_query` — is silently inactive.

By contrast, the `AuditGrantDrift` control (§16.5) uses a periodic background re-verification check (every `audit.grantCheckInterval`, default 5 min) and a critical alert for grant mutations detected after startup. No equivalent periodic check or alert exists for trigger enabled-state drift.

**Recommendation:** Update all `pg_trigger` queries that check for trigger presence to also verify `tgenabled != 'D'` (trigger is enabled). Add a periodic background check (analogous to the audit grant check) that re-verifies the trigger is present and enabled at runtime, not just at startup. Add a `TenantGuardTriggerDisabled` critical alert that fires if the trigger is found in a disabled state. Update the preflight failure message to distinguish "trigger absent" from "trigger disabled."

---

### FLR-025 CheckpointBarrier Global ACK Timeout Can Expire Before Per-Session Tiered Cap Upload Completes [Medium]
**Section:** 10.1, 4.4

The CheckpointBarrier protocol (§10.1) defines a global `checkpointBarrierAckTimeoutSeconds` (default 45 s). A pod sends `CheckpointBarrierAck` only after its checkpoint upload completes. If a pod does not ACK within the timeout, the gateway treats it as unresponsive and falls back to the last successful periodic checkpoint.

The tiered checkpoint caps (§4.4) allow up to 90 s for large-workspace sessions. The CRD validation rule enforces:

```
max_tiered_checkpoint_cap + checkpointBarrierAckTimeoutSeconds + 30 ≤ terminationGracePeriodSeconds
```

This constraint ensures the combined budget fits within `terminationGracePeriodSeconds` (preventing `preStop` hook timeout) but does **not** enforce:

```
checkpointBarrierAckTimeoutSeconds ≥ max_tiered_checkpoint_cap
```

With the defaults (`checkpointBarrierAckTimeoutSeconds = 45 s`, `max_tiered_checkpoint_cap = 90 s`), a session in the 90 s tier can legitimately still be uploading its checkpoint at second 46. At that point the global barrier ACK timeout expires, and the gateway falls back to the last periodic checkpoint — discarding the in-progress upload without error and without distinguishing a legitimately-slow-but-succeeding upload from an unresponsive pod.

The fallback is silent from the operator's perspective: no metric or alert distinguishes "barrier ACK timeout on a legitimately uploading pod" from "barrier ACK timeout on a truly unresponsive pod." The session proceeds with a lower-quality checkpoint (potentially up to `periodicCheckpointIntervalSeconds` — default 600 s — stale), silently defeating the purpose of the rolling-update barrier protocol for large-workspace sessions.

**Recommendation:** Add a CRD validation constraint that `checkpointBarrierAckTimeoutSeconds ≥ max_tiered_checkpoint_cap` (or equivalently, that `checkpointBarrierAckTimeoutSeconds ≥ tier_3_cap` where tier 3 is the largest tier). This prevents the default combination of 45 s global timeout and 90 s per-session cap from being deployed together. Additionally, add a metric (`lenny_checkpoint_barrier_ack_timeout_total`, labeled by pool and `was_uploading: true|false`) and a warning alert for barrier ACK timeouts that occur while the pod's checkpoint upload is still in progress — so operators can distinguish legitimate slow uploads from unresponsive pods.

---

### FLR-026 `awaiting_client_action` Session Expiry Leaves In-Transit DLQ Messages Without Sender Notification [Medium]
**Section:** 7.2, 7.3

When a session transitions to `awaiting_client_action` (retries exhausted) and subsequently **expires** after `maxResumeWindowSeconds` without client action, the spec defines the following:

- The session transitions to `expired` (terminal).
- Child task results archived in `session_dlq_archive` are replayed on parent resumption (§7.3 inter-session DLQ replay).
- Any DLQ messages with TTL still remaining are abandoned — the parent is now terminal and can never accept them.

Section §7.2 defines the `message_expired` delivery receipt (status `"expired"`) as the notification mechanism for senders whose messages exceed their DLQ TTL. However, this fires only on **natural TTL expiry** — i.e., when the DLQ entry's TTL countdown reaches zero. It does not fire when the *parent session* expires while the DLQ entry still has TTL remaining.

For a sender whose message is in the DLQ:
- The DLQ TTL is bounded by `maxResumeWindowSeconds` (default 900 s).
- If the parent session expires at second 750, the sender's message has 150 s of TTL remaining.
- The sender receives no `message_expired` delivery receipt at second 750 — that event will not fire until second 900 when the TTL naturally expires.
- For 150 s, the sender has no signal that their message is permanently undeliverable (the parent is already terminal, but the sender does not know this).

This matters in delegation trees: a sibling agent sending a message to `awaiting_client_action` session may continue executing and consuming budget for up to 150 s after the recipient's fate is sealed, with no way to self-cancel based on the delivery failure signal.

The spec is otherwise careful to provide timely delivery receipts — `rate_limited`, `dropped`, `scope_denied` are all synchronous. The `message_expired` path is the only case where a permanent delivery failure is signaled with a delay up to `maxResumeWindowSeconds` after the failure actually occurs.

**Recommendation:** When a session transitions to a terminal state (`expired`, `cancelled`, `failed`, `completed`), the gateway should immediately flush all pending DLQ entries for that session with a synthetic `message_expired` (or new `session_terminal`) delivery receipt to each sender, rather than waiting for natural TTL expiry. This closes the notification gap and allows senders — including delegation tree siblings — to react to permanent delivery failure promptly.
