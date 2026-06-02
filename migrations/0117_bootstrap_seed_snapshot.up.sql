-- §25.10 configuration-drift desired-state store. bootstrap_seed_snapshot
-- holds the desired platform state the drift detector compares the
-- running state against. The 'live' row is the desired state in effect;
-- the 'target' row is the in-flight desired state an upgrade will
-- promote to live at Verification-phase completion (§25.10 lines
-- 3786-3789). The table is platform-scoped (the §25 control plane is
-- not multi-tenanted at this boundary; it joins the PlatformPostgres()
-- tables reachable without a tenant or session id), so it has no tenant
-- column and no RLS policy.
--
-- The id CHECK pins the row identity to the two snapshot kinds so a
-- typo'd writer cannot create a third desired-state row the detector
-- would never read (F-25.10.13). source records provenance for the
-- staleness warning; written_at drives the §25.10 line 3801 staleness
-- threshold; upgrade_id is non-null only on the target row during an
-- active upgrade.
--
-- spec: §25.10 lines 3811-3820.

CREATE TABLE bootstrap_seed_snapshot (
    id            TEXT        PRIMARY KEY DEFAULT 'live'
                              CHECK (id IN ('live', 'target')),
    desired_state JSONB       NOT NULL,
    source        TEXT        NOT NULL,         -- 'helm-values', 'caller-supplied', 'snapshot-refresh'
    upgrade_id    TEXT,                          -- non-null when id='target' and an upgrade is in flight
    written_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    written_by    TEXT        NOT NULL
);
