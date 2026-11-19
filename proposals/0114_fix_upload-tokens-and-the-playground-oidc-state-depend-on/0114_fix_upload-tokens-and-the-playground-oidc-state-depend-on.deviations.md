# Deviations — 0114_fix_upload-tokens-and-the-playground-oidc-state-depend-on

The implementor owns this file. It stays empty until an implementation records a departure from what the proposal states.

## Proposed: Tier-4 connector OAuth test runs as the default tenant

**Status:** proposed

**Reported by:** Step S6 (CODE-4: connector state signer removal).

**What the proposal says.** Non-spec changes, Testing item 11 states that the tier-4 two-gateway test runs on one `--postgres-dsn` and one `--redis-url`, following `keySetReplicaArgs`. The item names no tenant.

**What landed instead.** `tests/tier4_integration/connector_oauth_test.go` reuses `keySetReplicaArgs` and drives the flow as the built-in default tenant through `deletionAdminDo`, with no bootstrap call.

**Why.** The step reports this as a fixture choice inside the proposal's wording. The `connectors` table has a foreign key to `tenants`, and migrations seed the default tenant row, so the test needs no extra setup. The step reports that the test has not been run yet.

**What a later reader would otherwise get wrong.** A reader of Testing item 11 could assume that the test creates its own tenant through a bootstrap call. A reader could also assume that the test has passed, although the step reports that it had not been run when this entry was written.

## Proposed: Redis startup log line text changed

**Status:** proposed

**Reported by:** Step S7 (CODE-5: playground state move and shared store helper).

**What the proposal says.** Non-spec changes, CODE-5 Wiring states: "Keep the startup log line for the Redis path in the helper." The step task repeats this instruction, and the helper sketch omits the line.

**What landed instead.** The line stays in the Redis branch of `oauthStateStore` in `cmd/lenny-gateway/credentialsurface.go`. Its text changed from `§9.3 connector OAuth state backed by Redis` to `§9.3 and §27.3.1 OAuth state backed by Redis`.

**Why.** The step reports that the helper also serves the playground, so the old text would misreport the store's scope when the connector flow is off. The step also reports that a search of `cmd`, `tests`, `docs`, and `charts` found no reader of the old string.

**What a later reader would otherwise get wrong.** A reader of the CODE-5 Wiring item could assume that the log line kept its original text, and could search logs or the tree for the old string. A reader of the helper sketch could assume that the helper emits no log line.

## Proposed: Changes outside the CODE-5 targets and a spec-map update ahead of TEST-1

**Status:** proposed

**Reported by:** Step S7 (CODE-5: playground state move and shared store helper).

**What the proposal says.** CODE-5 Targets lists `auth.go`, `crypto.go`, `playground.go`, `credentialsurface.go`, `httpsurface.go`, and `wiring_fields.go`. Testing item 20 places the `tests/spec-map.json` updates under TEST-1.

**What landed instead.** `cmd/lenny-gateway/workers.go` has a comment-only edit to the OAuth sweep block. `tests/spec-map.json` gains the new tier 3 file under §27.3 in this step.

**Why.** The step reports that the `workers.go` comment said the sweep ran only on the connector opt-in path, which stopped being true. The step also reports that `validate-maps` fails on an unmapped test file, so the new tier 3 file had to be mapped in this step.

**What a later reader would otherwise get wrong.** A reader of the CODE-5 Targets list could conclude that `workers.go` is unchanged by this proposal. A reader of Testing item 20 could assume that every `tests/spec-map.json` update landed under TEST-1, although this entry landed in step S7.

## Proposed: Tier-11 docs test landed in the docs step ahead of TEST-1

**Status:** proposed

**Reported by:** Step S8 (DOCS-1: client documentation).

**What the proposal says.** Testing item 19 sits under TEST-1, which lands in step S9, and names the docs check as a new tier-11 test.

**What landed instead.** `tests/tier11_docs/upload_token_row_authority_docs_test.go` landed in this docs step. It holds the item 19 assertions, plus pins for the REST token rows, the 409 rows, and the precedence sentence.

**Why.** The step reports that its task lists tier 11 as a tier to create for this step, and that the docs change has no other pin. The step reports that S9 should keep this file and not add a second check.

**What a later reader would otherwise get wrong.** A reader of Testing item 19 could assume that the tier-11 test landed under TEST-1 in step S9, and could add a second check there. A reader could also assume that the test holds only the item 19 assertions, although it also pins the REST token rows, the 409 rows, and the precedence sentence.

## Proposed: 409 row wording for a session past finalization

**Status:** proposed

**Reported by:** Step S8 (DOCS-1: client documentation).

**What the proposal says.** DOCS-1 says the `docs/api/mcp.md` and `docs/api/rest.md` 409 rows state that a valid token on "a session past finalization" returns `UPLOAD_TOKEN_CONSUMED`.

**What landed instead.** `docs/api/mcp.md` and `docs/api/rest.md` say "a session in `ready` or a later non-terminal state".

**Why.** The step reports that an ended session is also past finalization and answers 409 under §7.1 **Single-use invalidation:**. The step reports that the review log records this as a watchout, so the docs use the spec wording.

