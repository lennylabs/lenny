-- The §5.3 tenant isolation floor.
--
-- min_isolation_profile is the weakest §5.3 isolation profile
-- (standard, sandboxed, microvm) the tenant's sessions may run at. An
-- empty value means the tenant has set no floor and the platform
-- default applies. The §10.7 experiment-admission advisory check
-- compares each variant pool against this floor.

ALTER TABLE tenants
    ADD COLUMN min_isolation_profile TEXT NOT NULL DEFAULT '';
