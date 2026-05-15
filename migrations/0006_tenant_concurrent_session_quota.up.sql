-- The §11.2 per-tenant concurrent-session quota.
--
-- max_concurrent_sessions caps the number of non-terminal sessions a
-- tenant may hold at once. The gateway rejects a session create with
-- QUOTA_EXCEEDED once the tenant reaches this count. Zero (the
-- default) means the tenant has no concurrent-session limit.

ALTER TABLE tenants
    ADD COLUMN max_concurrent_sessions INT NOT NULL DEFAULT 0;
