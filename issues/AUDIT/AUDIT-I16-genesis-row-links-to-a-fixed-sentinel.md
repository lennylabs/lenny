---
id: AUDIT-I16
status: open
kind: build-gap
severity: low
requirements: []
---

# The first row of a tenant's chain links to a fixed all-zero sentinel rather than to the per-tenant genesis hash

## Specification

§11.7 (Audit Logging), item 3: "The first entry in each tenant partition uses a **per-tenant genesis hash** derived from a random nonce generated at tenant creation time: `genesis_hash = SHA-256("lenny-audit-genesis" || tenant_id || genesis_nonce)`." The same paragraph gives the reason: "a well-known fixed genesis hash would allow undetectable chain reconstruction by a database superuser."

## Evidence

- `pkg/audit/chain.go`: `GenesisPrevHash` is 64 zeros, and `sealAndInsert` in `pkg/audit/auditstore/auditstore.go` stamps it on the first row of every tenant's chain.
- `Chain.Verify`, `Chain.VerifyRows`, and `verifyChainWindow` in `pkg/audit/integrity/continuity.go` compare the first row's `prev_hash` with `GenesisPrevHash`.
- `pkg/tenancy/tenantstore/pgstore/pgstore.go` generates and stores `tenants.genesis_nonce`, and no audit code reads it to derive a genesis hash.
- Found while building step 0180.S1 on 2026-11-27, reading the code the model maps. The model treats the genesis link as a constant and does not depend on its value.

## Gap

Every tenant's chain starts from the same well-known value, which is the construction §11.7 rejects. A party that can rewrite `audit_log` rebuilds a tenant's chain from its first row without the tenant's nonce. §11.7 **Audit integrity gap — Postgres-only deployments** already accepts that a superuser who can read the nonce rebuilds the chain, so the gap matters for a writer of the audit instance who cannot read the tenant record, as under the separate billing and audit instance of §12.3.

## Suggested resolution

Derive the genesis link from the tenant's `genesis_nonce` in the append path and the verifiers, and read the nonce from the control-plane database where the verifier runs, or change §11.7 to the fixed sentinel through a change if the nonce is not worth its read. The `platform` pseudo-tenant has no tenant row and needs a stated nonce source either way.
