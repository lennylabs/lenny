---
layout: default
title: "ca-rotation"
parent: "Runbooks"
triggers: []
components:
  - certManager
  - gateway
  - controller
  - tokenService
symptoms:
  - "scheduled rotation of the cluster-internal CA is due"
  - "the current CA private key may be compromised and must be replaced"
  - "an annual key-management policy requires a fresh CA before the existing certificate expires"
tags:
  - mtls
  - cert-manager
  - ca
  - pki
  - key-rotation
requires:
  - cluster-access
related:
  - cert-manager-outage
  - jwt-key-rotation
  - credential-revocation
---

# ca-rotation

Procedure to rotate the cluster-internal Certificate Authority that issues the §10.3 mTLS leaf certificates. The CA is held in cert-manager (self-signed `ClusterIssuer` or Vault-backed for production) and signs the gateway, controller, Token Service, agent pod, and interceptor certificates listed in the §10.3 Certificate lifecycle table. cert-manager auto-renews each leaf at 2/3 of its TTL, so after the new CA becomes the active issuer every leaf rolls under it within a single TTL.

The §10.3 procedure uses a 24-hour overlap window during which both CAs are trusted but only one issues new leaves. The `CARotation` helper (`pkg/mtls/rotation.go`) tracks the stage transition in process so the gateway audit pipeline emits one `platform.ca_rotated` audit row per stage; the actual cert-manager `Issuer` and `Certificate` resources are managed by the Helm chart.

This is a planned operator procedure. Run it on the rotation schedule the deployment's key-management policy sets (typically annually), or immediately when the current CA private key is suspected compromised.

## Trigger

- A scheduled CA-rotation interval defined by the deployment's key-management policy has elapsed.
- The current CA private key is suspected compromised (HSM audit anomaly, Vault snapshot leak, key-material exposure during a cluster migration).
- The current CA certificate's `NotAfter` is within 30 days and the chart has staged a successor issuer.

## Diagnosis

### Step 1 — Read the active trust bundle

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl -n lenny-system get configmap lenny-ca-bundle \
  -o jsonpath='{.data.ca\.crt}' | \
  openssl crl2pkcs7 -nocrl -certfile /dev/stdin | \
  openssl pkcs7 -print_certs -text -noout | \
  grep -E "Subject:|Not After"
```

The output lists every CA the gateway, controller, and Token Service trust. A single block means there is no rotation in flight; two blocks means a rotation is in flight (Begin has run, Retire has not). Note each CA's `Subject` CN; the operator uses these as the `currentCAID` and `newCAID` arguments below.

### Step 2 — Confirm cert-manager is ready

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl -n cert-manager get pod -l app.kubernetes.io/instance=cert-manager
kubectl get clusterissuer lenny-ca-issuer -o jsonpath='{.status.conditions[?(@.type=="Ready")].status}'
```

Every cert-manager pod is `Running` and `Ready`, and the active `ClusterIssuer` reports `Ready=True`. A degraded cert-manager blocks leaf renewal; resolve via the `cert-manager-outage` runbook before rotating the CA.

### Step 3 — Read the current rotation state

The in-process `CARotation` helper (`pkg/mtls/rotation.go`) records the stage transitions; the gateway snapshot endpoint surfaces them. With no operator API yet wired, the platform audit chain is the authoritative source:

<!-- access: kubectl requires=cluster-access -->
```bash
psql "$LENNY_POSTGRES_DSN" -c "
  SELECT seq, payload->>'to' AS stage, payload->>'current_ca_id' AS ca, payload->>'at' AS at
  FROM audit_log
  WHERE tenant_id='platform' AND event_type='platform.ca_rotated'
  ORDER BY seq DESC LIMIT 5"
```

The most recent row's `stage` is the current stage (`idle`, `new_ca_deployed`, `promoted`, or `old_ca_retired`). A `stage=old_ca_retired` row (or no row at all) means a fresh rotation is safe to start.

## Remediation

The rotation walks the four stages of the §10.3 state machine. Each stage commits one audit row on the platform tenant chain (`platform.ca_rotated`) so the timeline survives an operator handover.

### Step 1 — Stage the new CA

Issue the new CA via the chart or via Vault, depending on the deployment. The new CA must:

- Have a `Subject` CN distinct from every CA already in the trust bundle.
- Have a `NotBefore` no later than the planned overlap start.
- Have a `NotAfter` at least 30 days past the planned overlap close (so cert-manager has headroom to rotate every leaf under the new CA).

Apply the chart upgrade that adds the new CA to the trust bundle (`certmanager.trustBundle.additionalCAs`). cert-manager picks up the new entries and the gateway, controller, and Token Service replicas re-read the trust bundle through the file-watching TLS config described in §10.3.

### Step 2 — Begin the rotation

Once every replica trusts both CAs, advance the in-process `CARotation` to `new_ca_deployed`. The chart's `certmanager.rotation.stage` value drives the transition; bumping it from `idle` to `new_ca_deployed` and running `helm upgrade` makes every gateway and controller replica call `CARotation.BeginNewCARotation(newCAID)` on next startup:

<!-- access: kubectl requires=cluster-access -->
```bash
helm upgrade lenny lennylabs/lenny -f values.yaml \
  --set certmanager.rotation.stage=new_ca_deployed \
  --set certmanager.rotation.newCAID=lenny-ca-2027
```

