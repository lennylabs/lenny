-- §25.11 backup, restore, schedule, and retention durable schema. These
-- tables back the §25.4 / §25.11 lenny-ops backup and restore
-- subsystems: ops_backups is the per-backup record (status, archive
-- location, checksum, platform + schema version for restore
-- compatibility), ops_backup_schedule is the singleton cron schedule the
-- §25.4 cron evaluator re-reads before each fire, ops_retention_policy
-- is the singleton retention policy the retention sweep enforces, and
-- ops_restore_state records per-shard restore progress so a partial
-- multi-shard restore is resumable.
--
-- All four tables are platform-scoped (the §25 control plane is not
-- multi-tenanted at this boundary; §25.4 line 1492 lists them among the
-- PlatformPostgres() tables), so no tenant column or RLS policy applies.
--
-- spec: §25.11 lines 4165-4295.

CREATE TABLE ops_backups (
    id              TEXT PRIMARY KEY,
    type            TEXT NOT NULL,            -- 'full', 'postgres', 'config'
    status          TEXT NOT NULL,            -- 'running', 'completed', 'failed', 'verified', 'verification_failed'
    started_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    completed_at    TIMESTAMPTZ,
    size_bytes      BIGINT,
    duration_ms     BIGINT,
    storage_path    TEXT,
    checksum        TEXT,
    components      JSONB NOT NULL DEFAULT '[]',
    started_by      TEXT NOT NULL,
    operation_id    TEXT,
    job_id          TEXT NOT NULL,
    error           TEXT,
    platform_version TEXT NOT NULL,           -- version at time of backup (for restore compatibility)
    schema_version  INT NOT NULL,             -- Postgres schema version at time of backup
    expires_at      TIMESTAMPTZ              -- computed from retention policy
);

CREATE TABLE ops_backup_schedule (
    id          TEXT PRIMARY KEY DEFAULT 'singleton',
    full_cron   TEXT NOT NULL DEFAULT '0 2 * * *',
    pg_cron     TEXT NOT NULL DEFAULT '0 */6 * * *',
    enabled     BOOLEAN NOT NULL DEFAULT true,
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE ops_retention_policy (
    id               TEXT PRIMARY KEY DEFAULT 'singleton',
    retain_days      INT NOT NULL DEFAULT 30,
    retain_count     INT NOT NULL DEFAULT 10,
    retain_min_full  INT NOT NULL DEFAULT 3,
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE ops_restore_state (
    id                TEXT PRIMARY KEY,
    backup_id         TEXT NOT NULL,
    started_at        TIMESTAMPTZ NOT NULL,
    completed_at      TIMESTAMPTZ,
    status            TEXT NOT NULL,      -- 'running', 'completed', 'failed', 'paused'
    shard_states      JSONB NOT NULL,     -- {shard_id: {status, started_at, completed_at, error}}
    started_by        TEXT NOT NULL,
    operation_id      TEXT,
    pre_restore_backup_id TEXT NOT NULL,  -- safety-net backup created in step 3
    failed_shard      TEXT,               -- first shard that failed (if status=failed)
    error             TEXT
);
