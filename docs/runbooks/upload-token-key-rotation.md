---
layout: default
title: "upload-token-key-rotation"
parent: "Runbooks"
triggers: []
components:
  - gateway
symptoms:
  - "an upload-token signing key may be exposed and must be replaced"
  - "the gateway memory, the database credentials, or the KMS grant that protects the upload-token keys was compromised"
  - "a key-management policy requires replacing the upload-token signing keys before their scheduled rotation"
tags:
  - upload-token
  - signing-key
  - kms
  - key-rotation
requires:
  - cluster-access
  - admin-api
related:
  - jwt-key-rotation
  - credential-revocation
  - kms-unavailable
---

# upload-token-key-rotation

This runbook replaces the shared signing keys of the upload token outside their scheduled rotation. The reader is a platform operator with cluster access and a `platform-admin` token for the admin API. [Security](../operator-guide/security.html#exposure-of-an-upload-token-key) describes what an exposed key allows and why the procedure rotates twice.

## How the gateway holds the upload-token keys

Every gateway replica signs and verifies upload tokens with the keys of the `upload-token` purpose in the Postgres table `platform_signing_keys`. Each key's secret is envelope-encrypted under the KMS key that `platform:upload-token-signing` names, and a replica holds the plaintext only in memory. A scheduled rotation writes the next key one publish lead (`--signing-key-publish-lead`, default `2m`) ahead of its activation, every replica loads it on its next refresh (`--signing-key-refresh-interval`, default `30s`), and the replaced key keeps verifying for its verification time (ten minutes at the defaults) after the next key becomes active.

The gateway accepts on a session only the token it issued at the session's creation: the session row records the token's digest, and any other token is refused with `401 UPLOAD_TOKEN_INVALID`. A holder of an exposed key can therefore use only tokens that key issued, until each expires or is consumed.

## Trigger

- The memory of a gateway replica, the gateway's database credentials, or the `Decrypt` grant on the KMS key that `platform:upload-token-signing` names is suspected compromised.
- A key-management policy requires replacing the keys at once.

## Diagnosis

### Step 1: Read the current keys

<!-- access: kubectl requires=cluster-access -->
```bash
DSN=$(kubectl -n lenny-system get secret lenny-datastore-conn \
  -o jsonpath='{.data.postgres-dsn}' | base64 -d)
kubectl -n lenny-system run -it --rm pg-client --image=postgres:16 --restart=Never -- \
  psql "$DSN" -c \
  "SELECT rotation_slot, key_id, active_from, retire_after, delete_after, forced
     FROM platform_signing_keys WHERE purpose = 'upload-token' ORDER BY rotation_slot"
```

The gateway image carries no Postgres client, so the command starts a short-lived client pod with the DSN that the chart's `lenny-datastore-conn` Secret holds. Any Postgres client that reaches the gateway's database runs the same query. The newest row is the active key, or the next key when its `active_from` is in the future. Rows with a `retire_after` are replaced keys that still verify until `delete_after`. `forced` is true for a key that a forced rotation wrote. Note the newest `rotation_slot`; each rotation adds one.

## Remediation

### Step 1: Close the access that exposed the key

A rotation helps only after the exposure ends, because a holder of the same access reads the keys written after it. Replace the compromised gateway pods or nodes, rotate the gateway's database credentials, or revoke the `Decrypt` grant the holder used, as the exposure requires. Do not delete rows of `platform_signing_keys`: deleting the newest row refuses every token it signed and stops session creation on each replica until the replacement key becomes active.

### Step 2: Force two rotations through the admin API

<!-- access: api method=POST path=/v1/admin/upload-token-keys/rotate -->
```bash
curl -sS -X POST "$LENNY_GATEWAY_URL/v1/admin/upload-token-keys/rotate" \
  -H "Authorization: Bearer $LENNY_ADMIN_TOKEN" | jq '.'
# Wait until the activeFrom of the first answer has passed (one publish lead, 2m by default).
sleep 120
curl -sS -X POST "$LENNY_GATEWAY_URL/v1/admin/upload-token-keys/rotate" \
  -H "Authorization: Bearer $LENNY_ADMIN_TOKEN" | jq '.'
```

Each accepted call writes the next key one publish lead ahead and answers with its `keyId`, `rotationSlot`, and `activeFrom`, and a refused call answers with an `error` object; the second call's `rotationSlot` is one above the first. The gateway refuses a forced rotation while the newest key is a forced rotation's key that is not active yet, so make the second call at or after the first answer's `activeFrom`. A call made earlier answers `409 INVALID_STATE_TRANSITION` with `details.keyId` and `details.activeFrom` of the pending key and writes nothing; repeat it at or after that `activeFrom`. When the first call already answers `409`, another caller forced a rotation within the last publish lead, possibly before the access was closed: look up the key's `platform.upload_token_key_rotated` event (Verification Step 1) to find the caller, make the first call at that `activeFrom`, and count the two rotations from the first accepted call. When `--signing-key-publish-lead` is set above `2m`, wait that long instead. A forced rotation writes after the newest key, including a key that a scheduled rotation published ahead of its activation, and retires that key when its own key becomes active, so the first call already replaces every key written before it. The second call keeps this path the same as the rotation-interval path below, where only the second rotation replaces a key that was pending at the time of the exposure, and it replaces the first call's key in case the access was not yet closed when the first call ran. A `503 SERVICE_UNAVAILABLE` answer means the gateway could not reach Postgres or the KMS; resolve that through [postgres-unavailable](postgres-unavailable.html) or [kms-unavailable](kms-unavailable.html) and retry.

### Step 3: Without the admin API, roll the gateways with a short rotation interval

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl -n lenny-system set env deploy/lenny-gateway LENNY_UPLOAD_TOKEN_KEY_ROTATION_INTERVAL=5m
# After two rotations, remove the override so the default of 24h applies again.
kubectl -n lenny-system set env deploy/lenny-gateway LENNY_UPLOAD_TOKEN_KEY_ROTATION_INTERVAL-
```

Use this path only when the admin API is unavailable. The chart does not set `--upload-token-key-rotation-interval`, so the environment variable `LENNY_UPLOAD_TOKEN_KEY_ROTATION_INTERVAL` sets it, and each `kubectl set env` rolls the gateway. A replica writes the next key when the active key has signed for the interval, so the first rotation follows within one refresh interval of the rollout when the active key is older than five minutes, and the second follows five minutes after the first new key becomes active. A key that was pending at the time of the exposure becomes active on this path, and the second rotation replaces it. Wait until the newest `rotation_slot` from Diagnosis Step 1 is two above the slot you noted, then remove the override.

## Verification

### Step 1: Confirm the rotations in the audit log

<!-- access: api method=GET path=/v1/admin/audit-events -->
```
GET /v1/admin/audit-events?eventType=platform.upload_token_key_rotated&limit=10
```

Each forced rotation appears as a `platform.upload_token_key_rotated` event with the caller, `key_id`, `rotation_slot`, and `active_from`. A rotation made by a short rotation interval writes no audit event; confirm it with Diagnosis Step 1.

### Step 2: Wait out the verification time

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl -n lenny-system run -it --rm pg-client --image=postgres:16 --restart=Never -- \
  psql "$DSN" -c \
  "SELECT count(*) FROM platform_signing_keys
     WHERE purpose = 'upload-token' AND rotation_slot <= <noted slot> + 1"
```

Replace `<noted slot>` with the slot from Diagnosis Step 1, and set `DSN` as Diagnosis Step 1 does. The count reaches zero when the gateway has deleted every key that was active or published at the time of the exposure, one verification time after the second rotation's key becomes active. Every token those keys signed has then expired. Legitimate sessions keep their tokens throughout, because each replaced key verifies until its tokens expire.

## Escalation

Escalate to the platform on-call rotation when:

- The rotation route answers `503` after Postgres and the KMS are healthy.
- The rotation route answers `409 INVALID_STATE_TRANSITION` after the `details.activeFrom` it names has passed, or names a `details.activeFrom` more than one publish lead in the future, which no forced rotation writes; the newest row of `platform_signing_keys` was then written outside the gateway.
- The keys of slots at or below the noted slot are still present after `delete_after` plus one refresh interval, which means the gateway's sweep is not running.
- Uploads fail with `401 UPLOAD_TOKEN_INVALID` for sessions created after the rotation, which means a replica did not load the new key; check each replica's reachability of Postgres.
