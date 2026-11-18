---
id: CHAN-I5
status: closed
kind: build-gap
severity: medium
requirements: []
closed: obsolete
closed-by: 0139
---

# The compliance suite does not assert the runtime-operations events schema

## Specification

§28.7 (Wire-contract artifact register), the `schemas/runtime-ops-events.schema.json` row: whether the schema-driven external-adapter compliance suite asserts against it is stated with that suite (§24.8).

## Evidence

- Claim register row (`tests/claim-map.json`, retired by 0116.S5): claim "Runtime-operations events schema asserted by the external-adapter compliance suite", status `ABSENT`, anchor `#287-wire-contract-artifact-register`, deferral `R8`.
- Surface recorded on the row: "cmd/lenny-compliance/schemavalidate.go".
- Note recorded on the row: "the suite compiles two schema files and reads no third, while spec/24, the adapter-contract reference, and the publishing guide all name schemas/runtime-ops-events.schema.json as an artifact it asserts against".

## Gap

The documentation names a schema the suite does not read.

## Suggested resolution

The row deferred the work to step R8, "Reciprocal host-conformance battery", of `gateway-runtime-comms-remediation.md`.

Closed at filing: 0139 removes the `schemas/runtime-ops-events.schema.json` row of §28.7 and folds the schema into `lenny-adapter-jsonl.schema.json` under decision O7 (a). 0138 ("RO-6 Conformance") also removes the external-adapter validation endpoint and §24.8.
