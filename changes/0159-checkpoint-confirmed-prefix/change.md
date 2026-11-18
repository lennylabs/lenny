# Checkpoint manifest records the contiguous confirmed prefix

Status: `Decided`, revision 1, 2026-11-17.

## Problem

The checkpoint manifest row (`checkpoint_manifest`) carries two counters that the gateway advances as it confirms each uploaded chunk. [§10.1.7](../../spec/10_gateway-internals.md#1017-prestop-hook-drain) (preStop Hook Drain) defines `chunk_count` as the number of chunks committed, "equal to the highest `{n}` + 1", and `workspace_bytes_uploaded` as the sum of the sizes of the committed chunks. Step 2 of the intent-row flow in the same section writes both with one conditional `UPDATE` guarded by `chunk_count < $n + 1`.

The gateway confirms chunks concurrently, one goroutine per acknowledged chunk, so confirmations finish in any order. Under the guarded `UPDATE`, the two definitions cannot both hold when they do. If chunk 1's confirmation runs first, it writes `chunk_count = 2` with chunk 1's bytes. Chunk 0's later write, `chunk_count = 1` with both chunks' bytes, then fails the guard, and the row keeps a count of two chunks paired with the bytes of one.

`main` at 491e78d46 persists exactly that row. In `pkg/gateway/checkpoint/checkpointer/uploaddriver.go`, `confirmChunk` takes a running byte total under the driver's mutex and writes it with the confirming chunk's own index. The durable `workspace_bytes_uploaded` is then under-reported while the in-memory total is correct. The completion arm does not rewrite the counters, so a completed checkpoint keeps the short value. The integration test `TestConcurrentPoolCapturesEachSessionIndependently` found it: the test failed in about 1% of runs under load (14 of 1500 in the gate image), with no data race reported, because every access is locked.

The row's values feed the resume threshold (`workspace_bytes_uploaded >= baseline_full_checkpoint_bytes * partialRecoveryThresholdFraction`), the reassembly of the run `[0, chunk_count)`, the §12.5 backstop's reservation release (`reserved_bytes − workspace_bytes_uploaded`), and the §11.2 counter rebuild.

## Scope

The change redefines `chunk_count` and `workspace_bytes_uploaded` as the length and byte sum of the contiguous run of confirmed chunks from index `0`, and changes the upload driver to write only that run. It adds the finalisation flush that step 3 of the intent-row flow states, a sweep for a deadline fire whose run is empty, and a completion check that the confirmed chunks form the run.

It does not change the store's guarded `UPDATE`, the in-process quota reconciliation (which already uses every confirmed chunk's size), or the §12.5 backstop.

Change [0131](../0131-incremental-checkpoint/change.md) (incremental checkpoint) retires partial-manifest reassembly in its decision D4, which removes the resume consumer of these counters. This change fixes the counters for the code as it stands until 0131 lands.

## Terms

| Term | Meaning |
| :-- | :-- |
| Confirmed chunk | A chunk the gateway has confirmed with a `StatObject` and recorded in the artifact catalog. |
| Contiguous run | The confirmed chunks `0` to `n` with no index missing. Its length is `n + 1`. |
| Gap | A missing confirmed index below a confirmed index. A chunk past a gap is confirmed but outside the run. |

## High-level design

The upload driver records each confirmed chunk's size by index. When a confirmation extends the contiguous run from index `0`, the driver writes the run's length and byte sum to the row through the existing guarded `UPDATE`. A confirmation past a gap writes nothing until the gap fills. The pair written is fixed by the run's length, so the guard keeps the correct row whichever concurrent write lands last, and the row's count and bytes always describe the same chunks.

Each terminal arm re-issues the run's update before `Finalise`, so a lost per-confirmation write is corrected. A deadline fire whose run is empty, while chunks past index `0` confirmed, sweeps those chunks, because `Finalise` soft-deletes a zero-chunk row and nothing else would reclaim them. The completion arm finalises `complete` only when the confirmed chunks form the run.

## Specification and non-specification changes

- [§10.1.7](../../spec/10_gateway-internals.md#1017-prestop-hook-drain): the `chunk_count` and `workspace_bytes_uploaded` definitions, step 2's conditional `UPDATE`, step 3's flush, the completion condition, the deadline-fire sweep, and the residue sentence of **Reassembly on resume**.
- `pkg/gateway/checkpoint/checkpointer/uploaddriver.go`: the run bookkeeping, the flush, the deadline-fire sweep, and the completion check.
- `tests/registers/spec-section-citations.yaml`: the package's section-citation count.

## Documentation

No documentation page changes. The change corrects internal manifest bookkeeping that no reader configures or observes, and no documentation page restates the two counters. The edited text sits in the numbered section §10.1.7, which has no page code for a documentation-impact waiver to name.

## Tests

Unit tier, in `pkg/gateway/checkpoint/checkpointer/uploaddriver_internal_test.go`, each citing §10.1.7 and, where it applies, §4.4.2, §11.2, or §12.5:

- an out-of-order confirmation, with chunk 1 finishing before chunk 0, persists a row whose count and bytes describe the same chunks;
- concurrent confirmations persist the full count and sum;
- a chunk past a gap is not counted until the gap fills, and a repeat confirmation counts nothing twice;
- a summary with a gap in the confirmed indices finalises partial, and one over a contiguous run finalises complete;
- a deadline fire with an empty run sweeps the confirmed chunks, and one with a non-empty run keeps them;
- abort reconciliation releases the reservation less every confirmed chunk, not only the run;
- the finalisation flush corrects a lost per-confirmation write on the completion arm and on an abort arm.

The integration test `TestConcurrentPoolCapturesEachSessionIndependently` is unchanged; it ran 900 times without `-race` and 900 times with `-race` at `-cpu 1,4,16` in the gate image with no failure.
