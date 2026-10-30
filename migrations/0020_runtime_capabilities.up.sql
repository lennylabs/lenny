-- The §5.1 capabilities block on a runtime.
--
-- capabilities carries the runtime's §5.1 capabilities block: the
-- interaction model (one_shot | multi_turn) and the mid-session
-- injection support (injection.supported, injection.modes). SQL NULL
-- means the runtime declares no capabilities block; per §5.1 a runtime
-- with no block does not accept message injection.

ALTER TABLE runtime_definitions
    ADD COLUMN capabilities JSONB;
