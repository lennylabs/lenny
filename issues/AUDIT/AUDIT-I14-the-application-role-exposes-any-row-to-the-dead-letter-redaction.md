---
id: AUDIT-I14
status: planned
kind: question
severity: low
requirements: []
change: 0180.S2
---

# The application role decides which rows are dead-lettered, so it can expose any row that names an erased user to the step-14 redaction

## Specification

§12.8 (Compliance Interfaces), step 14: the erasure job redacts every `audit_log` row "whose `ocsf_translation_state = 'dead_lettered'` and whose `payload` names the target user"; ordinary rows "retain their structured actor/subject identifiers under the §11.7 tamper-evidence and legal-obligation basis" (step 13).

## Evidence

- `migrations/0041_audit_state_machine_columns.up.sql` grants `lenny_app` `UPDATE (ocsf_translation_state, eventbus_publish_state, retry_count)`, and `lenny_audit_immutability()` admits any change of those columns, including from `succeeded` to `dead_lettered`.
- `DeadLetteredForUser` in `pkg/audit/auditstore/redaction.go` selects by that column, and the redaction it feeds carries a valid receipt, which every verifier accepts as `redacted_gdpr`.
- The model of change 0180 (`tests/formal/auditchain/`, configuration `app`): the application role sets row 1, which the translator did not dead-letter, to `dead_lettered`; an erasure job then redacts it with a signed receipt (`RedactedOnlyTranslatorDeadLetters` fails). Every other tampering of the role is detected or is a linked append (`TamperDetected` holds over 82,812 states).
- Found while building step 0180.S1 on 2026-11-27.

## Gap

A compromised application role, which otherwise only appends, causes the erasure of the content of an existing ordinary row through the legitimate redaction path, and the result verifies as a lawful redaction. The SIEM copy of the row keeps the original.

## Suggested resolution

Change 0180, open decision O7.
