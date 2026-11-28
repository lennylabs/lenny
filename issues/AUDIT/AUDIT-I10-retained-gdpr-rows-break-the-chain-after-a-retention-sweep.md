---
id: AUDIT-I10
status: planned
kind: spec-defect
severity: high
requirements: []
change: 0180.S2
---

# The retention sweep keeps gdpr.* rows among deleted rows, and every chain verifier then reports the tenant's chain broken

## Specification

§16.4 (Logging), **GDPR erasure receipt rows**: the audit GC "will not delete `gdpr.*` rows until they exceed this window regardless of the general `audit.retentionDays` setting." §11.7 (Audit Logging), item 3, **Authoritative total order**: "only a sequence-number gap accompanied by a non-linking `prev_hash` is a broken chain." The specification states how the verifiers treat the `gdpr.*` remnant of a deleted tenant (§12.8, **`gdpr.*` audit event exemption from user-level deletion.**), and does not state how they treat the rows a retention sweep removes from a live tenant's chain.

## Evidence

- `PruneRetention` in `pkg/audit/auditstore/auditstore.go` deletes the rows that are not `gdpr.*` and are older than `audit.retentionDays`, and keeps older `gdpr.*` rows.
- `verifyChainWindow` in `pkg/audit/integrity/continuity.go` checks each row of the newest `audit.startupChainCheckEntries` rows (default 1000) against the row before it in sequence order. `PeriodicCheck.CheckOnce` and the startup check run it; a broken result fires `AuditChainGap`, and with `audit.hardFailOnDrift` the periodic check shuts the gateway down.
- `Chain.VerifyRows` and `Chain.Verify` in `pkg/audit/chain.go`, which the audit query API and `Store.Verify` run over every row, check the same links.
- Every step-14 redaction appends `gdpr.erasure_deadletter_redacted` and `gdpr.erasure_deadletter_downstream_notified`, so a tenant that had an erasure has `gdpr.*` rows between ordinary rows.
- The model of change 0180 (`tests/formal/auditchain/`, configuration `retention`): rows 1 and 3 are ordinary, row 2 is a `gdpr.*` row, and a replica appends row 4 after the clock passes the cutoff. The sweep deletes rows 1 and 3 and keeps rows 2 and 4. Row 4's `prev_hash` is the hash of the deleted row 3, so the window check reports the chain broken (`NoWindowFalseAlarm` fails). With no `gdpr.*` row (`retention_nogdpr`) the window check holds.
- Found while building step 0180.S1 on 2026-11-27.

## Gap

Once `audit.retentionDays` passes the first erasure of a tenant whose newest 1000 rows reach back to a retained `gdpr.*` row, every periodic check reports the chain broken and fires `AuditChainGap`, and a gateway with `audit.hardFailOnDrift` shuts down. The query API reports the first row after each hole `broken`.

## Suggested resolution

Change 0180, open decision O4.