**What a later reader would otherwise get wrong.** A reader of DOCS-1 could search the docs for "a session past finalization" and not find it. A reader could also assume that `UPLOAD_TOKEN_CONSUMED` is documented for every session past finalization, although the landed rows exclude terminal states.

## Proposed: Token row text the proposal leaves unstated

**Status:** proposed

**Reported by:** Step S8 (DOCS-1: client documentation).

**What the proposal says.** DOCS-1 gives text only for the `docs/api/mcp.md` `UPLOAD_TOKEN_INVALID` row. It names no text for the other token rows it adds or for the `docs/api/rest.md` finalize rows.

**What landed instead.** Row text for `UPLOAD_TOKEN_MISMATCH` and `UPLOAD_TOKEN_CONSUMED` in `docs/api/mcp.md`, and for the four token codes in `docs/api/rest.md`, was written from the §15.1 catalog. The `docs/api/rest.md` `UPLOAD_TOKEN_INVALID` row uses the error-catalog meaning. The `docs/api/mcp.md` `UPLOAD_TOKEN_EXPIRED` row in `finalize_workspace` copies the existing `upload_files` row.

**Why.** The step reports that the proposal leaves this wording unstated, and that these are additions within DOCS-1 rather than changes to it.

**What a later reader would otherwise get wrong.** A reader could assume that the proposal's review covered the text of these rows, although the step wrote that text from the §15.1 catalog and the existing `upload_files` row. A reader could also assume that the `UPLOAD_TOKEN_INVALID` text in `docs/api/rest.md` matches the text DOCS-1 gives for `docs/api/mcp.md`.

## Proposed: Red run for item 17 and the eager-claim tests used the deployed gateway image

**Status:** proposed

**Reported by:** Step S10 (RECORDS-1: BUILD-GAPS and plan records, including the S9 red run results).

**What the proposal says.** The Testing section's red-evidence paragraph in the non-spec changes says to run items 15 and 17 and the tier-5 eager-claim tests in a worktree of the S3 commit, and to record the results in RECORDS-1.

**What landed instead.** The F-7.1.31 Resolution in `BUILD-GAPS.md` records the red run that the first S9 implementor reported. Item 15 ran on a build of 3aca8d069. Item 17 and the eager-claim tests ran from a 3aca8d069 worktree against the gateway image already deployed on kind (`src-496b879a69f8`). That image is an older build that predates row verification, and it is not a build of 3aca8d069. The Resolution states this caveat.

**Why.** The step reports that no kind run against an S3 image exists. The first S9 implementor did not rebuild images on the shared cluster, and later S9 attempts and verifiers did not repeat the red run. The record holds only the runs that took place.

**What a later reader would otherwise get wrong.** A reader of the proposal could assume that item 17 and the eager-claim tests were shown red against a gateway built from the S3 commit. The tier-5 red evidence for those tests comes from an older deployed image.

## Proposed: Position of the F-9.3.18 and F-27.3.17 lines in plan §10.2

**Status:** proposed

**Reported by:** Step S10 (RECORDS-1: BUILD-GAPS and plan records, including the S9 red run results).

**What the proposal says.** RECORDS-1 item 6 says to tick the F-27.3.16 and F-15.1.69 items and to add a closed line for F-9.3.18. Item 5 says to queue F-27.3.17 in plan §10.2. The proposal names no position for either line.

**What landed instead.** The closed F-9.3.18 line and the open F-27.3.17 line sit in `gateway-runtime-comms-remediation.md` §10.2 directly under the ticked F-27.3.16 item in the playground group. Neither line is in the 0114 unstaged-defects block that commit 30bf48b79 added.

**Why.** The step reports that the header of that block says it covers RECORDS-1 items 3 and 4 only, and that both ids are reserved for findings RECORDS-1 itself names.

**What a later reader would otherwise get wrong.** A reader could look for F-9.3.18 and F-27.3.17 in the 0114 unstaged-defects block of plan §10.2 and not find them. Both lines are in the playground group under F-27.3.16.

## Proposed: Replay assertion in the session server security test still accepts 409

**Status:** proposed

**Reported by:** Step sweep (stale code the landed spec edits left behind).

**What the proposal says.** The proposal stages no change to `pkg/gateway/sessionserver/security_test.go`.

**What landed instead.** `pkg/gateway/sessionserver/security_test.go` is unchanged. The replay-after-finalize assertion still accepts either status:

```go
if rr.Code != http.StatusGone && rr.Code != http.StatusConflict {
	t.Errorf("replay upload after finalize: got %d, want 410 or 409", rr.Code)
```

**Why.** The sweep reports that this assertion contradicts the landed spec. §7.1 Normal Flow, under the uploadToken format and security properties, states in **Single-use invalidation:** that "In `ready` or any later non-terminal state, a session's unexpired token returns `410 UPLOAD_TOKEN_CONSUMED`, on every replica." A replayed upload on a `ready` session now has to receive 410, and the assertion still accepts 409. The sweep left the line unchanged because it is test code rather than a comment, so changing it changes behaviour the proposal did not stage.

**What a later reader would otherwise get wrong.** A reader of `pkg/gateway/sessionserver/security_test.go` could take the old contract, under which a replay after finalize may answer 409, as current. A reader could also assume that this test pins the §7.1 410 requirement, although it passes when the gateway answers 409.
