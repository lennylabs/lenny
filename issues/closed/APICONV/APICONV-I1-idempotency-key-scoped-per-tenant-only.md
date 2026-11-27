---
id: APICONV-I1
status: closed
kind: spec-defect
severity: medium
requirements: []
change: 0178.S1
closed: fixed
closed-by: 0178.S1
---

# Idempotency keys are scoped per tenant, so a caller can replay another caller's cached response

## Specification

§11.5 (Idempotency), **Mechanism.**: "The gateway stores the key, request hash, and response in Postgres (table `idempotency_keys`, keyed by `(tenant_id, idempotency_key)`) with a TTL of 24 hours." The section does not name the caller in the key.

## Evidence

- `pkg/idempotency/idempotency.go`: `Key` holds `TenantID` and `Value`, and its comment cites the §11.5 tenant scope.
- The security review of 0161.S1 on 2026-11-19 found the gap (finding F5). It predates that step.
- Since archived change [0173](../../changes/archive/2026-11-25-0173-idempotency-stored-credentials/change.md), a replay restores the `uploadToken` of a stored session-create response from its redaction reference, so the response that another caller of the tenant receives can carry a working upload token for the first caller's session. The review of 0173.S1 recorded this (`changes/archive/2026-11-25-0173-idempotency-stored-credentials/digests/0173.S1.md`, "Pre-existing, for the owner"), and the severity was raised from low to medium for that reason on 2026-11-25.

## Gap

A caller in the same tenant who knows another caller's idempotency key and request body receives the cached response of that caller's create, which names a session the second caller does not own.

## Suggested resolution

Decide in §11.5 whether the key includes the caller's subject, so a repeated key from another principal is treated as a new key or refused, and change the key and its store accordingly, with a test that a second principal does not receive the first principal's response. The owner settled the scope on 2026-11-25: keys are scoped by the tenant and the subject, and change [0178](../../changes/0178-idempotency-keys-by-principal/change.md) step 0178.S1 makes the change.
