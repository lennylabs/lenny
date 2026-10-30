-- The §5.1 capabilityInferenceMode field on a runtime.
--
-- capability_inference_mode sets the default §5.3 capability for an
-- unannotated tool at connector or type:mcp runtime registration. §5.1
-- defines the field as strict | permissive with strict the default, so
-- existing rows take strict.

ALTER TABLE runtime_definitions
    ADD COLUMN capability_inference_mode TEXT NOT NULL DEFAULT 'strict';
