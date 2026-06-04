-- §25.11 Test Restore result record. ops_restore_test_results stores the
-- outcome of each automated test-restore run the lenny-restore-test
-- CronJob performs (download the latest backup, validate its checksum,
-- run pg_restore --list, restore into a scratch Postgres, and sample the
-- ArtifactStore replication target). The lenny-backup binary inserts one
-- row per run; the leader lenny-ops replica reads the latest row on each
-- scrape and publishes the §25.11 / §16.1 restore-test gauges
-- (lenny_restore_test_success, lenny_restore_test_duration_seconds,
-- lenny_restore_test_artifact_success_rate) plus the cumulative
-- lenny_restore_test_artifact_missing_total counter. The short-lived Job
-- pod cannot expose Prometheus series itself, so it records the outcome
-- here and lenny-ops re-exposes it, mirroring the
-- lenny_backup_last_successful_timestamp sampling path.
--
-- The table is platform-scoped (the §25 control plane is not
-- multi-tenanted at this boundary; §25.4 line 1492 lists the §25.11
-- backup/restore tables among the PlatformPostgres() tables), so no
-- tenant column or RLS policy applies.
--
-- spec: §25.11 lines 4098, 4128-4133, 4254-4256; §16.1 restore-test gates.

CREATE TABLE ops_restore_test_results (
    id                     TEXT PRIMARY KEY,
    backup_id              TEXT NOT NULL DEFAULT '',   -- backup the run restored; empty when no backup matched the selector
    backup_type            TEXT NOT NULL DEFAULT '',
    started_at             TIMESTAMPTZ NOT NULL,
    completed_at           TIMESTAMPTZ NOT NULL,
    success                BOOLEAN NOT NULL,           -- the §16.1 restore-test gate
    duration_ms            BIGINT NOT NULL DEFAULT 0,
    artifact_checked       BOOLEAN NOT NULL DEFAULT false,  -- whether the sampled-HEAD ArtifactStore check ran
    artifact_sampled       INT NOT NULL DEFAULT 0,     -- number of object keys sampled
    artifact_present       INT NOT NULL DEFAULT 0,     -- sampled keys present at the replication target
    artifact_missing       INT NOT NULL DEFAULT 0,     -- sampled keys absent (artifact_sampled - artifact_present)
    artifact_success_rate  DOUBLE PRECISION NOT NULL DEFAULT 0,
    error                  TEXT NOT NULL DEFAULT ''
);

-- The §25.11 metric sampler reads the most recent run by completion time.
CREATE INDEX ops_restore_test_results_completed_at_idx
    ON ops_restore_test_results (completed_at DESC);
