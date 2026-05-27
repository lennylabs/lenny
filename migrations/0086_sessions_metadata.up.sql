-- §7.1 line 6 — CreateSession(runtime, pool, retryPolicy, metadata)
-- carries a client-supplied metadata payload. The minimal gateway
-- preserves the payload verbatim across the session lifetime so a
-- client annotation set at creation surfaces on GET /v1/sessions/{id}
-- and feeds downstream §16.1 metric label dimensions that label by
-- metadata key.
--
-- The payload is a flat string→string map (JSONB column for
-- forward compatibility): non-string values are rejected at the gateway
-- decode boundary so the on-row shape stays bounded and predictable.
-- An absent payload stores SQL NULL and the gateway decodes it as nil
-- so the read envelope omits the field.
ALTER TABLE sessions
    ADD COLUMN IF NOT EXISTS metadata JSONB NULL;
