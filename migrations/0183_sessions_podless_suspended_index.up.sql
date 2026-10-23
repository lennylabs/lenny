-- §7.2 podless suspended backlog re-check index.
--
-- A suspended session that released its pod has no coordinating replica, so
-- a message held in its DLQ is resumed by the gateway leader's periodic
-- backlog re-check (§7.2 delivery path 6, Backlog resume.). The re-check
-- pages through each tenant's podless suspended sessions in id order, so
-- this partial index is keyed on (tenant_id, id) and restricted to the rows
-- the listing reads: state suspended with an empty pod_assignment. It lets
-- the listing read one page per tenant with no sort, and grows with the
-- podless suspended population rather than with the tenant's history.
--
-- spec: §7.2 (Message delivery routing — seven paths, path 6), §12.5
-- (Artifact Store).
CREATE INDEX IF NOT EXISTS idx_sessions_podless_suspended
    ON sessions (tenant_id, id)
    WHERE state = 'suspended' AND pod_assignment = '';
