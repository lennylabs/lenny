# Perspective 17: Credential Management — Iter5 Review

## Iter4 Fix Verification

- **CRD-013 (rotationTrigger enum consolidation)** — Fixed. §4.9 line 1410 defines the canonical seven-value enum (`proactive_renewal`, `fault_rate_limited`, `fault_auth_expired`, `fault_provider_unavailable`, `emergency_revocation`, `user_credential_rotated`, `user_credential_revoked`). The enum is referenced consistently from §4.7 line 822 (revocation-triggered ceiling), §4.9.2 line 1728 (`credential.rotation_ceiling_hit` field list), and §16.1 line 55 / §16.5 line 420 (metric and alert). No contradictions remain.
- **CRD-014 (ceiling-hit audit event)** — Fixed. §4.9.2 line 1728 adds `credential.rotation_ceiling_hit` carrying `tenant_id`, `session_id`, `lease_id`, `pool_id`, `credential_id`, `rotation_trigger`, `outstanding_inflight_count`, `elapsed_seconds`. §4.7 line 822 now documents the audit write at the same code point as the counter/alert and flags the event as SIEM-streamable tier-1 compromise signal.

## Findings

### CRD-015 Credential deny-list keying contract broken for user-scoped revocation [High]

**Section:** §4.9 lines 1348 (`POST /v1/credentials/{credential_ref}/revoke`), 1658 (Credential deny list)

§4.9 line 1658 normatively specifies the in-memory credential deny-list structure: "Each entry is keyed by `(poolId, credentialId)` and expires automatically when the last active lease against that credential reaches its natural TTL expiry. The `CredentialPoolStore` persists the `revoked` status durably so newly started gateway replicas rebuild their deny list on startup by querying for credentials in `revoked` state with active-or-recent leases."

§4.9 line 1348 then normatively specifies that user-initiated revocation "terminates [each active lease] immediately — proxy-mode leases via the credential deny list (same propagation as pool revocation)". User-scoped credentials, however, have no `poolId` — they are stored in `TokenStore` (not `CredentialPoolStore`) and are addressed by `credential_ref`. Three concrete correctness failures follow:

1. **No key available to insert.** The revocation handler has no `poolId` to populate in the `(poolId, credentialId)` tuple; any substitution (e.g., a sentinel `null` or the literal string `user`) is not specified and would not match the deny-list lookup path on the proxy request side, which is coded against pool credentials.
2. **LLM Proxy deny-list check does not consult `credential_ref`.** §4.9 line 1645 and line 1484 describe the proxy rejection path for denied credentials but only in terms of pool credentials. A revoked user credential with an in-flight proxy lease will not hit the deny-list short-circuit and the compromised key can continue being used for the remainder of its TTL — directly violating the guarantee in line 1348 that leases are "terminate[d] immediately" on user revocation.
3. **Startup rebuild path does not cover user credentials.** The rebuild query targets `CredentialPoolStore` entries in `revoked` state; `TokenStore` is never queried. A gateway replica restart immediately after a user revocation loses the deny-list entry and the revoked credential silently becomes accepted again on that replica.

This is a correctness gap in the primary security contract of user-initiated revocation (immediate invalidation). The severity is High — not Critical — because direct-delivery-mode user revocations still rotate via `RotateCredentials` RPC (line 1348) and reach the pod regardless of deny-list keying, and because the concrete exploitation requires the operator to have enabled user credentials with proxy delivery; but in proxy mode on a multi-tenant deployment (the recommended default per line 1482) this is the only stop between the user's revoke action and the compromised key reaching Anthropic/Bedrock/Vertex.

**Recommendation:** Extend the deny-list key to a tagged discriminated union: `{source: "pool", poolId, credentialId}` or `{source: "user", tenantId, credentialRef}`. Update §4.9 line 1658 to specify both key shapes, the matching rules the LLM Proxy uses on each inbound request, and the rebuild query: pool credentials from `CredentialPoolStore` WHERE status = 'revoked' UNION ALL user credentials from `TokenStore` WHERE status = 'revoked' AND EXISTS (active lease). Update §4.9 line 1348's "same propagation as pool revocation" phrase to explicitly reference the user-shaped deny-list entry. Add an integration test (`TestUserCredentialRevocationDenyListProxy`) that asserts an in-flight proxy request with a just-revoked user credential is rejected with `CREDENTIAL_REVOKED` before any upstream call.

