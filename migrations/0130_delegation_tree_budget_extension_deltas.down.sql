-- Reverses 0130_delegation_tree_budget_extension_deltas.up.sql.
ALTER TABLE delegation_tree_budget
    DROP COLUMN IF EXISTS ext_tokens,
    DROP COLUMN IF EXISTS ext_seconds,
    DROP COLUMN IF EXISTS ext_children,
    DROP COLUMN IF EXISTS ext_parallel_children,
    DROP COLUMN IF EXISTS ext_tree_size,
    DROP COLUMN IF EXISTS ext_file_export_files,
    DROP COLUMN IF EXISTS ext_file_export_bytes,
    DROP COLUMN IF EXISTS updated_at;
