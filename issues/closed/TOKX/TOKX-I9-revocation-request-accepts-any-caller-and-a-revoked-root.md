---
id: TOKX-I9
status: closed
kind: spec-defect
severity: medium
requirements: []
change: 0164.S1
closed: fixed
closed-by: 0164.S1
---

# The recursive-revocation request accepts any caller in the tenant and a revoked root

## Specification

§13.3 (Credential Flow), the paragraph on `a2a_delegation` child tokens: a token exchange with `requested_token_type=urn:ietf:params:oauth:token-type:access_token:revoked` triggers recursive revocation. §13.3 **Lineage of derived tokens.**, as change 0164 states it: every derived token records its parent, and a rotation records the token it replaces, so revoking a token that a rotation replaced also revokes its successors. The section does not state who may send a revocation request, nor whether its root may already be revoked.

## Evidence

- `pkg/tokenservice/tokenservice.go`, `handleRevocationRequest`: the only check on the caller is `caller.TenantID != subject.TenantID`.
- `pkg/tokenservice/tokenservice.go`, `authenticateCaller` and `verifyExchangeTokens`: the caller token and the subject token are verified against the key set alone, so a revoked token is accepted as either.
- `pkg/storage/issuedtokenstore/issuedtokenstore.go`, `RevokeCascade`: "an already-revoked root still cascades to any not-yet-revoked descendant".
- The security review of 0164.S1 on 2026-11-20 found the path (finding 1). The acceptance of any caller and of a revoked root predates 0164; the rotation edge 0164 records extends what such a request reaches.

## Gap

A holder of a token that its owner rotated away from, for example after a leak, sends it as both caller and subject of a revocation request and revokes the owner's current token and every token derived from it. Before 0164 the same request revoked only tokens derived from the leaked token by an `actor_token` child mint.

## Suggested resolution

The owner's answer to open decision O1 of change 0164 settles whether a rotation records its predecessor as parent. Independently of it, choose whether a revocation request requires the caller's `sub` to equal the subject's (or an administrator role), and whether it refuses a revoked caller token or a root whose row is already revoked.
