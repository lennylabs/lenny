-- §9.2 per-pool elicitation policy. The admin pool API accepts the
-- per-pool `elicitationDepthPolicy` (lines 90-98) and `urlModeElicitation`
-- (line 86) blocks on a pool definition; the gateway resolves them at
-- lenny/request_elicitation dispatch time so depth suppression and the
-- agent-initiated url-mode domain allowlist apply per the raising
-- session's pool. The column is JSONB so the schema can absorb spec
-- additions (new depth policies, additional url-mode controls) without a
-- forward-only column migration on every change.
--
-- A NULL row means the pool carries no explicit elicitation policy: the
-- dispatcher falls back to the §9.2 platform defaults (suppress_at_depth
-- at depth 3, agent-initiated url-mode blocked). See spec/09_mcp-
-- integration.md §9.2 lines 86, 90-98.
ALTER TABLE sandbox_warm_pools
    ADD COLUMN IF NOT EXISTS elicitation_policy JSONB;
