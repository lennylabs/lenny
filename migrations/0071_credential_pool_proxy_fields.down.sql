-- Reverses 0071_credential_pool_proxy_fields.
ALTER TABLE credential_pools
    DROP COLUMN IF EXISTS delivery_mode,
    DROP COLUMN IF EXISTS proxy_dialect,
    DROP COLUMN IF EXISTS proxy_endpoint;
