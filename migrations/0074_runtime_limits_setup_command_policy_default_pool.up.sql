-- §5.1 runtime limits, setupCommandPolicy, and defaultPoolConfig blocks,
-- previously dropped at the gateway boundary.
--
-- limits is the §5.1 limits object:
-- {"maxSessionAgeSeconds": int, "maxUploadSizeBytes": int,
--  "maxRequestInputWaitSeconds": int}. The §11.3 inter-agent
-- lenny/request_input timeout reads maxRequestInputWaitSeconds.
--
-- setup_command_policy is the §5.1 setupCommandPolicy object:
-- {"mode": "allowlist"|"shell", "shell": bool, "allowlist": [string],
--  "maxCommands": int}. The gateway enforces it at pod startup (§6.4).
--
-- default_pool_config is the §5.1 defaultPoolConfig object:
-- {"warmCount": int, "resourceClass": string, "egressProfile": string}.
-- The §5.2 pool resolver consults it before falling back to platform
-- defaults.
--
-- All three are NULL when the runtime declares no such block. The §5.1
-- merge table classifies each Override.
ALTER TABLE runtime_definitions
    ADD COLUMN limits JSONB,
    ADD COLUMN setup_command_policy JSONB,
    ADD COLUMN default_pool_config JSONB;
