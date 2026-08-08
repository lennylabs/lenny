-- §4.9 semantic-cache CachePolicy for a credential pool.
--
-- cache_policy holds the optional per-pool semantic-cache configuration
-- (enabled, strategy, ttl, similarity_threshold, backend) as a JSON
-- object, or SQL NULL when the pool declares no cachePolicy. §4.9 caching is disabled by default and opt-in per pool,
-- so a NULL column (or one with enabled false) leaves the LLM proxy
-- path uncached. The closed value sets for strategy ('semantic') and
-- backend ('redis' | 'memory') are validated in application code.
--
-- spec: §4.9.
ALTER TABLE credential_pools
    ADD COLUMN cache_policy JSONB;
