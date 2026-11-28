---
id: AUTHN-I3
status: open
kind: question
severity: low
requirements: []
---

# Change 0138's client identity records no client for an identity provider that names the client in another claim

## Specification

§10.2 (Authentication) normalizes an identity provider's token to a `user_bearer` and names no claim that carries the client application. Change [0138](../../changes/0138-rest-only-client-api/change.md), a member of the v1 program in `Draft`, names the client of a request by the token's `azp` or `client_id`: its webhook subscriptions filter by `?clientId=`, and its audit record names that client.

## Evidence

- 0138 `change.md`: each webhook subscription records the creating token's `azp`, and the owner lists and deletes subscriptions by that client with `?clientId=` (R6); the audit record of a narrowed request carries the token's `azp` or `client_id` ("Audit [design]"); R5 states that the token's `azp` or `client_id` enters the idempotency scope through the chain of an IdP token.
- RFC 9068 section 2.2 requires `client_id` in a JWT access token, and OpenID Connect Core 1.0 section 2 defines `azp`; an identity provider that issues tokens in neither profile can name the client in a claim of its own.
- Decision 11 of change [0178](../../changes/0178-idempotency-keys-by-principal/decisions.md) added the operator setting `auth.oidc.clientIdClaim` for the idempotency chain, and 0178's open decision O12 asks whether 0138 reads it. Open decision O17 of 0178 asks whether decision 11 is withdrawn, and O18 moves the question to 0138 if it is.
- Found by a design review of change 0178 on 2026-11-27.

## Gap

With an identity provider that names the client in another claim, 0138's webhook filter matches no subscription by client, and its audit record names no client, whatever change 0178 decides about the idempotency scope.

## Suggested resolution

Raise an open decision of change 0138 when its client identity is built, with options such as a client-claim setting of 0138's own, or `azp` and `client_id` only with the limit documented in the operator guide. Open decision O18 of change 0178 states where the question goes.

The owner answered O18 of change 0178 with option (a) on 2026-11-27 (0178 decision 17, IP-18): 0178's open decision O12 closes with no answer, and this issue is raised as an open decision of change 0138 when its client identity is built. "Open decisions" in 0138's records points to it.
