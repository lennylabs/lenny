-- §25.11 ArtifactStore replication durable state. One row per region
-- records the continuous MinIO bucket replication state and the last
-- runtime-residency-preflight outcome, so a lenny-ops (or gateway)
-- restart does not lose the fail-closed suspension a residency violation
-- imposed. The pkg/blobstore/replication Controller persists RegionState
-- rows here through its StateStore seam.
--
-- The table is platform-scoped (the §25 control plane is not
-- multi-tenanted at this boundary; §25.4 line 1492 lists the ops_* tables
-- among the PlatformPostgres() tables), so no tenant column or RLS policy
-- applies.
--
-- spec: §25.11 lines 4073-4098 (Runtime residency preflight), line 1492.

CREATE TABLE ops_artifact_replication_state (
    region                       TEXT PRIMARY KEY,
    status                       TEXT NOT NULL,            -- 'active', 'suspended_residency_violation', 'configuring', 'disabled'
    last_preflight_at            TIMESTAMPTZ,
    last_preflight_result        TEXT NOT NULL DEFAULT '',
    destination_endpoint         TEXT NOT NULL DEFAULT '',
    destination_bucket           TEXT NOT NULL DEFAULT '',
    destination_jurisdiction_tag TEXT NOT NULL DEFAULT '',
    replication_lag_seconds      INT NOT NULL DEFAULT 0,
    suspended_since              TIMESTAMPTZ
);
