-- Test-only fixture table for the §10.5 expand-contract migration
-- journey (tests/tier4_integration/schema_migration_journey_test.go).
-- It carries no product dependency: a minimal "widgets" catalog that
-- mirrors the shape of a real tenant-scoped table (an old, integer
-- pricing column) so the fixture migrations below can drive a genuine
-- Phase 1 (expand) -> Phase 3 (contract, gated) sequence without
-- touching the production migrations/ set or its schema_migrations
-- tracking row.
CREATE TABLE widgets (
    id          BIGSERIAL PRIMARY KEY,
    name        TEXT NOT NULL,
    price_cents INTEGER NOT NULL
);
