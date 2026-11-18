# Decisions: 0159 Checkpoint manifest records the contiguous confirmed prefix

## Open decisions

None.

## Decisions

Settled by the owner on 2026-11-17, answering "Is a fix ready to merge and passing the test? If so, do that." about the fix on `fix/checkpoint-confirmed-bytes-order`, whose report raised the question below:

1. **What the manifest row's counters hold.** `chunk_count` and `workspace_bytes_uploaded` hold the length and the byte sum of the contiguous run of confirmed chunks from index `0`. A chunk confirmed past a missing lower index is counted once the missing index confirms. The store's guarded `UPDATE` is unchanged, and the in-process quota reconciliation keeps using the size of every confirmed chunk, counted in the run or not, because each confirmed chunk carries an `artifact_store` row.

   The accepted residual: while a gap is open, a chunk past it has a catalog row but is outside `workspace_bytes_uploaded`. If the gateway crashes then, the §12.5 backstop releases `reserved_bytes − workspace_bytes_uploaded` and the sweep's catalog soft-delete decrements the chunk past the gap as well, so the tenant's storage counter ends low by the bytes of every chunk confirmed past the gap until the next absolute rebuild from the `artifact_store` sum. A `Rehydrate` that runs during a gap errs the other way, high by the same bytes, until the next rebuild. The grant window does not bound the error, because a grant slot frees on the chunk's acknowledgement rather than on its confirmation. With an honest adapter, which uploads one chunk at a time, the error is normally zero or one chunk. An adapter that never declares index `0` can make it every chunk confirmed in the attempt, up to the attempt's quota cap, but only when the gateway also crashes during the attempt; the in-process arms, including the timeout sweep, reconcile exactly. The general and security reviews of 2026-11-17 corrected the bound first stated for this residual.

   The review of the fix added three corrections that need no further decision, and they are part of this decision:
   - a deadline fire whose run is empty, while chunks past index `0` confirmed, sweeps those chunks, because `Finalise` soft-deletes the zero-chunk row and no resume or backstop selects it;
   - each terminal arm re-issues the run's update before `Finalise`, the flush that step 3 of the intent-row flow in §10.1.7 states, so a lost per-confirmation write does not leave the finalised row short;
   - the completion arm finalises `complete` only when the confirmed chunks form the run `0` to `chunk_count − 1`, and finalises `stream_truncated` otherwise.

   The alternative left aside: keep `chunk_count` as the highest confirmed index plus one and widen the guard so `workspace_bytes_uploaded` stays monotonic as well. That would change the specification's guard, and a row could then count a chunk that reassembly finds missing below `chunk_count`.
