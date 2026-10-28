-- §10.1 (Chunked-object storage model) and §4.4 (MinIO object key logging
-- for manual recovery): checkpoint_manifest.chunk_encoding is the closed
-- enum 'tar' or 'tar.gz'. The chunk object key suffix and the resume path's
-- decoder are both derived from this column, so a row holding any other
-- value names neither an object nor a decoder.
--
-- Migration 0062 declared this CHECK on session_partial_checkpoint_manifest.
-- Migration 0178 replaced that table with checkpoint_manifest and did not
-- carry the CHECK over, so the head schema accepted any value. This
-- migration restores the constraint at the storage boundary. The Go
-- manifest stores already refuse an out-of-enum value on Put, so no row
-- written through them violates it, and the ADD CONSTRAINT validates every
-- existing row before it commits.
--
-- The constraint is added in one validating statement rather than as
-- NOT VALID followed by VALIDATE CONSTRAINT: the migration runner applies
-- the file in a single transaction, so the split would hold the same lock
-- until commit and gain nothing.
--
-- spec: §10.1 (Chunked-object storage model), §4.4 (MinIO object key logging
-- for manual recovery).
ALTER TABLE checkpoint_manifest
    DROP CONSTRAINT IF EXISTS checkpoint_manifest_chunk_encoding_check;
ALTER TABLE checkpoint_manifest
    ADD CONSTRAINT checkpoint_manifest_chunk_encoding_check
    CHECK (chunk_encoding IN ('tar', 'tar.gz'));
