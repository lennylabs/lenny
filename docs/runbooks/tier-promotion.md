---
layout: default
title: "tier-promotion"
parent: "Runbooks"
triggers: []
components:
  - lenny-tier-promote
  - chart
  - admission
symptoms:
  - "preparing to upgrade an installation from tier 1 to tier 2"
  - "preparing to upgrade an installation from tier 2 to tier 3"
  - "validating the post-upgrade posture of a promoted installation"
tags:
  - tier-promotion
  - chart
  - presets
  - admission
  - posture
requires:
  - cluster-access
  - helm-cli
related:
  - jwt-key-rotation
  - ca-rotation
  - backup-and-restore
---

# tier-promotion

Procedure to promote a Lenny installation from tier 1 (single-tenant dev) to tier 2 (multi-tenant prod) or from tier 2 to tier 3 (multi-tenant enterprise) per §17.8.3. Each promotion changes the chart preset, raises the replica counts, and enables additional admission-plane controls; the pre-checks confirm the source posture is healthy and the post-checks confirm the promoted posture is in force.

## Trigger

Run the promotion before flipping production traffic to the upgraded release. This runbook applies when:

- preparing to upgrade an installation from tier 1 to tier 2
- preparing to upgrade an installation from tier 2 to tier 3
- validating the post-upgrade posture of a promoted installation

The `lenny-tier-promote` CLI runs the gate set in `pkg/tierpromotion` against the live cluster; a failing gate aborts the procedure and reports the offending check.

## Diagnosis

The pre-promotion gates verify that the source posture is healthy and that the target posture is reachable. Each check below maps to one `lenny-tier-promote` rule; a `FAIL` identifies the offending resource. The CLI takes `--from` and `--to` directly; it has no `validate` subcommand.

### Step 1 — Confirm the cluster context and namespace

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl config current-context
kubectl get ns lenny-system
```

### Step 2 — Run the promotion gates against the current release

<!-- access: cluster requires=helm-cli -->
```bash
lenny-tier-promote \
  --from tier1 \
  --to tier2 \
  --namespace lenny-system
```

The validator checks chart-values diff, deployed-replicas, persistent-storage class, secret-encryption posture, audit-retention, §13.1/§13.2 admission-webhook coverage, the autoscaling provider (§17.8.3 line 1285), the SCL-036 burst-absorption floor (§17.8.2 line 950), and the Phase 13.5 attestations required by §17.8.3 (LLM Proxy extraction ratio, gateway GC pause, `maxSessionsPerReplica` calibration). A `FAIL` on any check aborts the promotion; the diagnostic detail identifies the offending resource.

### Step 3 — Take a fresh backup

<!-- access: api method=POST path=/v1/admin/backups -->
```
POST /v1/admin/backups
```

Take a fresh backup (see [backup-and-restore.md](backup-and-restore.md)). Record the backup id; the promotion procedure rolls back to this snapshot on a Phase 4 failure.

## Remediation

The upgrade applies the target tier preset, waits for the rolling restart, and re-runs the gates to confirm the promoted posture. A failed post-check rolls back to the pre-promotion release.

### Step 4 — Apply the target tier's preset values file

<!-- access: cluster requires=helm-cli -->
```bash
helm upgrade lenny charts/lenny \
  --namespace lenny-system \
  --values charts/lenny/presets/values-tier2.yaml \
  --wait --timeout 10m
```

The chart's pre-install Job (`lenny-preflight`) runs the §17.9 admission-plane checks before the upgrade proceeds. A non-zero exit from the Job aborts the upgrade with the rendered manifests un-applied.

### Step 5 — Wait for the rolling restart to complete

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl rollout status -n lenny-system deployment/lenny-gateway
kubectl rollout status -n lenny-system deployment/lenny-ops
```

### Step 6 — Re-run the gate against the promoted release

<!-- access: cluster requires=helm-cli -->
```bash
lenny-tier-promote \
  --from tier1 \
  --to tier2 \
  --namespace lenny-system
```

The gate reads the live cluster, so running it after the upgrade confirms that the replica counts, the admission-webhook inventory, the storage class, and the §17.8.3 attestations now match the target tier.

### Step 7 — Smoke-test the admin API and an end-to-end session

<!-- access: lenny-ctl -->
```bash
lenny-ctl admin tenants list
```

<!-- access: api method=POST path=/v1/sessions -->
```bash
curl -fsS -X POST https://<gateway-host>/v1/sessions \
  -H "Authorization: Bearer <token>" \
  -d '{"runtimeRef":"echo","tenantId":"default"}'
```

### Step 8 — Confirm the new alert posture is wired

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl get prometheusrule -n lenny-system
```

Confirm that the Prometheus rules for the target tier are loaded.

### Step 9 — Roll back on a failed post-check

<!-- access: cluster requires=helm-cli -->
```bash
helm rollback lenny --namespace lenny-system
```

If a post-check fails, revert the chart values with the command above.

### Step 10 — Verify the rollback restored the prior posture

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl get deployment -n lenny-system \
  -l app.kubernetes.io/part-of=lenny \
  -o custom-columns=NAME:.metadata.name,REPLICAS:.spec.replicas
```

<!-- access: cluster requires=helm-cli -->
```bash
helm get values lenny -n lenny-system
```

The promotion gate validates promotions only and rejects a target tier below the source, so confirm the revert directly against the cluster rather than re-running the gate in the demotion direction. If the underlying Postgres or admission-plane state is inconsistent after a rollback, restore from the backup recorded in Step 3 (see [backup-and-restore.md](backup-and-restore.md)).

## Notes

The promotion is a one-way operation in the sense that tier 1's single-tenant defaults do not satisfy tier 2's multi-tenant invariants. The promotion gate validates promotions only and rejects a request whose target tier is below the source (`pkg/tierpromotion` errors on a demotion). A tier-2-to-tier-1 demotion for incident recovery is handled by `helm rollback` to the prior release (step 9) followed by a restore from the backup recorded in step 3, not by the gate.

Tier 3 promotion additionally enables the §10.3 mTLS PKI, the §11.7 SIEM forwarder, and the §25.11 backup-retention extension to 90 days. The §17.8.3 line 1285 NO-GO criterion makes KEDA mandatory at Tier 3; the validator rejects a chart that still renders `autoscaling.provider: hpa`. Before invoking the Tier 3 gate, run the Phase 13.5 benchmark harness and attest each result on the CLI:

    lenny-tier-promote \
      --from tier2 --to tier3 \
      --namespace lenny-system \
      --chart-values-tier tier3 \
      --audit-retain-days 90 \
      --secret-encryption-verified \
      --autoscaling-provider keda \
      --min-replicas 5 \
      --max-sessions-per-replica 400 \
      --llm-proxy-extraction-attested \
      --gc-pause-attested \
      --max-sessions-per-replica-calibrated

The three attestation flags correspond one-to-one with the §17.8.3 Step 1 benchmarks: `--llm-proxy-extraction-attested` (line 1263), `--gc-pause-attested` (line 1264), and `--max-sessions-per-replica-calibrated` (line 1265).
