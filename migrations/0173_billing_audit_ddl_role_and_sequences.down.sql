-- Reverse 0173: drop the fixed platform-chain audit sequence, the
-- lenny_app USAGE default privilege keyed to lenny_ddl, lenny_ddl's
-- grants, and the lenny_ddl role.
--
-- A full rollback removes the provisioning machinery this migration
-- installed, including the per-tenant billing_seq_/audit_seq_ sequences
-- the runtime handler created under the lenny_ddl role: because lenny_ddl
-- owns those sequences, the role cannot be dropped while they exist, so
-- DROP OWNED BY lenny_ddl clears them together with the FOR ROLE default
-- privilege ACL keyed to lenny_ddl. This is a rollback of the whole
-- provisioning surface, distinct from the §12.8 per-tenant teardown, which
-- runs as lenny_erasure (holding no DROP privilege) and deliberately
-- leaves an orphaned sequence in place while the role model stays intact.
--
-- Order matters: objects lenny_ddl owns and default privileges keyed to it
-- must be cleared before the role can be dropped.

-- Drop the fixed platform-chain audit sequence (owned by the migration
-- role, so DROP OWNED BY lenny_ddl below does not cover it).
DROP SEQUENCE IF EXISTS audit_seq_d294fcce0cc88587843099d85dd805aeef1b09a6;

-- Reverse lenny_ddl's grants and default privileges, drop everything it
-- owns (the runtime per-tenant sequences and the FOR ROLE default-privilege
-- ACL), then drop the role. Guarded on lenny_ddl still existing so the down
-- is idempotent and safe on an instance where the role was never created.
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'lenny_ddl') THEN
        REVOKE SELECT ON billing_events, audit_log FROM lenny_ddl;
        REVOKE USAGE, CREATE ON SCHEMA public FROM lenny_ddl;
        -- DROP OWNED removes the per-tenant sequences lenny_ddl created and
        -- the FOR ROLE default-privilege grant to lenny_app.
        DROP OWNED BY lenny_ddl;
        DROP ROLE lenny_ddl;
    END IF;
END $$;
