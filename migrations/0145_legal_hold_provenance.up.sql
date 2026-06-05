-- §15.1 "GET /v1/admin/legal-holds" list endpoint: the list returns
-- each active hold's provenance (setBy, setAt, note). The boolean
-- legal_hold flag on sessions and artifact_store records that a hold is
-- active but not who set it, when, or why. These columns carry that
-- provenance so the list endpoint reports it without reconstructing it
-- from the audit ledger, and so the POST /v1/admin/legal-hold note
-- (required when hold is true per §15.1 line 864) is durable.
-- See spec/15_external-api-surface.md §15.1 lines 864-865.
ALTER TABLE sessions
    ADD COLUMN IF NOT EXISTS legal_hold_set_by TEXT        NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS legal_hold_set_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS legal_hold_note   TEXT        NOT NULL DEFAULT '';

ALTER TABLE artifact_store
    ADD COLUMN IF NOT EXISTS legal_hold_set_by TEXT        NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS legal_hold_set_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS legal_hold_note   TEXT        NOT NULL DEFAULT '';
