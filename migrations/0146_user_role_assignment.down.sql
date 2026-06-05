ALTER TABLE users
    DROP COLUMN IF EXISTS role_assigned,
    DROP COLUMN IF EXISTS role_assigned_by,
    DROP COLUMN IF EXISTS role_assigned_at;
