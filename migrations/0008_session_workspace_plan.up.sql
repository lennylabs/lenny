-- The §14 WorkspacePlan stored on each session.
--
-- POST /v1/sessions and POST /v1/sessions/start record the inner
-- workspacePlan payload here. The finalize and start handlers read it
-- back to materialize the workspace onto the claimed pod, and
-- GET /v1/sessions/{id} returns the stored plan with its gateway-written
-- read-only fields (notably gitClone.resolvedCommitSha) populated, per
-- §15.1. NULL when the session was created without a workspace plan.

ALTER TABLE sessions
    ADD COLUMN workspace_plan JSONB;
