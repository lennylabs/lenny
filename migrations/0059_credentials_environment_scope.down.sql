-- Rollback for 0059: drop the environment column from both credential
-- tables and restore the original key shapes.

DROP INDEX IF EXISTS idx_connector_credentials_environment;
ALTER TABLE connector_credentials DROP CONSTRAINT connector_credentials_pkey;
ALTER TABLE connector_credentials
    ADD CONSTRAINT connector_credentials_pkey
        PRIMARY KEY (tenant_id, connector_id, user_id);
ALTER TABLE connector_credentials DROP COLUMN environment;

DROP INDEX IF EXISTS idx_credentials_environment;
ALTER TABLE credentials
    DROP CONSTRAINT IF EXISTS credentials_tenant_user_provider_environment_key;
ALTER TABLE credentials
    ADD CONSTRAINT credentials_tenant_id_user_id_provider_key
        UNIQUE (tenant_id, user_id, provider);
ALTER TABLE credentials DROP COLUMN environment;
