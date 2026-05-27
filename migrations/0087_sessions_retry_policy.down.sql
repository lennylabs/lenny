ALTER TABLE sessions
    DROP COLUMN IF EXISTS last_checkpoint_workspace_bytes,
    DROP COLUMN IF EXISTS retry_policy;
