-- Reverses 0013_runtime_labels.
ALTER TABLE runtime_definitions
    DROP COLUMN IF EXISTS labels;
