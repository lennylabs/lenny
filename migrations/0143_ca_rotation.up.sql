-- §10.3 "CA rotation procedure": the gateway walks a durable state
-- machine (idle -> new_ca_deployed -> promoted -> old_ca_retired) when
-- the cluster-internal CA is rotated. The spec requires each stage to be
-- durable so an operator who interrupts the procedure (e.g. to wait for
-- cert-manager to reissue leaves) resumes from the recorded stage rather
-- than restarting. The rotation is platform-global (one cluster CA), so
-- this is a single-row table pinned by a constant id; the version column
-- serializes concurrent stage transitions across gateway replicas.
-- See spec/10_gateway-internals.md §10.3 lines 344-350.
CREATE TABLE IF NOT EXISTS ca_rotation (
    id                  TEXT        PRIMARY KEY DEFAULT 'singleton'
                            CHECK (id = 'singleton'),
    stage               TEXT        NOT NULL
                            CHECK (stage IN ('idle', 'new_ca_deployed', 'promoted', 'old_ca_retired')),
    current_ca_id       TEXT        NOT NULL,
    new_ca_id           TEXT        NOT NULL DEFAULT '',
    overlap_started_at  TIMESTAMPTZ,
    overlap_window_secs BIGINT      NOT NULL DEFAULT 0,
    version             BIGINT      NOT NULL DEFAULT 1,
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);

GRANT SELECT, INSERT, UPDATE, DELETE ON ca_rotation TO lenny_app;
