---
id: BILLEV-I1
status: open
kind: build-gap
severity: medium
requirements: []
---

# Tokens used after a session's last periodic checkpoint are never billed

## Specification

§11.2.1 states that billing events are written for every billable unit and are never deferred, batched, or dropped. Token usage reaches billing through the periodic `token_usage.checkpoint` deltas.

## Evidence

- `pkg/billing/billingcheckpoint/billingcheckpoint.go`: the checkpointer emits `token_usage.checkpoint` deltas on an interval (default 300 s, `--billing-token-checkpoint-interval-seconds`).
- `cmd/lenny-gateway/worker_seams.go` (`billingSessionLister`, about lines 158 to 160) lists only sessions that are not terminal, so a session that ends between two checkpoints is never checkpointed again.
- The `session.created` and `session.completed` events carry no token counts (`billingcheckpoint.go` package doc).
- Found by the analysis of change 0176 open decision O2 on 2026-11-26.

## Gap

The tokens a session uses after its last periodic checkpoint, up to one interval's worth, reach no billing event, so every session that ends is under-billed by its final partial interval.

## Suggested resolution

Emit a final `token_usage.checkpoint` delta in the terminal pipeline before the session is listed as terminal, or have the lister include sessions that ended since the previous tick, with a test that a session ending mid-interval bills all its tokens.
