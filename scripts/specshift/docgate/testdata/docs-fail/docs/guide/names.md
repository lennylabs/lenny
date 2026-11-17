---
title: Names
spec: [POOL]
---

# Names

Each name below is defined nowhere: `lenny_bogus_metric_total`, `lenny.dev/bogus`, `dev.lenny.bogus_happened`, and `lenny-ctl pool bogus`.

A pool keeps `minWarm` idle pods (default `3`), and `recycle.mode` defaults to `always`.

| Field | Default | Unit |
|:------|:--------|:-----|
| `minWarm` | `2` | bytes |
| `pool.claimHoldTTL` | 45s | seconds |

| Limit | Value |
|:------|:------|
| `maxPodsPerPool` | 500 |
