# Proposal: Persist a deny credential-propagation marker and suppress LLM-credential assignment for credentialPropagation: deny children

- **Status:** Approved (2026-07-18). Converged after 2 adversarial review rounds (0 findings fixed). Both §9 open decisions resolved at sign-off: persist the boolean `CredentialDeny` marker (not a full enum column); keep the SPEC-1 §8.3 origin-chain clarification.
- **Date:** 2026-07-18.
- **Scope:** A spec-first correction of the §8.3 credential-propagation `deny` behavior in `spec/08_recursive-delegation.md` (the multi-hop origin-chain paragraph at `:474`), plus the persistence and gateway code that must honor it: a new `CredentialDeny` field on `sessionstore.Session` (`pkg/gateway/session/sessionstore/sessionstore.go`), a migration for the backing column (`migrations/0179_sessions_credential_deny.{up,down}.sql`), the Postgres bind/scan round-trip (`pkg/gateway/session/sessionstore/pgstore/pgstore.go`), the delegation-Service stamp on the child row (`pkg/gateway/mcpfabric/delegation/service.go`), and the finalize-time suppression in the §4.9 engine (`pkg/gateway/sessionserver/start.go`). The current implementation stamps a `deny` child as a self-origin session that is byte-identical to an `independent` child, so it mints an LLM credential for a child the spec forbids from receiving one. This proposal persists the missing `deny` bit and fails the child closed at credential assignment. It closes the two remaining pieces of T-8.3.1 (High). It adds no new SPI, changes no enum, and touches neither the §8.3 delegation-time availability pre-check nor the §8.4 `approvalMode` `deny` value.

This document stages the proposed spec, code, and test changes. It does not modify any spec, code, or doc file. Apply the changes in the "Proposed changes" section after sign-off, spec edit first.

## 1. Problem

§8.3 defines `credentialPropagation: deny` as "Child receives no LLM credentials (for runtimes that don't need LLM access, e.g., pure file-processing tools)" (`spec/08_recursive-delegation.md:443`). The implementation cannot honor this. At delegation time `credentialOriginID` (`pkg/gateway/mcpfabric/delegation/service.go:1349-1357`) returns the parent's origin only for `inherit` and returns `childID` for every other mode, so a `deny` child is stamped `CredentialOriginSessionID == childID` (a self-origin), byte-identical to an `independent` child (`service.go:1437`; `sessionstore.go:160-173`). The persisted `Session` row carries no `deny` marker. Its only credential-mode field is `CredentialOriginSessionID` (pgstore `selectCols` `:132`, insert `:197`/`:235`, bind `:315-321`, scan `:1080-1084`).

At finalize (`finalize.go:246`), `resolveCredentialPools` (`start.go:1362-1510`) has only an `inherit` origin-pool branch (guarded by `row.CredentialOriginSessionID != "" && != row.ID` at `:1400`) and no `deny` branch. A `deny` child therefore flows down the full independent intersection path and `credrouter.PreClaim` (`:1452`) mints an LLM credential from the tenant `credentialPolicy`, which is the credential the spec forbids.

A `deny` hop also has a downstream edge. Because a `deny` child is self-origin, an `inherit` hop later delegated from it resolves its origin to the `deny` child (`resolveDelegationCredentialQuery` in `pkg/gateway/mcpfabric/mcptools/mcptools.go:1368`; `credentialOriginID` in `buildChildSession`), and the origin branch in `start.go` derives eligibility from the origin runtime's `supportedProviders` (`:1404-1408`). A `deny` runtime that happens to declare providers would therefore wrongly credential the `inherit` grandchild unless the `deny` marker is consulted on the origin row as well.

Proposal 0043 landed the enum, the validator, the cross-environment `inherit` gate, and the `inherit` shared-pool assignment. Proposal 0044 landed the delegation-time availability pre-check, which already correctly skips `deny` (`mcptools_register.go:2055-2068`). Both deferred `deny`-mode suppression and the full assignment-matrix test to this cluster. Finding T-8.3.1 (High, `TEST-GAPS.md:2610`) records both remaining pieces explicitly: (1) `deny`-mode credential suppression, which needs a persisted mode marker that 0043 omitted, and (2) the full `inherit → independent → deny` assignment-matrix tier-4 test (`delegation_credential_propagation_test.go`, `TEST-GAPS.md:2615-2616`).

