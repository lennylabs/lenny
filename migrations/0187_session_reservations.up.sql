-- spec: §12.8 (Compliance Interfaces: Lifecycle fence and drain), §12.3,
-- §4.2
--
-- Session-ID reservations for the creation fence.
--
-- A creation path that writes data keyed to a new session's ID before it
-- inserts the session row, as derive's snapshot copy and a delegated
-- child's file export do, first reserves the ID for the session's owner
-- under the creation fence, in its own transaction. Erasure of the owner
-- treats a reserved ID as one of the owner's sessions and ends the
-- reservation, so the erasure reaches the data even when no session row
-- is ever inserted. The insert of the session row deletes the
-- reservation in its own transaction.
--
-- user_id records the owner the reservation was made for. The session
-- store's user-scoped delete removes the rows whose user_id is the
-- erased user, through idx_session_reservations_user, in the
-- transaction that deletes that user's session rows. The tenant-scoped
-- delete removes every row of the tenant.
--
-- The table carries the §12.3 apparatus every tenant-scoped table uses:
-- the lenny_tenant_guard trigger rejects a write with no
-- app.current_tenant or with a tenant_id other than the context's, and
-- ENABLE/FORCE ROW LEVEL SECURITY with the lenny_tenant_isolation policy
-- filters reads through current_setting('app.current_tenant', false),
-- which raises when the setting is undefined, so a read with no tenant
-- context fails closed. The all-tenants sentinel '__all__' reads every
-- tenant's rows only together with the lenny.allow_all_sentinel opt-in,
-- as migration 0057 requires of every isolation policy (§4.2). Rows are
-- inserted and deleted and never updated, so lenny_app is granted no
-- UPDATE.
CREATE TABLE session_reservations (
    tenant_id  TEXT        NOT NULL REFERENCES tenants(id),
    session_id UUID        NOT NULL,
    user_id    TEXT        NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, session_id)
);

CREATE INDEX idx_session_reservations_user ON session_reservations (tenant_id, user_id);

CREATE TRIGGER lenny_tenant_guard
    BEFORE INSERT OR UPDATE OR DELETE ON session_reservations
    FOR EACH ROW EXECUTE FUNCTION lenny_tenant_guard();

ALTER TABLE session_reservations ENABLE ROW LEVEL SECURITY;
ALTER TABLE session_reservations FORCE ROW LEVEL SECURITY;
CREATE POLICY lenny_tenant_isolation ON session_reservations
    USING (
        tenant_id = current_setting('app.current_tenant', false)
        OR (
            current_setting('app.current_tenant', false) = '__all__'
            AND current_setting('lenny.allow_all_sentinel', true) = 'true'
        )
    );

GRANT SELECT, INSERT, DELETE ON session_reservations TO lenny_app;
