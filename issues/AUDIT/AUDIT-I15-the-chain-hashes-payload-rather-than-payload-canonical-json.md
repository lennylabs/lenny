---
id: AUDIT-I15
status: open
kind: build-gap
severity: low
requirements: []
---

# The chain hashes the payload column, and payload_canonical_json, which §11.7 names as the hash input, is written and never read

## Specification

§11.7 (Audit Logging), item 3, **Canonicalization.**: "The `pg_jcs_canonicalize()` PL/pgSQL function (installed by the Phase 13 migration) computes the canonical form at INSERT time and stores it in an immutable generated column; the `prev_hash` trigger reads from that column, not from the raw `payload` jsonb." §12.8 step 14 states that the redaction recomputes `payload_canonical_json` from the redacted payload.

## Evidence

- `migrations/0001_initial_schema.up.sql`: `payload_canonical_json` is an ordinary `JSONB NOT NULL` column that the application fills, not a generated column.
- `migrations/0165_audit_log_redaction_grant.up.sql` grants `lenny_erasure` `UPDATE (payload, payload_canonical_json)`, so the role writes either column alone.
- `scanRow` in `pkg/audit/auditstore/auditstore.go`, `scanChainRows` in `pkg/audit/integrity/continuity.go`, and `scanRedactedRow` in `pkg/audit/integrity/redaction.go` read `payload` and recompute the canonical form with `audit.CanonicalPayload`; no reader selects `payload_canonical_json`. `rowToCanonical` in `pkg/gateway/api/admin/audit_query.go` serves `payload` as the raw-canonical form.
- No `prev_hash` trigger exists (AUDIT-I8).
- Found while building step 0180.S1 on 2026-11-27. The model (`tests/formal/auditchain/`) treats the two columns as one.

## Gap

The column §11.7 names as the hash input is not what any verifier hashes. An `UPDATE` of `payload_canonical_json` alone changes no verdict, and an external verifier that hashes the stored column, as the text tells it to, disagrees with the platform's verifiers after such an update or after any drift between the two columns.

## Suggested resolution

Either make `payload_canonical_json` a generated column of `payload`, so the two cannot disagree, and have the verifiers read it, or change §11.7 through a change to name `payload` canonicalized by the verifier as the hash input and drop the column.
