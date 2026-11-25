---
id: TOKX-I16
status: open
kind: spec-defect
severity: low
requirements: []
---

# The impersonation bearer always carries `caller_type: human`

## Specification

§13.3 (Credential Flow), "Platform-admin impersonation." rule 1 (b): the gateway signs a `user_bearer` for the target user in the target tenant, with the session id as `jti`, the caller's `scope` claim, and the bounded `exp`. The rule does not state the bearer's `caller_type`. "Lenny JWT claim structure" says an exchange copies `caller_type` from the `subject_token` and cannot elevate it, and the impersonation mint has no subject token.

## Evidence

- `pkg/credential/impersonation/service.go`, `sign`: the bearer's claims set `CallerType: "human"` whatever the target user's caller type, so impersonating a `service` or `agent` principal yields a `human` bearer.
- The `caller_type` claim feeds the different-caller rule of "Client authentication on `/v1/oauth/token`." and the agent-operability surfaces of §25.1, which treat `human`, `service`, and `agent` callers differently.
- Gap G7 of change 0165 (a formal model of the token rules), on 2026-11-20.

## Gap

The bearer can carry a `caller_type` the impersonated user does not have, and the specification does not say which `caller_type` it carries.

## Suggested resolution

State the impersonation bearer's `caller_type` in rule 1 (the target user's recorded caller type, or `human` with the reason), and set it in `sign` accordingly. Decided by the owner (change 0167 decision 11 files it without a fix).
