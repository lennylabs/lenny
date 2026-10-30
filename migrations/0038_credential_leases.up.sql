-- The §4.9 gateway-replica credential-lease store.
--
-- credential_leases is the durable form of the per-replica working set
-- the §4.9 LLM reverse proxy resolves a bearer lease token through. A
-- lease is platform-global: the in-memory credleasestore.Store is keyed
-- by lease id alone and a pool-backed lease (the common proxy-mode case)
-- carries no tenant_id at all, so this table is not tenant-scoped. Like
-- connectors and runtime_definitions it carries no lenny_tenant_guard
-- trigger and no RLS. The lenny_app role is created by migration 0002.
--
-- platform-global
CREATE TABLE credential_leases (
    -- lease_id is the lease's opaque identifier and the primary key.
    -- GetByID resolves a lease through it.
    lease_id      TEXT        PRIMARY KEY,
    -- lease_token is the opaque bearer token a proxy-mode lease carries.
    -- It is set only for delivery_mode = 'proxy' leases and is NULL for
    -- a direct-mode lease. GetByToken resolves a lease through it, so it
    -- carries a unique index. A partial UNIQUE constraint keeps the
    -- token unique among proxy-mode rows while permitting many
    -- direct-mode rows with a NULL token.
    lease_token   TEXT,
    -- delivery_mode is the §4.9 credential delivery mode ('proxy' or
    -- 'direct'). Membership is validated in application code by
    -- Lease.Validate before a row is written.
    delivery_mode TEXT        NOT NULL,
    -- lease holds the full credential.Lease record as JSON. The proxy
    -- hot path reads back the whole lease, so the record is stored
    -- verbatim rather than decomposed into columns.
    lease         JSONB       NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- A proxy-mode lease token is unique across the store; direct-mode rows
-- carry a NULL token and are excluded from the constraint.
CREATE UNIQUE INDEX idx_credential_leases_token
    ON credential_leases (lease_token)
    WHERE lease_token IS NOT NULL;

GRANT SELECT, INSERT, UPDATE, DELETE ON credential_leases TO lenny_app;
