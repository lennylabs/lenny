-- §8.2 / §9.2 / §16.7 / §17.2 deployment-config reconciliation state. The
-- single-row durable record of the last-applied Helm deployment-scope
-- configuration the gateway has already audited: the cycle-detection
-- mode (§8.2), the self-recursion master gate, the delegation default
-- maxDepth, and the elicitation-content-integrity platform floor (§9.2 /
-- §17.2). The POST /v1/admin/deployment/config-change reconciliation
-- endpoint diffs an incoming Helm render against this row to decide which
-- deployment-transition audit events to emit (gateway.cycle_detection_mode_changed,
-- gateway.allow_self_recursion_changed, gateway.default_max_depth_changed,
-- platform.elicitation_content_integrity_floor_changed, and the per-tenant
-- tenant.elicitation_content_integrity_floor_clamp fanout), then persists
-- the new values so the next upgrade's diff has a baseline that survives a
-- gateway restart. The persisted last_revision is the Helm .Release.Revision
-- the row reflects; the endpoint treats a revision at or below it as an
-- idempotent replay (a retried post-upgrade hook) and emits nothing.
--
-- This is platform-operational deployment state, not tenant-scoped: there
-- is exactly one row (scope = 'platform'), no lenny_tenant_guard trigger,
-- and no RLS policy. The per-tenant floor-clamp audit rows the endpoint
-- fans out land in the RLS-protected per-tenant audit chain through the
-- audit log, not here. spec: §16.7 lines 672, 676, 677, 682; §17.2 line 86.
CREATE TABLE deployment_config_state (
    -- scope pins the table to a single row. 'platform' is the only value;
    -- the CHECK enforces the singleton invariant.
    scope                TEXT        PRIMARY KEY DEFAULT 'platform'
        CHECK (scope = 'platform'),
    -- cycle_detection_mode is the §8.2 gateway.cycleDetection.mode value
    -- (enforce | warn | permissive). Empty until first recorded.
    cycle_detection_mode TEXT        NOT NULL DEFAULT '',
    -- allow_self_recursion is the §8.2 gateway.allowSelfRecursion master
    -- gate (yes | no). Empty until first recorded.
    allow_self_recursion TEXT        NOT NULL DEFAULT '',
    -- default_max_depth is the §8.2 step-5 gateway.delegation.defaultMaxDepth
    -- Helm fallback. 0 means unset/unrecorded.
    default_max_depth    INTEGER     NOT NULL DEFAULT 0,
    -- elicitation_floor is the §9.2 / §17.2 platform minimum-enforcement
    -- floor (off | detect-only | enforce). Empty until first recorded.
    elicitation_floor    TEXT        NOT NULL DEFAULT '',
    -- last_revision is the Helm .Release.Revision this row reflects; the
    -- reconciliation endpoint rejects a revision at or below it as a replay.
    last_revision        BIGINT      NOT NULL DEFAULT 0,
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Mutable platform-operational state: lenny_app reads the baseline and
-- upserts the new render after each reconciliation.
GRANT SELECT, INSERT, UPDATE ON deployment_config_state TO lenny_app;
