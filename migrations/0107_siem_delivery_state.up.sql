-- §12.3 line 97 SIEM outbox forwarder checkpoint. The forwarder tails
-- the committed audit_log rows and delivers each to the external SIEM
-- after Postgres has durably committed it. siem_delivery_state records
-- the per-tenant-chain delivery high-water mark so a forwarder restart
-- replays from the last confirmed delivery point without duplication or
-- gap: last_acked_sequence is the highest audit_log.sequence_number the
-- SIEM has acknowledged for that tenant chain, advanced only after the
-- SIEM accepts the record. last_acked_created_at carries the committed
-- timestamp of that row so the forwarder can compute
-- lenny_audit_siem_delivery_lag_seconds (the gap between the latest
-- committed and the latest acknowledged audit event) without a second
-- audit_log scan.
--
-- The table is platform-internal forwarder bookkeeping, not a
-- tenant-scoped operational table: the SIEM outbox worker runs as the
-- platform control plane (app.current_tenant = '__all__'), so no
-- lenny_tenant_guard trigger or RLS policy is attached. tenant_id is the
-- audit chain selector ('platform' for the platform-admin chain), the
-- same key space as audit_log.tenant_id.
--
-- §16 (Observability) line 378 — the audit partition GC reads
-- last_acked_sequence here as the SIEM delivery guard: it must not drop
-- an audit partition whose most recent event sequence exceeds the
-- forwarder's acknowledged high-water mark.

CREATE TABLE siem_delivery_state (
    tenant_id             TEXT        NOT NULL PRIMARY KEY,
    last_acked_sequence   BIGINT      NOT NULL DEFAULT 0,
    last_acked_created_at TIMESTAMPTZ,
    updated_at            TIMESTAMPTZ NOT NULL DEFAULT now()
);

GRANT SELECT, INSERT, UPDATE, DELETE ON siem_delivery_state TO lenny_app;
