---
id: APICONV-I12
status: closed
kind: spec-defect
severity: low
requirements: []
closed: obsolete
closed-by: 0178
---

# IdP tokens that name the client in neither azp nor client_id share a rotation chain across client applications

## Specification

§11.5 Idempotency, **Record scope.**: the chain of an `idp` token is its `iss` with its `azp` claim; when it has none, its `iss` with its `client_id` claim (RFC 9068); when it has neither, its `iss` with its `aud` values in sorted order.

## Evidence

- `idpChain` in `pkg/auth/jwt/chain.go` implements the three fallbacks.
- The security review of steps 0178.S3 and 0178.S4 reports that some identity providers name the client in another claim, citing `cid` in Okta access tokens and `appid` in Entra ID v1 tokens; this was not checked against the providers' documentation.
- Such a token falls to the audience fallback, so every client application of one subject whose tokens carry the same audiences shares one chain.

## Gap

Another client application of the user that knows the key and the body receives the user's stored response, including a create response's restored `uploadToken`. §11.5 states the shared scope only for tokens "that name the same client, or that name none and carry the same audiences".

## Suggested resolution

Decision 11 of change 0178 (owner's answer to IP-10, 2026-11-27, option (a)): the operator setting `auth.oidc.clientIdClaim`, default unset, names the claim the gateway reads ahead of `azp` and `client_id`. Step 0178.S3 builds it.

Closed as obsolete on 2026-11-27. Decision 16 of change 0178 (the owner's answer to IP-17, option (a)) withdraws decision 11 and the setting `auth.oidc.clientIdClaim`, and decision 12 withdraws the rotation chain under design C, so no record scope names a client and the gap has no subject. `stored_by_client` records `azp`, else `client_id`, as attribution only.
