-- Reverse 0135_platform_registry_config: drop the §25.8 runtime registry
-- override singleton. The chart-rendered platform.registry.* Helm values
-- remain the configuration source after this table is removed.

DROP TABLE IF EXISTS platform_registry_config;
