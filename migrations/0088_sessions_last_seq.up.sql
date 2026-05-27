-- §7.3 line 397 — sessions.last_seq is the authoritative per-session
-- monotonic SessionEvent.SeqNum counter (§15). The gateway advances
-- last_seq atomically with each persisted event — including frames
-- synthesised on coordinator-handoff reattach (§10.4) — so the counter
-- survives handoff, replica restart, and resume_pending → resuming →
-- running recovery without rewinds or duplicates. Any coordinator-local
-- copy is an advisory cache primed from Postgres at handoff step 0
-- alongside last_checkpoint_workspace_bytes (§10.1 coordinator handoff
-- protocol); the persisted last_seq value is the source of truth on any
-- disagreement.
--
-- CHECK (>= 0) enforces the monotonic-non-decreasing floor at the row
-- level; UPDATE callers use GREATEST(last_seq, $1) so a late writer
-- never rewinds an in-flight publish from a sibling replica. F-7.3.3.
ALTER TABLE sessions
    ADD COLUMN IF NOT EXISTS last_seq BIGINT NOT NULL DEFAULT 0
        CHECK (last_seq >= 0);
