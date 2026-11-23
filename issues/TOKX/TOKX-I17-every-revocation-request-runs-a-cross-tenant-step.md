---
id: TOKX-I17
status: open
kind: spec-defect
severity: low
requirements: []
---

# Every recursive-revocation request runs a cross-tenant update and writes a `cross_tenant_read` event

## Specification

§13.3 (Credential Flow), "Platform-admin impersonation." rule 7: the Token Service's revocation of the impersonation rows a recursive-revocation request reaches through `impersonator_jti` runs under `__all__` before the request's cascade, needs no `platform-admin` caller, and emits one `cross_tenant_read` event. **Recursive-revocation request authorization.** accepts the request from the token's own user, and a `subject_token` whose row is already revoked is accepted.

## Evidence

- `pkg/tokenservice/tokenservice.go`, `revokeImpersonationsFrom`: every request served by a durable store reads the subtree, runs `RevokeImpersonationsByImpersonator`, and emits a `cross_tenant_read` event, whether or not any token of the subtree started an impersonation.
- `pkg/storage/issuedtokenstore/impersonation.go`, `RevokeImpersonationsByImpersonator`: an `UPDATE` under the `__all__` sentinel keyed on `impersonator_jti = ANY($2)` with the whole subtree, revoked rows included.
- `pkg/audit/auditstore/translation.go`, `EmitCrossTenantRead`: a synchronous append to the shared `platform` audit chain.
- A user can resend the revocation of an already-revoked token of their own as often as the per-caller rate limit of `/v1/oauth/token` allows, and each request repeats the cross-tenant update over the whole historical subtree and one append to the `platform` chain with `row_count` 0. The `cross_tenant_read` events of non-administrator callers then outnumber those of platform-admin reads.
- Found by the security review of 0167.S1 on 2026-11-22 (finding 2). Change 0167 chose to run the step on every request because the cascade's tenant cannot tell which tokens started an impersonation ("Limits, and alternatives rejected" in its `change.md`).

## Gap

The cross-tenant step and its audit write are unbounded by anything but the token endpoint's rate limit, and most `cross_tenant_read` events record no cross-tenant row.

## Suggested resolution

Record, in the impersonator's own tenant, which bearers started an impersonation (a column on the bearer's own row or a small tenant-scoped table that `RecordImpersonation` writes), and run the cross-tenant step only when the subtree contains such a bearer; or skip the step when the root was already revoked and the subtree has not changed. Either changes rule 7's statement that each request emits one event, so the owner decides.
