---
id: TOKX-I5
status: closed
kind: build-gap
severity: low
requirements: []
change: 0164.S1
closed: fixed
closed-by: 0164.S1
---

# The token exchange checks the caller's tenant but not its identity

## Specification

§13.3 (Credential Flow), **Client authentication on `/v1/oauth/token`.**: the caller's identity (`sub`, `tenant_id`, `caller_type`) is extracted from the bearer token and must be the same as, or a superset of, the `subject_token`'s identity; a caller cannot mint a token on behalf of a different user unless it presents that user's token as `subject_token` and narrows scope from it.

## Evidence

- `pkg/tokenexchange/tokenexchange.go`, `Validate`: the caller is compared with the subject on `tenant_id` only. No check compares `sub` or `caller_type`, and none requires a narrower scope when they differ.
- `pkg/tokenservice/tokenservice.go`, `issuedRecord` and `delegationLineage`: an exchange without an `actor_token` records no `parent_jti`, so a recursive revocation of the subject does not reach a token derived from it by scope narrowing or by an exchange from another caller.
- Since 0163.S1, an exchange without `scope` issues the subject's scope, so a caller in the same tenant that holds another user's token obtains a copy with a new `jti` and the same scope. Before 0163.S1 the copy carried no scope restriction.
- The security review of 0163.S1 on 2026-11-19 found the gap (finding 3). It predates that step.

## Gap

A caller that obtains another user's token in the same tenant can exchange it for a fresh token of equal scope, and revoking the original does not revoke the copy before its `exp`, which is bounded by the original's.

## Suggested resolution

Enforce the caller-identity rule in `Validate`: when the caller's `sub` differs from the subject's, require a requested scope strictly narrower than the subject's, or refuse the exchange. Whether derived tokens record the subject as their parent, so that a cascade reaches them, is a separate choice for the owner.
