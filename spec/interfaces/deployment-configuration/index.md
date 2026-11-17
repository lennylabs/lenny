---
code: DCFG
type: index
summary: >-
  The deployment configuration: the Helm values structure, the values schema, chart-rendered resources, and feature gates.
status: placeholder
---

# Deployment configuration

The deployment configuration: the Helm values structure, the values schema, chart-rendered resources, and feature gates.

## Pages

| Page | Code | Summary |
| :-- | :-- | :-- |
| [Answer files](answer-files.md) | `DCFGANS` | Answer files: composition dimensions and the catalog of answer files. |
| [Bootstrap seed](bootstrap-seed.md) | `DCFGSEED` | The bootstrap seed format that installs runtimes, pools, and tenants before the admin API is reachable. |
| [Tier presets](tier-presets.md) | `DCFGTIER` | The tier preset files and the values each tier sets. |

## Content today

This index owns content of its own as well as the listing above. The sources below hold that content until the page is written.

Numbered sections:

- [§16.9 Prometheus Scrape Targets and CRDs](../../16_observability.md#169-prometheus-scrape-targets-and-crds), in part
- [§17.2 Namespace Layout](../../17_deployment-topology.md#172-namespace-layout), in part
- [§17.6 Packaging and Installation](../../17_deployment-topology.md#176-packaging-and-installation), in part
- [§17.6 Packaging and Installation › Helm `values.schema.json`](../../17_deployment-topology.md#helm-valuesschemajson)
- [§17.8.6 Image Registry and Air-Gap](../../17_deployment-topology.md#1786-image-registry-and-air-gap), in part
- [§25.4 The `lenny-ops` Service › Deployment › Helm Values Hierarchy](../../25_agent-operability.md#helm-values-hierarchy)
- [§25.8 Platform Lifecycle Management › Image Registry Configuration › Helm Values](../../25_agent-operability.md#helm-values), in part
- [§25.13 Bundled Alerting Rules › Helm Rendering](../../25_agent-operability.md#helm-rendering), in part
- [§25.13 Bundled Alerting Rules › Operator Customization Model](../../25_agent-operability.md#operator-customization-model), in part
- [§27.2 Placement and gating](../../27_web-playground.md#272-placement-and-gating), in part
