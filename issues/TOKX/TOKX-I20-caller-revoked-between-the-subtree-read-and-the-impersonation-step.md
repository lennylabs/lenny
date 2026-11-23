---
id: TOKX-I20
status: open
kind: build-gap
severity: low
requirements: []
---

# A caller revoked between the subtree read and the impersonation step still ends impersonation sessions

## Specification

§13.3 (Credential Flow) "Platform-admin impersonation." rule 7: the Token Service first reads, in the request's tenant, the `jti` of the root and of each lineage descendant, and refuses the request with `401 invalid_client` when the caller token's row is revoked; it then revokes the impersonation rows the link names, in its own transaction. **Recursive-revocation request authorization.** reads the caller's row under a share lock in the transaction that revokes, so a revocation of the caller that commits first refuses the request.

## Evidence

- `pkg/tokenservice/tokenservice.go`, `revokeImpersonationsFrom`: `LineageSubtree` (which reads the caller under `FOR SHARE`) and `RevokeImpersonationsByImpersonator` run in two transactions, so the share lock is released before the impersonation update runs.
- A revocation of the caller token that commits between the two transactions does not refuse the impersonation step, which ends the sessions; the cascade then refuses the request as a revoked caller.
- Found by the security review of 0165.S2 on 2026-11-23 (finding 3). The property tests of 0165.S2 apply operations one at a time and do not reach it; [TOKX-I13](TOKX-I13-derivation-committed-during-a-cascade-escapes-it.md) tracks the other windows of the revocation request.

## Gap

A caller whose token is revoked by a concurrent request can still end the impersonation sessions the subject's lineage started.

## Suggested resolution

Re-read the caller's row under `FOR SHARE` inside the transaction of `RevokeImpersonationsByImpersonator`, refusing a revoked caller there, or run the subtree read and the impersonation update in one transaction under `__all__`. Settle it together with change 0165 open decision 15, whose recommended option changes how the cascade reads the caller.
