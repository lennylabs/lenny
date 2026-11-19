---
id: GWSTREAM-I1
status: open
kind: build-gap
severity: low
requirements: []
---

# The session event bus does not enforce the tenant on every subscription

## Specification

`GWSEAM-Subscribe` on `spec/interfaces/gateway-seams.md` states what the bus enforces: it binds a session to the tenant of the first publish that names one on the serving replica and refuses a later subscription by another non-empty tenant. 0117 decision 42 kept that behavior in the move step and filed the tightening here.

## Evidence

- `pkg/gateway/streamproxy/sessionevents` (moved by 0117.S6): the bus does not refuse a subscription with an empty tenant.
- Several publishers publish without a tenant, so a session whose events so far carry none is not bound: the platform tools seam's `agent_output`, elicitation, and input-expiry events, the lease elicitation, and the eviction bridge's `session.lost`.
- A reader on a replica that never published for the session receives a backlog from the cross-replica relay, whose entries carry no tenant.
- Every route that reaches the bus today checks the tenant first (the session read check and session lookup on the event and log streams, and `Authorize` on the platform tools seam's attach stream), so no client path exploits this; the bus check is defense in depth.

## Gap

The bus's own tenant binding has holes that a future route or seam could reach without a prior tenant check.

## Suggested resolution

Have the untenanted publishers pass the session's tenant, make `Subscribe` refuse an empty tenant from a seam, and have relay entries carry the tenant that `History` checks. 0138.S1 reshapes `GWSEAM-Publish` and is a natural place to do it, or the stream area's change.
