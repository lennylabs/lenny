-- The §10.7 built-in eval-result registry.
--
-- eval_results is tenant-scoped: it carries the lenny_tenant_guard
-- trigger and a row-level security policy, exactly like the other
-- §12.3 tenant-scoped tables. The trigger function and the lenny_app
-- role are created by migration 0002.
--
-- Eval results are per-session: every row carries session_id with a
-- foreign key to sessions(id) ON DELETE CASCADE, so the §12.8 erasure
-- of a session removes its eval results in the same cascade. The
-- scalar §10.7 fields the evalstore.EvalResult struct exposes directly
-- (scorer, the experiment attribution, the aggregate score, the
-- delegation and conclusion flags, the audit timestamp) are typed
-- columns. The optional per-dimension scores map and the caller
-- metadata map are stored as jsonb documents.

CREATE TABLE eval_results (
    tenant_id                  TEXT        NOT NULL REFERENCES tenants(id),
    -- id is the §10.7 eval-result identifier (the `eval_` prefixed
    -- token assigned by evalstore.NewID), not a UUID.
    id                         TEXT        NOT NULL,
    -- session_id is the session the score pertains to. ON DELETE
    -- CASCADE so a §12.8 session erasure removes its eval results.
    session_id                 UUID        NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    -- experiment_id and variant_id are the §10.7 experiment
    -- attribution, auto-populated from the session's experiment
    -- context; empty when the session is not enrolled.
    experiment_id              TEXT        NOT NULL DEFAULT '',
    variant_id                 TEXT        NOT NULL DEFAULT '',
    -- scorer identifies the scoring method (e.g. 'llm-judge').
    scorer                     TEXT        NOT NULL DEFAULT '',
    -- score is the normalized aggregate score (0.0-1.0). NULL when the
    -- submission provided only the per-dimension scores map.
    score                      DOUBLE PRECISION,
    -- scores holds the optional §10.7 per-dimension scores map. NULL
    -- when the submission carried no per-dimension breakdown.
    scores                     JSONB,
    -- metadata holds the arbitrary caller key-value pairs. NULL when
    -- the submission carried none.
    metadata                   JSONB,
    -- delegation_depth is the session's depth in its delegation tree.
    delegation_depth           BIGINT      NOT NULL DEFAULT 0,
    -- inherited mirrors the session's experimentContext.inherited flag.
    inherited                  BOOLEAN     NOT NULL DEFAULT false,
    -- submitted_after_conclusion is true when the eval was submitted
    -- after the attributed experiment transitioned to concluded.
    submitted_after_conclusion BOOLEAN     NOT NULL DEFAULT false,
    -- created_at is the server-generated UTC ingestion timestamp.
    created_at                 TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, id)
);

-- idx_eval_results_session backs the per-session storage-bound count,
-- the oldest-first ListBySession readback, and the §12.8 erasure.
CREATE INDEX idx_eval_results_session
    ON eval_results (tenant_id, session_id, created_at);

-- idx_eval_results_experiment backs the §10.7 results aggregation,
-- which lists every eval result attributed to an experiment.
CREATE INDEX idx_eval_results_experiment
    ON eval_results (tenant_id, experiment_id, created_at)
    WHERE experiment_id <> '';

CREATE TRIGGER lenny_tenant_guard
    BEFORE INSERT OR UPDATE OR DELETE ON eval_results
    FOR EACH ROW EXECUTE FUNCTION lenny_tenant_guard();

ALTER TABLE eval_results ENABLE ROW LEVEL SECURITY;
ALTER TABLE eval_results FORCE ROW LEVEL SECURITY;
CREATE POLICY lenny_tenant_isolation ON eval_results
    USING (tenant_id = current_setting('app.current_tenant', true));

GRANT SELECT, INSERT, UPDATE, DELETE ON eval_results TO lenny_app;
