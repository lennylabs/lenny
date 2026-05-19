---
layout: default
title: "jwt-key-rotation"
parent: "Runbooks"
triggers: []
components:
  - gateway
  - tokenService
symptoms:
  - "scheduled rotation of the JWT signing key is due"
  - "the Token Service signing key may be compromised and must be replaced"
  - "a key-management policy update requires a new kid before the previous one expires"
tags:
  - jwt
  - signing-key
  - kms
  - token-service
  - key-rotation
requires:
  - cluster-access
  - kms-access
related:
  - ca-rotation
  - credential-revocation
  - token-service-outage
---

# jwt-key-rotation

Procedure to rotate the gateway's JWT signing key on the §10.3 / §13.3 overlap-window schedule. The Token Service mints session-capability JWTs using an HMAC-SHA256 key sealed under the platform KMS key-encryption key (`KMSSigner`, `pkg/auth/jwt/kmssigner.go`). The gateway holds the unwrapped key in process memory only; the durable form is the envelope-encrypted blob the chart loads into the Token Service.

Each replica wraps the signer in a `RotatingVerifier` (`pkg/auth/jwt/rotating.go`) that holds a current key plus zero or more previous keys, each addressable by its JOSE `kid`. A token signed under the current key always verifies. A token signed under a previous key verifies for the 24-hour overlap window the §10.3 default sets; past the window, the verifier rejects with reason `key_retired`. The JWK Set published at `/.well-known/jwks.json` carries the current key plus every retained previous key during the overlap window.

This is a planned operator procedure. Run it on the rotation schedule the deployment's key-management policy sets, or immediately when the current key is suspected compromised.

## Trigger

- A scheduled key-rotation interval defined by the deployment's key-management policy has elapsed.
- The current signing key is suspected compromised (KMS audit anomaly, replica memory dump, backup leak).
- A new kid has been pre-staged in the gateway's `--bearer-trust-hmac-key-file` and the existing kid must be retired before the overlap window closes.

## Diagnosis

### Step 1 — Read the active JWK Set

<!-- access: kubectl requires=cluster-access -->
```bash
curl -fsS https://lenny-gateway.lenny-system.svc/.well-known/jwks.json | jq
```

The response is a JWK Set whose `keys[0].kid` names the current signing key and whose `keys[1..]` name every retained previous key. A single-entry response means there is no rotation in flight; a multi-entry response means a previous rotation is still inside its overlap window.

### Step 2 — Confirm every gateway replica advertises the same set

<!-- access: kubectl requires=cluster-access -->
```bash
for pod in $(kubectl -n lenny-system get pod -l lenny.dev/component=gateway -o name); do
  kubectl -n lenny-system exec "$pod" -- \
    curl -fsS http://localhost:8080/.well-known/jwks.json | jq '.keys[].kid'
done
```

Every replica must report the same kid list. A divergent reply means a replica has rotated independently or has not yet seen the new key; resolve that before rotating, because clients balancing across replicas will see inconsistent verification outcomes.

### Step 3 — Confirm the audit pipeline can record a rotation

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl -n lenny-system logs deploy/lenny-gateway | grep platform.jwt_signing_key_rotated | tail -n 5
```

A rotation lifecycle transition writes a `platform.jwt_signing_key_rotated` row on the platform tenant audit chain. The grep returns prior rotation rows if any. A missing audit backend (Postgres outage) does not block a rotation but does lose the audit trail; run the `audit-pipeline-degraded` runbook first if the chain backend is degraded.

## Remediation

The rotation is a three-stage transition on the rotating verifier. Each stage is the input to the verifier's `Rotate` or `RetireExpired` call; the audit pipeline records one `platform.jwt_signing_key_rotated` row per stage.

### Step 1 — Generate the new key

The Token Service signing key is HMAC-SHA256. Generate a fresh 32-byte secret:

<!-- access: kubectl requires=cluster-access -->
```bash
head -c 32 /dev/urandom | base64
```

Wrap it under the platform KMS KEK named by `jwt.TokenServiceKEKAlias` (`platform:token-service-signing`). The wrapping step depends on the deployment's KMS provider; the result is an envelope-sealed blob the chart's `tokenService.signingKeySealed` value carries. Generate a kid that names the rotation epoch (e.g., `ts-2026-05-19`); the kid must not match the current or any retained previous kid.

### Step 2 — Promote the new key

Once the wrapped key reaches every Token Service replica, signal the rotation. The chart wires the new wrapped key under `tokenService.signingKeySealed` and `tokenService.signingKid`; a `helm upgrade` rolls the replicas under the new key:

<!-- access: kubectl requires=cluster-access -->
```bash
helm upgrade lenny lennylabs/lenny -f values.yaml \
  --set tokenService.signingKid=ts-2026-05-19 \
  --set-file tokenService.signingKeySealed=new-key.sealed
