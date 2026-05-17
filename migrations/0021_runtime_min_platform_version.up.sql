-- The §5.1 minPlatformVersion field on a runtime.
--
-- min_platform_version is the lowest Lenny gateway version the runtime
-- supports. §5.1 has the gateway reject runtime registration when its
-- own version is below this. An empty string declares no minimum.

ALTER TABLE runtime_definitions
    ADD COLUMN min_platform_version TEXT NOT NULL DEFAULT '';
