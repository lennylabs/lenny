-- §10.5 "RuntimeUpgrade State Machine": a tracked, pauseable runtime
-- image rollout for a single SandboxWarmPool walks a durable state
-- machine (pending -> expanding -> draining -> contracting -> complete,
-- with paused as a side-state). The spec requires each phase to be
-- durable so the operator-driven rollout survives a gateway restart and
-- resumes from the recorded phase rather than restarting; it also
-- preserves the old pool configuration in previous_pool_spec for
-- rollback until the upgrade reaches Complete (line 507). One upgrade
-- targets one pool, so this table is keyed by pool name; the version
-- column serializes concurrent phase transitions across gateway
-- replicas. This is platform-operational state (one cluster runtime
-- catalog) rather than per-tenant: it carries no tenant column, no
-- per-tenant write guard, and no row-level isolation policy, matching
-- the ca_rotation precedent.
-- See spec/10_gateway-internals.md §10.5 lines 466-540.
CREATE TABLE IF NOT EXISTS runtime_upgrade (
    pool                          TEXT        PRIMARY KEY,
    phase                         TEXT        NOT NULL
                                      CHECK (phase IN ('pending', 'expanding', 'draining',
                                                       'contracting', 'complete', 'paused')),
    prior_phase                   TEXT        NOT NULL DEFAULT '',
    new_image                     TEXT        NOT NULL,
    previous_pool_spec            JSONB,
    schema_version                TEXT        NOT NULL DEFAULT '',
    drain_first                   BOOLEAN     NOT NULL DEFAULT FALSE,
    canary_percent                INTEGER     NOT NULL DEFAULT 0
                                      CHECK (canary_percent >= 0 AND canary_percent <= 100),
    stabilization_window_secs     BIGINT      NOT NULL DEFAULT 120,
    drain_timeout_secs            BIGINT      NOT NULL DEFAULT 0,
    auto_advance                  BOOLEAN     NOT NULL DEFAULT FALSE,
    pause_reason                  TEXT        NOT NULL DEFAULT '',
    paused_at                     TIMESTAMPTZ,
    phase_entered_at              TIMESTAMPTZ NOT NULL DEFAULT now(),
    draining_sessions             INTEGER     NOT NULL DEFAULT 0,
    version                       BIGINT      NOT NULL DEFAULT 1,
    created_at                    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at                    TIMESTAMPTZ NOT NULL DEFAULT now()
);

GRANT SELECT, INSERT, UPDATE, DELETE ON runtime_upgrade TO lenny_app;
