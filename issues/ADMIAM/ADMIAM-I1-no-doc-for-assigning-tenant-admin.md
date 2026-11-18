---
id: ADMIAM-I1
status: open
kind: doc-gap
severity: medium
requirements: []
---

# No documentation page shows how to give a user a tenant role

## Specification

§15.1 defines `PUT /v1/admin/tenants/{id}/users/{userId}/role` with a `role` body, and the bootstrap seed format's `roles` list. §10.2 defines the `tenant-admin` role.

## Evidence

- `docs/operator-guide/multi-tenancy.md` describes what a `tenant-admin` may do but shows no command or request that grants the role. No page under `docs/` shows the bootstrap seed format's `users` entry with `roles`.
- The 0157 pilot journey `tenant-admin-onboarding` passed in one trial and failed in two (runs `20261118T165838Z-1ae96b` and `20261118T185934Z-0a42f2`). In the failing trials the agent guessed the seed format and wrote `role` for `roles` (see ADMIN-I1).

## Gap

An operator onboarding a tenant has to find the role assignment in the API reference or guess it.

## Suggested resolution

Add a task to the multi-tenancy guide that creates a tenant, grants a user `tenant-admin` through `PUT /v1/admin/tenants/{id}/users/{userId}/role` (and the CLI command CLIADM-I1 adds), and reads the role back, with the seed-file form beside it.
