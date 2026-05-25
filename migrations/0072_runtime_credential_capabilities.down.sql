-- Reverses 0072_runtime_credential_capabilities.
ALTER TABLE runtime_definitions
    DROP COLUMN IF EXISTS credential_capabilities;
