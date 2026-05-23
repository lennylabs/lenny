-- §4.2 line 158-159 session record fields the Session Manager
-- explicitly tracks: retry counters, policy enforcement state, and
-- resume eligibility window. See spec/04_system-components.md §4.2:
--
--   "Retry counters and policy enforcement"
--   "Resume eligibility and window"
--
-- retry_count tracks how many times the watchdog/coordinator retry
-- path has retried this logical session (resume on a fresh pod,
-- coordinator handoff retry, etc.). Monotonically non-decreasing
-- across every transition. Starts at zero and is bumped by the
-- coordinator on each retry.
--
-- policy_enforcement_state is the JSONB blob the Session Manager
-- uses to track per-session policy decisions and admission counters
-- (e.g., delegation policy enforcement bookkeeping, rate-limit
-- decision audit, last circuit-breaker decision). Schemaless on
-- purpose so the gateway can extend the payload without a migration
-- per policy field.
--
-- resume_eligible_until is the §4.2 "resume window" deadline: a
-- session that becomes resume-eligible at session start may be
-- resumed up to this UTC instant. NULL when the session has no
-- resume budget (already-terminal sessions, sessions whose budget
-- expired, sessions created without a resume window).

ALTER TABLE sessions
    ADD COLUMN retry_count              BIGINT NOT NULL DEFAULT 0
        CHECK (retry_count >= 0),
    ADD COLUMN policy_enforcement_state JSONB  NOT NULL DEFAULT '{}'::jsonb,
    ADD COLUMN resume_eligible_until    TIMESTAMPTZ NULL;
