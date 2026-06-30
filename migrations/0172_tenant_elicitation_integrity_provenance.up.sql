-- §15.1 elicitation-content-integrity provenance columns the Postgres
-- tenant store did not persist.
--
-- elicitation_content_integrity (migration 0024) persists only the §9.2
-- enforcement mode. The §15.1 PUT
-- /v1/admin/tenants/{id}/elicitation-content-integrity records mode,
-- justification, changedAt, and changedBy on the tenant, and the matching
-- GET returns justification, changedAt, and changedBy in its body. These
-- three columns carry that provenance alongside the mode:
--
--   elicitation_content_integrity_justification — the operator-supplied
--     reason for the stored mode (REQUIRED by §15.1 when mode is
--     detect-only or off).
--   elicitation_content_integrity_changed_at — the RFC 3339 UTC instant
--     the mode was last set.
--   elicitation_content_integrity_changed_by — the operator's OIDC sub
--     recorded at the last change.
--
-- Each is append-only with an empty/NULL default, carrying the same
-- "never set" semantics the in-memory tenant store represents with the
-- zero value.
ALTER TABLE tenants
    ADD COLUMN elicitation_content_integrity_justification TEXT NOT NULL DEFAULT '',
    ADD COLUMN elicitation_content_integrity_changed_at    TIMESTAMPTZ,
    ADD COLUMN elicitation_content_integrity_changed_by     TEXT NOT NULL DEFAULT '';