### CRD-016 `credential.deleted` does not record whether the deleted `credential_ref` still had active leases [Low]

**Section:** §4.9 line 1349 (`DELETE /v1/credentials/{credential_ref}`), §4.9.2 line 1721 (`credential.deleted` event)

`DELETE /v1/credentials/{credential_ref}` explicitly states: "Active session leases are unaffected — they continue using the previously materialized credentials until they expire naturally." Those orphan leases continue rotating and failing against a `credential_ref` that no longer exists in `TokenStore`. The `credential.deleted` audit event (§4.9.2 line 1721) carries only `tenant_id`, `user_id`, `provider`, `credential_ref` — it does not record the `active_leases_at_deletion` count. Forensic questions that cannot be answered from audit alone:

1. "Did the user delete an unused credential, or did they delete a credential with N in-flight leases that kept draining the compromised key?"
2. "Did the user re-register a new credential for the same provider and expect prompt takeover, unaware that sessions already holding leases would keep using the stale key?"

Combined with §4.9 line 1353's "re-registering for the same provider replaces the previous one" (which is silent on whether the new record reuses the old `credential_ref` or mints a new one), the audit correlation chain between the deleted credential, its still-active leases, and the new registration is broken.

**Recommendation:** Add an `active_leases_at_deletion` (uint32) field to the `credential.deleted` event at §4.9.2 line 1721 alongside the existing fields. When non-zero, operators see at deletion time how many leases would continue to use the stale credential — matching the `active_leases_terminated`/`active_leases_rotated` fields already on `credential.user_revoked`/`credential.rotated`. Separately, state at §4.9 line 1353 whether `credential_ref` is stable across re-registration of the same provider (recommended: yes, to preserve audit correlation; or: no, emit a `credential.ref_rotated` linking event).

### CRD-017 CLI RBAC scope contradiction for credential-pool commands persists (iter4 CRD-017 carry-forward) [Low]

**Section:** §24.5 lines 85–93 vs §4.9 line 1102, §15.2 line 805

§4.9 line 1102 normatively states: "A `tenant-admin` can create, update, and delete credential pools for their own tenant via the admin API." §15.2 line 805 reaffirms the endpoint is "tenant-scoped; `tenant-admin` sees own tenant's pools". Every row in §24.5 lines 85–93 still lists `platform-admin` as the sole required role — `list`, `get`, `add-credential`, `update-credential`, `remove-credential`, `revoke-credential`, `revoke-pool`, `re-enable`. The admin-time RBAC live-probe motivation text at §4.9 line 1209 specifically invokes "a `tenant-admin` who lacks rights to patch the Token Service RBAC Role" as the threat being mitigated — a scenario that is impossible if the CLI is correct and tenant-admin cannot reach these paths at all.

This is unchanged from iter3 CRD-011 → iter4 CRD-017. It is a documentation inconsistency, not a correctness defect, hence Low per iter5 severity anchoring.

**Recommendation:** Change the "Min Role" column in §24.5 lines 86, 87, 88, 89, 90, and 93 from `platform-admin` to `platform-admin` or `tenant-admin` (scoped to own tenant). Keep `revoke-credential` (line 91) and `revoke-pool` (line 92) as `platform-admin`-only if emergency revocation is intentionally platform-only — and if so, add that restriction to §4.9 line 1102 and §15.2 line 810/812.

### CRD-018 Fault-driven rotation path still has no audit event (iter4 CRD-018 carry-forward, partial) [Low]

**Section:** §4.9.2 lines 1718–1732

iter4 CRD-018 asked for a `credential.rotated_fallback` (or equivalent) audit event on each fault-driven rotation (Fallback Flow step 4) so investigators can reconstruct "what caused this lease to rotate" without cross-joining against metric series whose high-cardinality label rule at §16.1.1 forbids `session_id` labels. The iter5 spec closes the **ceiling-hit subset** via `credential.rotation_ceiling_hit` (§4.9.2 line 1728) but not the common case: a fault-driven rotation that completes normally — the adapter drained the in-flight counter within 300s and sent `credentials_rotated` without tripping the ceiling — emits no audit event at all. The complete enumeration of rotation triggers that are forensically silent in v1:

