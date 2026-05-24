-- §4.3 line 202 "scoped by user + connector + tenant + environment".
--
-- Pre-fix, both credential tables ignored the environment dimension:
--
--   connector_credentials primary key (tenant_id, connector_id, user_id)
--   credentials           primary key (tenant_id, ref)
--                         unique     (tenant_id, user_id, provider)
--
-- A connector OAuth token granted in `staging` would be the same row a
-- `production` session reads, defeating the spec's explicit scoping
-- contract.
--
-- This migration adds an environment column to both tables:
--
--   * environment is a string identifier (the §10.6 environments.name
--     within the tenant). The value '' (empty string) represents the
--     "no environment named" case used by sessions that omit the field.
--   * environment is NOT a foreign key to environments(tenant_id,
--     name): the environments table is opt-in and sessions that omit
--     environment must still be able to register credentials. Empty
--     environment scopes the row to "no environment" (the default
--     access path); a non-empty value scopes it to that environment.
--   * The unique constraint widens to include environment so the same
--     (tenant, user, provider) triple can hold one credential per
--     environment.
--
-- For migration 0048's connector_credentials: the primary key widens
-- to (tenant_id, connector_id, user_id, environment) so a connector
-- token can be issued per (user, environment) pair.

-- ---- credentials -----------------------------------------------------------

ALTER TABLE credentials
    ADD COLUMN environment TEXT NOT NULL DEFAULT ''
        CHECK (environment ~ '^[A-Za-z0-9_-]{0,128}$');

ALTER TABLE credentials DROP CONSTRAINT IF EXISTS credentials_tenant_id_user_id_provider_key;
ALTER TABLE credentials
    ADD CONSTRAINT credentials_tenant_user_provider_environment_key
        UNIQUE (tenant_id, user_id, provider, environment);

CREATE INDEX idx_credentials_environment
    ON credentials (tenant_id, environment);

-- ---- connector_credentials -------------------------------------------------

ALTER TABLE connector_credentials
    ADD COLUMN environment TEXT NOT NULL DEFAULT ''
        CHECK (environment ~ '^[A-Za-z0-9_-]{0,128}$');

ALTER TABLE connector_credentials DROP CONSTRAINT connector_credentials_pkey;
ALTER TABLE connector_credentials
    ADD CONSTRAINT connector_credentials_pkey
        PRIMARY KEY (tenant_id, connector_id, user_id, environment);

CREATE INDEX idx_connector_credentials_environment
    ON connector_credentials (tenant_id, environment);
