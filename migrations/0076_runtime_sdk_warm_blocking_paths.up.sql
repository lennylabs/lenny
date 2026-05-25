-- §5.1 runtime sdkWarmBlockingPaths list, previously dropped at the
-- gateway boundary.
--
-- sdk_warm_blocking_paths is the §5.1 top-level sdkWarmBlockingPaths
-- array of glob patterns: ["CLAUDE.md", ".claude/*", ...]. When the
-- runtime's capabilities.preConnect is true and an uploaded file matches
-- a pattern, the §6.1 warm-pool controller demotes the SDK-warm pod
-- before use. The companion capabilities.preConnect flag is stored
-- inside the existing capabilities JSONB column. NULL when the runtime
-- declares no list (a non-preConnect runtime, or one that ApplyDefaults
-- has not seeded).
ALTER TABLE runtime_definitions
    ADD COLUMN sdk_warm_blocking_paths JSONB;
