# Non-spec changes: Four spec sites disagree on the finalize workspace-failure envelope

These changes are staged. Applying them is the implementation's work, under the
implementation checklist.

## Design (implementation-facing)

The gateway returns what SPEC-1 states (writePodClaimError in `pkg/gateway/sessionserver/start.go`) except for the adapter's post-promotion symlink re-validation, which the summary lists under **Defects in the shipped tree that this proposal does not stage**. This proposal stages no code change.

## Staged code changes

This proposal stages no code change.

## Staged schema, chart, and migration changes

This proposal stages no schema, chart, or migration change.

## Staged docs changes

No page under `docs/` repeats the §6.2 or §7.2 claim. `docs/reference/error-catalog.md` already describes `WORKSPACE_PLAN_INVALID` as a create-time schema failure and `SESSION_CREATION_FAILED` as covering workspace materialization, and the finalize sections of `docs/api/rest.md` and `docs/api/mcp.md` name no workspace code.

### DOCS-1 · docs/runbooks/minio-failure.md · Full outage tenant notice

Anchor: item 1 under the *Remediation* heading "Step 2 — Full outage". Replace

```markdown
1. Inform affected tenants: new session creation is degraded -- workspace finalize will return `INTERNAL_ERROR`.
```

with

```markdown
1. Inform affected tenants: new session creation is degraded.
```

Leave the rest of the page unedited.

## Testing

This proposal stages no test. The edge case below records what stays unpinned.

## Edge cases and accepted failure modes

- **No handler-level test pins the generic finalize materialization envelope.** `TestFinalizeRejectsOverLimitArchiveAsNonRetryable_spec_13_4` in `pkg/gateway/sessionserver/start_pod_test.go` pins the 413 case, and `TestWritePodClaimErrorFallback_spec_7_1_4` in `pkg/gateway/sessionserver/podclaimerror_internal_test.go` pins writePodClaimError's `503 SESSION_CREATION_FAILED` fallback with `Retry-After`. No test pins handleFinalize's routing of a non-archive staging or `FinalizeWorkspace` failure to that fallback. No existing tier-11 gate reads the §6.2 bullet, the §7.2 paragraph, or the §15.1 catalog rows.

## Files touched on application (non-spec)

- `docs/runbooks/minio-failure.md` (DOCS-1)
