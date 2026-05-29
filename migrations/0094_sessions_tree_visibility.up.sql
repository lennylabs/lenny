-- §8.5 line 540 / §8.3 lines 311-319 — `treeVisibility` is the
-- delegation-lease visibility boundary that scopes what
-- `lenny/get_task_tree` and `GET /v1/sessions/{id}/tree` return for a
-- session: `full` (the entire tree rooted at the apex, including
-- siblings and their descendants), `parent-and-self` (only the session's
-- own node and its direct parent), or `self-only` (only the session's
-- own node). In v1 the delegation lease is realised by the child session
-- row (§4.2 line 161 design clarification), so the boundary persists on
-- the sessions row rather than in a separate delegation_leases table.
--
-- The §8.2 delegation Service stamps an explicit resolved value onto
-- every child it admits (a child lease may narrow but never widen the
-- parent's effective visibility — the §8.3 monotonicity rule). Root and
-- standalone sessions carry the empty string, which the read path
-- normalises to the §8.5 default `full` (api/v1/session.TreeVisibility
-- .OrDefault), so an existing row backfilled to '' reads as `full`
-- without a data migration. The enum is kept as TEXT with a CHECK so a
-- future visibility level only updates the constraint. F-8.5.2 /
-- F-8.9.2 / F-13.5.8.
ALTER TABLE sessions
    ADD COLUMN IF NOT EXISTS tree_visibility TEXT NOT NULL DEFAULT ''
        CHECK (tree_visibility IN ('', 'full', 'parent-and-self', 'self-only'));