This is distinct from the §8.4 `approvalMode: deny` value ("Delegation not permitted", `spec/08_recursive-delegation.md:523`), an unrelated enum in a different table.

## 2. Decisions

- **Persist a minimal boolean `CredentialDeny` marker on the session row rather than a full `CredentialPropagation` enum column.** `CredentialOriginSessionID` already distinguishes `inherit` (a non-self ancestor origin) from `independent`/`deny` (self-origin), so the only information the row lacks is the `deny` bit. A boolean adds exactly that bit. A full enum column would duplicate the `inherit`-versus-`independent` distinction that `CredentialOriginSessionID` already carries and create a second source of truth that can drift from the origin id. The reviewer ratifies this choice in §9 (open question 1).
- **Suppress at finalize by failing closed.** A `deny` row resolves to zero eligible providers, so `credrouter.PreClaim` runs no assignment, mints no lease, and delivers the pod no LLM credential. This matches the code-best-practices deny-on-doubt rule for credential paths and mirrors the existing "unresolvable origin → empty intersection → `CREDENTIAL_POOL_EXHAUSTED`" fail-closed behavior at `start.go:1400-1410` rather than adding a new bypass.
- **Put the entire suppression in `resolveCredentialPools`.** That function is the single §4.9 engine used by both finalize (`finalize.go:246`) and the delegation-time pre-check (`start.go:1319-1337`). Fixing it there fails a `deny` child closed everywhere. Two edits are needed: a top-of-function early return when the row is `deny`, and a `deny` check on the origin row inside the existing origin branch so an `inherit` hop whose origin traces to a `deny` session also fails closed.
- **Treat a `deny` session as an origin-chain terminator that holds no origin pool.** An `inherit` hop whose origin resolves to a `deny` session is rejected with `CREDENTIAL_POOL_EXHAUSTED` at delegation time and receives no credential at finalize. This follows deductively from the existing definitions (`deny` grants no pool at `:443`; `inherit` forwards the parent's live pool at `:490`), so the accompanying spec touch clarifies rather than changes behavior.
- **No change to the delegation-time availability pre-check gate** (`mcptools_register.go:2055-2068`), which already runs only for `inherit`/`independent`/omitted and skips `deny`. The 0044 gate is correct; only the finalize-time and origin-row suppression are missing.
- **Keep the change scoped to `credentialPropagation: deny` (§8.3).** The §8.4 `approvalMode: deny` value (`spec/08_recursive-delegation.md:523`) is untouched.
- **Spec-first, per `spec-driven-development.md`.** Land the §8.3 origin-chain clarification, then the persistence plumbing (sessionstore field, migration, pgstore bind/scan), then the delegation-Service stamp and the `resolveCredentialPools` suppression, then the tests.

## 3. The credential-assignment path after the change

`resolveCredentialPools` is the sole §4.9 credential-eligibility engine. After the change it treats a `deny` row and a `deny` origin as holding no pool:

- **A `deny` child row.** The early return at the top of the function yields an empty provider map before any intersection is computed, so `credrouter.PreClaim` is never called, no lease is minted, and the finalize path assigns the pod no LLM credential. The delegation-time pre-check (which reuses the same engine against a synthetic row) is unaffected, because the pre-check gate in `mcptools_register.go` already skips `deny` and never calls the engine for a `deny` hop.
- **An `inherit` child whose origin is a `deny` session.** Inside the existing origin branch, after the origin row is fetched, a `deny` origin sets the intersection to empty rather than deriving eligibility from the origin runtime's `supportedProviders`. The `inherit` hop then fails closed to `CREDENTIAL_POOL_EXHAUSTED` at delegation time and receives no credential at finalize, because a `deny` session holds no pool for a descendant to inherit.

An `inherit` child of a non-`deny` origin, an `independent` child, and a root or top-level session are unchanged: `CredentialDeny` is `false` on their rows, so neither the early return nor the origin-branch `deny` check fires.

## 4. Proposed changes

### SPEC-1. Name a deny hop as an origin-chain terminator that holds no origin pool

**Target:** `spec/08_recursive-delegation.md` §8.3, the multi-hop origin-chain paragraph (`:474`).

**Rationale:** The origin-chain sentence at `:474` traces the origin pool "back through any number of contiguous `inherit` hops to the session at the top of the `inherit` chain," and its parenthetical lists only two terminators: "the session where `independent` was last used, or the root session if the chain reaches the root." A `deny` hop is a third terminator that holds no origin pool, so an `inherit` hop whose origin resolves to a `deny` session has nothing to inherit. The delegation-time `CREDENTIAL_POOL_EXHAUSTED` rejection for that case is already covered by the pre-check text at `:470`, and the empty-pool fail-closed by `:443` and `:490`, so this touch is confined to the illustrative parenthetical at `:474`, which currently omits `deny`. It names the terminator without restating the behavior the surrounding lines already fix.

**Anchor:** In the sentence beginning "That origin pool traces back through any number of contiguous `inherit` hops," replace the parenthetical only. Leave the rest of the paragraph, the worked multi-hop example at `:476-488`, the per-hop-model closing sentence at `:490`, and the §8.4 `approvalMode` table at `:517-523` unchanged.

**Change (staged text).** Replace:

```
(the session where `independent` was last used, or the root session if the chain reaches the root)
```

with:

```
(the session where `independent` was last used, the root session if the chain reaches the root, or no origin pool at all when a `deny` hop terminated the chain, because a `deny` hop grants no credentials and therefore establishes no origin pool for a descendant `inherit` hop to draw from)
```

**Preserved unchanged:** the origin-pool definition, the compatibility-check reference-pool rule, the worked multi-hop example, the per-hop-model closing sentence, and the §8.4 `approvalMode` table.

### CODE-1. Add a persisted CredentialDeny marker to sessionstore.Session

**Target:** `pkg/gateway/session/sessionstore/sessionstore.go`, next to `CredentialOriginSessionID` (`:173`).

**Rationale:** The row must carry the `deny` bit so finalize (and the pre-check, which builds a synthetic row) can distinguish a `deny` child from an `independent` child. Today both are self-origin and indistinguishable (`sessionstore.go:160-173`).

**Change (staged text).** Add, immediately after the `CredentialOriginSessionID` field:

```go
// CredentialDeny is true iff the delegate_task hop that created this
// child set credentialPropagation: deny, meaning the child receives no
// LLM credentials (§8.3 line 443: "Child receives no LLM credentials").
// inherit versus independent is already carried by
// CredentialOriginSessionID (an inherit child copies its parent's
// origin; an independent child is its own origin), so this field records
// only the deny case. The finalize-time §4.9 engine reads it to fail a
// deny child closed at credential assignment, and reads it on an origin
// row so an inherit hop whose origin traces to a deny session also fails
// closed. The delegation Service stamps it at child-row creation; the
// value is invariant once the row is created.
// spec: §8.3 line 443.
CredentialDeny bool
```

### CODE-2. Add migration 0179 for the sessions.credential_deny column

**Target:** `migrations/0179_sessions_credential_deny.up.sql` and `migrations/0179_sessions_credential_deny.down.sql`.

**Rationale:** `0178_checkpoint_manifest` is the highest existing migration; the new persisted field needs a backing column. `0176_session_credential_origin` is the model for a single-column additive session migration.

**Change (staged text).** `0179_sessions_credential_deny.up.sql`:

```sql
-- §8.3 line 443 — credential_deny records that the delegate_task hop that
-- created this child session set credentialPropagation: deny, so the child
-- receives no LLM credentials. inherit versus independent is already carried
-- by credential_origin_session_id (migration 0176); this column adds only the
-- deny bit that self-origin cannot express, because an independent child and a
-- deny child are both self-origin. The finalize-time §4.9 engine reads it and
-- resolves a deny row (and an inherit hop whose origin row is deny) to zero
-- eligible providers, failing the child closed at credential assignment.
-- Invariant after creation, matching credential_origin_session_id.
ALTER TABLE sessions
    ADD COLUMN IF NOT EXISTS credential_deny BOOLEAN NOT NULL DEFAULT false;
```

`0179_sessions_credential_deny.down.sql`:

```sql
ALTER TABLE sessions DROP COLUMN IF EXISTS credential_deny;
```

The migration's column presence and `NOT NULL DEFAULT false` behavior are covered by the centralized `prod_columns_test.go` registry rather than a per-migration `_test.go` file (see TEST-4). Do not add a `0179_*_test.go` file.

### CODE-3. Bind and scan credential_deny in pgstore

**Target:** `pkg/gateway/session/sessionstore/pgstore/pgstore.go`: `selectCols` (`:132`), the insert column list (`:197`), the `VALUES` clause (`:235`), the args slice (`:315-321`), and the scan targets (`:1080-1084`).

**Rationale:** The Postgres store must round-trip the new field. The column is set once at creation (like `credential_origin_session_id`), so it belongs in `insertSQL` and `selectCols` rather than `updateSQL`.

**Change (staged description).**

1. `selectCols` (`:132`): append `credential_deny` after the `COALESCE(credential_origin_session_id::text, '')` line. It is a plain `BOOLEAN NOT NULL`, so no `COALESCE` wrapper is needed.
2. Insert column list (`:197`): add `credential_deny` after `credential_origin_session_id`.
3. `VALUES` clause (`:235`): add a new positional bind `$58` after `NULLIF($57, '')::uuid`, with a short comment tying it to §8.3 line 443.
4. Args slice (`:321`): bind `sess.CredentialDeny` immediately after `sess.CredentialOriginSessionID`.
5. Scan targets (`:1084`): add `&s.CredentialDeny` after `&s.CredentialOriginSessionID`.

No `updateSQL` change; the field is invariant after creation. Run `gofumpt` and `goimports`.

### CODE-4. Stamp CredentialDeny on the delegated child row

**Target:** `pkg/gateway/mcpfabric/delegation/service.go`, `buildChildSession`'s child `Session` literal (`:1437`).

**Rationale:** The delegation Service is the only place a hop's mode is known (`req.CredentialPropagation`, already validated by the lease validator). `credentialOriginID` collapses `deny` to a self-origin, so the `deny` bit must be captured alongside it.

**Change (staged text).** In the child `Session` literal, immediately after the `CredentialOriginSessionID: credentialOriginID(...)` field, add:

```go
// §8.3 line 443 — a deny hop grants the child no LLM credentials. A deny
// child is stamped self-origin by credentialOriginID above (identical to
// an independent child), so the deny bit is captured separately here for
// the finalize-time §4.9 engine to suppress assignment.
CredentialDeny: req.CredentialPropagation == lease.CredentialPropagationDeny,
```

Extend the adjacent doc comment (`service.go:1429-1436`) to note that a `deny` child is stamped both as a self-origin and with the `deny` marker so finalize suppresses its assignment. The memstore stores the `Session` by value (`memstore.go:46` copies the struct), so it round-trips the new field with no code change.

### CODE-5. Suppress credential assignment for deny rows and inherit-from-deny origins in resolveCredentialPools

**Target:** `pkg/gateway/sessionserver/start.go`, `resolveCredentialPools`: the top of the function (`:1362-1365`) and the existing origin branch (`:1400-1410`).

**Rationale:** `resolveCredentialPools` is the single §4.9 engine used by both finalize (`finalize.go:246`) and the delegation-time pre-check (`start.go:1319-1337`). Failing a `deny` row closed here suppresses assignment everywhere and also rejects an `inherit` hop whose origin traces to a `deny` session, with no change to the `mcptools_register.go` gate. Both behaviors are anchored by existing spec lines: §8.3 line 443 (`deny` grants no credentials), line 470 (the `inherit` pre-check rejects when the origin pool has no assignable slot, which a `deny` origin lacks), line 474 (the origin pool is the actual live pool and never a reconstructed one), and line 490 (`inherit` forwards the parent's live pool unchanged).

**Change (staged description).**

1. Early return. After the nil-registry guard at `:1363-1365`, add:

```go
// spec: §8.3 line 443 — a deny hop grants the child no LLM credentials.
// A deny row resolves to zero eligible providers, so PreClaim runs no
// assignment and no lease is minted (fail closed). CredentialOriginSessionID
// cannot express this: a deny child is self-origin, identical to an
// independent child, so the persisted deny marker is the only signal.
if row.CredentialDeny {
	return nil, nil, nil, nil
}
```

2. Origin-row deny check. Inside the existing origin branch (`:1400-1410`), after `s.store.Get` succeeds and before the origin-runtime resolution, fail closed when the origin row is `deny`:

```go
if originRow.CredentialDeny {
	// spec: §8.3 line 443, 490 — a deny session holds no origin pool, so
	// an inherit hop from it has nothing to inherit and fails closed to
	// CREDENTIAL_POOL_EXHAUSTED rather than deriving eligibility from the
	// deny runtime's supportedProviders.
	intersection = nil
} else if originRt, rtErr := runtimestore.Resolve(ctx, s.runtimes, originRow.RuntimeRef); rtErr != nil {
	intersection = nil
} else {
	originEligible := credrouter.Intersection(originRt.SupportedProviders, policy)
	intersection = intersectProviders(intersection, originEligible)
}
```

Extend the branch's existing fail-closed comment (`:1384-1399`) to cover the `deny` origin. Run `gofumpt` and `goimports`.

### TEST-1. Add the tier-4 inherit/independent/deny assignment-matrix integration test

**Target:** `tests/tier4_integration/delegation_credential_propagation_test.go` (new).

**Rationale:** T-8.3.1's named deliverable (`TEST-GAPS.md:2615`) is absent. The two existing delegation credential tests cover only `inherit` and `independent` (`cross_environment_delegation_test.go`, `delegation_credential_pool_race_test.go`). The matrix pins the `deny` assignment path the fix adds, and it must also exercise the fix's cross-hop behavior.

**Change (staged description).**

- Build the test on the in-process `sessionserver.New` plus `httptest` finalize fixture the two sibling credential tests already use (the `crossEnvCall` / `assertInheritChildDrawsFromOriginPool` pattern with `store.Update`), rather than the `tests/testinfra/gateway` subprocess driver. The sibling assignment tests deliberately avoid the subprocess driver because it does not expose per-child finalize assignment directly.
- **Deny leaf (the fix's suppression path):** delegate a `deny` child, finalize it, and assert it is assigned no credential pool and no user provider (`resolveCredentialPools` returns empty). This pins the early-return suppression in CODE-5.
- **Deny-origin terminator edge (the fix's novel cross-hop path):** restructure the tree so a `deny` hop has an `inherit` child below it (`deny → inherit`). Assert the `inherit`-from-`deny` grandchild is rejected with `CREDENTIAL_POOL_EXHAUSTED` at delegation time and receives no credential at finalize. This gives the origin-row `deny` branch at `start.go:1400-1410` integration coverage rather than leaving it unit-only (TEST-3 covers only the stamp).
- Keep the `inherit` and `independent` legs minimal, since inherit and independent assignment are already covered by the two existing tier-4 tests; the file earns its weight through the `deny` leaf and the `deny`-origin terminator edge.
- Carry a `// spec: 8.3 (deny receives no LLM credentials; inherit-from-deny fails closed)` annotation and a `// diagnosis:` comment stating that a failure means a `deny` child was assigned a credential the spec forbids, or an `inherit` hop drew from a `deny` origin that holds no pool.

### TEST-3. Unit-assert the delegation Service stamps CredentialDeny on a deny child

**Target:** `pkg/gateway/mcpfabric/delegation/service_test.go`, `TestDelegateAcceptsCredentialPropagationEnum_spec_8_3` (`:1703`).

**Rationale:** CODE-4 adds `CredentialDeny: req.CredentialPropagation == lease.CredentialPropagationDeny` to the child struct literal, a field no current test asserts. The existing test already loops over `""`, `inherit`, `independent`, and `deny`, calls `svc.Delegate`, and retrieves the committed child via `store.Get`, but it discards the child (`if _, err := store.Get(...)`). Extending it pins CODE-4's only new bit at tier-1 with no new scaffolding.

**Change (staged description).** In `TestDelegateAcceptsCredentialPropagationEnum_spec_8_3`, capture the retrieved child (`child, err := store.Get(...)`) and assert `child.CredentialDeny == (mode == lease.CredentialPropagationDeny)` for each of the four modes it already iterates. Do not add a new test function or a new table case. Do not assert `CredentialOriginSessionID == childID`; the pure-function `TestCredentialOriginID` (`credential_origin_internal_test.go:57-62`) already owns the `deny → childID` mapping. Keep the existing `// spec:` annotation.

### TEST-4. Round-trip credential_deny through the real store and register the migration column

**Target:** `tests/tier2_component/stores/sessionstore_test.go` (`TestSessionStoreContract`, near the `credential_origin_session_id` round-trip at `:760-800`), `tests/tier2_component/migrations/prod_columns_test.go` (`:558-564`), and `pkg/gateway/session/sessionstore/memstore/memstore_test.go` (`TestCredentialOriginSessionIDRoundTrips`, `:746`).

**Rationale:** The persistence plumbing (CODE-1..3) needs a real-schema round-trip and the migration column must be asserted present. The `pgstore` package has no real-Postgres harness (its tests are pure arg-encoding and in-memory encode/decode against a `fakePool` that never dials), so the real-DB round-trip belongs in `tests/tier2_component/stores/sessionstore_test.go`, where `startStore` spins a real Postgres container and runs migrations, and where the analogous `CredentialOriginSessionID` round-trip already lives.

**Change (staged description).**

- In `TestSessionStoreContract`, add a `credential_deny` round-trip subtest mirroring the `credential_origin_session_id` round-trip at `:760-800`: `Create` a session with `CredentialDeny: true`, `Get` it, and assert `CredentialDeny` reads back `true`; and `Create` a session that leaves the field unset and assert it defaults to `false`. This exercises the CODE-3 bind/scan and the CODE-2 `NOT NULL DEFAULT false` against a real schema.
- In `prod_columns_test.go`, add `{migration: "0179", table: "sessions", columns: []string{"credential_deny"}}` after the `0176` entry (`:562-563`). This asserts the migration created the column.
- In `TestCredentialOriginSessionIDRoundTrips` (memstore), append a single assertion that a session created with `CredentialDeny: true` reads back `true` after a `Create`/`Get` and survives the `ExportState`/`ImportState` cycle. The `Session` struct has no JSON tags and `ExportState` marshals all exported fields generically, so an exported bool round-trips by construction; a one-line assertion is sufficient and no separate test function is added.

Carry `// spec: 8.3 (deny marker persistence)` on the added assertions.

### DOC-1. Mark the two remaining T-8.3.1 pieces resolved on application

**Target:** `TEST-GAPS.md`, T-8.3.1 (`:2610-2616`).

**Rationale:** T-8.3.1 stays OPEN pending exactly the two pieces this proposal delivers: `deny`-mode credential suppression (which needed the persisted marker 0043 omitted) and the full `inherit → independent → deny` assignment-matrix tier-4 test.

**Change (staged description).** On application, flip the T-8.3.1 checkbox to resolved, referencing this proposal, the new `tests/tier4_integration/delegation_credential_propagation_test.go`, and the persisted `CredentialDeny` marker. Note that the fix chose a boolean `deny` marker over a full enum column, and that proposals 0043 and 0044 landed the earlier pieces. Applied at implementation time, consistent with how findings are closed.

## 5. Non-goals

- No change to the delegation-time availability pre-check gate (`mcptools_register.go:2055-2068`); it already skips `deny` correctly and its 0044 behavior is unchanged.
- No change to `credentialOriginID`'s self-origin collapse for `deny` (`service.go:1349-1357`); a `deny` child remains a self-origin, and the new boolean carries the extra `deny` bit rather than a new origin encoding.
- No change to `approvalMode: deny` (`spec/08_recursive-delegation.md:523`, §8.4 "Delegation not permitted"), an unrelated enum in a different table.
- No full `CredentialPropagation` enum column on the session row. The boolean `deny` marker is the minimal surface, and `inherit` versus `independent` stays encoded in `CredentialOriginSessionID` (subject to the reviewer's ratification in §9).
- No change to the `inherit` shared-pool assignment or the cross-environment `CREDENTIAL_PROVIDER_MISMATCH` gate landed by proposal 0043; the origin branch in `resolveCredentialPools` is extended rather than rewritten.
- No `updateSQL` change; `credential_deny` is invariant after creation, matching `credential_origin_session_id`.

## 6. Testing

The change reaches tier 0 (static), tier 1 (the delegation-Service stamp and the memstore round-trip, in-process), tier 2 (the pgstore field round-trip and the migration column against a real Postgres container in envtest/component), and tier 4 (the multi-hop credential-assignment flow across the gateway and the credential-pool store) per `.claude/rules/test-coverage.md`. The spec edit (SPEC-1) carries no runtime behavior and is covered by the tier-0 static suite plus spec-map validation. Each test below covers a non-happy path and carries a `// spec:` tie.

- **tier-1 delegation stamp (TEST-3, boundary):** `TestDelegateAcceptsCredentialPropagationEnum_spec_8_3` asserts `child.CredentialDeny == (mode == deny)` across the four modes it already iterates. The non-happy path is the `deny` mode that must set the marker while `inherit`, `independent`, and the omitted default must leave it `false`. `// spec: 8.3 (deny marker stamp on the child row)`.
- **tier-1 memstore round-trip (TEST-4, boundary):** `TestCredentialOriginSessionIDRoundTrips` gains an assertion that `CredentialDeny: true` survives a `Create`/`Get` and the snapshot cycle. The non-happy path is a snapshot restore that drops the marker, which would silently re-credential a `deny` child after a gateway restart. `// spec: 8.3 (deny marker persistence)`.
- **tier-2 pgstore round-trip and migration column (TEST-4, spec-named-failure):** `TestSessionStoreContract` asserts `credential_deny` round-trips as `true` and defaults `false` against a real schema, and `prod_columns_test.go` asserts the `0179` column exists. The non-happy path is a bind/scan or `NOT NULL DEFAULT` defect that persists a `deny` child as non-`deny`, re-enabling the forbidden assignment. `// spec: 8.3 (deny marker persistence and default)`.
- **tier-4 deny-leaf suppression (TEST-1, spec-named-failure):** the new `delegation_credential_propagation_test.go` delegates a `deny` child, finalizes it, and asserts it is assigned no credential pool and no user provider. The non-happy path is the spec-forbidden assignment: a `deny` child that receives an LLM credential minted from the tenant `credentialPolicy`. `// spec: 8.3 (deny receives no LLM credentials)`.
- **tier-4 inherit-from-deny terminator (TEST-1, spec-named-failure):** the same file delegates `deny → inherit` and asserts the `inherit` grandchild is rejected with `CREDENTIAL_POOL_EXHAUSTED` at delegation time and receives no credential at finalize. The non-happy path is a `deny` runtime that declares providers wrongly credentialing the inheriting grandchild through the origin branch. `// spec: 8.3 (inherit hop whose origin is deny holds no pool)`.

## 7. Findings closed on application

This proposal closes the two remaining pieces of T-8.3.1 (credentialPropagation has no full implementation or test, High). Proposals 0043 and 0044 landed the enum, validator, cross-environment gate, inherit shared-pool assignment, and the delegation-time availability pre-check; this proposal adds the persisted `deny` marker, the finalize-time and origin-row suppression, and the `inherit → independent → deny` assignment-matrix tier-4 test that T-8.3.1 names. The changes are applied at spec-edit, code, and test time and need no operator hardware beyond the Postgres container the tier-2 and tier-4 tests already use.

## 8. Resolved in adversarial review

Subsequent adversarial review rounds populate this section. The challenge-round revisions carried in the draft are folded into the staged changes above. First, SPEC-1 was shrunk from a new sentence at `:490` to a one-clause fix of the `:474` parenthetical, because the delegation-time `CREDENTIAL_POOL_EXHAUSTED` rejection and the empty-pool fail-closed are already covered by the existing lines `:443`, `:470`, and `:490`; CODE-5 cites those existing lines rather than a new spec sentence. Second, TEST-1 was moved off the `tests/testinfra/gateway` subprocess driver onto the in-process `sessionserver.New` plus `httptest` finalize fixture the sibling credential tests use, and its tree was restructured to `deny → inherit` so the origin-row `deny` branch gets integration coverage rather than remaining unit-only. Third, TEST-3 was reduced from a new table case to a two-line extension of the existing `TestDelegateAcceptsCredentialPropagationEnum_spec_8_3`, dropping the `CredentialOriginSessionID == childID` assertion that `TestCredentialOriginID` already owns. Fourth, TEST-4 dropped the `pgstore_test.go` target (which has no real-DB harness) in favor of the real-Postgres `TestSessionStoreContract` and the centralized `prod_columns_test.go` registry, and reduced the memstore leg to a one-line assertion; CODE-2's migration-column coverage was redirected to `prod_columns_test.go` rather than a per-migration `_test.go` file.

## 9. Open decisions for review

### Persistence choice — boolean marker versus full enum column

A minimal boolean `CredentialDeny` marker is recommended, because `CredentialOriginSessionID` already encodes `inherit` versus `independent` and the boolean adds only the missing `deny` bit. A full persisted `CredentialPropagation` enum column, as the T-8.3.1 finding text literally phrases it ("a persisted credentialPropagation mode column"), is more self-documenting but duplicates the origin-derived distinction and risks drift with the origin id. The reviewer ratifies which persistence surface to land. **Resolved at sign-off: the boolean `CredentialDeny` marker.**

### Whether the inherit-from-deny edge warrants the SPEC-1 clarification

SPEC-1 documents a `deny` hop as an origin-chain terminator that holds no origin pool, in the `:474` parenthetical. Under `spec-driven-development` the clarification is included by default, but the behavior is already determined by the existing lines `:443`, `:470`, and `:490`, so the reviewer may judge the existing text sufficient and drop SPEC-1, handling the edge as a code-only deductive consequence. CODE-5 is anchored by the existing lines either way. **Resolved at sign-off: keep SPEC-1.**

## 10. Files touched on application

- `spec/08_recursive-delegation.md`: SPEC-1 (§8.3 origin-chain parenthetical at `:474`, naming a `deny` hop as a terminator that holds no origin pool).
- `pkg/gateway/session/sessionstore/sessionstore.go`: CODE-1 (add `CredentialDeny bool`, `:173`).
- `migrations/0179_sessions_credential_deny.up.sql`, `migrations/0179_sessions_credential_deny.down.sql`: CODE-2 (add and drop the `sessions.credential_deny` column).
- `pkg/gateway/session/sessionstore/pgstore/pgstore.go`: CODE-3 (bind and scan `credential_deny`; `:132`, `:197`, `:235`, `:315-321`, `:1080-1084`).
- `pkg/gateway/mcpfabric/delegation/service.go`: CODE-4 (stamp `CredentialDeny` on the child row, `:1437`).
- `pkg/gateway/sessionserver/start.go`: CODE-5 (early return for a `deny` row and origin-row `deny` check in `resolveCredentialPools`, `:1362`, `:1400`).
- `tests/tier4_integration/delegation_credential_propagation_test.go`: TEST-1 (deny-leaf suppression and inherit-from-deny terminator, new file).
- `pkg/gateway/mcpfabric/delegation/service_test.go`: TEST-3 (assert the `deny` stamp in `TestDelegateAcceptsCredentialPropagationEnum_spec_8_3`, `:1703`).
- `tests/tier2_component/stores/sessionstore_test.go`, `tests/tier2_component/migrations/prod_columns_test.go`, `pkg/gateway/session/sessionstore/memstore/memstore_test.go`: TEST-4 (real-schema round-trip, migration-column registration, and memstore snapshot assertion).
- `TEST-GAPS.md`: DOC-1 (mark T-8.3.1 resolved, `:2610`).
