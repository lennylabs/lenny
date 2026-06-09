-- §12.8 sub-step 4 legal-hold escrow ledger records. One row per resource
-- migrated to the region-scoped legal-hold escrow bucket during a Phase 3.5
-- force-delete override. The row is the durable marker Phase 4's
-- DeleteByTenant skip logic reads (legal_hold_escrow: true) and the index
-- the §12.8 line 884 escrow-GC release path queries when the hold is later
-- cleared via POST /v1/admin/legal-hold (hold: false) on the tombstoned
-- tenant.
--
-- The records describe a tenant being deleted but MUST survive the tenant
-- tombstone (the hold may be cleared long after Phase 4), so the table is
-- platform-operational state written under the platform tenant — there is
-- no lenny_tenant_guard trigger and no RLS policy, mirroring the
-- legal_hold.escrowed audit events which are likewise written on the
-- platform chain to outlive the tenant. The table holds no T3/T4 personal
-- data: only resource identifiers, the escrow object key, and the escrow
-- region (the escrowed payload itself lives in the escrow bucket under the
-- region-scoped escrow KEK, not here).
--
-- spec: §12.8 lines 884-885; §16.7 line 694.
CREATE TABLE legal_hold_escrow_records (
    tenant_id            TEXT        NOT NULL,
    -- The §12.8 escrow object key (legal-hold-escrow/{tenant}/{type}/{id}),
    -- the deletion target the release path hands to the escrow bucket.
    escrow_object_key    TEXT        NOT NULL,
    resource_type        TEXT        NOT NULL,
    resource_id          TEXT        NOT NULL,
    escrow_region        TEXT        NOT NULL,
    escrow_kek_id        TEXT        NOT NULL,
    tenant_delete_job_id TEXT        NOT NULL,
    -- session_id / artifact_uri are the release-lookup keys: clearing a
    -- session hold releases every artifact escrowed under it; clearing an
    -- artifact's own hold releases exactly it.
    session_id           TEXT        NOT NULL DEFAULT '',
    artifact_uri         TEXT        NOT NULL DEFAULT '',
    original_hold_set_at TIMESTAMPTZ,
    migrated_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- released_at / released_by record the escrow-GC release; a released
    -- row is excluded from the active-set queries so a re-cleared hold does
    -- not re-delete (idempotent).
    released_at          TIMESTAMPTZ,
    released_by          TEXT        NOT NULL DEFAULT '',
    PRIMARY KEY (tenant_id, escrow_object_key)
);

-- The release path queries active records by session or by artifact, so a
-- partial index over the unreleased rows keeps the clear-time lookup
-- bounded.
CREATE INDEX legal_hold_escrow_records_active_session
    ON legal_hold_escrow_records (tenant_id, session_id)
    WHERE released_at IS NULL;
CREATE INDEX legal_hold_escrow_records_active_artifact
    ON legal_hold_escrow_records (tenant_id, artifact_uri)
    WHERE released_at IS NULL;

GRANT SELECT, INSERT, UPDATE, DELETE ON legal_hold_escrow_records TO lenny_app;
