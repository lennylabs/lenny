-- §11.2 per-tenant token budget persistence.
--
-- The tenant row carries the §11.2 per-tenant LLM-token budget
-- (token_quota_per_window) and the per-tenant quota reset period
-- (quota_reset_period: 'hourly', 'daily', 'monthly', 'rolling', or ''
-- for the platform default). Before this migration the Postgres-backed
-- tenant store persisted neither column, so a per-tenant token budget
-- set through the admin API never survived a round-trip and the
-- §4.8 QuotaEvaluator always resolved the tenant-scope limit to zero
-- (unlimited). The reset period was a single platform-wide setting.
--
-- spec: spec/11_policy-and-controls.md §11.2 line 31
-- ("Quota reset periods are configurable per quota type: hourly, daily,
-- monthly, or rolling window"; "Tenant quotas are configured via the
-- admin API or Helm values").
ALTER TABLE tenants
    ADD COLUMN token_quota_per_window BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN quota_reset_period     TEXT   NOT NULL DEFAULT '';
