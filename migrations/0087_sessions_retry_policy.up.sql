-- §7.3 line 377-393 — CreateSession carries a client-supplied
-- retryPolicy bounded by deployer caps. The gateway persists the
-- effective (post-clamp) policy on the sessions row so a coordinator
-- handoff or replica restart picks up the same caps without re-running
-- the admission code path. The schema is JSONB so the failure-class
-- lists and any future fields can extend without a migration per field.
--
-- An absent policy stores SQL NULL; the gateway decodes NULL as nil so
-- the §15.1 GET envelope omits the field. Non-empty objects round-trip
-- verbatim through the api/v1/session.RetryPolicy decoder.
ALTER TABLE sessions
    ADD COLUMN IF NOT EXISTS retry_policy JSONB NULL;

-- §7.3 line 397 / §10.1 coordinator-handoff step 0 — the per-session
-- last_checkpoint_workspace_bytes is the authoritative size of the
-- most recent successful workspace checkpoint. The §7.2 line 138
-- workspaceRecoveryFraction depends on it for the partial-workspace
-- resume path, and the §10.1 preStop tiered-cap selection reads it to
-- pick the right drain budget. The column is NULL until the first
-- successful checkpoint records a non-zero size.
ALTER TABLE sessions
    ADD COLUMN IF NOT EXISTS last_checkpoint_workspace_bytes BIGINT NULL
        CHECK (last_checkpoint_workspace_bytes IS NULL
            OR last_checkpoint_workspace_bytes >= 0);
