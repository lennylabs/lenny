---
proposal: 0079_fix_name-who-starts-the-next-sessions-runtime-on-a-recycled-pod
title: Name who starts the next session's runtime on a recycled pod
kind: fix
status: Draft
drafted-date: 2026-08-25
drafted-by: 
reviewed-date: 
reviewed-by: 
approved-date: 
approved-by: 
implemented-date: 
implemented-by: 
---

## Review history

An adversarial review run executed on 2026-09-30. The spec loop ran six rounds and did not converge. The run fixed five findings. Two full-pool sweeps were performed during the spec loop. The non-spec loop was not run. Because the spec loop did not converge, open decisions the review examined remain unsettled.

## Original status block

# Proposal: Name who starts the next session's runtime on a recycled pod

- **Scope:** Closes BUILD-GAPS F-5.2.33 part (b). No component creates a second runtime process in a sidecar pod, so the runtime process is kept for the pod's life and a later session binds to it as a slot. This proposal stops the adapter from ending the runtime at occupancy zero, reports at the recycle boundary whether the runtime can serve the next session, gates process reuse on the existing process-level isolation acknowledgment, keeps a kept runtime to one tenant, removes the occupancy-zero drain frame, and states the lifetime and the scrub's reach in the specification. Part (a) is proposal 0078, whose pod-scope listener teardown this proposal extends.
