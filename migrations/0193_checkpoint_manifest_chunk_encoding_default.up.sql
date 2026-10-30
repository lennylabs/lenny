-- §10.1 (Chunked-object storage model) and §4.4 (MinIO object key logging
-- for manual recovery): the checkpoint_manifest.chunk_encoding default
-- names the encoding the producer writes.
--
-- Migrations 0062 and 0178 defaulted chunk_encoding to 'tar'. The only chunk
-- producer, the adapter's workspace archiver, writes gzip-compressed tar
-- chunks, and the gateway declares 'tar.gz' on every intent row. The gateway
-- always sets the column, so no row takes the default today. The default
-- moves to 'tar.gz' so that an INSERT omitting the column records the
-- encoding of the bytes rather than an encoding no producer writes. Existing
-- rows are left unchanged: the column is a statement about each row's own
-- chunks, and the CHECK constraint still admits both values.
--
-- spec: §10.1 (Chunked-object storage model), §4.4 (MinIO object key logging
-- for manual recovery).
ALTER TABLE checkpoint_manifest
    ALTER COLUMN chunk_encoding SET DEFAULT 'tar.gz';
