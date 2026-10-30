-- §15.5 item 7 lists "checkpoint metadata ([Section 7.1])" among the
-- Postgres-persisted record types that MUST carry a `schemaVersion` integer
-- field (starting at 1). session_checkpoints is the per-commit checkpoint
-- metadata catalog the gateway writes on every successful checkpoint commit
-- (one row per (tenant, session, ref)); it is the Postgres-persisted
-- checkpoint-metadata record, so it carries the schema revision as a
-- query-filterable column.
--
-- v1 writers start at 1; the CHECK enforces the §15.5 "starting at 1"
-- floor. The field is set at write time by the gateway and is immutable
-- once written (rotation only ever flips `retained`/`deleted_at`).

ALTER TABLE session_checkpoints
    ADD COLUMN schema_version INT NOT NULL DEFAULT 1
        CHECK (schema_version >= 1);
