---
layout: default
title: "jwt-key-rotation"
parent: "Runbooks"
triggers:
  - alert: KMSSignQuotaApproaching
    severity: warning
components:
  - gateway
  - tokenService
  - ops
symptoms:
  - "scheduled rotation of the JWT signing key is due"
  - "the JWT signing key may be compromised and must be replaced"
  - "a key-management policy requires a new signing key version"
  - "the fleet-wide KMS sign request rate approaches the configured Sign quota"
  - "token mints return 503 KMS_SIGNING_UNAVAILABLE while the KMS throttles sign requests"
tags:
  - jwt
  - signing-key
  - kms
  - token-service
  - lenny-ops
  - key-rotation
  - quota
requires:
  - cluster-access
  - kms-access
  - admin-api
related:
  - ca-rotation
  - credential-revocation
  - token-service-outage
---

# jwt-key-rotation

This runbook covers two operator tasks for the key that signs every Lenny-issued JWT: rotating the key, and responding to the `KMSSignQuotaApproaching` alert. The reader is a platform operator with cluster access, Helm access to the Lenny release, and permission to create keys and grant permissions in the deployment's KMS.

## How Lenny signs and verifies tokens

Every Lenny-issued JWT is signed with ES256 (ECDSA over P-256 with SHA-256). The gateway and the Token Service both sign with one key, which the reserved KMS alias `platform:jwt-signing` names in the chart's `kms.aliases` value. On AWS KMS, GCP Cloud KMS, and Azure Key Vault the private key stays in the KMS: each signer sends a digest to the KMS `Sign` operation and receives the signature. With `kms.provider: local`, which is for development only, every process derives the key from the shared local master key.

The optional alias `platform:jwt-verify` names a second key that verifies tokens and never signs them. It is mapped only while a rotation is in progress.

Each gateway replica, each Token Service replica, and `lenny-ops` fetch the public key of every mapped alias once at startup, with the KMS `GetPublicKey` operation, and hold those keys as a key set for the life of the process. Each token header carries a `kid`, which is the RFC 7638 thumbprint of the public key that signed it. A token whose `kid` is absent is rejected with reason `missing_kid`, and a token whose `kid` is not in the key set is rejected with reason `unknown_kid`. Verification never calls the KMS. A process reads new alias values only when it restarts, so every change in this runbook is a Helm rollout of the gateway, the Token Service, and `lenny-ops`.

On a cloud provider, each alias must name an immutable key version:

| Provider | Accepted reference | Refused reference |
|:--|:--|:--|
| AWS KMS | A key ID or key ARN | An alias name or alias ARN |
| GCP Cloud KMS | A CryptoKeyVersion name ending in `/cryptoKeyVersions/<n>` | A CryptoKey name without a version |
| Azure Key Vault | A key identifier with a non-empty `@<version>` suffix | A key identifier without a version |

A process refuses to start when either alias names a refused reference.

## Trigger

Run the rotation procedure in these cases:

