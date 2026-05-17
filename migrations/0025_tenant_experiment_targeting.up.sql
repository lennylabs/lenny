-- The §10.7 tenant experimentTargeting block.
--
-- experiment_targeting holds the tenant's OpenFeature external-targeting
-- configuration (provider, hot-path timeout, and the provider-specific
-- sub-block) as a JSON object, modeled on experiment.TargetingConfig.
-- The empty object {} is the default: the tenant configures no external
-- targeting and only percentage-mode experiments route.

ALTER TABLE tenants
    ADD COLUMN experiment_targeting JSONB NOT NULL DEFAULT '{}'::jsonb;
