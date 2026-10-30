ALTER TABLE custom_roles
    DROP COLUMN IF EXISTS version;

ALTER TABLE delegation_policies
    DROP COLUMN IF EXISTS version;

ALTER TABLE experiment_definitions
    DROP COLUMN IF EXISTS version;
