-- The §5.1 runtime label set.
--
-- labels carries the runtime's §5.1 label map. §5.1 requires labels
-- from v1 as the primary mechanism for environment runtimeSelector
-- matching (§10.6). An empty object means the runtime carries no
-- labels.

ALTER TABLE runtime_definitions
    ADD COLUMN labels JSONB NOT NULL DEFAULT '{}'::jsonb;
