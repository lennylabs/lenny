-- §11.2.1 "Category 2 — Operator-initiated manual corrections" pending
-- registry. Durable backing for the dual-control approval workflow: a
-- correction request is recorded here in the 'pending' state, routed
-- through a second platform-admin's approval (dual-control) or
-- committed by the submitter (single-control), and only promoted to the
-- immutable billing_events ledger once approved. The committed
-- correction lives in billing_events as an appended billing_correction
-- event; this table holds only the pending request and its approval
-- outcome, so a gateway restart no longer loses pending corrections or
-- the four-eyes audit trail the spec rules out for financial controls.
--
-- The §11.2.1 four-eyes workflow is platform-admin operated and spans
-- tenants (the approve/reject endpoints address a request by its
-- opaque approval_request_id without a tenant scope), so this is
-- platform-operational state: the table is intentionally NOT
-- tenant-isolated (no lenny_tenant_guard trigger, no RLS policy).
-- tenant_id is a data column identifying which tenant's ledger the
-- correction adjusts. Unlike the append-only billing_events ledger it
-- feeds, this table is mutable: a row transitions
-- pending → approved | rejected | expired exactly once.
CREATE TABLE billing_correction_pending (
    -- id is the §11.2.1 approval_request_id (128-bit hex).
    id                 TEXT             PRIMARY KEY,
    tenant_id          TEXT             NOT NULL REFERENCES tenants(id),
    -- corrects_sequence is the sequence_number of the original
    -- billing_events row this correction supersedes.
    corrects_sequence  BIGINT           NOT NULL,
    reason_code        TEXT             NOT NULL,
    detail             TEXT             NOT NULL DEFAULT '',
    -- Replacement (superseding) cost values for the corrected event.
    tokens_input       BIGINT           NOT NULL DEFAULT 0,
    tokens_output      BIGINT           NOT NULL DEFAULT 0,
    pod_minutes        DOUBLE PRECISION NOT NULL DEFAULT 0,
    -- state is the §11.2.1 billing_correction_pending lifecycle enum.
    state              TEXT             NOT NULL DEFAULT 'pending'
        CHECK (state IN ('pending', 'approved', 'rejected', 'expired')),
    -- submitted_by is the platform-admin sub that opened the request;
    -- the four-eyes rule forbids this identity from approving it.
    submitted_by       TEXT             NOT NULL,
    -- decided_by is the platform-admin sub that approved or rejected the
    -- request; empty while pending or after expiry.
    decided_by         TEXT             NOT NULL DEFAULT '',
    dual_control       BOOLEAN          NOT NULL DEFAULT FALSE,
    -- committed_sequence is the sequence_number of the billing_correction
    -- event written to the ledger on approval; 0 until committed.
    committed_sequence BIGINT           NOT NULL DEFAULT 0,
    submitted_at       TIMESTAMPTZ      NOT NULL DEFAULT now(),
    -- decided_at is NULL while the request is pending.
    decided_at         TIMESTAMPTZ
);

-- The List query filters by (tenant_id, state); the expiry sweep and
-- the lenny_billing_correction_pending_total Counts scan by state.
CREATE INDEX idx_billing_correction_pending_tenant_state
    ON billing_correction_pending (tenant_id, state);
CREATE INDEX idx_billing_correction_pending_state
    ON billing_correction_pending (state);

-- The pending registry is mutable platform-operational state, so
-- lenny_app needs full DML (unlike the append-only ledger tables which
-- are INSERT + SELECT only).
GRANT SELECT, INSERT, UPDATE, DELETE ON billing_correction_pending TO lenny_app;
