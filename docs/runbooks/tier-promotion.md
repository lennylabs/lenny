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

## When to run

Run the promotion before flipping production traffic to the upgraded release. The `lenny-tier-promote` CLI runs the gate set in `pkg/tierpromotion` against the live cluster; a failing gate aborts the procedure and reports the offending check.

## Pre-checks (run before the upgrade)

1. Confirm the cluster context and namespace.

       kubectl config current-context
       kubectl get ns lenny-system

2. Run the promotion gates against the current release.

       lenny-tier-promote validate \
         --from tier1 \
         --to tier2 \
         --namespace lenny-system

   The validator checks chart-values diff, deployed-replicas, persistent-storage class, secret-encryption posture, audit-retention, and §13.1/§13.2 admission-webhook coverage. A `FAIL` on any check aborts the promotion; the diagnostic detail identifies the offending resource.

3. Take a fresh backup via `POST /v1/admin/backups` (see [backup-and-restore.md](backup-and-restore.md)). Record the backup id; the promotion procedure rolls back to this snapshot on a Phase 4 failure.

## Upgrade

4. Apply the target tier's preset values file.

       helm upgrade lenny charts/lenny \
         --namespace lenny-system \
         --values charts/lenny/presets/values-tier2.yaml \
         --wait --timeout 10m

   The chart's pre-install Job (`lenny-preflight`) runs the §17.9 admission-plane checks before the upgrade proceeds. A non-zero exit from the Job aborts the upgrade with the rendered manifests un-applied.

5. Wait for the rolling restart to complete.

       kubectl rollout status -n lenny-system deployment/lenny-gateway
       kubectl rollout status -n lenny-system deployment/lenny-ops

## Post-checks (run after the upgrade)

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

## Rollback

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

Tier 3 promotion additionally enables the §10.3 mTLS PKI, the §11.7 SIEM forwarder, and the §25.11 backup-retention extension to 90 days. Run `tier-promotion validate --from tier2 --to tier3` to enumerate the additional gates.
