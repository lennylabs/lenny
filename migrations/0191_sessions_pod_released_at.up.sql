-- §7.1 (Post-terminal window) — the release marker and the index of the
-- terminal arm of the pod-holder predicate.
--
-- pod_released_at records that the gateway released a terminal session's
-- pod or slot. The replica that released the session's binding, or
-- reclaimed the claim of a session that never started, writes it once that
-- release has returned. It is NULL until then, and it stays NULL for every
-- row written before this migration and for a release whose record write
-- failed. A terminal session whose row names a pod holds that pod while
-- pod_released_at is NULL and terminated_at plus the terminal pipeline
-- budget has not passed.
--
-- The §5.2 rehydration seed, the §12.4 Redis-outage capacity gate, and the
-- slot-counter rebuild's pod list read the holders of a pod as two arms: the
-- non-terminal arm through the migration 0080 index and the terminal arm
-- through this one. The index leads with terminated_at because a NULL marker
-- never clears on its own, so only a range on terminated_at keeps the
-- terminal arm's scan to the rows inside the budget.
ALTER TABLE sessions
    ADD COLUMN pod_released_at timestamptz;

CREATE INDEX idx_sessions_unreleased_terminal_by_pod
    ON sessions (terminated_at, pod_assignment)
    WHERE pod_assignment <> ''
      AND pod_released_at IS NULL
      AND state IN ('completed', 'failed', 'cancelled', 'expired');
