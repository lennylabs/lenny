-- spec: §12.1 line 5, §12.8 Phase 4 tenant deletion, §11.7 immutability.
-- billing_events is append-only: migration 0002 grants lenny_erasure
-- UPDATE (user_id) for the §12.8 user-erasure pseudonymize path, but no
-- DELETE. The §12.8 Phase 4 tenant-teardown path
-- (billingstore.Store.DeleteByTenant) removes a torn-down tenant's
-- billing rows entirely; the lenny_billing_immutability trigger already
-- permits DELETE under lenny.erasure_mode, so the only missing piece is
-- the table-level DELETE privilege for the lenny_erasure role.
GRANT DELETE ON billing_events TO lenny_erasure;
