-- §8.2 line 52 / §8.3 line 286 — the gateway automatically attaches the
-- parent's registered `tracingContext` (map[string]string of opaque
-- tracing identifiers a runtime registered via lenny/set_tracing_context)
-- to every delegated child. A pod restart that triggers a session reload
-- from Postgres must return the same context so the child can stitch its
-- traces into the parent's trace tree. The column is JSONB so the
-- string→string flat map round-trips verbatim; NULL stores the "no
-- context registered" case the read path returns as a nil map. F-8.2.14.
ALTER TABLE sessions
    ADD COLUMN IF NOT EXISTS tracing_context JSONB NULL;

-- §8.3 line 266 / §8.10 — `cascadeOnFailure` is the lease policy that
-- governs the fate of a session's children when the session reaches a
-- terminal state. The §8.10 default (`cancel_all`) applies when the
-- session names no value. Persisting it on the sessions row is what lets
-- the §8.10 orphan-cleanup loop tune cascade per-tree on a
-- Postgres-backed deployment instead of always reading the zero-value
-- after a load. The enum is `cancel_all` / `await_completion` / `detach`
-- (api/v1/session.CascadePolicy), kept as TEXT with a CHECK so a future
-- enum addition only updates the constraint. The empty-string default
-- preserves the in-Go convention "empty resolves to default" without a
-- separate nullable column. F-8.2.15.
ALTER TABLE sessions
    ADD COLUMN IF NOT EXISTS cascade_on_failure TEXT NOT NULL DEFAULT ''
        CHECK (cascade_on_failure IN ('', 'cancel_all', 'await_completion', 'detach'));
