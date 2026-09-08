# Deviations: Derive message scope from the address type

Where the landed code departs from what the proposal states. Recorded on 2026-09-08 from the deviation
the implementation's S2 step returned.

## 1. `protoServiceRequests` lost its unread return value (S2, TEST-1)

**The proposal says.** TEST-1(a) says the comment on `protoServiceRequests`
(`tests/tier0_static/adapter_proto_parse_test.go:64-67`) is restated as the addressing-convention gate.
It says nothing about the function's signature.

**What landed.** The comment was restated to say what the gate reads the function for, which is selecting
the request messages the stream-envelope clause applies to, and the return type was narrowed from
`map[string]string` (request message to declaring service) to `map[string]bool` (the set of
request-message names). The function name and file are unchanged, and its single caller already discarded
the value.

**Why.** The declaring service was read only by the table-reconciliation gate this step retires. After the
retirement it was a compiling-but-dead half of the return value, so the restated comment could only either
justify a consumer that does not exist, which is the defect the step's own review findings named, or
document dead data. `code-best-practices.md` points at removing it.

**What a later reader gets wrong.** Reading TEST-1(a) as the whole of the change to that file would
suggest the signature is untouched, and a later caller written against the old two-value shape would not
compile.

**Suggested next step.** None. The narrowing is complete and its single caller is updated in the same
commit.

## Not deviations

The three tier-0 cases that fail over `.claude/tests/*.mjs` fixtures are pre-existing. They fail
identically at `6b72e469a^`, before any of this session's changes, with the same counts, which a worktree
run at that commit established rather than inferred. They are outside this proposal's files.
