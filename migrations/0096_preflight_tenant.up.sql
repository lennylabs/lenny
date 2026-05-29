-- Migration 0096: seed the reserved __preflight__ tenant.
--
-- spec: §12.8 lines 743-758 — the MemoryStore erasure preflight
-- (memorystore.ValidateMemoryStoreErasure) seeds a synthetic agent_memory
-- row under (tenant_id='__preflight__', user_id='__preflight_user__'),
-- erases it, and asserts the row does not survive. agent_memory.tenant_id
-- carries a NOT NULL foreign key to tenants(id) (migration 0032), so the
-- reserved tenant must exist for the default Postgres backend's preflight
-- Write to satisfy the constraint.
--
-- The row is marked soft-deleted (deleted_at set) so the §10.2
-- tenant-claim extractor rejects it for real traffic and the tenant list
-- endpoints exclude it by default. Foreign-key validation and the
-- agent_memory RLS policy ignore deleted_at, so the preflight write/erase
-- cycle still runs under this tenant. genesis_nonce is a benign zero
-- placeholder, matching the default-tenant seed pattern (migration 0053).
INSERT INTO tenants (id, genesis_nonce, deleted_at)
VALUES ('__preflight__', '\x00', '2000-01-01 00:00:00+00')
ON CONFLICT (id) DO NOTHING;
