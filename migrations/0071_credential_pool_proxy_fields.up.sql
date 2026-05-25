-- §4.9 proxy-mode delivery fields for a credential pool.
--
-- delivery_mode selects proxy or direct credential delivery for the
-- pool ('proxy' | 'direct'). Empty selects the deployment default
-- (proxy for multi-tenant, direct for single-tenant), resolved by the
-- leasing path. The closed value set is validated in application code.
--
-- proxy_dialect is the wire dialect a proxy-mode pool's lease exposes
-- ('openai' | 'anthropic'); it must match a dialect the bound Runtime
-- declares in credentialCapabilities.proxyDialect (§5.1). Empty for a
-- direct-mode pool.
--
-- proxy_endpoint is the HTTPS endpoint of the LLM reverse proxy a
-- proxy-mode lease points the runtime SDK at. The admin API rejects an
-- http:// endpoint with InvalidProxyEndpointScheme (spec line 1513) so
-- a lease token is never sent in plaintext on the cluster network.
ALTER TABLE credential_pools
    ADD COLUMN delivery_mode  TEXT NOT NULL DEFAULT '',
    ADD COLUMN proxy_dialect  TEXT NOT NULL DEFAULT '',
    ADD COLUMN proxy_endpoint TEXT NOT NULL DEFAULT '';
