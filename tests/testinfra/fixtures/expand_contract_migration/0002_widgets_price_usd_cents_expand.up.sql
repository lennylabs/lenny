-- spec: §10.5 Phase 1 (expand) — "Add new columns/tables, deploy code
-- that writes to both old and new." The new column must be NULL-able
-- (or carry a server-side DEFAULT) "until Phase 3 removes the old
-- columns", because during a rolling deploy an old-version replica
-- that does not know about the new column still issues INSERT
-- statements that omit it.
ALTER TABLE widgets
    ADD COLUMN price_usd_cents INTEGER;
