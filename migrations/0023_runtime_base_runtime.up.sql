-- The §5.1 baseRuntime reference on a runtime.
--
-- base_runtime names the base runtime a derived runtime resolves
-- against via the §5.1 merge algorithm. An empty string marks a
-- standalone runtime.

ALTER TABLE runtime_definitions
    ADD COLUMN base_runtime TEXT NOT NULL DEFAULT '';
