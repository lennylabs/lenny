-- §10.6 line 663 / §10.6 line 674 — `environmentId` populated on all
-- billing events for sessions created in an environment context; v1
-- accommodation that lets downstream rollups aggregate by environment
-- without re-joining the (eventually-erased) session row. Stored as a
-- TEXT column with empty-string default so a non-environment session
-- round-trips as the zero value, mirroring the §10.6 sessions row's
-- environment column convention (migration 0014). F-10.6.9.
ALTER TABLE billing_events
    ADD COLUMN IF NOT EXISTS environment_id TEXT NOT NULL DEFAULT '';
