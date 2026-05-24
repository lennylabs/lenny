-- Reverses 0069_runtime_recursion_classes_providers.
ALTER TABLE runtime_definitions
    DROP COLUMN IF EXISTS allow_self_recursion,
    DROP COLUMN IF EXISTS allowed_resource_classes,
    DROP COLUMN IF EXISTS supported_providers;
