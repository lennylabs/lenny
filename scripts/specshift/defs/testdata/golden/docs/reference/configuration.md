---
title: Configuration Reference
---

# Configuration Reference

Lists the settings an operator sets.

## Gateway configuration

| Field | Type | Default | Description | Validation |
|:------|:-----|:--------|:------------|:-----------|
| `gateway.handWritten` | int | 5 | A row that stays hand-written until its area lands. | Must be > 0. |

<!-- generated:reference-settings -->

## Pools configuration

| Field | Type | Default | Unit | Scopes | Read by | Description | Validation |
| :-- | :-- | :-- | :-- | :-- | :-- | :-- | :-- |
| `minWarm` | integer | `0`; by capacity tier: `5` (tier1), `50` (tier2), `500` (tier10) |  | pool | WarmPoolController, Pod claimer | The number of idle pods a pool keeps. | From `0` to `10000`. |
| `recycle.mode` | enum | `never` |  | pool | WarmPoolController | Whether a pod serves a second session. | One of `never`, `always`. |

<!-- /generated:reference-settings -->

## Capacity sizing

Text after the list stays.
