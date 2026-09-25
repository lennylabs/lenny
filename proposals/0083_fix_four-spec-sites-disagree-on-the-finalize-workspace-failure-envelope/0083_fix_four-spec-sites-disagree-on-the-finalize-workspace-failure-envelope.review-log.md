# Review log: Four spec sites disagree on the finalize workspace-failure envelope

## Standing context

### Settled

- **Direction of the fix**: the §15.1 catalog and the gateway agree, so §6.2 and §7.2 are corrected to them. No workspace envelope is added at finalize (initial draft, 2026-09-25).
- **Session-mode finalize outcome**: `503 SESSION_CREATION_FAILED` with `Retry-After` for a staging or `FinalizeWorkspace` failure, and `413 UPLOAD_ARCHIVE_LIMIT_EXCEEDED` for an `*upload.ValidationError` (handleFinalize in `pkg/gateway/sessionserver/sessionserver.go`; writePodClaimError in `pkg/gateway/sessionserver/start.go`).
- **Concurrent-workspace finalize**: prepareAtFinalize returns `(nil, nil)` when `MaxConcurrentSessions > 1`, so no workspace failure can surface at `/finalize` on that path.

### Traps

- The archive-limit outcome on the concurrent-workspace `/start` path was not traced. An `*upload.ValidationError` inside a `SlotBindError` classifies as transient and reaches writePodClaimError unwrapped, but a `SlotFailedError` is matched before the archive case. The staged text therefore defers the slot path to §5.2 and names no code for it.
- `SLOT_FAILED` has no §15.1 catalog row; §5.2's **Client error on exhaustion:** bullet describes the envelope's fields only. Do not name `SLOT_FAILED` in staged spec text without adding the row.
- Proposal 0082 appends to the end of the §15.1 finalize row's Notes cell. An edit to that cell here needs a different anchor or coordination with 0082.

### Open

- OD-1, OD-2, and OD-3 in the summary.

### Deferred

## Ledger
