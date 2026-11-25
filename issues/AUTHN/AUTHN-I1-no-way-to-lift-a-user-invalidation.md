---
id: AUTHN-I1
status: open
kind: spec-defect
severity: medium
requirements: []
---

# The specification defines no way to lift a user invalidation

## Specification

§11.4 (User Invalidation) defines three levels, soft disable, hard disable, and full revoke, and the full-revoke steps, and names no operation that returns an invalidated user to normal. §13.3 (Credential Flow) **Rotation lineage and leaked tokens.**, as change 0164 states it, tells an administrator to fully revoke a user when more than one of the user's tokens may have leaked.

## Evidence

- `pkg/gateway/api/admin/users.go`, the invalidate handler: every mode sets `Disabled`, and `hard_disable` and `full_revoke` also set the `DeletedAt` tombstone. No handler clears either field.
- `docs/api/admin.md` documents `POST /v1/admin/users/{userId}/invalidate` and no call that reverses it. `docs/operator-guide/security.md` states that the admin API has no call that lifts a full revoke.
- Found by the general and security reviews of 0164.S1 (revision 4) on 2026-11-22 (general 2, security 4).

## Gap

An administrator who fully revokes a user to answer a token leak, as §13.3 advises, cannot restore the user's access through the platform.

## Suggested resolution

Define in §11.4 an operation that lifts an invalidation level, with its authorization, audit event, and effect on the user's row, or state that an invalidated user is re-provisioned under a new identity. This needs the owner.
