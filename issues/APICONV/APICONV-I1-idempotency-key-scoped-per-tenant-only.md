---
id: APICONV-I1
status: open
kind: spec-defect
severity: low
requirements: []
---

# Idempotency keys are scoped per tenant, so a caller can replay another caller's cached response

## Specification

§11.5 (Idempotency), **Mechanism.**: "The gateway stores the key, request hash, and response in Postgres (table `idempotency_keys`, keyed by `(tenant_id, idempotency_key)`) with a TTL of 24 hours." The section does not name the caller in the key.

## Evidence

- `pkg/idempotency/idempotency.go`: `Key` holds `TenantID` and `Value`, and its comment cites the §11.5 tenant scope.
- The security review of 0161.S1 on 2026-11-19 found the gap (finding F5). It predates that step.

## Gap

A caller in the same tenant who knows another caller's idempotency key and request body receives the cached response of that caller's create, which names a session the second caller does not own.

## Suggested resolution

Decide in §11.5 whether the key includes the caller's subject, so a repeated key from another principal is treated as a new key or refused, and change the key and its store accordingly, with a test that a second principal does not receive the first principal's response.
