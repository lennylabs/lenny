---
id: CLAIM-I5
kind: build-gap
severity: medium
requirements: [CLAIM-R4, CLAIM-R13, CLAIM-R14]
change: null
legacy: null
---

# The Postgres fallback and the pod state mirror ignore the claim set

CLAIM-R13 requires the Postgres fallback to take an idle row whose `pod_set` is in the claim set, and to measure mirror lag over the claim set's pod sets. CLAIM-R14 requires each mirror row to record its pod set.

The `agent_pod_state` table (`migrations/0001_initial_schema.up.sql`) has no `pod_set` column, and the fallback in `pkg/gateway/podlifecycle/podsession/fallbackclaim.go` takes its row through `pkg/agentpodstate/pgstore/pgstore.go`, whose row lock and lag query select by `pool_id` alone. The claimer's claim set (`pkg/gateway/core/podclaimer/claimset.go`) is not passed to the fallback, so a fallback acquisition can take a pod of another pod hash once pod sets exist.

The fix adds the column and its writes in the WarmPoolController's mirror, passes the claim set from the claimer to the fallback (for example on `NoIdlePodError`, which already carries the refused pods to it), and filters the row lock's `SELECT` by it. It depends on CLAIM-I4.