- `fault_rate_limited` without ceiling hit
- `fault_auth_expired` without ceiling hit
- `fault_provider_unavailable` without ceiling hit
- `emergency_revocation` in direct-delivery mode without ceiling hit (operator-initiated, successfully drained)
- `user_credential_rotated` / `user_credential_revoked` via `RotateCredentials` RPC without ceiling hit

For all of these, the only surviving signal is the `lenny_credential_rotations` counter (labeled by `error_type` only per §16.1.1). The audit record lacks the `session_id` needed to answer "which session was rotated, why, to what replacement credential." The `credential.fallback_exhausted` event (line 1729) fires only at the terminal state — after `maxRotationsPerSession` is exhausted — so intermediate rotations remain unrecorded.

**Recommendation:** Add a `credential.rotation_completed` audit event at §4.9.2, emitted by the gateway at Fallback Flow step 5 (after the replacement `CredentialLease` is issued), with fields: `tenant_id`, `session_id`, `lease_id` (old), `new_lease_id`, `pool_id`, `old_credential_id`, `new_credential_id`, `rotation_trigger` (one of the non-`proactive_renewal` values), `error_type` (when trigger is fault-driven), `delivery_mode`, `rotation_count`. This makes `credential.rotation_ceiling_hit` a strict specialization (emitted in addition to `credential.rotation_completed` when the ceiling is hit), closes the forensic gap without duplicating the ceiling-hit event's existing fields, and preserves the existing audit budget (the event fires at most `maxRotationsPerSession` times per session).

### CRD-019 User-scoped credential rotation race with in-flight `credential_ref` lookups unspecified [Low]

**Section:** §4.9 line 1347 (`PUT /v1/credentials/{credential_ref}`), §4.9 lines 1357–1362 (Resolution at session creation)

`PUT /v1/credentials/{credential_ref}` states the Token Service "atomically replaces the encrypted material" and "active leases backed by this credential are immediately rotated via `RotateCredentials` RPC … so running sessions pick up the new material within one rotation cycle." The "one rotation cycle" phrase is undefined — §4.9's Fallback Flow is a 7-step state machine, not a single atomic step. Two race windows are observable:

1. **Concurrent session creation.** Between `TokenStore` row update (new encrypted material written) and the enumeration of active leases backed by the old material, a concurrent `POST /v1/sessions` can resolve the user credential (line 1359's per-provider lookup) and be handed the *new* material while leases scheduled for rotation are still holding the *old* material. The gateway has two parallel sessions — one with each material — for the rotation-cycle window.
2. **Concurrent delete-then-rotate.** Line 1349 specifies `DELETE` detaches leases; line 1347 specifies `PUT` rotates leases. There is no specified behavior when a `DELETE` and a `PUT` arrive concurrently on the same `credential_ref`; lease enumeration for rotation may execute against a record that is in the process of being deleted.

Neither race is a credential-leak by itself, but both result in an observable divergence from the one-writer contract a user reasonably expects from the rotate-and-propagate path (especially when the user's intent is to rotate *away* from a compromised key).

**Recommendation:** Normatively state at §4.9 line 1347 that rotation acquires a per-`credential_ref` advisory lock spanning material replacement, lease enumeration, and `RotateCredentials` RPC dispatch; concurrent `GET`/`POST`/`PUT`/`DELETE`/`revoke` on the same `credential_ref` block on the lock with a bounded wait before returning `409 CREDENTIAL_OPERATION_CONFLICT`. Document the per-`credential_ref` lock in the storage architecture section of §12 alongside the existing per-tenant audit advisory lock pattern.

## Convergence assessment

Iter5 is close to convergence on credential management, but **not yet converged** due to CRD-015 (High, new): the deny-list keying contract for user-scoped proxy-mode revocation is structurally incompatible with the iter4-codified pool-keyed deny list. This is a genuine correctness gap in the "immediate invalidation" guarantee on user revocation in the recommended multi-tenant proxy-mode configuration, not a documentation issue.

The remaining open items (CRD-016, CRD-017, CRD-018, CRD-019) are all Low and map to forensic-trail and documentation-consistency improvements that do not block correctness or security posture. Per the iter5 severity calibration feedback, these are anchored to their iter3/iter4 Low rubric equivalents; they have not escalated.

**Status:** Not converged — resolve CRD-015 before declaring.
