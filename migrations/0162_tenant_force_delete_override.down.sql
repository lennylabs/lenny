-- Reverses 0162_tenant_force_delete_override.

ALTER TABLE tenants
    DROP COLUMN IF EXISTS force_delete_hold_override,
    DROP COLUMN IF EXISTS force_delete_justification,
    DROP COLUMN IF EXISTS force_delete_by,
    DROP COLUMN IF EXISTS force_delete_at;
