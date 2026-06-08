-- §10.7 line 1088: the lenny_eval_aggregates materialized view is
-- defined in the schema migration system (alongside all other DDL) and
-- is created during database migration — never at runtime by the
-- gateway. Deployers opt into it by setting evalAggregationRefreshSeconds
-- to a positive value; with the default 0 the view exists but is never
-- queried, and the gateway aggregates on read from eval_results. F-10.7.12.
--
-- eval_results carries FORCE ROW LEVEL SECURITY (§12.3 / migration 0029).
-- A materialized-view REFRESH runs the defining query as the view's
-- OWNER, and a non-superuser owner under FORCE RLS would populate only
-- the current tenant's rows. So:
--
--   * The view is owned by a dedicated BYPASSRLS aggregator role
--     (lenny_eval_aggregator) so a single cross-tenant REFRESH populates
--     every tenant's aggregates.
--   * The gateway (lenny_app) refreshes through a SECURITY DEFINER
--     function owned by that role, so the REFRESH executes with the
--     aggregator's BYPASSRLS privilege without lenny_app itself ever
--     bypassing RLS.
--   * The gateway READS through a tenant-scoped view that filters by
--     app.current_tenant, so the §12.3 isolation invariant holds on the
--     matview read path. lenny_app is NOT granted direct SELECT on the
--     matview, so it cannot read across tenants.

-- --- aggregator role ----------------------------------------------------
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'lenny_eval_aggregator') THEN
        CREATE ROLE lenny_eval_aggregator NOLOGIN BYPASSRLS;
    END IF;
END $$;
GRANT USAGE ON SCHEMA public TO lenny_eval_aggregator;
GRANT SELECT ON eval_results TO lenny_eval_aggregator;

-- --- materialized view --------------------------------------------------
-- The view carries one row per aggregate at three grains, disambiguated
-- by agg_kind so a real empty-string scorer never collides with a
-- sentinel:
--   * 'variant'   — count(DISTINCT session_id) per (experiment, variant),
--                   over every row including those with no score. This is
--                   the §10.7 VariantResults.sampleCount.
--   * 'scorer'    — count / mean / p50 / p95 over the non-null top-level
--                   `score` per (variant, scorer). This is ScorerStats.
--   * 'dimension' — the same aggregates over each per-dimension value in
--                   the `scores` jsonb map, keyed by dimension. Only
--                   results that submitted a value for the dimension are
--                   counted, matching §10.7 line 1088 per-dimension
--                   semantics.
--
-- percentile_disc (nearest-rank, smallest value with cumulative fraction
-- >= q) reproduces the gateway's on-read percentile
-- (rank = ceil(q * n)), so the matview path and the base-table path
-- return identical p50 / p95. Created WITH DATA so the first gateway
-- REFRESH ... CONCURRENTLY has a populated view to refresh against.
CREATE MATERIALIZED VIEW lenny_eval_aggregates AS
SELECT
    tenant_id,
    experiment_id,
    variant_id,
    'variant'::text                   AS agg_kind,
    ''::text                          AS scorer,
    ''::text                          AS dimension,
    count(DISTINCT session_id)        AS sample_count,
    NULL::double precision            AS mean_score,
    NULL::double precision            AS p50_score,
    NULL::double precision            AS p95_score
FROM eval_results
WHERE experiment_id <> ''
GROUP BY tenant_id, experiment_id, variant_id
UNION ALL
SELECT
    tenant_id,
    experiment_id,
    variant_id,
    'scorer'::text,
    scorer,
    ''::text,
    count(*),
    avg(score),
    percentile_disc(0.5)  WITHIN GROUP (ORDER BY score),
    percentile_disc(0.95) WITHIN GROUP (ORDER BY score)
FROM eval_results
WHERE experiment_id <> '' AND score IS NOT NULL
GROUP BY tenant_id, experiment_id, variant_id, scorer
UNION ALL
SELECT
    er.tenant_id,
    er.experiment_id,
    er.variant_id,
    'dimension'::text,
    er.scorer,
    kv.key,
    count(*),
    avg(kv.val::double precision),
    percentile_disc(0.5)  WITHIN GROUP (ORDER BY kv.val::double precision),
    percentile_disc(0.95) WITHIN GROUP (ORDER BY kv.val::double precision)
FROM eval_results er, LATERAL jsonb_each_text(er.scores) AS kv(key, val)
WHERE er.experiment_id <> '' AND er.scores IS NOT NULL
GROUP BY er.tenant_id, er.experiment_id, er.variant_id, er.scorer, kv.key
WITH DATA;

-- Unique index across the full grain key: required for
-- REFRESH MATERIALIZED VIEW CONCURRENTLY, and it backs the per-experiment
-- read. agg_kind makes the three grains disjoint, so the key is unique.
CREATE UNIQUE INDEX idx_lenny_eval_aggregates_key
    ON lenny_eval_aggregates (tenant_id, experiment_id, variant_id, agg_kind, scorer, dimension);

ALTER MATERIALIZED VIEW lenny_eval_aggregates OWNER TO lenny_eval_aggregator;

-- --- SECURITY DEFINER refresh function ----------------------------------
-- Owned by the BYPASSRLS aggregator, so the gateway (lenny_app, which has
-- EXECUTE) drives a cross-tenant REFRESH ... CONCURRENTLY without holding
-- BYPASSRLS itself. spec: §10.7 line 1088.
CREATE FUNCTION refresh_lenny_eval_aggregates() RETURNS void
    LANGUAGE plpgsql
    SECURITY DEFINER
    SET search_path = public
AS $$
BEGIN
    REFRESH MATERIALIZED VIEW CONCURRENTLY lenny_eval_aggregates;
END;
$$;
ALTER FUNCTION refresh_lenny_eval_aggregates() OWNER TO lenny_eval_aggregator;
REVOKE ALL ON FUNCTION refresh_lenny_eval_aggregates() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION refresh_lenny_eval_aggregates() TO lenny_app;

-- --- tenant-scoped read view --------------------------------------------
-- The gateway reads aggregates through this view, which filters by
-- app.current_tenant. security_barrier prevents a pushed-down predicate
-- from observing rows outside the tenant filter. lenny_app has SELECT on
-- the view only (never on the matview), so the tenant filter cannot be
-- bypassed.
CREATE VIEW lenny_eval_aggregates_tenant WITH (security_barrier = true) AS
SELECT tenant_id, experiment_id, variant_id, agg_kind, scorer, dimension,
       sample_count, mean_score, p50_score, p95_score
FROM lenny_eval_aggregates
WHERE tenant_id = current_setting('app.current_tenant', true);
GRANT SELECT ON lenny_eval_aggregates_tenant TO lenny_app;
