-- Reverse 0207: drop the §7.1 forced-rotation mark. A row loses only the
-- record of which kind of rotation wrote it.
ALTER TABLE platform_signing_keys
    DROP COLUMN IF EXISTS forced;
