---
code: CKIDX
type: index
area: sessions
summary: >-
  Lists the checkpoint pages: capture, storage and retention, and restore.
---

# Checkpoints

Checkpoints are split into capture, storage, and restore. Capture decides when and how a slot's files are written, storage keeps and collects what was written, and restore puts it back into a slot.

## Pages

| Page | Code | Summary |
| :-- | :-- | :-- |
| [Checkpoint capture](checkpoint-capture.md) | CKPT | States when a checkpoint is taken, how capture runs in passes, quiesce and abort, size limits and drain budgets, and the checkpoint capability model. |
| [Checkpoint storage](checkpoint-storage.md) | CKSTORE | States the checkpoint catalog, reference closure, quota, retention, garbage collection, and the behavior when the object store fails. |
| [Restore](restore.md) | RESTORE | States how a checkpoint is restored into a slot for resume, derive, and download, and how a restore is sealed. |
| [Capture passes](capture-passes.md) | CKPASS | States how a checkpoint is captured in passes, the change tracker, and the size limits and drain budgets that bound a capture. |
