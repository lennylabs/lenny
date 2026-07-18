-- §8.3 line 443 — credential_deny records that the delegate_task hop that
-- created this child session set credentialPropagation: deny, so the child
-- receives no LLM credentials. inherit versus independent is already carried
-- by credential_origin_session_id (migration 0176); this column adds only the
-- deny bit that self-origin cannot express, because an independent child and a
-- deny child are both self-origin. The finalize-time §4.9 engine reads it and
-- resolves a deny row (and an inherit hop whose origin row is deny) to zero
-- eligible providers, failing the child closed at credential assignment.
-- Invariant after creation, matching credential_origin_session_id.
ALTER TABLE sessions
    ADD COLUMN IF NOT EXISTS credential_deny BOOLEAN NOT NULL DEFAULT false;
