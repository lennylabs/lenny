-- §5.2 post-recovery slot-counter rehydration index.
--
-- After a Redis restart the slot counters (lenny:pod:{pod}:active_slots)
-- reset to zero while pods may still host active concurrent-mode slots.
-- The §5.2 "Post-recovery rehydration atomicity" path seeds the counter
-- from Postgres via SessionStore.GetActiveSlotsByPod(pod_id), which
-- counts the live (non-terminal) sessions bound to a pod. The first
-- slot allocation for each concurrent-workspace pod after a restart
-- triggers one such query, producing a burst across all rehydrating
-- pods as traffic resumes.
--
-- This partial index keeps each lookup under the 5ms target the spec
-- assumes at Tier 3: it is keyed on pod_assignment and restricted to the
-- non-terminal states, so it indexes only rows a slot count can include.
-- Sessions not bound to a pod (empty pod_assignment) and terminal
-- sessions are excluded. The four terminal states match
-- session.TerminalStates().
--
-- spec: spec/05_runtime-registry-and-pool-model.md §5.2 line 521
-- ("The GetActiveSlotsByPod query is indexed (sessions(pod_name) WHERE
-- state = 'active') and returns at most maxConcurrent rows").
CREATE INDEX idx_sessions_active_by_pod
    ON sessions (pod_assignment)
    WHERE pod_assignment <> ''
      AND state NOT IN ('completed', 'failed', 'cancelled', 'expired');
