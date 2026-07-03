-- Per-tenant billing/audit sequence provisioning: CREATE-privileged DDL
-- role, lenny_app USAGE on the DDL role's runtime-created sequences, and
-- the fixed platform-chain audit sequence.
--
-- §11.2.1 and §11.7 assign each tenant a real per-tenant Postgres
-- sequence (billing_seq_<40hex>, audit_seq_<40hex>) that gateway replicas
-- draw with nextval inside the ledger INSERT transaction, so the
-- sequence_number stays monotonic across the retention sweep and §12.8
-- teardown deletes that a MAX(sequence_number)+1 scheme cannot survive.
-- The per-tenant sequences are created at runtime by the tenant-create
-- handler (§15.1), not by this migration, because a fresh install has no
-- tenants when the migration runs (§17.4). This migration provisions the
-- role and grant machinery that runtime provisioning depends on, plus the
-- one fixed sequence no tenant-creation path ever creates.
--
--   1. lenny_ddl — a distinct CREATE-privileged login role reserved for
--      per-tenant DDL. §11.7 item 7 states gateway replicas connect as
--      lenny_app, which is created NOLOGIN with USAGE ON SCHEMA public and
--      table-level DML only (migration 0002:22,29,161) and holds no
--      CREATE ON SCHEMA, so a runtime CREATE SEQUENCE through the lenny_app
--      pool would raise permission denied for schema public. lenny_ddl
--      holds CREATE ON SCHEMA public so the runtime CREATE SEQUENCE
--      succeeds, and SELECT on billing_events / audit_log so the setval
--      re-seed can read a tenant's MAX(sequence_number). It holds no
--      INSERT/UPDATE/DELETE on either ledger table and no BYPASSRLS, so it
--      can provision sequences and read the ledger to re-seed but never
--      writes or rewrites the ledger, and the re-seed read scopes to the
--      one tenant it provisions through the SET LOCAL app.current_tenant
--      RLS context. lenny_app never gains CREATE ON SCHEMA.
--
--   2. lenny_app USAGE on the DDL role's future sequences. nextval
--      requires USAGE on the sequence, but the per-tenant sequences are
--      created at runtime by lenny_ddl rather than by the migration role,
--      so the default-privilege grant is keyed to the creating role:
--      ALTER DEFAULT PRIVILEGES FOR ROLE lenny_ddl. The FOR ROLE clause is
--      load-bearing: default privileges attach based on the creating role,
--      so a grant without it would apply only to sequences the migration
--      role itself creates and would not attach to lenny_ddl's sequences,
--      leaving nextval under the lenny_app session raising permission
--      denied for sequence. A one-time USAGE grant covers any sequence
--      that already exists when the migration runs.
--
--   3. audit_seq_d294fcce0cc88587843099d85dd805aeef1b09a6 — the fixed
--      platform-chain audit sequence. Platform-admin audit events are
--      recorded on the fixed "platform" pseudo-tenant chain, which is not
--      a registered tenants row (audit_log carries no foreign key to
--      tenants, 0001:131-134) and is never created through a
--      tenant-creation path, so the runtime provisioning helper never
--      reaches it. The seal/insert path assigns the sequence for every
--      audit write without branching on tenant, so under the nextval model
--      the first platform-chain Append would call nextval on this sequence;
--      absent it, the platform-chain write fails with "relation does not
--      exist" and, because the admin sink is fire-and-forget, drops the
--      platform-admin compliance chain silently from Day 1. The name is the
--      §10.2 derivation applied to the compile-time-constant chain id
--      'platform': the lowercase hex of the first 20 bytes of
--      SHA-256('platform'), a fixed 40-hex digest. Migrations cannot call
--      Go, so the literal is embedded; the derivation is verified against
--      pkg/common/seqname in the migration test.
--
-- This migration runs against every audit-bearing Postgres instance: the
-- billing/audit instance (LENNY_PG_BILLING_AUDIT_DSN when configured,
-- otherwise the primary), the primary in the separate-instance topology
-- (where the §13.3 issued-token write-before-issue path creates and
-- consumes a per-tenant audit_seq_<tenant-40hex> sequence), and every
-- CMP-058 regional platform-Postgres instance a platform-chain audit write
-- can land on (resolved by StoreRouter.PlatformPostgresForRegion, reached
-- by appendPlatformTargeted -> appendOnPool). The migration file is
-- instance-agnostic; the deployment applies it to each audit-bearing
-- instance, so the role, grants, and fixed platform sequence exist wherever
-- a nextval can run.
--
-- spec: §11.7 item 7 (least-privilege role model), §15.1 (tenant-create
-- sequence provisioning), §10.2 (length-bounded safe-derived sequence name).

