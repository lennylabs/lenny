-- Reverse 0181: restore the DEFAULT 0 on both columns.
--
-- No row is rolled back. §4.2 states that coordination_generation is
-- monotonically non-decreasing and is never reset, so a row the forward
-- backfill moved from 0 to 1 keeps the value it now carries, as does every
-- row a handoff has advanced since. A predicate that moved rows back to 0
-- could not tell the two apart in any case.
--
-- spec: §4.2, §10.1, §10.5.

ALTER TABLE sessions
    ALTER COLUMN coordination_generation SET DEFAULT 0;

ALTER TABLE coordination_lease
    ALTER COLUMN coordination_generation SET DEFAULT 0;
