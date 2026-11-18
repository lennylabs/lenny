---
id: BACKUP-I1
status: open
kind: build-gap
severity: medium
requirements: []
---

# The configuration export saves a pool's warm targets and not its settings

## Specification

§25.11 (Backup and Restore API), "Backup Execution", **Full backup** flow, step 2: the backup Job "Exports platform configuration (runtimes, pools, tenants, quotas) as JSON."

## Evidence

- `pkg/ops/backup/runner/export.go`, `exportPools`: each pool is exported as `poolConfig`, which holds `name`, `minWarm`, `maxWarm`, and `sdkWarmDisabled` only.
- Change 0137.S5 merged the template and the warm pool into one PodSet per pool. The PodSet the export lists now also carries the pool settings, such as `runtimeRef`, `isolationProfile`, `egressProfile`, `deliveryMode`, `resourceClass`, `sessionPolicy`, and `topologySpreadConstraints`, and the export leaves them out. Before the merge the export read the warm pool alone and did not read the template either, so the gap predates the merge.
- `pkg/ops/backup/runner/export_test.go` pins the exported fields at name, minWarm, and maxWarm.

## Gap

A restore from the configuration archive brings back a pool's warm targets without its runtime, isolation profile, egress, credential delivery, resource class, or session policy. The pool records in Postgres hold these settings, and the PodSet is derived from them, so the export reads the derived object and omits most of what defines the pool.

## Suggested resolution

Decide which carrier the configuration export reads for pools: the Postgres pool records the admin API writes, which are the source of truth (§4.6.2), or the whole PodSet spec. State the exported pool fields when the backup and restore page lands, and extend `exportPools` and its test to match.
