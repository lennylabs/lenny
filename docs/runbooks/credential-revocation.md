---
layout: default
title: "credential-revocation"
parent: "Runbooks"
triggers:
  - alert: CredentialCompromiseSuspected
    severity: critical
components:
  - credentialPools
  - tokenService
symptoms:
  - "suspected provider-key compromise"
  - "persistent provider 4xx from a single credential ID"
  - "compliance-initiated rotation"
tags:
  - credentials
  - security
  - rotation
  - revocation
requires:
  - admin-api
  - cluster-access
related:
  - credential-pool-exhaustion
  - token-service-outage
---

# credential-revocation

Emergency or planned rotation of a compromised or deprecated provider credential. Supports immediate rotation (fastest propagation) and planned rotation (zero-disruption).

## Trigger

- Security on-call reports suspected credential compromise (stolen API key, leaked GitHub PAT, provider-side alert).
- Persistent 4xx from a single credential ID (`lenny_credential_provider_error_total{credential_id="..."}` > 0 with `auth` or `forbidden` class).
- Compliance-initiated rotation (scheduled or ad hoc).

## Diagnosis

### Step 1 — Identify the credential

<!-- access: lenny-ctl -->
```bash
lenny-ctl admin credential-pools list
lenny-ctl admin credential-pools get --pool <pool>
```

<!-- access: api method=GET path=/v1/admin/credential-pools/{name} -->
```
GET /v1/admin/credential-pools/<name>
```

Note the `credentialId` and the referenced Kubernetes Secret.

### Step 2 — Confirm the blast radius

<!-- access: api method=GET path=/v1/admin/credential-leases -->
```
GET /v1/admin/credential-leases?credentialId=<id>&state=active
```

Record the list of active leases — you'll need them for Step 3 of remediation.

### Step 3 — Verify compromise signal

If the trigger is a security alert, get the raw provider evidence — revocation cannot be rolled back, so confirm the compromise before acting.

## Remediation

### Step 1 — Revoke the credential

<!-- access: lenny-ctl -->
```bash
lenny-ctl admin credential-pools revoke-credential \
  --pool <pool> --credential <credential-id> --reason "<r>"
```

<!-- access: api method=POST path=/v1/admin/credential-pools/{name}/credentials/{credId}/revoke -->
```
POST /v1/admin/credential-pools/<name>/credentials/<credId>/revoke
```

Effect: no new leases will be issued from this credential. Existing leases remain valid until their TTL expires (minutes to hours, depending on `credentialLeaseTTL`).

### Step 2 — Revoke at the provider

Revoke or rotate the key at the provider (OpenAI, Anthropic, GitHub, etc.). This is the authoritative action — the provider-side rotation invalidates the key immediately for all callers, not just Lenny.

### Step 3 — Terminate active leases (emergency only)

If the compromise requires severing in-flight traffic immediately:

<!-- access: lenny-ctl -->
```bash
for s in <session-id-1> <session-id-2> ...; do
  lenny-ctl admin sessions force-terminate "$s"
done
```

> This disrupts user sessions. Only do this if continued traffic on the compromised credential is worse than session disruption.

### Step 4 — Rotate the Secret

Update the Kubernetes Secret referenced by the credential:

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl create secret generic <secret-name> -n lenny-system \
  --from-literal=apiKey="<new-key>" \
  --dry-run=client -o yaml | kubectl apply -f -
```

Or, if you're using external-secrets-operator / sealed-secrets, update the upstream source.

### Step 5 — Add the rotated credential back (or replace)

<!-- access: lenny-ctl -->
```bash
lenny-ctl admin credential-pools add-credential \
  --pool <pool> --provider <provider>
```

`--secret-ref` and `--max-concurrent-sessions` are body fields on the admin API; set them via the API call or in the pool's Helm values when the rotated credential must carry non-default settings.

Remove the old `credential-id` if the rotation is permanent:

<!-- access: lenny-ctl -->
```bash
lenny-ctl admin credential-pools remove-credential \
  --pool <pool> --credential <old-credential-id>
