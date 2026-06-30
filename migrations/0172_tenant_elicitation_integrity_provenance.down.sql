-- Reverses 0172_tenant_elicitation_integrity_provenance. The up is an
-- append-only ALTER TABLE, so the reversal is a clean column drop with no
-- data backfill to undo.
ALTER TABLE tenants
    DROP COLUMN IF EXISTS elicitation_content_integrity_justification,
    DROP COLUMN IF EXISTS elicitation_content_integrity_changed_at,
    DROP COLUMN IF EXISTS elicitation_content_integrity_changed_by;
