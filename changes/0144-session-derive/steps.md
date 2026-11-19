# Steps: 0144 Session derive

## Steps

### 0144.S1 One route
- status: Planned
- aliases: [SD-1]
- lanes: [specification, code, documentation]
- closes: [F-15.1.76, F-15.1.77, F-15.1.78, F-7.3.36, CAPISES-I3]

Everything above.

A plan with no clone depends on no other step. A plan with a clone needs the [egress design](../0127-egress-and-git/change.md)'s pod-side clone, as any `gitClone` does. The withdrawal of SD-P4 rests on setup at the slot UID ([SI D6](../0145-session-isolation/decisions.md#decisions)), on the materialization helper ([`0153-workspace-materialization`](../0153-workspace-materialization/steps.md#steps)), and on proposal AR-D of [`0119-agent-runtimes-only`](../0119-agent-runtimes-only/steps.md#steps), which removes `executionMode` and lands the `registered` check of DR-4 at the post-chain point. The placement checks arrive with [`0136-placement`](../0136-placement/steps.md#steps). "Warm stores on the target", `coveredStores`, and `withheldStores` arrive with [DR-11](../0147-tenant-derived-runtimes/change.md#warm-stores), and "Stops" with [DR-4](../0147-tenant-derived-runtimes/change.md#dr-4-tenant-runtime-records), [DR-6](../0147-tenant-derived-runtimes/change.md#dr-6-base-changes), and [SO-2](../0142-security-oversight/change.md#so-2-tenant-security-record-and-posture). SD-1 also extracts the plan check function that [DR-9](../0147-tenant-derived-runtimes/change.md#dr-9-default-git-clones) calls. The `PreRoute` payload with `kind: derive` comes from the first proposal of [`0141-runtime-routing`](../0141-runtime-routing/steps.md#steps): if SD-1 lands first, that proposal adds `kind: derive` to the derive path, and otherwise SD-1 adopts the payload it landed. Revision selection arrives with [`0140-runtime-revisions`](../0140-runtime-revisions/steps.md#steps), the account link check with [UC-2](../0151-user-credential-delivery/change.md#flow), the refusal of `deferConnectors` with proposal CR-A of [`0124-connector-removal`](../0124-connector-removal/steps.md#steps), and the pack reference and `checkpointing_disabled` with [IC-5](../0131-incremental-checkpoint/change.md#ic-5-restore-derive-download-and-seal) and [IC-8](../0131-incremental-checkpoint/change.md#ic-8-checkpoint-policy-interval-and-change-tracker).

It can land before those designs, and each adds its rule to the checks when it lands.
