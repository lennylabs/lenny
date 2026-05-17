-- The §5.1 toolCapabilityOverrides map on a runtime.
--
-- tool_capability_overrides carries the runtime's §5.1
-- toolCapabilityOverrides: an explicit §5.3 capability set per tool
-- name that overrides MCP-annotation capability inference. SQL NULL
-- means the runtime sets no overrides and every tool is inferred.

ALTER TABLE runtime_definitions
    ADD COLUMN tool_capability_overrides JSONB;
