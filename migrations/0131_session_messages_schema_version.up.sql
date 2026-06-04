-- §15.5 item 7 requires every Postgres-persisted record type to carry a
-- `schemaVersion` integer field (starting at 1) that identifies the schema
-- revision used to write the record. §15.4.1 line 1694 names this table
-- explicitly:
--
--   "Every `MessageEnvelope` persisted to the `session_messages` table
--    carries this field. ... the gateway writes it at inbox-enqueue time
--    and it is immutable once written."
--
-- The transcript rows the gateway persists for §7.2 are the persisted
-- MessageEnvelope records, so they MUST carry the schema version as a
-- query-filterable column rather than only inside the JSON payload. v1
-- writers start at 1; the CHECK enforces the §15.5 "starting at 1" floor.
-- The session_messages immutability/tenant-guard trigger (migration 0002)
-- already rejects every UPDATE, so the column is immutable once written
-- with no additional trigger change.

ALTER TABLE session_messages
    ADD COLUMN schema_version INT NOT NULL DEFAULT 1
        CHECK (schema_version >= 1);
