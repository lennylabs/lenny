-- §4.3 line 193 / §13.3 per-dialect cap discipline.
--
-- spec: §4.3 line 193 cross-ref to §13.3 — issued tokens MUST record
-- the dialect cap that capped the lifetime so a forensic reconstruction
-- of "why did this token live exactly Nh" is possible after the fact.
-- The §13.3 caps are `lenny-gateway: 24h`, `lenny-ops: 1h`,
-- `llm-proxy: 1h`.
--
-- The §4.3 audience set is also intrinsically multi-valued: a
-- token-exchange request can list more than one audience (RFC 8693).
-- Storing a space-joined string in a single TEXT column blurs that
-- and prevents dialect-aware reverse lookups.
--
-- This migration:
--
--   1. Adds `audiences TEXT[]` so each issued token records its
--      audience set as a list.
--   2. Adds `dialect_cap_applied_seconds INT` so the applied
--      per-dialect cap (`lenny-gateway: 24h` → 86400) is preserved
--      independently of `exp - issued_at`, which may have been further
--      tightened by `subject.exp` / `actor.exp`.
--
-- The legacy single-valued `audience TEXT` column is preserved for
-- existing readers; the writer now writes both columns and downstream
-- code prefers `audiences` when present.

ALTER TABLE issued_tokens
    ADD COLUMN audiences                   TEXT[] NOT NULL DEFAULT '{}'::text[],
    ADD COLUMN dialect_cap_applied_seconds INTEGER NOT NULL DEFAULT 0
        CHECK (dialect_cap_applied_seconds >= 0);

-- Backfill `audiences` from the legacy single-valued column so existing
-- rows are reconstructible by either reader. The legacy value can be a
-- space-joined string (see pkg/tokenservice/tokenservice.go), so split
-- on whitespace.
UPDATE issued_tokens
SET audiences = COALESCE(
    NULLIF(string_to_array(audience, ' '), ARRAY[]::text[]),
    '{}'::text[]
)
WHERE audiences = '{}'::text[]
  AND audience <> '';

-- Index for audience lookups; the §13.3 revocation/forensic path
-- queries by audience as well as by jti.
CREATE INDEX idx_issued_tokens_audiences ON issued_tokens USING GIN (audiences);
