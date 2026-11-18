-- Reverse 0198: restore the earlier name of the pool record table.
-- spec: §5.2 (Pool Configuration and Execution Modes).
ALTER INDEX pools_pkey RENAME TO sandbox_warm_pools_pkey;
ALTER TABLE pools RENAME TO sandbox_warm_pools;
