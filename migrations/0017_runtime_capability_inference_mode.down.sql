-- Reverses 0017_runtime_capability_inference_mode.
ALTER TABLE runtime_definitions
    DROP COLUMN IF EXISTS capability_inference_mode;
