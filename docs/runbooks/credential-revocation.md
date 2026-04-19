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

## Escalation

Escalate to:

- **Security on-call** immediately on any suspected compromise — they decide on blast-radius containment (Step 3).
- **Provider support** if the provider-side key cannot be rotated through normal channels and a human-mediated key reset is needed.
- **Compliance officer** if the compromise may require customer notification under contractual or regulatory obligations.

Cross-reference: Spec §4.9 (credential leasing service).
