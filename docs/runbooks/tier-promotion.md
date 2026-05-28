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

The pre-promotion gates verify that the source posture is healthy and that the target posture is reachable. Each check below maps to one `lenny-tier-promote validate` rule; a `FAIL` identifies the offending resource.

1. Confirm the cluster context and namespace.

       kubectl config current-context
       kubectl get ns lenny-system

2. Run the promotion gates against the current release.

       lenny-tier-promote validate \
         --from tier1 \
         --to tier2 \
         --namespace lenny-system

   The validator checks chart-values diff, deployed-replicas, persistent-storage class, secret-encryption posture, audit-retention, §13.1/§13.2 admission-webhook coverage, the autoscaling provider (§17.8.3 line 1285), the SCL-036 burst-absorption floor (§17.8.2 line 950), and the Phase 13.5 attestations required by §17.8.3 (LLM Proxy extraction ratio, gateway GC pause, `maxSessionsPerReplica` calibration). A `FAIL` on any check aborts the promotion; the diagnostic detail identifies the offending resource.

3. Take a fresh backup via `POST /v1/admin/backups` (see [backup-and-restore.md](backup-and-restore.md)). Record the backup id; the promotion procedure rolls back to this snapshot on a Phase 4 failure.

## Remediation

The upgrade applies the target tier preset, waits for the rolling restart, and re-runs the gates to confirm the promoted posture. A failed post-check rolls back to the pre-promotion release.

### Upgrade

4. Apply the target tier's preset values file.

       helm upgrade lenny charts/lenny \
         --namespace lenny-system \
         --values charts/lenny/presets/values-tier2.yaml \
         --wait --timeout 10m

   The chart's pre-install Job (`lenny-preflight`) runs the §17.9 admission-plane checks before the upgrade proceeds. A non-zero exit from the Job aborts the upgrade with the rendered manifests un-applied.

5. Wait for the rolling restart to complete.

       kubectl rollout status -n lenny-system deployment/lenny-gateway
       kubectl rollout status -n lenny-system deployment/lenny-ops

### Post-checks (run after the upgrade)

6. Run the same gate set against the promoted release.

       lenny-tier-promote validate \
         --from tier1 \
         --to tier2 \
         --namespace lenny-system \
         --post-upgrade

   The post-upgrade pass confirms the replica counts, the admission webhook inventory, and the storage class match the target tier.

7. Smoke-test the admin API and an end-to-end session.

       lenny-ctl tenants list
       lenny-ctl session start --tenant default --runtime echo

8. Confirm the new alert posture is wired by checking that the Prometheus rules for the target tier are loaded.

       kubectl get prometheusrule -n lenny-system

### Rollback

If a post-check fails, roll back to the pre-promotion release.

9. Revert the chart values.

       helm rollback lenny --namespace lenny-system

10. Verify the rollback restored the prior posture.

        lenny-tier-promote validate \
          --from tier2 \
          --to tier1 \
          --namespace lenny-system \
          --post-upgrade

11. If the underlying Postgres or admission-plane state is inconsistent after a rollback, restore from the backup recorded in step 3 (see [backup-and-restore.md](backup-and-restore.md)).

## Notes

The promotion is a one-way operation in the sense that tier 1's single-tenant defaults do not satisfy tier 2's multi-tenant invariants. A 2→1 demotion is supported by the same CLI for incident recovery but requires acknowledgment of the data-loss implications (`--acknowledge-demotion`).

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
