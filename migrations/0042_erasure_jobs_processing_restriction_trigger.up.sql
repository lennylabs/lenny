-- The §12.8 GDPR erasure-job registry and the Article 18
-- processing-restriction database trigger.
--
-- §12.8 specifies two database-level controls this migration adds.
--
-- 1. The erasure_jobs table. POST /v1/admin/users/{user_id}/erase
--    records a DeleteByUser job here; the job persists a phase field
--    so a controller restart resumes from the safe point rather than
--    re-running completed steps (§12.8 "Erasure job phase field").
--    The table also gives the processing-restriction trigger below a
--    relation to test the user's active-job status against.
--
-- 2. The processing-restriction BEFORE UPDATE trigger. §12.8
--    ("Processing restriction during erasure", "Database-level
--    enforcement") requires that users.processing_restricted cannot
--    be cleared while an erasure job for that user is still in
--    progress. Clearing it via an application bug, a migration error,
--    or direct database manipulation would lift GDPR Article 18
--    restriction-of-processing while the erasure is incomplete. The
--    trigger rejects an UPDATE that sets processing_restricted = false
--    when an active erasure job exists for the user. It exempts the
--    lenny_erasure role — which clears the flag as part of the
--    erasure completion receipt transaction — and the explicit
--    clear-processing-restriction admin endpoint, which sets the
--    session-local variable lenny.clear_processing_restriction = 'true'
--    (analogous to the lenny.erasure_mode bypass on the audit and
--    billing immutability triggers in migration 0002).

-- --- erasure_jobs --------------------------------------------------------
-- erasure_jobs is tenant-scoped: it carries the lenny_tenant_guard
-- trigger and a row-level security policy, exactly like the other
-- §12.3 tenant-scoped tables. status is the §12.8 phase enum; the
-- four non-terminal values are the ones the processing-restriction
-- trigger treats as "an erasure job is in progress".
CREATE TABLE erasure_jobs (
    id           TEXT        PRIMARY KEY,
    tenant_id    TEXT        NOT NULL REFERENCES tenants(id),
    user_id      TEXT        NOT NULL,
    -- status mirrors the §12.8 erasure-job phase enum. initiated,
    -- store_deleting, pseudonymizing, and verifying are the
    -- in-progress states; completed and failed are terminal.
    status       TEXT        NOT NULL DEFAULT 'initiated',
    -- total is the running sum of per-store deleted-row counts.
    total        INTEGER     NOT NULL DEFAULT 0,
    -- failure carries the error reason when status = 'failed'.
    failure      TEXT        NOT NULL DEFAULT '',
    started_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    completed_at TIMESTAMPTZ,
    CONSTRAINT erasure_jobs_status_check
        CHECK (status IN ('initiated', 'store_deleting', 'pseudonymizing',
                          'verifying', 'completed', 'failed'))
);

-- An index on (user_id, status) so the processing-restriction trigger's
-- active-job lookup is a single index probe per UPDATE.
CREATE INDEX erasure_jobs_user_status_idx ON erasure_jobs (user_id, status);

CREATE TRIGGER lenny_tenant_guard
    BEFORE INSERT OR UPDATE OR DELETE ON erasure_jobs
    FOR EACH ROW EXECUTE FUNCTION lenny_tenant_guard();

ALTER TABLE erasure_jobs ENABLE ROW LEVEL SECURITY;
ALTER TABLE erasure_jobs FORCE ROW LEVEL SECURITY;
CREATE POLICY lenny_tenant_isolation ON erasure_jobs
    USING (tenant_id = current_setting('app.current_tenant', true));

-- --- Processing-restriction trigger -------------------------------------
-- §12.8 GDPR Article 18: users.processing_restricted may only be
-- cleared (set to false) when no erasure job for that user is still
-- in progress. The guard clause reading lenny.clear_processing_restriction
-- mirrors the lenny.erasure_mode bypass on the §11.7 immutability
-- triggers; both are session-local variables an authorized code path
-- sets for the duration of one transaction.
CREATE FUNCTION lenny_processing_restriction_guard() RETURNS trigger AS $$
BEGIN
    -- Only an UPDATE that flips processing_restricted true -> false is
    -- governed. Setting the flag, or any UPDATE that leaves it
    -- unchanged, passes straight through.
    IF NOT (OLD.processing_restricted = true AND NEW.processing_restricted = false) THEN
        RETURN NEW;
    END IF;
    -- The lenny_erasure role clears the flag as part of the erasure
    -- completion receipt transaction; the explicit
    -- clear-processing-restriction admin endpoint sets the
    -- session-local variable. Either exempts the UPDATE.
    IF current_user = 'lenny_erasure'
        OR current_setting('lenny.clear_processing_restriction', true) = 'true' THEN
        RETURN NEW;
    END IF;
    -- Reject the clear when an erasure job for the user is still in a
    -- non-terminal phase.
    IF EXISTS (
        SELECT 1 FROM erasure_jobs
        WHERE erasure_jobs.user_id = NEW.subject
          AND erasure_jobs.tenant_id = NEW.tenant_id
          AND erasure_jobs.status IN ('initiated', 'store_deleting',
                                      'pseudonymizing', 'verifying')
    ) THEN
        RAISE EXCEPTION
            'ERASURE_JOB_ACTIVE: cannot clear processing_restricted while erasure job for user % is in progress',
            NEW.subject
            USING ERRCODE = 'insufficient_privilege';
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER lenny_processing_restriction_guard
    BEFORE UPDATE ON users
    FOR EACH ROW EXECUTE FUNCTION lenny_processing_restriction_guard();

-- --- Grants -------------------------------------------------------------
-- lenny_app has full DML on erasure_jobs: it writes job records and
-- advances the phase. lenny_erasure holds SELECT for the trigger's
-- active-job lookup and UPDATE on the status/failure/completion
-- columns so the erasure completion receipt transaction can mark the
-- job terminal.
GRANT SELECT, INSERT, UPDATE, DELETE ON erasure_jobs TO lenny_app;
GRANT SELECT ON erasure_jobs TO lenny_erasure;
GRANT UPDATE (status, total, failure, completed_at) ON erasure_jobs TO lenny_erasure;

-- §12.8 erasure path: the erasure job runs its DELETEs as the
-- lenny_erasure role. The role already holds DELETE on audit_log and
-- UPDATE on billing_events.user_id (migration 0002); it additionally
-- needs DELETE on the per-tenant user registry so DeleteByUser can
-- remove the user row itself, and SELECT so the dependency-ordered
-- deletion can resolve the user's scope. The lenny_erasure role
-- clears users.processing_restricted as part of the completion
-- receipt transaction — the processing-restriction trigger above
-- exempts the role for exactly that write.
GRANT SELECT, DELETE, UPDATE (processing_restricted, erasure_job_id) ON users TO lenny_erasure;
