---
id: TOKX-I10
status: open
kind: build-gap
severity: low
requirements: []
---

# The recursive revocation has no bound on the depth of a chain or on the duration of a cascade

## Specification

§13.3 (Credential Flow) **Lineage of derived tokens.**, as change 0164 states it: every token an exchange issues records its parent, and the recursive revocation walks `parent_jti`. §12.2 (Storage Roles), `TokenIssuanceStore`: the partial index `(tenant_id, parent_jti)` keys each step of the walk. Neither section bounds the depth of a chain or the time a cascade may take.

## Evidence

- `pkg/storage/issuedtokenstore/issuedtokenstore.go`, `RevokeCascade`: a recursive CTE walks the whole subtree in one transaction with no `statement_timeout`, and it walks already-revoked nodes, because the `revoked_at IS NULL` filter applies only in the `UPDATE`.
- A caller may exchange one of its tokens, presented as the subject, while it authenticates with another of its tokens, at an equal scope, so each exchange adds one level to a chain. A self-rotation records the token it replaces as its parent (decision 10 of 0164) and adds one level as well. At a per-caller limit of 300 requests a minute, a chain grows by about 18,000 levels an hour, for as long as the tokens in it stay valid.
- The security review of 0164.S1 on 2026-11-20 found the cost (finding 3). 0164 records a parent for every exchange, which makes such chains possible, and adds the index, which removes the per-level scan of the tenant's rows.

## Gap

A tenant user builds a deep chain and repeats revocation requests over it, each one a long Postgres transaction that walks the whole chain.

## Suggested resolution

Choose a bound: refuse an exchange whose subject is more than a set number of links deep, stop the walk at nodes an earlier cascade revoked, or set a statement timeout on `RevokeCascade` with an operator-tunable default. The bound and its default need the owner.
