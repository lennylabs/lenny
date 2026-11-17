# Steps: 0131 Incremental checkpoints

## Steps

### 0131.S1 Capture in passes
- status: Planned
- aliases: [IC-1]
- lanes: [specification, code, documentation]

The design is in [IC-1](change.md#ic-1-capture-in-passes).

### 0131.S2 Storage format
- status: Planned
- aliases: [IC-2]
- lanes: [specification, code, documentation]

The design is in [IC-2](change.md#ic-2-storage-format).

### 0131.S3 Checkpoint stream
- status: Planned
- aliases: [IC-3]
- lanes: [specification, code, documentation]

The design is in [IC-3](change.md#ic-3-checkpoint-stream).

### 0131.S4 Catalog, reference closure, and quota
- status: Planned
- aliases: [IC-4]
- lanes: [specification, code, documentation]

The design is in [IC-4](change.md#ic-4-catalog-reference-closure-and-quota).

### 0131.S5 Restore, derive, download, and seal
- status: Planned
- aliases: [IC-5]
- lanes: [specification, code, documentation]

The design is in [IC-5](change.md#ic-5-restore-derive-download-and-seal).

### 0131.S6 Retention, garbage collection, erasure, and legal hold
- status: Planned
- aliases: [IC-6]
- lanes: [specification, code, documentation]

The design is in [IC-6](change.md#ic-6-retention-garbage-collection-erasure-and-legal-hold).

### 0131.S7 Size limits and drain budgets
- status: Planned
- aliases: [IC-7]
- lanes: [specification, code, documentation]

The design is in [IC-7](change.md#ic-7-size-limits-and-drain-budgets). Whichever of EG-2 ([0127.S2](../0127-egress-and-git/steps.md#steps)) and this step lands second keeps the other's rendering and applies its own part.

### 0131.S8 Checkpoint policy, interval, and change tracker
- status: Planned
- aliases: [IC-8]
- lanes: [specification, code, documentation]
- depends-on: [0131.S1]

The design is in [IC-8](change.md#ic-8-checkpoint-policy-interval-and-change-tracker). The interval override lands together with 0131.S1 or after it, and never before it.

### 0131.S9 Removal of the embedded SIGSTOP checkpoint path
- status: Planned
- aliases: [IC-9]
- lanes: [specification, code, documentation]

The design is in [IC-9](change.md#ic-9-removal-of-the-embedded-sigstop-checkpoint-path). It depends on no other step of this change and can land first. It lands together with the removal of the embedded deployment model under PL-2 (0118.S1) or before it.
