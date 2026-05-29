-- §10.7 lines 868, 905 — every EvalResult carries a `delegation_depth`
-- (uint32, 0 for root sessions) "auto-populated by the gateway from the
-- session's delegation lineage". The depth is invariant once a session
-- is admitted (a child's depth is parent.depth+1, fixed at delegation
-- time), so it is recorded on the sessions row rather than recomputed by
-- walking parent_session_id on every eval submission. The §8.2
-- delegation Service stamps the resolved depth onto each child it admits;
-- a root or standalone session keeps the default 0. The Results API
-- `?delegation_depth=` filter and `?breakdown_by=delegation_depth`
-- (§10.7 lines 949, 952) read this column through the eval_results copy.
-- F-10.7.5.
ALTER TABLE sessions
    ADD COLUMN IF NOT EXISTS delegation_depth INTEGER NOT NULL DEFAULT 0
        CHECK (delegation_depth >= 0);
