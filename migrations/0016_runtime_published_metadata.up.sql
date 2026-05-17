-- The §5.1 publishedMetadata list on a runtime.
--
-- published_metadata carries the runtime's §5.1 publishedMetadata
-- entries: named, opaque metadata values (agent cards, OpenAPI specs,
-- cost manifests) each with a content type and a visibility class. SQL
-- NULL means the runtime publishes no metadata.

ALTER TABLE runtime_definitions
    ADD COLUMN published_metadata JSONB;
