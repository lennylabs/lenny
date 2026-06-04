-- §15.1 "ETag-based optimistic concurrency": every admin resource in
-- Postgres carries an integer `version` column that starts at 1 and is
-- incremented on every successful write. The quoted decimal version is
-- the resource's strong entity tag (`"3"`), enforced on PUT via the
-- If-Match precondition and exposed on GET/list responses. This
-- migration adds the column to the first batch of admin resources to
-- adopt the contract (custom roles, delegation policies, experiments);
-- the remaining admin tables adopt it in follow-on migrations as their
-- handlers are wired. Existing rows default to version 1.
-- See spec/15_external-api-surface.md §15.1 lines 1207-1224.
ALTER TABLE custom_roles
    ADD COLUMN IF NOT EXISTS version INTEGER NOT NULL DEFAULT 1;

ALTER TABLE delegation_policies
    ADD COLUMN IF NOT EXISTS version INTEGER NOT NULL DEFAULT 1;

ALTER TABLE experiment_definitions
    ADD COLUMN IF NOT EXISTS version INTEGER NOT NULL DEFAULT 1;
