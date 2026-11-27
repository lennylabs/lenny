-- §7.1 (Forced rotation) — the bound on forced rotations.
--
-- forced records that POST /v1/admin/upload-token-keys/rotate wrote the
-- key rather than a scheduled rotation. A forced rotation is refused while
-- the purpose's newest key is a forced key whose active_from has not
-- passed, so at most one forced rotation runs per publish lead (decision
-- 13 of change 0177). Every row written before this migration was written
-- by a scheduled rotation or as a first key, so the default false is its
-- value.
--
-- spec: §7.1 (uploadToken format and security properties), §12.3
-- (Platform signing keys).
ALTER TABLE platform_signing_keys
    ADD COLUMN forced BOOLEAN NOT NULL DEFAULT false;
