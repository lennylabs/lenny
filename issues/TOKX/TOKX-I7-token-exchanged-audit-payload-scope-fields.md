---
id: TOKX-I7
status: open
kind: build-gap
severity: low
requirements: []
---

# The `token.exchanged` audit row carries one scope field where the catalog names two

## Specification

§16.7 (Section 25 Audit Events), `token.exchanged`: the payload carries `requested_scope` and `granted_scope`, which may differ from the requested scope when narrowing was applied.

## Evidence

- `pkg/tokenservice/tokenservice.go`, `exchangeAuditPayload`: the payload has a single `scope` field. An accepted exchange writes the issued scope to it (`persistIssuedToken`), and a rejected exchange writes the requested scope (`validateExchange`).
- Since 0163.S1, the granted scope also differs from the requested scope when the request names none, and the row cannot show that the scope was defaulted.
- The general review of 0163.S1 on 2026-11-19 found the drift (finding 3). It predates that step.

## Gap

A SIEM rule that reads `requested_scope` or `granted_scope` finds neither field, and an accepted row does not record what the caller asked for.

## Suggested resolution

Emit `requested_scope` and `granted_scope` in `exchangeAuditPayload` as §16.7 names them, with a test of an accepted exchange without `scope` whose `requested_scope` is empty and whose `granted_scope` is the subject's scope.
