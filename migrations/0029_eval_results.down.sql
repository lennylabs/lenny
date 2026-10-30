-- Reverses 0029_eval_results. Dropping the table cascades its
-- indexes, lenny_tenant_guard trigger, RLS policy, and lenny_app
-- grants.
DROP TABLE IF EXISTS eval_results;
