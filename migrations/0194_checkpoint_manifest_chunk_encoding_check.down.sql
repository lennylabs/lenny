-- Reverses 0194_checkpoint_manifest_chunk_encoding_check: drops the
-- chunk_encoding CHECK constraint and returns checkpoint_manifest to the
-- unconstrained column migration 0178 created. No row is rewritten.
-- spec: §10.1 (Chunked-object storage model).
ALTER TABLE checkpoint_manifest
    DROP CONSTRAINT IF EXISTS checkpoint_manifest_chunk_encoding_check;