```

On startup each replica reads the wrapped key, constructs the `RotatingVerifier` with it as the current key, and inherits the previously seen current key as the now-previous entry from the durable `signing_keys` table. The verifier emits a `platform.jwt_signing_key_rotated` audit row with `transition=promote_current`, `from_key_id=<old kid>`, and `to_key_id=<new kid>`. Tokens signed under the old kid continue to verify for the 24-hour overlap window; tokens signed under the new kid verify immediately.

### Step 3 — Wait for the overlap window to close

The overlap default is 24 hours. During this window the gateway accepts a token signed under either kid, the JWK Set publishes both, and every client caching the document picks up the new key on its next refresh.

Confirm the rotation has propagated:

<!-- access: kubectl requires=cluster-access -->
```bash
curl -fsS https://lenny-gateway.lenny-system.svc/.well-known/jwks.json | \
  jq '.keys[].kid'
```

The response lists the new kid first (current) and the old kid second (previous). Both verifying paths are active.

### Step 4 — Retire the old key

After the overlap window closes (24 hours after Step 2), the gateway's housekeeping sweep prunes the previous kid. The sweep runs on a fixed cadence in process; an operator who needs to force the retire ahead of the cadence drops the previous-kid record from the durable `signing_keys` table:

<!-- access: kubectl requires=cluster-access -->
```bash
psql "$LENNY_POSTGRES_DSN" \
  -c "DELETE FROM signing_keys WHERE kid='ts-2026-05-18' AND retired_at IS NOT NULL"
```

On the next sweep each replica calls `RotatingVerifier.RetireExpired()`, the previous-kid entry leaves the key set, and the verifier emits a second `platform.jwt_signing_key_rotated` audit row with `transition=retire_previous` and `from_key_id=<old kid>` (no successor — `to_key_id` is empty). The JWK Set drops the old kid from its `keys` array immediately.

A token still signed under the retired kid now fails verification with reason `key_retired`. Long-lived sessions that survived the overlap window receive `401 token_expired_or_revoked` and the client refreshes against `/v1/oauth/token` to obtain a new-key token.

## Verification

### Step 1 — Confirm the JWK Set carries only the new kid

<!-- access: kubectl requires=cluster-access -->
```bash
curl -fsS https://lenny-gateway.lenny-system.svc/.well-known/jwks.json | \
  jq '.keys | length'
```

The result is `1` once the previous kid is retired. A higher count means either the retire step did not run on every replica, or another rotation has since started.

### Step 2 — Confirm token verification under the new key

<!-- access: kubectl requires=cluster-access -->
```bash
TOKEN=$(curl -fsS -H "Authorization: Bearer $BOOTSTRAP_TOKEN" \
  -d 'grant_type=urn:ietf:params:oauth:grant-type:token-exchange&...' \
  https://lenny-gateway.lenny-system.svc/v1/oauth/token | jq -r .access_token)
echo "$TOKEN" | cut -d. -f1 | base64 -d | jq '.kid'
```

The decoded JOSE header's `kid` field equals the new kid. A different value means the local `lenny-ctl` cache is stale; refresh it and retry.

### Step 3 — Confirm the audit rows landed

<!-- access: kubectl requires=cluster-access -->
```bash
psql "$LENNY_POSTGRES_DSN" -c "
  SELECT seq, event_type, payload->>'transition' AS transition, payload->>'from_key_id' AS from_kid
  FROM audit_log
  WHERE tenant_id='platform' AND event_type='platform.jwt_signing_key_rotated'
  ORDER BY seq DESC LIMIT 5"
```

Two rows correspond to this rotation: one with `transition=promote_current` and one with `transition=retire_previous`. The platform audit chain stays `verified` per the §11.7 verifier; an `audit verify` job that reports a break against these rows indicates a wider audit-pipeline incident, not a rotation-specific fault.

## Escalation

Escalate to the platform on-call rotation when:

- A `Rotate` call returns an error other than the expected `rotation_invalid` cases (nil next, empty kid, kid collision). The verifier's key set is unchanged; investigate the wrapping path or the KMS provider before retrying.
- A retired-kid token continues to verify past the overlap window. The verifier or the deployed binary may be ahead of the published key set; cross-reference the replica's `--jwks-publish` log line and confirm `RetireExpired` reports the expected count.
- A `key_retired` rejection arrives at the gateway while the overlap window is open. The verifier clock or the durable rotation record may be out of sync with the wall clock; check `lenny_time_drift_seconds` and the §13.3 NTP posture.

## Spec references

- §10.3 mTLS PKI (key-overlap window default 24h).
- §13.3 Credential Flow (JWK Set publication, `platform.jwt_signing_key_rotated` audit event).
- §11.7 audit hash chain (`platform.*` events land on OCSF apiActivity 6003).