Each replica emits a `platform.ca_rotated` row with `from=idle`, `to=new_ca_deployed`, `current_ca_id=<old>`, and `trusted_ca_ids=[<old>,<new>]`. cert-manager still signs every new leaf with the old CA at this stage; the new CA is in the trust bundle but is not yet the issuer.

### Step 3 — Promote the new CA

Once the operator has staged the new `ClusterIssuer` to be the active one (the chart's `certmanager.activeIssuer` value), advance the rotation to `promoted`:

<!-- access: kubectl requires=cluster-access -->
```bash
helm upgrade lenny lennylabs/lenny -f values.yaml \
  --set certmanager.rotation.stage=promoted \
  --set certmanager.activeIssuer=lenny-ca-2027
```

Each replica calls `CARotation.PromoteNewCA()` on next startup and emits a row with `from=new_ca_deployed`, `to=promoted`, `current_ca_id=<new>`, and `trusted_ca_ids=[<old>,<new>]`. cert-manager now signs every new `Certificate` with the new CA. Existing leaves signed under the old CA continue to validate; cert-manager auto-renews each leaf at 2/3 of its TTL under the new CA.

### Step 4 — Wait for every leaf to renew

The longest leaf TTL is the gateway / controller / Token Service 24h cert. Within 16 hours (2/3 of 24h) every long-lived leaf has been reissued under the new CA. Agent pod certificates (4h TTL) renew faster.

Confirm the renewal has propagated:

<!-- access: kubectl requires=cluster-access -->
```bash
for ns in lenny-system lenny-agents lenny-agents-kata; do
  kubectl -n $ns get certificate -o json | \
    jq -r '.items[] | select(.status.conditions[] | select(.type=="Ready" and .status=="True")) | "\(.metadata.name) \(.status.notAfter)"'
done
```

Every certificate has a `Ready=True` status and a `notAfter` past the overlap-window close. A certificate that did not renew indicates a cert-manager issuance failure; investigate via the `cert-manager-outage` runbook before retiring the old CA.

### Step 5 — Retire the old CA

After the §10.3 24-hour overlap window has closed (24 hours after Begin in Step 2), advance the rotation to `old_ca_retired`:

<!-- access: kubectl requires=cluster-access -->
```bash
helm upgrade lenny lennylabs/lenny -f values.yaml \
  --set certmanager.rotation.stage=old_ca_retired \
  --set certmanager.trustBundle.additionalCAs=null
```

Each replica calls `CARotation.RetireOldCA()`. The call refuses with kind `overlap_open` if the upgrade lands before the window closes; the remediation is to wait. Once the window has closed the call commits a row with `from=promoted`, `to=old_ca_retired`, `current_ca_id=<new>`, and `trusted_ca_ids=[<new>]`. cert-manager and the gateway replicas re-read the bundle and stop trusting the old CA.

## Verification

### Step 1 — Confirm the trust bundle holds only the new CA

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl -n lenny-system get configmap lenny-ca-bundle \
  -o jsonpath='{.data.ca\.crt}' | \
  openssl crl2pkcs7 -nocrl -certfile /dev/stdin | \
  openssl pkcs7 -print_certs -text -noout | \
  grep "Subject:"
```

The output lists exactly one `Subject` block. A two-block response means the chart upgrade in Step 5 did not propagate; reapply it.

### Step 2 — Confirm every leaf is signed by the new CA

<!-- access: kubectl requires=cluster-access -->
```bash
for ns in lenny-system lenny-agents; do
  for secret in $(kubectl -n $ns get secret -l cert-manager.io/issuer-name -o name); do
    kubectl -n $ns get "$secret" -o jsonpath='{.data.tls\.crt}' | \
      base64 -d | openssl x509 -noout -issuer
  done
done | sort -u
```

Every `issuer=` line names the new CA's subject. An issuer line that still names the old CA indicates a leaf cert-manager has not yet rotated; force-renew it via `kubectl cert-manager renew`.

### Step 3 — Confirm the audit timeline

<!-- access: kubectl requires=cluster-access -->
```bash
psql "$LENNY_POSTGRES_DSN" -c "
  SELECT seq, payload->>'from' AS from_stage, payload->>'to' AS to_stage,
         payload->>'current_ca_id' AS current_ca, payload->>'at' AS at
  FROM audit_log
  WHERE tenant_id='platform' AND event_type='platform.ca_rotated'
  ORDER BY seq DESC LIMIT 4"
```

Three rows correspond to this rotation: `to=new_ca_deployed`, `to=promoted`, `to=old_ca_retired`. The platform audit chain stays `verified` per the §11.7 verifier.

## Escalation

Escalate to the platform on-call rotation when:

- The `ca-rotation-retire-old` call refuses with kind `overlap_open` after 24 hours. The verifier clock or the rotation start record may be out of sync with the wall clock; check `lenny_time_drift_seconds` and the §13.3 NTP posture before forcing the retire.
- A leaf certificate fails to renew under the new CA. Resolve via `cert-manager-outage` before retiring the old CA; running Step 5 with un-renewed leaves breaks every connection still presenting an old-CA cert.
- A handshake fails with `bad certificate` after the retire step. A consumer outside the cluster may have pinned the old CA; coordinate with the consumer to update its trust store before declaring the rotation complete.

## Spec references

- §10.3 mTLS PKI (Certificate lifecycle, cert-manager Failure Modes and CA Rotation).
- §11.7 audit hash chain (`platform.*` events land on OCSF apiActivity 6003).
- §13.2 Network Isolation (selector consistency under a rotated CA).
