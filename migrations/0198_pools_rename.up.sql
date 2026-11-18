-- The pool record table takes the name the admin API gives its records:
-- pools. Nothing is deployed, so the rename keeps no view or alias of the
-- earlier name. The grants, the tenant_access_grants foreign key, and the
-- columns move with the table; the primary-key index is renamed to match.
-- spec: §5.2 (Pool Configuration and Execution Modes).
ALTER TABLE sandbox_warm_pools RENAME TO pools;
ALTER INDEX sandbox_warm_pools_pkey RENAME TO pools_pkey;
