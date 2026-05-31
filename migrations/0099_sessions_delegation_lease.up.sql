-- §8.2 lines 38-48 — the delegation lease carries a `lease_slice`
-- (maxTokenBudget, maxChildrenTotal, maxTreeSize, maxParallelChildren,
-- perChildMaxAge) that bounds the resources a child subtree may consume.
-- §8.2 requires the gateway to reject any lease_slice that exceeds the
-- parent's granted budget (`BUDGET_EXHAUSTED`). The granted slice is
-- recorded on the child session row so every descendant validates its
-- own requested slice against the ancestor ceiling without re-walking
-- parent_session_id. The value is stamped once at delegation admission
-- and is invariant for the session lifetime, so it is written on the
-- sessions row (like delegation_depth) rather than a separate table. A
-- root or standalone session has no granted slice and stores NULL.
-- F-8.2.2.
ALTER TABLE sessions
    ADD COLUMN IF NOT EXISTS delegation_lease JSONB;
