-- §9.3 line 136 / §5.1 — connector tool-capability metadata derived from
-- MCP ToolAnnotations. The synchronous registration path makes no
-- outbound call (§15.1 line 1144), so capability inference runs as a
-- separate post-create refresh on the sanctioned outbound path; these
-- columns hold its result.
--
-- capability_inference_mode is the §5.1 mode (`strict` default infers an
-- unannotated tool as admin, `permissive` infers it as write).
-- capabilities is the §5.1 union of inferred capabilities across the
-- connector's tools/list. tool_capabilities maps each tool name to its
-- inferred capability set, feeding the §5.3 call-time
-- TOOL_CAPABILITY_DENIED check. capabilities_refreshed_at records the
-- last successful refresh.
--
-- Purely additive: every column carries a default (or is nullable), so
-- existing INSERT statements that do not name them continue to work.
-- F-9.3.8.
ALTER TABLE connectors
    ADD COLUMN capability_inference_mode TEXT        NOT NULL DEFAULT 'strict',
    ADD COLUMN capabilities              JSONB       NOT NULL DEFAULT '[]',
    ADD COLUMN tool_capabilities         JSONB       NOT NULL DEFAULT '{}',
    ADD COLUMN capabilities_refreshed_at TIMESTAMPTZ;
