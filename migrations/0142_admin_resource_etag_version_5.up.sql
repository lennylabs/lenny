-- §15.1 "ETag-based optimistic concurrency": every admin resource in
-- Postgres carries an integer `version` column that starts at 1 and is
-- incremented on every successful write. The quoted decimal version is
-- the resource's strong entity tag (`"3"`), enforced on PUT via the
-- If-Match precondition and exposed on GET/list responses. Migrations
-- 0138/0139/0140/0141 adopted custom roles, delegation policies,
-- experiments, users, environments, connectors, credential_pools, and
-- tenants; this migration completes the rollout with the runtime
-- definitions resource. Existing rows default to version 1.
-- See spec/15_external-api-surface.md §15.1 lines 1207-1224.
ALTER TABLE runtime_definitions
    ADD COLUMN IF NOT EXISTS version INTEGER NOT NULL DEFAULT 1;
