-- The §5.1 setupPolicy block on a runtime.
--
-- setup_policy carries the runtime's §5.1 setupPolicy: the aggregate
-- cap on the pod setup phase (timeoutSeconds) and the disposition when
-- the cap is exceeded (onTimeout: fail | warn). SQL NULL means the
-- runtime declares no setup policy.

ALTER TABLE runtime_definitions
    ADD COLUMN setup_policy JSONB;
