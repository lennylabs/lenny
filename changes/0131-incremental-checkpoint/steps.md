# Steps: 0131 Incremental checkpoints

## Steps

The specification lane of each step lands on the pages that the page table of "Specification sections affected" in `change.md` names, and the area change of each page's area writes those pages, as the program's "Order" states. A step's code lane follows the specification edits it implements. Open decisions D24 to D39 in `decisions.md` touch these steps, and each names the steps it blocks.

### 0131.S1 Capture in passes
- status: Planned
- aliases: [IC-1]
- lanes: [specification, code, documentation]
- depends-on: [0131.S2, 0131.S3, 0131.S4, 0145.S2, 0145.S3, 0118.S4]

The design is in [IC-1](change.md#ic-1-capture-in-passes). Pre-copy uploads packs of the IC-2 format through the IC-3 grants into the IC-4 catalog, so this step follows 0131.S2 to 0131.S4. Every pass reads a slot's files through the broker of [0145.S2](../0145-session-isolation/steps.md#steps) and runs the capture copy through the asynchronous worker of 0145.S3, and the slot volumes and `lenny-spool` come from [0118.S4](../0118-agent-pod-layout/steps.md#steps). The order against 0153.S4, which builds `capture-copy` and depends on this step, is open decision D27, and the order against 0139.S1, which moves the quiesce frames to `CH-MSGSOCK`, is open decision D28.

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

The design is in [IC-4](change.md#ic-4-catalog-reference-closure-and-quota). Its platform chunk sets need the warm stores of 0147.S8, `platformChunkReuse` of 0150.S2, the pod set keys of 0140.S1, and the warm-up checkout of 0153.S8, and each of those edges would close a cycle through 0135.S4, which depends on this step; open decision D28 settles the order. How the platform tenant under which IC-4 records platform-owned packs exists in Postgres is open decision D38, the counterpart of decision 25 of [`0121-billing-resource-attribution`](../0121-billing-resource-attribution/decisions.md); under its recommended option this step names the migration that provisions the platform tenant and states that the storage reconciliation lists the `/platform/` prefix.

### 0131.S5 Restore, derive, download, and seal
- status: Planned
- aliases: [IC-5]
- lanes: [specification, code, documentation]
- depends-on: [0131.S2, 0131.S4, 0145.S2, 0145.S3]

The design is in [IC-5](change.md#ic-5-restore-derive-download-and-seal). A restore reads packs of the IC-2 format that the IC-4 catalog names, and writes a slot's trees through the broker of 0145.S2. The order against 0153.S4, which builds `restore-complete`, is open decision D27.

### 0131.S6 Retention, garbage collection, erasure, and legal hold
- status: Planned
- aliases: [IC-6]
- lanes: [specification, code, documentation]
- depends-on: [0131.S4]

The design is in [IC-6](change.md#ic-6-retention-garbage-collection-erasure-and-legal-hold). It builds the copy of a pack snapshot and of an output file's packs that the hold worker of [0128.S4](../0128-forensics-evidence-preservation/steps.md#steps) calls, and it calls the `EnsureCopied` and the escrow prefix that 0128.S4 defines, so it lands with or after 0128.S4. That order stays a sentence, because the edge would close a cycle through 0135.S4: 0128.S4 waits in part on 0135.S4, which depends on this step as a whole edge, so with the edge `changes.py check` would report the three steps as waiting on each other through partial dependencies. Open decision D28 settles it.

### 0131.S7 Size limits and drain budgets
- status: Planned
- aliases: [IC-7]
- lanes: [specification, code, documentation]
- closes: [F-4.4.30]

The design is in [IC-7](change.md#ic-7-size-limits-and-drain-budgets). Its **Attempt deadline.** gives the checkpoint timeout a defined start on every path, which closes F-4.4.30. Whichever of EG-2 ([0127.S2](../0127-egress-and-git/steps.md#steps)) and this step lands second keeps the other's rendering and applies its own part; the order stays a sentence, because either order works. Which step builds the agent pod's eviction checkpoint trigger, whose budget this step changes, is open decision D33.

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

The design is in [IC-9](change.md#ic-9-removal-of-the-embedded-sigstop-checkpoint-path). It depends on no other step of this change and can land first. It owns the removal of `pkg/adapter/embeddedcheckpoint` and of the `embedded` value of `pkg/checkpoint`'s `Level`, and [0118.S1](../0118-agent-pod-layout/steps.md#steps), the removal of the embedded deployment model under PL-2, depends on it.

## Validation owed

The list is "Validation owed" under "Benchmarks" in `change.md`. Each item gates the step whose design it measures:

| Item in `change.md` | Waits on it |
| :-- | :-- |
| 1. Pods of 2 and 4 slots with and without the checkpoint stagger, and concurrent restores | 0131.S1 and 0131.S5 |
| 2. The parallel stat walk with the tracker on a pod with 2 slots, with hostile renames, deletions, and link swaps | 0131.S1 |
| 3. `walkFallbackConcurrency` on Kata's shared file system | 0131.S1 |
| 4. The 11.0 s `FICLONE` of a 4 GiB file under writes, and the clone term of the pause estimate | 0131.S1 |
| 5. The spool headroom on gVisor's default mount | 0131.S7 |
| 6. A frozen export in copy mode under gVisor of a file the runtime rewrites | 0131.S3 |
| 7. FIFOs on gVisor's default mount as metadata-only entries | 0131.S1 and 0131.S5 |
| 8. The tracker's watch budget under runc with user namespaces | 0131.S8 |
| 9. The capture copy, `restore-complete`, and a running repack through the asynchronous worker | 0131.S1 and 0131.S5 |
| 10. Restore with large memory limits, packs in flight, Kata's metadata cache mode, and the first checkpoint on local NVMe | 0131.S5 |
| 11. The disk-backed `/session` tree | 0131.S1 and 0131.S5 |
| 12. Identical chunk IDs from two pods staging the same commit | 0131.S4 |
| 13. The first checkpoint of a session that adopted a warm-up checkout | 0131.S4 |
| 14. The adapter's memory for the contributions' chunk lists | 0131.S4 |
| 15. The time a contribution adds before READY | 0131.S4 |
| 16. The owner's test on pods with several runtimes | Every step: the owner required it before the design is final (`decisions.md`, "Owner's direction", 2026-11-13) |
| 17. Decision D11's margin | 0131.S1 |
