-- The §11.2 per-tenant storage quota.
--
-- storage_quota_bytes caps the total artifact storage a tenant may
-- consume. The gateway rejects an upload or checkpoint write with
-- STORAGE_QUOTA_EXCEEDED once the tenant's reserved-plus-committed
-- byte count would exceed this value. Zero (the default) means the
-- tenant has no storage limit.

ALTER TABLE tenants
    ADD COLUMN storage_quota_bytes BIGINT NOT NULL DEFAULT 0;
