---
code: CKPT
type: capability
area: sessions
summary: >-
  States when a checkpoint is taken, how capture runs in passes, quiesce and abort, size limits and drain budgets, and the checkpoint capability model.
status: placeholder
---

# Checkpoint capture

This page is a placeholder. The sources under "Content today" hold its content until the page is written.

## Content today

Numbered sections:

- [§4.4.2 Checkpoint Atomicity and Partial Manifests](../../../04_system-components.md#442-checkpoint-atomicity-and-partial-manifests), in part
- [§4.4.3 Checkpoint Quiescence, Timeout, and Abort Handling](../../../04_system-components.md#443-checkpoint-quiescence-timeout-and-abort-handling), in part
- [§4.4.4 Checkpoint SLOs and Scheduling](../../../04_system-components.md#444-checkpoint-slos-and-scheduling), in part
- [§4.4.5 Checkpoint Storage Failure and Eviction Fallback](../../../04_system-components.md#445-checkpoint-storage-failure-and-eviction-fallback), in part
- [§4.7.2 Checkpoint and Interrupt Mutual Exclusion](../../../04_system-components.md#472-checkpoint-and-interrupt-mutual-exclusion), in part
- [§13.2.3 Checkpoint Capability Model](../../../13_security-model.md#1323-checkpoint-capability-model), in part
- [§13.3 Credential Flow](../../../13_security-model.md#133-credential-flow), in part
- [§16.5 Alerting Rules and SLOs](../../../16_observability.md#165-alerting-rules-and-slos), in part

Changes of the v1 program whose content lands on this page:

- [0131-incremental-checkpoint](../../../../changes/0131-incremental-checkpoint/change.md): Requirements, Security properties, IC-3, IC-8
- [0145-session-isolation](../../../../changes/0145-session-isolation/change.md): SI-6
- [0153-workspace-materialization](../../../../changes/0153-workspace-materialization/change.md): WM-11
