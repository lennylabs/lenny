-- §24.13 line 150 / §10.5 line 417: the expand-contract phase-tracking
-- table that backs `GET /v1/admin/schema/migrations/status`. The
-- migration Job (lenny-migrate) UPSERTs one row per migration version it
-- advances through at end-of-run, recording the resolved expand-contract
-- `phase` (`phase1_applied` | `phase2_deployed` | `phase3_applied` |
-- `complete`), the Phase 3 enforcement-gate outcome (`gate_check_result`:
-- `pass` | `fail:<N>_rows` | `not_run`), the Kubernetes Job that applied
-- it (`migration_job_name`), and the apply timestamp.
--
-- A version with no row falls back to the synthesized `complete` /
-- `not_run` projection (v1 migrations are single-file, so an applied
-- migration with no Job-recorded phase is `complete`). The status surface
-- prefers a recorded row when present so the operator sees the real
-- applied-at timestamp, Job name, and Phase 3 gate result.
--
-- spec: §24.13 line 150 (`phase`, `gateCheckResult`, `migrationJobName`,
-- `appliedAt`); §10.5 line 417 (Phase 3 enforcement-gate count outcome).

CREATE TABLE schema_migration_phase (
    version            BIGINT      PRIMARY KEY,
    phase              TEXT        NOT NULL,
    applied_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    gate_check_result  TEXT        NOT NULL DEFAULT 'not_run',
    migration_job_name TEXT,
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT now()
);
