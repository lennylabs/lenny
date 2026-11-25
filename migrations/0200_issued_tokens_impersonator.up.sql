-- §13.3 impersonator link on issued_tokens.
--
-- A platform-admin impersonation bearer's row records the platform-admin's
-- own bearer that started it, by tenant and jti, so a recursive revocation
-- that reaches that bearer also revokes the impersonation (§13.3
-- "Platform-admin impersonation" rules 1, 4, and 7). The link lives in its
-- own columns because an impersonation row keeps parent_jti null, which is
-- how rule 2 identifies it, and because the bearer that started it is in
-- another tenant's lineage. The partial index keys the cross-tenant lookup
-- the Token Service runs after a cascade commits.
--
-- spec: §12.2 (TokenIssuanceStore columns and indexes), §13.3 (Lineage of
-- derived tokens, Platform-admin impersonation).
ALTER TABLE issued_tokens ADD COLUMN impersonator_tenant_id TEXT NULL;
ALTER TABLE issued_tokens ADD COLUMN impersonator_jti TEXT NULL;
CREATE INDEX idx_issued_tokens_impersonator ON issued_tokens (impersonator_tenant_id, impersonator_jti) WHERE impersonator_jti IS NOT NULL;
