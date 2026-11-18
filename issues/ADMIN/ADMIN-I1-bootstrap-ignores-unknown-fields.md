---
id: ADMIN-I1
status: open
kind: build-gap
severity: high
requirements: []
---

# The admin bootstrap ignores unknown fields and reports success

## Specification

§15.1, the `POST /v1/admin/bootstrap` row and its seed format, defines each seeded user by `subject`, `tenantId`, and `roles`. The section does not state how the endpoint treats a field the format does not define.

## Evidence

- The 0157 pilot run `20261118T185934Z-0a42f2`, journey `tenant-admin-onboarding`, trials 1 and 2: the agent seeded `{"users":[{"tenantId":"globex-<nonce>","subject":"carol@globex.com","role":"tenant-admin"}]}` through `lenny-ctl bootstrap --from-values`. The response reported `users.createdCount: 1` with no error, and `GET /v1/admin/tenants/globex-<nonce>/users` then showed no role for Carol, so the postcondition `carol-is-tenant-admin` failed.
- `pkg/gateway/externalapi/admin/users.go`, `UserPayload`: the field is `roles` (a list). The bootstrap decoder does not refuse the unknown `role` key, so `upsertUsers` (`bootstrap.go`) creates the user with no role.

## Gap

A misspelled or misplaced field in a seed file silently creates a principal without the access the operator meant to grant, and the response claims success.

## Suggested resolution

State in the admin API page, when it lands, that the bootstrap refuses an unknown field with `VALIDATION_ERROR` naming the field, and decode the request with unknown fields refused. Add a test that a user seeded with `role` is refused.
