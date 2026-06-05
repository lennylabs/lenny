-- §15.1 "GET /v1/admin/tenants/{id}/users" and "PUT|DELETE
-- /v1/admin/tenants/{id}/users/{user_id}/role": the platform-managed
-- role assignment for a user within a tenant. The assignment takes
-- precedence over the user's OIDC-derived role claim (§10.2 line 294);
-- removing it (the DELETE) reverts the user to the OIDC claim while
-- retaining the row so the user still lists.
--
-- role_assigned records whether an assignment is present and controls
-- the OIDC override. It defaults to TRUE so any pre-existing user row
-- keeps the legacy "row exists -> Roles override OIDC" behavior; the
-- DELETE path sets it FALSE explicitly. role_assigned_by /
-- role_assigned_at carry the §15.1 line 826 assignment provenance
-- (operator subject and timestamp) surfaced by the list endpoint.
-- See spec/15_external-api-surface.md §15.1 lines 826-828.
ALTER TABLE users
    ADD COLUMN IF NOT EXISTS role_assigned    BOOLEAN     NOT NULL DEFAULT TRUE,
    ADD COLUMN IF NOT EXISTS role_assigned_by TEXT        NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS role_assigned_at TIMESTAMPTZ;
