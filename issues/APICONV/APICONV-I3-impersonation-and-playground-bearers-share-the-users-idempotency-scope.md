---
id: APICONV-I3
status: planned
change: 0178.S3
kind: spec-defect
severity: medium
requirements: []
---

# The impersonation bearer and the playground bearer share the user's idempotency records

## Specification

[§11.5](../../spec/11_policy-and-controls.md#115-idempotency) (Idempotency), **Mechanism.**, scopes an idempotency record by the tenant, the subject, the bound session, and the acting party that the bearer's `act` claim names, each empty when absent. Decision 5 of change [0178](../../changes/0178-idempotency-keys-by-principal/decisions.md) intends a bearer that another party holds to file its records apart from the user's, and leaves open how the platform-admin impersonation bearer and the playground session-capability bearer are scoped apart. [§13.3](../../spec/13_security-model.md#133-credential-flow) **Platform-admin impersonation.** rule 1 (b) lists the claims of the impersonation bearer and names no `act` claim.

## Evidence

- `pkg/credential/impersonation/service.go`, `sign`: the impersonation bearer is a `user_bearer` whose `sub` is the target user, with no `act`, `session_id`, or `origin` claim. The admin is recorded only in the bearer's `issued_tokens` row (`act_sub`).
- `pkg/gateway/playground/token.go`, `completeMint`: the playground bearer is a `session_capability` bearer whose `sub` is the user, with `origin: playground` and no `act` or `session_id` claim.
- `pkg/idempotency/idempotencymw/idempotency.go` and `pkg/gateway/platformtools/mcp/idempotency.go`: the REST middleware and the MCP hook resolve the scope from the principal's tenant, subject, session id, and `act` claim, so both bearers resolve the user's own scope.
- `cmd/lenny-gateway/httpsurface.go`: the idempotency middleware wraps the API front, so a replay returns before the session handler runs `requireCreatingBearerAllowed` (`pkg/auth/callercheck`), which refuses an impersonation bearer. The MCP hook likewise replays before `lenny/delegate_task` calls GWSEAM-CheckCreatingCaller.

Found while building step 0178.S3.

## Gap

An impersonation bearer, or a playground bearer, that sends the user's `Idempotency-Key` and body receives the response stored for the user's own bearer, including the user's session id and, on a session-creating operation, the `uploadToken` the replay signs again. The user receives a response stored for either bearer in the same way. An impersonation bearer obtains a session-creating response although the creating-caller check refuses it a session.

## Suggested resolution

Open decision O4 of change 0178 lists the options: an `act` claim on the impersonation bearer, a lookup of the bearer's `issued_tokens` row, or a stated limit with the creating-caller check run before a replay; and the `origin` claim in the scope for the playground bearer, or a stated limit. The step that builds the owner's answer adds unit, contract, and security tests in which an impersonating admin and a playground bearer that reuse the user's key and body receive none of the user's stored responses.
