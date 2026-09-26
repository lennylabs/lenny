# Non-spec changes: Four spec sites disagree on the finalize workspace-failure envelope

These changes are staged. Applying them is the implementation's work, under the
implementation checklist.

## Design (implementation-facing)

The gateway already returns what SPEC-1 states (writePodClaimError in `pkg/gateway/sessionserver/start.go`), so this proposal stages no code change.

## Staged code changes

This proposal stages no code change.

## Staged schema, chart, and migration changes

This proposal stages no schema, chart, or migration change.

## Staged docs changes

This proposal stages no docs change. No page under `docs/` repeats the §6.2 or §7.2 claim. `docs/reference/error-catalog.md` already describes `WORKSPACE_PLAN_INVALID` as a create-time schema failure and `SESSION_CREATION_FAILED` as covering workspace materialization, and the finalize sections of `docs/api/rest.md` and `docs/api/mcp.md` name no workspace code.

## Testing

This proposal stages no test. Whether to add one is open decision OD-3 in the summary, and the edge case below records the gap.

## Edge cases and accepted failure modes

- **No test pins the generic finalize materialization envelope.** `TestFinalizeRejectsOverLimitArchiveAsNonRetryable_spec_13_4` in `pkg/gateway/sessionserver/start_pod_test.go` pins the 413 case. No test found in the tree pins the `503 SESSION_CREATION_FAILED` answer to a non-archive staging or `FinalizeWorkspace` failure at finalize. No existing tier-11 gate reads the §6.2 bullet, the §7.2 paragraph, or the §15.1 catalog rows. Whether to add a test is open decision OD-3; this draft adds none because no code or gate changes.

## Files touched on application (non-spec)

None.
