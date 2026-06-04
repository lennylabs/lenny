-- §15.1 "ETag-based optimistic concurrency": every admin resource in
-- Postgres carries an integer `version` column that starts at 1 and is
-- incremented on every successful write. The quoted decimal version is
-- the resource's strong entity tag (`"3"`), enforced on PUT via the
-- If-Match precondition and exposed on GET/list responses. Migration
-- 0138 adopted the first batch (custom roles, delegation policies,
-- experiments); this migration extends the contract to the users and
-- environments resources. Existing rows default to version 1.
-- See spec/15_external-api-surface.md §15.1 lines 1207-1224.
ALTER TABLE users
    ADD COLUMN IF NOT EXISTS version INTEGER NOT NULL DEFAULT 1;

ALTER TABLE environments
    ADD COLUMN IF NOT EXISTS version INTEGER NOT NULL DEFAULT 1;
