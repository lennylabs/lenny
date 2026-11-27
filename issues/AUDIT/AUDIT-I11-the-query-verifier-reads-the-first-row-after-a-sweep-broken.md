---
id: AUDIT-I11
status: open
kind: build-gap
severity: medium
requirements: []
---

# The query verifier asserts the genesis sentinel on whatever row comes first, so the first row left by a retention sweep reads broken

## Specification

§11.7 (Audit Logging), item 3, **Authoritative total order**: the audit log "is pruned by the retention sweep", and "A post-sweep non-1 genesis that links to the genesis sentinel is likewise not a break."

## Evidence

- `Chain.VerifyRows` (through `classifyRow`) and `Chain.Verify` in `pkg/audit/chain.go` require the first row they are given to carry `GenesisPrevHash`. `handleListAuditEvents` and `handleGetAuditEvent` in `pkg/gateway/api/admin/audit_query.go` pass every row of the tenant, and `Store.Verify` does the same.
- `PruneRetention` in `pkg/audit/auditstore/auditstore.go` deletes the oldest rows and leaves the first remaining row's `prev_hash` pointing at a deleted row.
- `verifyChainWindow` in `pkg/audit/integrity/continuity.go` asserts the sentinel only for sequence number 1, so the periodic check does not report this.
- The model of change 0180 (`tests/formal/auditchain/`, configurations `retention` and `retention_nogdpr`): after a sweep deletes rows 1 to 3 and keeps row 4, the query verifier reports row 4 `broken` (`NoQueryFalseAlarm` fails) while the window check holds.
- Found while building step 0180.S1 on 2026-11-27.

## Gap

After the first retention sweep of a tenant, the audit query API reports the oldest remaining row `broken`, counts it in `chainIntegrityReport`, and emits the broken-chain signal whenever a page holds that row.

## Suggested resolution

Change 0180, open decision O4: assert the sentinel only when the first row is the chain's first sequence value, as the window check does, and treat the holes of AUDIT-I10 the same way in both verifiers.
