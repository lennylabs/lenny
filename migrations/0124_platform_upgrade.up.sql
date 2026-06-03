-- §25.8 platform-upgrade durable state. platform_upgrade_state is the
-- singleton orchestration row the §25.8 phase machine drives (OpsRoll ->
-- CRDUpdate -> SchemaMigration -> GatewayRoll -> ControllerRoll ->
-- Verification -> Complete, plus Paused / RolledBack), carrying the
-- resolved per-component target images and the previous_phases trail so
-- a restart resumes mid-upgrade. platform_upgrade_check_cache is the
-- singleton release-channel cache the hourly platform_upgrade_check cron
-- writes, so an air-gapped install can pre-populate it and the check
-- serves cached data when the channel is unreachable.
--
-- Both tables are platform-scoped (the §25 control plane is not
-- multi-tenanted at this boundary; §25.4 line 1492 lists them among the
-- PlatformPostgres() tables), so no tenant column or RLS policy applies.
--
-- spec: §25.8 lines 3579-3605.

CREATE TABLE platform_upgrade_state (
    id                    TEXT PRIMARY KEY DEFAULT 'singleton',
    target_version        TEXT NOT NULL,
    target_images         JSONB NOT NULL,         -- resolved image references per component
    current_phase         TEXT NOT NULL,           -- OpsRoll, CRDUpdate, SchemaMigration,
                                                   -- GatewayRoll, ControllerRoll, Verification,
                                                   -- Complete, Paused, RolledBack
    previous_phases       TEXT[] NOT NULL DEFAULT '{}',
    started_by            TEXT NOT NULL,
    started_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    paused_at             TIMESTAMPTZ,
    completed_at          TIMESTAMPTZ,
    pre_upgrade_backup_id TEXT,
    error                 TEXT,
    metadata              JSONB NOT NULL DEFAULT '{}'
    -- metadata includes: previousImages (map of component -> old image ref),
    --                     phaseTimings, preflightResults
);

CREATE TABLE platform_upgrade_check_cache (
    id              TEXT PRIMARY KEY DEFAULT 'singleton',
    checked_at      TIMESTAMPTZ NOT NULL,
    current_version TEXT NOT NULL,
    latest_version  TEXT,
    response        JSONB NOT NULL,
    ttl_seconds     INT NOT NULL DEFAULT 21600
);