- A rotation interval that the deployment's key-management policy defines has elapsed.
- The signing key is suspected compromised, for example through a KMS audit anomaly or a leaked credential that holds the `Sign` permission. Read the compromise entry under [Escalation](#escalation) before starting.

Run the [KMS Sign quota](#kms-sign-quota) procedure when the `KMSSignQuotaApproaching` warning alert fires.

## Diagnosis

The steps below establish the starting state before a rotation. Each rotation step later reuses the alias-value check in Step 2.

### Step 1: Read the current alias mapping

<!-- access: cluster requires=helm-cli -->
```bash
helm -n lenny-system get values lenny -o json | jq -r '.kms.aliases' | tr ',' '\n' | grep '^platform:jwt-'
```

A deployment with no rotation in progress prints one line, `platform:jwt-signing=<reference>`. A second line naming `platform:jwt-verify` means an earlier rotation has not finished. Determine which rotation step that deployment reached before starting a new rotation: when `platform:jwt-verify` names the key that signed before the current one, the earlier rotation is waiting for its step 3.

### Step 2: Confirm every pod runs the same alias values

<!-- access: kubectl requires=cluster-access -->
```bash
for d in lenny-gateway lenny-token-service lenny-ops; do
  kubectl -n lenny-system rollout status "deploy/$d" --timeout=15m
done
for d in lenny-gateway lenny-token-service lenny-ops; do
  sel=$(kubectl -n lenny-system get deploy "$d" -o json |
    jq -r '.spec.selector.matchLabels | to_entries | map("\(.key)=\(.value)") | join(",")')
  kubectl -n lenny-system get pods -l "$sel" -o json |
    jq -r --arg d "$d" '.items[] | "\($d)\t\(.metadata.name)\t\([.spec.containers[].args[]? | select(startswith("--kms-alias="))] | join(" "))"'
done
```

Each `rollout status` call returns success, and every printed line carries the same `--kms-alias` value, which matches the Helm value from Step 1. A pod with a different value is still running an earlier revision. Wait for its rollout to finish, or investigate a rollout that does not progress, before continuing. A rotation step that starts while any pod holds a different key set leaves some processes unable to verify tokens that others accept.

### Step 3: Confirm the processes can reach the KMS

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl -n lenny-system get pods -o wide | grep -E '^lenny-(gateway|token-service|ops)-'
```

Every listed pod is `Running` and ready, with a restart count that is not climbing. A pod in `CrashLoopBackOff` whose log reports a JWT key set error could not fetch a public key at startup. Resolve KMS reachability or permissions before rotating, as [KMS outage during a rotation](#kms-outage-during-a-rotation) describes.

## Remediation

Rotation is three Helm rollouts of the gateway, the Token Service, and `lenny-ops`. Each rollout begins only after the previous one has completed on every gateway, Token Service, and `lenny-ops` pod, as Diagnosis Step 2 confirms.

Edit `kms.aliases` in the release's values file rather than passing it with `--set`, because the value is a comma-separated list and `--set` splits it at each comma. Keep every other alias in the list unchanged.

### Step 1: Create the new key and grant access

Create a P-256 signing key in the deployment's KMS:

- AWS KMS: key spec `ECC_NIST_P256`, key usage `SIGN_VERIFY`. Reference the key by its key ID or key ARN.
- GCP Cloud KMS: purpose `ASYMMETRIC_SIGN`, algorithm `EC_SIGN_P256_SHA256`. Reference the CryptoKeyVersion, ending in `/cryptoKeyVersions/<n>`.
- Azure Key Vault: key type `EC`, curve `P-256`. Reference the key with its `@<version>` suffix.

Grant the permissions below on the new key before any rollout maps it. Every key that `platform:jwt-signing` or `platform:jwt-verify` names needs the same grants.

| ServiceAccount | Component | KMS permissions |
|:--|:--|:--|
| `lenny-gateway` | Gateway | `Sign` and `GetPublicKey` |
| `lenny-token-service` | Token Service | `Sign` and `GetPublicKey` |
| `lenny-ops-sa` | `lenny-ops` | `GetPublicKey` only |

On AWS these are `kms:Sign` and `kms:GetPublicKey`. On GCP they are `cloudkms.cryptoKeyVersions.useToSign` and `cloudkms.cryptoKeyVersions.viewPublicKey`. On Azure Key Vault they are the key `sign` and `get` operations. A pod that starts without `GetPublicKey` on a mapped key exits, so a missing grant appears as a crashlooping rollout.

With `kms.provider: local`, no key is created and no grant applies. The local provider derives a key from the master key and the reference string, so a new, unused reference string, such as `jwt-2026-11`, names a new key.

### Step 2: First rollout, map the new key for verification

Add `platform:jwt-verify` with the new key's reference and leave `platform:jwt-signing` on the current key:

```yaml
kms:
  aliases: "platform:jwt-signing=<current-key-reference>,platform:jwt-verify=<new-key-reference>,<other aliases>"
```

<!-- access: cluster requires=helm-cli -->
```bash
helm upgrade lenny lennylabs/lenny -n lenny-system -f values.yaml --wait --timeout 15m
```

Run Diagnosis Step 2. After this rollout, every process verifies tokens signed by either key and signs with the current key.

### Step 3: Second rollout, sign with the new key

Swap the two mappings, so that `platform:jwt-signing` names the new key and `platform:jwt-verify` names the previous key:

```yaml
kms:
  aliases: "platform:jwt-signing=<new-key-reference>,platform:jwt-verify=<previous-key-reference>,<other aliases>"
```

<!-- access: cluster requires=helm-cli -->
```bash
helm upgrade lenny lennylabs/lenny -n lenny-system -f values.yaml --wait --timeout 15m
```

Run Diagnosis Step 2. Every new token is now signed with the new key, and tokens signed with the previous key keep verifying.

Record the time this rollout completed. The wait in Step 5 starts at that time.

### Step 4: Rotate the bootstrap admin credential

Rotate the bootstrap admin credential after the second rollout completes:

<!-- access: lenny-ctl -->
```bash
lenny-ctl admin users rotate-token --user lenny-admin
```

The gateway mints a new admin token with the new key, writes it to the `lenny-admin-token` Secret, and revokes the previous token immediately. Update every system that holds the admin token to read the new value from the Secret, as [Initial Admin Credential](../operator-guide/installation.html#initial-admin-credential) describes. The admin token's lifetime is longer than the wait in Step 5, so an admin token signed with the previous key would stop verifying at the third rollout.

### Step 5: Third rollout, remove the previous key

Wait at least 24 hours after the second rollout completed. That wait equals the lifetime cap for tokens issued for the gateway, which bounds every token the previous key signed other than the bootstrap admin credential. Gateway-minted impersonation and playground bearers have shorter caps, so every token the previous key signed has expired by the end of the wait.

Remove `platform:jwt-verify` from the list:

```yaml
kms:
  aliases: "platform:jwt-signing=<new-key-reference>,<other aliases>"
```

<!-- access: cluster requires=helm-cli -->
```bash
helm upgrade lenny lennylabs/lenny -n lenny-system -f values.yaml --wait --timeout 15m
```

Run Diagnosis Step 2. A token signed with the previous key now fails verification with reason `unknown_kid`. Clients that renew their tokens before expiry are unaffected, because their current tokens were signed with the new key.

Remove the previous key's grants, and disable or schedule deletion of the previous key according to the deployment's key-management policy, only after this rollout completes.

## KMS outage during a rotation

A process loads its key set only at startup. A gateway, Token Service, or `lenny-ops` process that cannot fetch the public key of a mapped alias at startup exits, and its pod enters `CrashLoopBackOff` until the KMS becomes reachable. The process makes no further KMS call to verify tokens, so pods that are already running keep verifying every token in their key set through a KMS outage.

During an outage, the gateway and the Token Service cannot sign. Every mint, such as a `POST /v1/oauth/token` exchange or a playground bearer, fails with a retryable `503 KMS_SIGNING_UNAVAILABLE` response, and tokens issued before the outage keep verifying until they expire. The `KMSSigningUnavailable` alert fires when a signer's circuit breaker trips.

Do not start a rotation rollout while the KMS is unreachable. The new pods of a rolling update fail to start, so the rollout stops, `helm upgrade --wait` times out, and the old pods the rollout has not yet replaced keep serving. A Deployment whose rolling update allows an unavailable pod, such as the gateway, runs with one fewer ready replica until the rollout is reverted or completes. Roll back with `helm rollback lenny -n lenny-system` or wait for the KMS to recover, and then run Diagnosis Step 2 before continuing. Avoid deleting or restarting healthy pods during the outage, because a restarted pod cannot start until the KMS returns.

## Verification

### Step 1: Confirm the final alias values on every pod

<!-- access: kubectl requires=cluster-access -->
```bash
helm -n lenny-system get values lenny -o json | jq -r '.kms.aliases' | tr ',' '\n' | grep '^platform:jwt-'
```

The output is one line, `platform:jwt-signing=<new-key-reference>`. Run Diagnosis Step 2 and confirm every gateway, Token Service, and `lenny-ops` pod carries the same `--kms-alias` value.

### Step 2: Confirm the admin token is signed with the new key

<!-- access: kubectl requires=cluster-access -->
```bash
TOKEN=$(kubectl -n lenny-system get secret lenny-admin-token -o jsonpath='{.data.token}' | base64 -d)
hdr=$(printf '%s' "$TOKEN" | cut -d. -f1 | tr '_-' '/+')
while [ $(( ${#hdr} % 4 )) -ne 0 ]; do hdr="${hdr}="; done
printf '%s' "$hdr" | base64 -d | jq '{alg, kid}'
```

The header shows `alg` `ES256` and a `kid` that differs from the `kid` of an admin token taken before the rotation.

### Step 3: Confirm the admin token is accepted

<!-- access: api method=GET path=/v1/admin/health -->
```
GET /v1/admin/health
Authorization: Bearer <token from the lenny-admin-token Secret>
```

The gateway returns `200`. A `401` response means the token was signed with a key outside the running key set: check that Step 4 of the remediation ran after the second rollout, and that every system reads the token from the Secret.

## KMS Sign quota

Every Lenny-issued JWT costs one KMS `Sign` call. The chart value `kms.signQuotaPerSecond`, required when `kms.provider` is a cloud provider, records the provider's asymmetric `Sign` quota for the account and region. Each gateway replica publishes that value as the `lenny_kms_sign_quota_per_second` gauge.

The `KMSSignQuotaApproaching` warning alert fires when the fleet-wide rate of sign requests that reach the KMS stays above 80% of `kms.signQuotaPerSecond` for more than 10 minutes. The rate counts `lenny_jwt_sign_requests_total` across the gateway and the Token Service, excluding requests the signer's circuit breaker refused before calling the KMS. The alert never fires when no quota is configured, which is the case with `kms.provider: local`.

When the sign rate exceeds the provider quota, the KMS throttles sign requests. A throttled mint returns a retryable `503 KMS_SIGNING_UNAVAILABLE` response with `Retry-After: 1`. Throttling does not count toward the signer's circuit breaker, so the breaker stays closed and requests below the quota keep succeeding. Tokens already issued keep verifying, because verification makes no KMS call.

### Step 1: Read the sign rate by component and outcome

<!-- access: api method=GET path=/v1/admin/metrics -->
```
GET /v1/admin/metrics?q=lenny_jwt_sign_requests_total&groupBy=component,outcome&window=30m
GET /v1/admin/metrics?q=lenny_kms_sign_quota_per_second&window=30m
```

The `component` label is `gateway` or `token-service`, and the `outcome` label is `ok`, `throttled`, `circuit_open`, or `error`. A non-zero `throttled` rate means the provider is already throttling and clients are receiving `503` responses. A rate that rose suddenly on one component points at its traffic, such as a burst of session creation on the gateway or of `POST /v1/oauth/token` exchanges on the Token Service.

### Step 2: Confirm the configured quota matches the provider

<!-- access: cluster requires=helm-cli -->
```bash
helm -n lenny-system get values lenny -o json | jq '.kms.signQuotaPerSecond'
```

Compare the value with the asymmetric `Sign` quota the provider reports for the account and region in its quota console. A configured value below the provider quota makes the alert fire early. A configured value above it makes the alert fire late or not at all, while clients already receive throttled `503` responses.

### Step 3: Raise the provider quota and the chart value together

Request an increase of the provider's asymmetric `Sign` quota for the account and region through the provider's quota process. Size the request to at least the deployment's peak sign rate, which is the sum of the session creation rate, the delegation rate, the token renewal rate of every holder that renews before expiry, and infrequent operator mints.

After the provider applies the increase, set `kms.signQuotaPerSecond` to the new quota and roll out:

<!-- access: cluster requires=helm-cli -->
```bash
helm upgrade lenny lennylabs/lenny -n lenny-system -f values.yaml \
  --set kms.signQuotaPerSecond=<new-provider-quota> --wait --timeout 15m
```

Raise both values together. Raising only the chart value silences the alert while the provider keeps throttling.

When the provider cannot raise the quota, lower the renewal rate instead. `auth.capabilityTokenTtlSeconds` sets the default lifetime of playground bearers and of `POST /v1/oauth/token` tokens requested without `expires_in`, and holders of those tokens renew once per lifetime. A longer lifetime lowers the renewal rate and extends the time a leaked token stays usable.

### Step 4: Confirm the rate is below the threshold

<!-- access: api method=GET path=/v1/admin/metrics -->
```
GET /v1/admin/metrics?q=lenny_jwt_sign_requests_total&groupBy=outcome&window=15m
```

The `throttled` rate returns to zero, and the alert resolves once the fleet rate stays below 80% of the new `kms.signQuotaPerSecond`.

## Escalation

Escalate to the platform on-call rotation when:

- A rotation rollout does not complete because new pods exit at startup, and the KMS is reachable. Check the permissions in Remediation Step 1 and the key-version reference format before retrying.
- A token signed with the new key fails on some pods after Diagnosis Step 2 reports one alias value everywhere. A pod may be running an image or chart revision that does not match the rest of the release.
- The signing key is suspected compromised. The previous key keeps verifying tokens until the third rollout. Involve the security on-call before starting, so that the response to the compromise covers the tokens the previous key signed.
- The `throttled` outcome rate stays above zero after the provider quota increase, or the provider declines the increase.
