ALTER TABLE tenants
    DROP COLUMN IF EXISTS suspended,
    DROP COLUMN IF EXISTS suspended_reason,
    DROP COLUMN IF EXISTS suspended_at,
    DROP COLUMN IF EXISTS suspended_by;
