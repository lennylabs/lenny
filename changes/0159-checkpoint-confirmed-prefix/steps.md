# Steps: 0159 Checkpoint manifest records the contiguous confirmed prefix

## Validation owed

## Steps

### 0159.S1 Contiguous confirmed prefix in the manifest row
- status: Planned
- lanes: [specification, code]

The §10.1.7 definitions of `chunk_count` and `workspace_bytes_uploaded`, step 2's conditional `UPDATE`, step 3's flush, the completion condition, and the deadline-fire sweep state the contiguous run of confirmed chunks (decision 1). The upload driver in `pkg/gateway/checkpoint/checkpointer` records each confirmed chunk's size by index, writes the run's length and sum when the run grows, re-issues that write before each `Finalise`, sweeps a deadline fire whose run is empty, and refuses completion over a gap, with unit tests for each.