-- --- lenny_ddl role -----------------------------------------------------
-- Created LOGIN (the operator supplies its login credential through the
-- LENNY_PG_BILLING_AUDIT_DDL_DSN / LENNY_PG_PRIMARY_DDL_DSN the chart
-- plumbs), guarded IF NOT EXISTS like migration 0002's role creation so
-- the migration is idempotent across the audit-bearing instances it runs
-- on. No BYPASSRLS: the setval re-seed read scopes to a single tenant
-- under the RLS GUC.
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'lenny_ddl') THEN
        CREATE ROLE lenny_ddl LOGIN;
    END IF;
END $$;

-- CREATE ON SCHEMA public so the runtime CREATE SEQUENCE succeeds;
-- SELECT on the ledger tables so the setval re-seed reads the per-tenant
-- MAX(sequence_number). No INSERT/UPDATE/DELETE on either ledger table:
-- lenny_ddl provisions and reads but never writes the ledger.
GRANT USAGE, CREATE ON SCHEMA public TO lenny_ddl;
GRANT SELECT ON billing_events, audit_log TO lenny_ddl;

-- --- lenny_app USAGE on lenny_ddl's sequences ---------------------------
-- FOR ROLE lenny_ddl is load-bearing: it attaches the USAGE default to
-- sequences lenny_ddl creates, which nextval under the lenny_app session
-- requires. A grant without FOR ROLE would attach only to the migration
-- role's own sequences.
ALTER DEFAULT PRIVILEGES FOR ROLE lenny_ddl IN SCHEMA public
    GRANT USAGE ON SEQUENCES TO lenny_app;

-- One-time USAGE grant covering any billing/audit sequence that already
-- exists when this migration runs (default privileges apply only to
-- sequences created after this statement).
DO $$
DECLARE
    seq TEXT;
BEGIN
    FOR seq IN
        SELECT sequence_name FROM information_schema.sequences
        WHERE sequence_schema = 'public'
          AND (sequence_name LIKE 'billing_seq_%' OR sequence_name LIKE 'audit_seq_%')
    LOOP
        EXECUTE format('GRANT USAGE ON SEQUENCE public.%I TO lenny_app', seq);
    END LOOP;
END $$;

-- --- Fixed platform-chain audit sequence --------------------------------
-- audit_seq_ + lowercase-hex(SHA-256('platform')[:20]) — the §10.2
-- derivation applied to the compile-time-constant chain id 'platform'.
-- Verified against pkg/common/seqname.AuditSequenceName("platform") in the
-- migration test. lenny_app draws nextval on it for every platform-chain
-- audit write, so it needs USAGE; the migration role owns it, so the
-- explicit GRANT is required (the FOR ROLE lenny_ddl default privilege
-- above does not cover a sequence the migration role creates).
CREATE SEQUENCE IF NOT EXISTS audit_seq_d294fcce0cc88587843099d85dd805aeef1b09a6
    START WITH 1 INCREMENT BY 1 NO CYCLE;
GRANT USAGE ON SEQUENCE audit_seq_d294fcce0cc88587843099d85dd805aeef1b09a6 TO lenny_app;
