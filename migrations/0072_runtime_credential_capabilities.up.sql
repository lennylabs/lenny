-- §5.1 runtime credentialCapabilities block, previously dropped at the
-- gateway boundary.
--
-- credential_capabilities is the §5.1 credentialCapabilities object:
-- {"hotRotation": bool, "proxyDialect": [string]}. It declares the
-- runtime's mid-session credential hot-rotation support and the §4.9
-- LLM-proxy dialects its SDK speaks (openai, anthropic). A pool bound to
-- the runtime that uses deliveryMode: proxy must declare a proxyDialect
-- in this set. The §5.1 merge table classifies it Override. NULL when
-- the runtime declares no credentialCapabilities block (direct-mode-only
-- runtimes set proxyDialect to []).
ALTER TABLE runtime_definitions
    ADD COLUMN credential_capabilities JSONB;
