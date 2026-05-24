-- §5.1 runtime registry fields previously dropped at the gateway boundary.
--
-- allow_self_recursion is the §5.1 / §8.2 runtime-layer cycle-detection
-- opt-in (the LayerRuntime input to the three-layer AND gate). FALSE
-- means this runtime rejects every self-recursive delegation hop.
--
-- allowed_resource_classes is the §5.1 set of resource classes the
-- runtime permits (Prohibited on derived runtimes; pool config
-- constrains further). NULL when the runtime declares no class set.
--
-- supported_providers is the §5.1 set of credential providers the
-- runtime's SDK supports (Override; a derived runtime may restrict but
-- not expand beyond its base). NULL when the runtime declares none.

ALTER TABLE runtime_definitions
    ADD COLUMN allow_self_recursion BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN allowed_resource_classes JSONB,
    ADD COLUMN supported_providers JSONB;
