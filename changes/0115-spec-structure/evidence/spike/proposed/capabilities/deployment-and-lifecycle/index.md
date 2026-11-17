---
code: DEPAREA
type: index
area: deployment-and-lifecycle
summary: >-
  Lists the capabilities that install, size, upgrade, back up, and restore a Lenny deployment.
---

# Deployment and lifecycle

The capabilities in this area install, size, upgrade, back up, and restore a Lenny deployment, and run it locally for development.

## Reading paths

- To change what installation creates, read [Installation](installation.md), then [Deployment configuration](../../interfaces/deployment-configuration/index.md).
- To change the upgrade procedure, read [Platform upgrades](platform-upgrades.md), then [Upgrade and compatibility](../../cross-cutting/upgrade-and-compatibility.md).
- To change backups, read [Backup and restore](backup-and-restore.md), then [Backup jobs](../../components/lenny-ops/backup-jobs.md).

## Capabilities

| Page | Code | Summary |
| :-- | :-- | :-- |
| [Installation](installation.md) | INSTALL | States what installation creates, the bootstrap seed, namespace quotas, image registries and air-gapped installs. |
| [Local development](local-development.md) | LOCALDEV | States the local development modes, the embedded stack, its backends, and its guard rails. |
| [Platform upgrades](platform-upgrades.md) | UPGRADE | States the platform upgrade state machine, upgrade checks and release channels, schema migration, CRD upgrades, and configuration diff and apply. |
| [Backup and restore](backup-and-restore.md) | BACKUP | States backup creation, scheduling, retention, verification, restore with its safety checks, and disaster recovery targets. |
| [Capacity tiers](capacity-tiers.md) | TIER | Defines the capacity tiers, the scale each tier targets, and the workload assumptions behind them. |

## Components that carry out this area

- [lenny-ops](../../components/lenny-ops/index.md): The lenny-ops Deployment: purpose, Helm values, Kubernetes resources, RBAC, NetworkPolicy, leader election, startup, self-monitoring, and TLS.
- [Preflight job](../../components/preflight-job.md): The lenny-preflight Job and the checks it runs, each linked to the requirement it verifies.
- [Backup jobs](../../components/lenny-ops/backup-jobs.md): The lenny-backup Jobs that lenny-ops creates: pod specification, ServiceAccount, NetworkPolicy, and bucket policy.

## Interfaces this area exposes

- [Deployment configuration](../../interfaces/deployment-configuration/index.md): The deployment configuration: the Helm values structure, the values schema, chart-rendered resources, and feature gates.
- [Answer files](../../interfaces/deployment-configuration/answer-files.md): Answer files: composition dimensions and the catalog of answer files.
- [Tier presets](../../interfaces/deployment-configuration/tier-presets.md): The tier preset files and the values each tier sets.
- [Install and local](../../interfaces/cli/install-and-local.md): Install, bootstrap, preflight, the installation wizard, the local stack, and image commands.
