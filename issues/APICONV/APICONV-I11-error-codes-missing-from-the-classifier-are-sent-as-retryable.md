---
id: APICONV-I11
status: open
kind: build-gap
severity: low
requirements: []
---

# An error code missing from the classifier table is sent to clients as retryable

## Specification

§15.1 error categories and the `retryable` flag.

## Evidence

- `errorclassify.Classify` returns `CategoryTransient` with `retryable: true` for any code missing from its table (`pkg/api/errorclassify/errorclassify.go:44-49`), and `writeError` in the idempotency middleware uses it (`pkg/idempotency/idempotencymw/idempotency.go:755`).
- The Go SDK retries on `Retryable` (`sdks/client/go/lenny/client.go:482`).
- Found by the adversarial review of a per-subject idempotency key with a conflict error, on 2026-11-26.

## Gap

A new permanent error code that is not added to the table makes clients retry a request that cannot succeed.

## Suggested resolution

Add a static check that every error code the gateway writes has a table entry, or classify unknown codes as permanent and non-retryable.
