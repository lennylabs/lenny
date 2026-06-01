-- §12.9 line 1048 / §15.1 line 816: workspace_tier is a closed
-- data-classification enum. The tenant-settable values are '' (the
-- implicit T3 — Confidential default), 'T3', and 'T4' (Restricted);
-- 'T1'/'T2' classify other data categories and are not selectable as a
-- tenant workspaceTier. The admin POST/PUT and bootstrap paths now
-- reject out-of-enum values, but a direct database write could still
-- persist a stale tier that every downstream consumer reads as "not T4"
-- (KMS probe skipped, SSE-KMS resolver falls back, t4-node-isolation
-- predicate skipped). This CHECK constraint is the defense-in-depth
-- backstop at the storage boundary.
ALTER TABLE tenants
    ADD CONSTRAINT tenants_workspace_tier_check
    CHECK (workspace_tier IN ('', 'T3', 'T4'));
