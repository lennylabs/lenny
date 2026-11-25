# Decisions: 0173 Credentials in stored idempotency responses

## Open decisions

None.

## Decisions

1. **Credentials in stored idempotency responses.** Settled by the owner on 2026-11-20, question DV-6 of the M3 readiness decision page, option (a), recorded as decision D14 of [0150](../0150-tenant-security-settings/decisions.md#decisions) (open decision O21 there). A response that carries a credential is not stored with it: a critical operation that returns one stores the response with the token removed and mints a fresh token on replay, or calls `idempotencymw.DoNotStore`, and §12.9 classifies idempotency records. D14 placed the work in the step named when the proposed upload-token change was created. That change was never created, so this change carries the work, and 0173.S1 is the step D14 names. The options not taken were (b), encrypting the stored response body under the tenant key, and (c), classifying the records as Secret and relying on volume encryption.
