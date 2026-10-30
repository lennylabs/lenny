-- Reverses 0193_checkpoint_manifest_chunk_encoding_default: restores the
-- 'tar' default migration 0178 declared. The up changes only the default and
-- rewrites no row, so the reversal has no data to restore.
-- spec: §10.1 (Chunked-object storage model).
ALTER TABLE checkpoint_manifest
    ALTER COLUMN chunk_encoding SET DEFAULT 'tar';
