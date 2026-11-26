-- §7.1 (Pipeline-pending session) — the pipeline mark.
--
-- terminal_pipeline_done_at records that the gateway replica which ran a
-- terminal session's terminal pipeline finished the pipeline's last step:
-- the session-log close, the final quota checkpoint, and the
-- session.completed billing event follow the pod release, so the release
-- marker (pod_released_at, migration 0191) does not record them. It is NULL
-- until the replica writes it, and it stays NULL for every row written
-- before this migration, for a row created in a terminal state, and for a
-- pipeline that the process exit cut or that ran past its budget.
--
-- A terminal session is pipeline-pending while terminal_pipeline_done_at is
-- NULL and terminated_at plus the terminal pipeline budget has not passed.
-- User erasure and tenant deletion wait while a session of their scope is
-- pipeline-pending (§12.8 Lifecycle fence and drain). They read the scope's
-- rows by tenant and user, so the column needs no index of its own.
ALTER TABLE sessions
    ADD COLUMN terminal_pipeline_done_at timestamptz;
