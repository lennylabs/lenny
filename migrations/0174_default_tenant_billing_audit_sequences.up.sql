-- Fixed billing/audit sequences for the built-in "default" tenant.
--
-- The built-in "default" tenant row is inserted directly into `tenants`
-- by migrations 0053 and 0054 (backfilling pre-tenant-scoped rows to a
-- tenant_id, `INSERT INTO tenants (id, genesis_nonce) VALUES ('default',
-- '\x00') ON CONFLICT (id) DO NOTHING`), not through the admin-API
-- tenant-create paths (POST /v1/admin/tenants,
-- pkg/gateway/externalapi/admin/bootstrap.go upsertTenants) that
-- provision a tenant's billing_seq_/audit_seq_ Postgres sequences
-- (pkg/gateway/externalapi/admin/tenant_sequence_provision.go). The
-- gateway's own auth chain admits "default" unconditionally "even
-- before the row is persisted" (cmd/lenny-gateway/main.go
-- bearerTenantRegistry) precisely so a single-tenant-mode or Embedded
-- Mode install works without any tenant-creation step, but nothing
-- currently provisions its Postgres sequences to match: a deployment
-- whose bootstrap seed never names "default" explicitly (the tier-5
-- e2e Kind install only seeds "acme", tests/testinfra/kind/e2e-values.yaml)
-- never provisions them at all, and the first billing or audit write
-- attributed to "default" fails with "relation ... does not exist"
-- (SQLSTATE 42P01) under the nextval model migration 0173 introduced.
--
-- This is the exact category migration 0173 already solved for the
-- fixed "platform" pseudo-tenant audit chain (see that migration's
-- comment): a chain that is used for billing/audit writes without ever
-- going through a tenant-creation path needs its sequence provisioned
-- at migration time. "default" additionally needs a billing_seq_,
-- unlike "platform", because "default" is a real tenant that can carry
-- billing events (usage, sessions), where "platform" is a pseudo-tenant
-- used only for the platform-admin audit chain.
--
-- Requires migration 0173 (creates lenny_app and the FOR ROLE
-- lenny_ddl default privilege the runtime CREATE SEQUENCE path relies
-- on); this migration creates the two sequences directly under the
-- migration role, so it grants USAGE explicitly rather than relying on
-- the default privilege, exactly as 0173 does for the platform
-- sequence.
--
-- spec: §11.2.1 (billing ledger sequence), §11.7 (audit ledger
-- sequence), §15.1 (tenant-create sequence provisioning), §10.2
-- (length-bounded safe-derived sequence name).

-- billing_seq_37a8eec1ce19687d132fe29051dca629d164e2c4 /
-- audit_seq_37a8eec1ce19687d132fe29051dca629d164e2c4 — the §10.2
-- derivation applied to the compile-time-constant tenant id 'default'.
-- Verified against pkg/common/seqname.{Billing,Audit}SequenceName("default")
-- in the migration test.
CREATE SEQUENCE IF NOT EXISTS billing_seq_37a8eec1ce19687d132fe29051dca629d164e2c4
    START WITH 1 INCREMENT BY 1 NO CYCLE;
GRANT USAGE ON SEQUENCE billing_seq_37a8eec1ce19687d132fe29051dca629d164e2c4 TO lenny_app;

CREATE SEQUENCE IF NOT EXISTS audit_seq_37a8eec1ce19687d132fe29051dca629d164e2c4
    START WITH 1 INCREMENT BY 1 NO CYCLE;
GRANT USAGE ON SEQUENCE audit_seq_37a8eec1ce19687d132fe29051dca629d164e2c4 TO lenny_app;
