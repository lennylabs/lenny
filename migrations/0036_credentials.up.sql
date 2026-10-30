-- The §4.9 / §15.1 end-user credential registry.
--
-- credentials is tenant-scoped: it carries the lenny_tenant_guard
-- trigger and a row-level security policy, exactly like the other
-- §12.3 tenant-scoped tables. The trigger function and the lenny_app
-- role are created by migration 0002.
--
-- A credential is addressed by an opaque ref (credential_ref). The
-- (tenant_id, user_id, provider) triple is unique: re-registering the
-- same triple replaces the secret per §15.1. The secret material is
-- stored as held by the in-memory store; KMS-envelope encryption is a
-- later phase.

CREATE TABLE credentials (
    tenant_id  TEXT        NOT NULL REFERENCES tenants(id),
    -- ref is the opaque credential_ref the user addresses the
    -- credential by. It is unique within the tenant.
    ref        TEXT        NOT NULL,
    -- user_id scopes the credential to a single user within the
    -- tenant, alongside tenant_id.
    user_id    TEXT        NOT NULL,
    -- provider is the §4.9 credential provider. Membership in the
    -- built-in enum is validated in application code.
    provider   TEXT        NOT NULL,
    -- secret is the raw secret material. It is never returned on the
    -- wire per §15.1; the REST handlers project a secret-free payload.
    secret     TEXT        NOT NULL DEFAULT '',
    -- status is the credential lifecycle state ('active' or 'revoked').
    status     TEXT        NOT NULL DEFAULT 'active',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- rotated_at is the last secret-replacement instant; NULL until the
    -- credential is rotated or re-registered.
    rotated_at TIMESTAMPTZ,
    -- revoked_at is the revocation instant; NULL while the credential
    -- is active.
    revoked_at TIMESTAMPTZ,
    PRIMARY KEY (tenant_id, ref),
    -- The (tenant, user, provider) triple is unique: one credential
    -- per provider per user per §15.1.
    UNIQUE (tenant_id, user_id, provider)
);

CREATE TRIGGER lenny_tenant_guard
    BEFORE INSERT OR UPDATE OR DELETE ON credentials
    FOR EACH ROW EXECUTE FUNCTION lenny_tenant_guard();

ALTER TABLE credentials ENABLE ROW LEVEL SECURITY;
ALTER TABLE credentials FORCE ROW LEVEL SECURITY;
CREATE POLICY lenny_tenant_isolation ON credentials
    USING (tenant_id = current_setting('app.current_tenant', true));

GRANT SELECT, INSERT, UPDATE, DELETE ON credentials TO lenny_app;
