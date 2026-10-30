-- The §12.8 legal-hold flag on sessions.
--
-- When legal_hold is true, the artifact retention GC suspends all
-- rotation for the session (TTL deletion and the latest-2-checkpoints
-- trim) and a §12.8 GDPR erasure of the session owner is blocked by
-- the legal-hold preflight. The flag is set and cleared via
-- POST /v1/admin/legal-hold.

ALTER TABLE sessions
    ADD COLUMN legal_hold BOOLEAN NOT NULL DEFAULT false;
