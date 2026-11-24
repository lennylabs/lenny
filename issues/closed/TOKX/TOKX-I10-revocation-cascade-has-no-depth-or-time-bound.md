---
id: TOKX-I10
status: closed
kind: build-gap
severity: low
requirements: []
closed: fixed
closed-by: 0168.S1
---

# The recursive revocation has no bound on the depth of a chain or on the duration of a cascade

## Specification

§13.3 (Credential Flow) **Lineage of derived tokens.**, as change 0164 states it: every token an exchange issues records its parent, and the recursive revocation walks `parent_jti`. §12.2 (Storage Roles), `TokenIssuanceStore`: the partial index `(tenant_id, parent_jti)` keys each step of the walk. Neither section bounds the depth of a chain or the time a cascade may take.

## Evidence

- `pkg/storage/issuedtokenstore/issuedtokenstore.go`, `RevokeCascade`: a recursive CTE walks the whole subtree in one transaction with no `statement_timeout`, and it walks already-revoked nodes, because the `revoked_at IS NULL` filter applies only in the `UPDATE`.
- A caller may exchange one of its tokens, presented as the subject, while it authenticates with another of its tokens, at an equal scope, so each exchange adds one level to a chain. A self-rotation records the token it replaces as its parent (decision 10 of 0164) and adds one level as well. At a per-caller limit of 300 requests a minute, a chain grows by about 18,000 levels an hour, for as long as the tokens in it stay valid.
- A rotation of a token that a running cascade has already locked waits for the cascade to commit while it holds the §11.7 per-tenant lock: `lockAndRecheck` takes the lock, its re-read is a plain `SELECT` that sees the token live, and `revokePrevious` then blocks on the cascade's row lock. A tenant user who starts a revocation over a deep chain and rotates one of the chain's tokens stalls every lock-taking issuance and audit write of the tenant until the cascade commits (security review of 0164.S1, revision 4, finding 3).
- The security review of 0164.S1 on 2026-11-20 found the cost (finding 3). 0164 records a parent for every exchange, which makes such chains possible, and adds the index, which removes the per-level scan of the tenant's rows.

## Gap

A tenant user builds a deep chain and repeats revocation requests over it, each one a long Postgres transaction that walks the whole chain.

## Suggested resolution

Choose a bound: refuse an exchange whose subject is more than a set number of links deep, stop the walk at nodes an earlier cascade revoked, or set a statement timeout on `RevokeCascade` with an operator-tunable default. The bound and its default need the owner.

## Resolution

Change 0168 (decision 12) closes it in step 0168.S1. Each `issued_tokens` row records its `depth` (migration 0203), step (1a) refuses an exchange whose parent row is at `auth.tokenLineageMaxDepth` (default 32) with `lineage_depth_exceeded`, and the cascade and the full revoke's walk run under the statement timeout `auth.tokenRevocationTimeoutMs` (default 5000 ms): a cascade that reaches it revokes nothing and answers the retryable `503 REVOCATION_TIMEOUT`, and a full revoke reports it as partial-failure detail. A wide subtree is bounded by the timeout only.
