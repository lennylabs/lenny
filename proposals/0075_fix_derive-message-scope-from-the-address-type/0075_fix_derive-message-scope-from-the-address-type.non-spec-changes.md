## 4. Detailed design

TEST-2 has one open point of its own. `sessionScopedMessages` records each member's retired field number
alongside its name, and `CoordinatorFenceRequest` never carried the duplicate address the retirement
removed: the commit that introduced it put the field on `InterruptRequest`, `SignalDeadlineRequest`,
`ResumeRequest`, `CheckpointBarrierRequest`, and `ReportUsageRequest` alone (`01d19af01`). Adding the fence
to the map therefore requires deciding what that column holds for a message with no retired number, rather
than copying a neighbouring row.

## 5. Proposed changes

**IMPLEMENTOR TO FILL THE BLANKS.** The staged blocks below are indicative. They name the target and the
change; the exact text is written during convergence, against the post-0073 and post-0076 state of each
file.

### TEST-1. Replace the tier-0 gate

The tier-0 file 0073's §8 adds for table reconciliation is replaced by the rule gate of D2. The existing
scope test that accepts either class word in the table's cell
(`tests/tier0_static/adapter_proto_message_scope_test.go:75-81`) loses its subject with the table and is
retired with it.

### TEST-2. Cover the fence in the tier-3 session-address suite

`tests/tier3_contract/adapter_session_address/session_address_wire_test.go`: add
`CoordinatorFenceRequest` to `sessionScopedMessages`, settling the retired-field-number column per §4, and
delete the coverage clause at `:40-43` that claims the fence is pinned by an arm that does not reach it.

## 8. Testing

**IMPLEMENTOR TO FILL THE BLANKS.** The tiers reached are 0 (the new gate, build, vet), 3 (the
session-address suite TEST-2 amends), and 11 (the specification and any reader-facing page that restates
the classification). The specific cases are written during convergence; at minimum the tier-0 gate must be
shown to fail on a proto that violates the rule, and the tier-3 case must pin the fence's address the way
it pins every other session-scoped message's.
