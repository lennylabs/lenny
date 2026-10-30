-- §5.1 runtime workspaceDefaults, runtimeOptionsSchema, and sharedAssets
-- blocks, previously dropped at the gateway boundary.
--
-- workspace_defaults is the §5.1 workspaceDefaults object:
-- {"files": [{"path": string, "content": string, "ref": string}],
--  "setupCommands": [{"cmd": string, "timeoutSeconds": int}]}. The §14
-- workspace-plan path materializes it before client uploads. The §5.1
-- merge table classifies it Append.
--
-- runtime_options_schema is the §5.1 runtimeOptionsSchema JSON Schema
-- fragment the §14 path validates session-creation runtimeOptions
-- against. The §5.1 merge table classifies it Override (restrict-only).
--
-- shared_assets is the §5.1 sharedAssets array:
-- [{"type": "artifact"|"inline", "ref": string, "path": string,
--   "content": string, "destPath": string}]. The §6.4 pod-init flow
-- materializes them into /workspace/shared/ for a concurrent-mode
-- runtime. The §5.1 merge table classifies it Append.
--
-- All three are NULL when the runtime declares no such block.
ALTER TABLE runtime_definitions
    ADD COLUMN workspace_defaults JSONB,
    ADD COLUMN runtime_options_schema JSONB,
    ADD COLUMN shared_assets JSONB;
