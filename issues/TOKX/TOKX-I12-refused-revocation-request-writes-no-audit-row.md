---
id: TOKX-I12
status: open
kind: spec-defect
severity: low
requirements: []
---

# A refused recursive-revocation request writes no audit row

## Specification

§13.3 (Credential Flow) **Audit coverage.**: every token exchange, accepted or rejected, emits a `token.exchanged` audit event. §13.3 **Recursive-revocation request authorization.**, as change 0164 states it: the Token Service refuses a revocation request from a caller that is neither the token's own user nor a tenant administrator, and one whose caller token is revoked. §16.7 (Section 25 Audit Events) lists the `exchange_type` values of `token.exchanged` (`admin_rotation`, `delegation_mint`, `lease_issue`, `scope_narrow`), none of which names a revocation request.

## Evidence

- `pkg/tokenservice/tokenservice.go`, `handleRevocationRequest`: the cross-tenant refusal, the `403 unauthorized_client` refusal, and the `401 invalid_client` refusal of a revoked caller each call `finish`, which records metrics only, and write no audit row.
- The mint path writes a rejected `token.exchanged` row for every refusal (`validateExchange`, `rejectRevokedSource`).
- The security review of 0164.S1 on 2026-11-20 found the gap (finding 3). The cross-tenant refusal predates 0164, and the two refusals 0164 adds are new instances.

## Gap

A replayed leaked token, or a user's attempt to revoke another user's token, leaves no trace in the audit log, so a SIEM rule over the audit log does not see it.

## Suggested resolution

Choose the event a refused revocation request writes: a rejected `token.exchanged` row, which needs an `exchange_type` value for a revocation request in §16.7, or a rejected `token.revoked` row or a new event. Write it under the rejected-exchange discipline of §13.3 **Write-before-issue ordering.**, failing closed on an audit write failure. The event needs the owner.
