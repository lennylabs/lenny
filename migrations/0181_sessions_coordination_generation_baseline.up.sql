-- §4.2 / §10.1 — baseline the session record's coordination generation at 1.
--
-- A session row created at 0 carries a value no coordinator can be strictly
-- above without first minting one: the resume path fences on the value it
-- reads without incrementing it, so a session that resumed but was never
-- handed off is fenced at the gateway's floor of 1 while its row still reads
-- 0, and the first crash takeover's compare-and-swap mints 1 as well. The pod
-- then refuses that takeover fence as coordinator_handoff_stale. Baselining
-- the row at 1 makes the first handoff mint 2, strictly above the value any
-- replica already carries for the session.
--
-- coordination_lease.coordination_generation mirrors the session row's value
-- for the barrier-target query (§10.1.8), so its default states the same
-- baseline as the row it mirrors.
--
-- The inline CHECK (coordination_generation >= 0) that migration 0050 created
-- on the session row is deliberately left as it stands. The migrate Job is a
-- pre-install/pre-upgrade hook that completes before the gateway Deployment
-- rolls, so this schema is ahead of the binaries for the whole rolling window
-- and the still-running old fleet inserts an explicit zero. §10.5 places a
-- constraint that old-version writes violate in a Phase 3 migration in a
-- subsequent deployment. The two session-store Create floors that land with
-- this migration are the enforcement of the baseline; the column default
-- baselines nothing, because the insert names the column.
--
-- spec: §4.2, §10.1, §10.5.

ALTER TABLE sessions
    ALTER COLUMN coordination_generation SET DEFAULT 1;

-- The backfill writes across every tenant through the §12.3 tenant guard
-- trigger, which rejects any write made with no app.current_tenant set, and
-- the sessions isolation policy admits the platform cross-tenant sentinel.
-- Both settings are SET LOCAL, so they are confined to this migration's own
-- transaction and no session inherits them. Migration 0180's whole-table
-- sessions rewrite takes the same pair.
SET LOCAL lenny.allow_all_sentinel = 'true';
SET LOCAL app.current_tenant = '__all__';

UPDATE sessions SET coordination_generation = 1 WHERE coordination_generation = 0;

SET LOCAL app.current_tenant TO DEFAULT;
SET LOCAL lenny.allow_all_sentinel TO DEFAULT;

ALTER TABLE coordination_lease
    ALTER COLUMN coordination_generation SET DEFAULT 1;
