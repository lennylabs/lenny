---
title: Pools
spec: [POOL, CLIPOOL]
---

# Pools

A pool keeps `minWarm` idle pods (default `2`). The claim hold is `pool.claimHoldTTL`, which defaults to `30s`.

Create one with `lenny-ctl pool create --name alpha`. Watch `lenny_warmpool_idle_pods`, the histogram series `lenny_warmpool_claim_duration_seconds_bucket`, and the `WarmPoolLow` alert. A pod carries `lenny.dev/pool=alpha`, and a drained pool emits `dev.lenny.pool_drained`. A claim can fail with the `WARM_POOL_EXHAUSTED` error.

| Field | Type | Default | Unit | Description |
|:------|:-----|:--------|:-----|:------------|
| `minWarm` | integer | `2` | pods | The idle pods a pool keeps. |
| `pool.claimHoldTTL` | duration | 0.5m | seconds | How long a claim holds a pod. |
| `recycle.mode` | enum | never | -- | Whether a pod serves a second session. |

| Limit | Value | Unit |
|:------|:------|:-----|
| `maxPodsPerPool` | 10000 | pod |

Values a gate cannot compare are reported as undecided: `minWarm` quotes several defaults in the next table.

| Field | Default |
|:------|:--------|
| `minWarm` | 2 (small), 5 (large) |

Names from areas that have not migrated are undecided: `lenny_legacy_requests_total`, `dev.lenny.legacy_event`, `lenny-ctl legacy sweep`, and the `LegacyAlert` alert. A name only its context classifies, such as the `UnknownThing` alert, is undecided as well.

```yaml
# A fenced example is not read.
metric: lenny_not_in_any_catalog
```

<!-- A comment is not read: `lenny_commented_out_metric`. -->
