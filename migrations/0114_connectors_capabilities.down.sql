ALTER TABLE connectors
    DROP COLUMN IF EXISTS capability_inference_mode,
    DROP COLUMN IF EXISTS capabilities,
    DROP COLUMN IF EXISTS tool_capabilities,
    DROP COLUMN IF EXISTS capabilities_refreshed_at;
