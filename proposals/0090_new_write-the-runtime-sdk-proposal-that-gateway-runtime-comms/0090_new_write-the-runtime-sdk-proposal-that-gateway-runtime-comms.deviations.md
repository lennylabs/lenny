# Deviations: Runtime SDKs assume one session per process

The implementor owns this file. It stays empty until an implementation records a departure from what the proposal states.

## Proposed: CODE-7 client inventory omits sites added by earlier steps

**Status:** proposed

**Reported by:** step S22 (record the CODE-7 client-inventory deviation), for the sites step S20 (CODE-7: Part B connection handshake) covered.

**What the proposal says.** The CODE-7 **Client inventory.** table lists one row for each in-tree program, function, or test helper that dials or accepts `CH-MSGSOCK` or `CH-RUNTIMEOPS`, and the paragraph after it states "No other in-tree code dials or accepts either socket." The last row places the `CH-RUNTIMEOPS` listener of TEST-1's per-SDK `session_started` cases in `tests/tier3_contract/sdks/runtime_sdk_test.go`.

**What landed instead.** Steps S11 (CODE-3), S16 (CODE-6), and S19 (TEST-1) added or extended tests that dial or accept one of the sockets, so the inventory and its closing statement were incomplete by the time S20 landed. S20 updated the following sites, which the inventory does not name, to perform the runtime connection handshake in commit a79d3bfc3:

- `tests/tier7a_load_local/direct_usage_per_session_fold_test.go`
- `tests/tier3_contract/runtimeops_emitted/emitted_frames_test.go`
- `tests/tier3_contract/adapter_jsonl/session_frame_wire_order_test.go`
- `cmd/runtimes/streaming-echo/sessionframes_test.go`
- `cmd/lenny-compliance/stub_test.go`

The per-SDK `session_started` `CH-RUNTIMEOPS` fake listener that the last inventory row names is `opsListener` (`startOpsListener`), in `tests/tier3_contract/sdks/runtime_sdk_sessions_test.go` rather than in `tests/tier3_contract/sdks/runtime_sdk_test.go`.

**Why.** The inventory was written against the tree before the earlier steps of this proposal landed. Every site that dials or accepts either socket must complete the §4.7.11 runtime connection handshake, or its connection is refused, so S20 covered each added site in the same commit as the inventoried rows. TEST-1 placed its per-SDK session cases in a separate file from the existing SDK contract test.

**What a later reader would otherwise get wrong.** A reader relying on the inventory to find every client or listener of `CH-MSGSOCK` and `CH-RUNTIMEOPS` would miss the five tests above and would look for the per-SDK `session_started` listener in the wrong file. A later change to the handshake that updates only the inventoried rows would leave those tests failing.