```

### Step 6 — Propagation verification

<!-- access: lenny-ctl -->
```bash
lenny-ctl diagnose credential-pool <pool>
```

Checks:

- The disabled credential no longer appears in `availableCount`.
- The new credential is active and reflected in `availableCount`.
- Provider-error rate on the old credential returns to zero after its leases expire.

<!-- access: api method=GET path=/v1/admin/metrics -->
```
GET /v1/admin/metrics?q=lenny_credential_provider_error_total&groupBy=credential_id&window=30m
```

### Step 7 — Audit trail

All credential state transitions are written to `audit_log`. Confirm the revocation is recorded:

<!-- access: api method=GET path=/v1/admin/audit-events -->
```
GET /v1/admin/audit-events?event_type=credential.revoked&since=1h
GET /v1/admin/audit-events?event_type=credential.added&since=1h
```

This is the compliance evidence for the rotation.

## User-scoped credentials

The remediation above applies to **pool-managed** credentials (`/v1/admin/credential-pools/...`). User-scoped credentials registered via `POST /v1/credentials` (see Spec §4.9 "User credential management endpoints") use a distinct endpoint family and audit trail.

### When it applies

- A user reports that their personal provider key has been compromised.
- Security telemetry attributes provider 4xx to a specific user-scoped `credential_ref` rather than a pool credential.
- Compliance requires rotation of a named user's credentials (e.g., departing employee, contractor offboarding).

### Remediation

#### Step U1 — Identify the user credential

<!-- access: api method=GET path=/v1/credentials -->
```
GET /v1/credentials
```

Invoked as the affected user, or as `platform-admin` impersonating the user. Record the `credential_ref` and `provider`.

#### Step U2 — Revoke the user credential

<!-- access: api method=POST path=/v1/credentials/{credential_ref}/revoke -->
```
POST /v1/credentials/<credential_ref>/revoke
Body: {"reason": "<r>", "note": "<optional note>"}
```

Effect: the Token Service marks the credential as `revoked`, adds a user-shaped entry to the credential deny list (`{source: "user", tenantId, credentialRef}`), and immediately invalidates all active leases backed by it — proxy-mode leases via the deny list, direct-mode leases via `RotateCredentials` RPC. Emits `credential.user_revoked`.

Unlike pool revocation, `POST .../revoke` on a user credential retains the record in `revoked` state for audit. Running sessions with active leases are cut off as soon as the deny list propagates (Redis pub/sub with Postgres `LISTEN/NOTIFY` fallback).

If the revocation should be non-disruptive (no mid-session cutoff), use `DELETE /v1/credentials/{credential_ref}` instead — active leases continue using the previously materialized credential until natural TTL expiry.

#### Step U3 — Revoke at the provider

Same as Step 2 above: rotate or revoke the provider-side key. For user credentials this is the user's responsibility — an operator performing the revocation on behalf of a compromised user should confirm the provider-side action with the user or their manager.

#### Step U4 — Rotate (user re-registers)

The user (or platform-admin acting for the user) re-registers a new credential for the same provider:

<!-- access: api method=POST path=/v1/credentials -->
```
POST /v1/credentials
Body: {"provider": "<provider>", "credential": "<new-secret>", "label": "<optional>"}
```

New sessions created after re-registration resolve the new credential per the tenant's `credentialPolicy.preferredSource`.

#### Step U5 — Audit trail

<!-- access: api method=GET path=/v1/admin/audit-events -->
```
GET /v1/admin/audit-events?event_type=credential.user_revoked&since=1h
GET /v1/admin/audit-events?event_type=credential.registered&since=1h
```

These events record `tenant_id`, `user_id`, `provider`, `credential_ref`, `reason`, and `active_leases_terminated` — the compliance record for the user-scoped rotation. The `lenny_user_credential_revoked_with_active_leases` gauge (labeled by `tenant_id`, `provider`) feeds the `CredentialCompromised` alert (Spec §16.5) alongside its pool-scoped counterpart.

## Escalation

Escalate to:

- **Security on-call** immediately on any suspected compromise — they decide on blast-radius containment (Step 3).
- **Provider support** if the provider-side key cannot be rotated through normal channels and a human-mediated key reset is needed.
- **Compliance officer** if the compromise may require customer notification under contractual or regulatory obligations.

Cross-reference: Spec §4.9 (credential leasing service).
