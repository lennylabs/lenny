-- Reverse the §4.5 ll. 311 content-addressed snapshot hash column.

ALTER TABLE sessions DROP COLUMN IF EXISTS workspace_snapshot_hash;
