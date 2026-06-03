-- §25.2 historical baselines for the canonical Progress Envelope. The
-- table holds one row per operation kind (platform_upgrade, restore,
-- backup, ...) with the p50/p90 completion durations and the number of
-- completed operations that fed them. lenny-ops updates the row on each
-- operation completion; a new operation of a kind with sample_size >= 3
-- receives etaMethod "historical_p50" and etaConfidence >= 0.5, while
-- below that threshold the ETA falls through to a lower-confidence
-- method.
--
-- The table is platform-scoped (the §25 control plane is not
-- multi-tenanted at this boundary; §25.4 line 1492 lists the ops_* tables
-- among the PlatformPostgres() set), so no tenant column or RLS policy
-- applies.
--
-- spec: §25.2 lines 393-394.

CREATE TABLE ops_operation_baselines (
    kind            TEXT PRIMARY KEY,
    p50_duration_ms BIGINT NOT NULL,
    p90_duration_ms BIGINT NOT NULL,
    sample_size     BIGINT NOT NULL,
    last_updated    TIMESTAMPTZ NOT NULL DEFAULT now()
);
