-- Reverses 0156_eval_aggregates_matview.up.sql. F-10.7.12.
DROP VIEW IF EXISTS lenny_eval_aggregates_tenant;
DROP FUNCTION IF EXISTS refresh_lenny_eval_aggregates();
DROP MATERIALIZED VIEW IF EXISTS lenny_eval_aggregates;
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'lenny_eval_aggregator') THEN
        REVOKE ALL ON eval_results FROM lenny_eval_aggregator;
        REVOKE ALL ON SCHEMA public FROM lenny_eval_aggregator;
        DROP ROLE lenny_eval_aggregator;
    END IF;
END $$;
