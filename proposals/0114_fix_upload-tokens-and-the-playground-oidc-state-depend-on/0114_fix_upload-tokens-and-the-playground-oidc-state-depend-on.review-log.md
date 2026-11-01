# Review log — 0114_fix_upload-tokens-and-the-playground-oidc-state-depend-on

## Standing context

Changelog: this pass lifted the durable residue of every ledger entry from spec round 1 through non-spec-recheck round 1 into a standing context that was empty. It merged repeated entries under one subject, chiefly the "signed `state`" search trap, the digest-on-create fact, the digestless `derive_failure` row, the phase-order fact, and the "anchors match" facts. It reached the 200-line target.
Changelog, deleted as closed or superseded: the §15.1 `derive_failure` 409 watch-outs (SPEC-2(e) changed the row); the DEFERRED checklist S1, S2, and S3 lines and the [spec.3.fix-G1.1] and [spec.1.fix-G1.1] non-spec lines (applied, per [reconcile.1] and [spec.1.follow-up-fix.1]); the `closeSession` DEFERRED lines (remedy recorded, per [reconcile.1]); the post-Put recheck DEFERRED and OPEN lines (staged, per [non-spec-recheck.1.fix-G2.1]); the rest.md upload-line OPEN (no edit owed, per [non-spec-recheck.1.review-docs-alignment.1]); the anonymous-login OPEN (closed by [f1.human-decisions]); the watch-out on the summary `uploadAborts` entry's `store.Get` sentences (same fix); and the watch-outs that Testing item 6 omits the tier-7a `transitionStore` (item 6 now carries it). The "SPEC-1(f) ships without its pin" and "fixer left two edits unapplied" watch-outs went with the DEFERRED lines they described.
Changelog, disagreements: `## Retired` does not exist in this file and only this section is editable, so an older entry that a newer one displaced is dropped here rather than moved. Dropped this way: [spec.1.review-docs-alignment.1] saying two `oidc_state_*` codes land (newer: SPEC-3 names one) and [spec.3.review-performance.1] saying the Redis client bound was unverified (newer: go-redis defaults). The `## Ledger` section is untouched.

### Settled

Upload token, staged design:

- **Single Authentication home:** SPEC-1(d) **Authentication:** is the one home of the token predicate; the §15.1 `UPLOAD_TOKEN_INVALID` row cites it and lists no disjuncts; DOCS-1 rows are the licensed restatement.
- **Check order stays unconditional:** the token checks run ahead of the §15.1 precondition, matching the shipped order where a wired verifier runs before `session.Validate` in `admitUpload` and `handleFinalize`.
- **`derive_failure` row:** it refuses `/upload`, `/upload-archive`, and `/finalize` at the SPEC-1 token checks, and SPEC-2(e) changes the §15.1 Derive-failure table to say so.
- **Derive-failure cell wording:** the cell cites §7.1 **Check order:** and names no status, because a foreign-session token answers 403 ahead of the digest check.
- **`Verify` ordering:** as staged it checks scope before the empty digest, so a foreign token on a digestless row answers 403 and the session's own token answers 401.
- **§7.1 rule 2 and §7.2 summaries:** both name only `/terminate`, `/start`, `/resume`, and `DELETE` as 409 and need no edit.
- **Check-order labels:** §7.1 has one **Authentication:** and one **Check order:** label, and SPEC-1 precedes every citation of them.
- **Digest on every path:** create and derive and replay stamp digest and expiry through `mintUploadToken`, and `create_and_start` stamps directly, each on a new row before the insert; `persistDeriveFailure` never mints.
- **No re-mint:** no path re-mints over a live digest, and an idempotent create replay returns the original token, which still matches the row.
- **Grep for mints:** `mintUploadToken` has callers in create.go, derive.go, and replay.go; a grep for `IssueDetailed` alone misses derive and replay.
- **No crash window:** a crash between mint and insert leaves no row; a persisted `derive_failure` row holds no digest and no client got a token for it.
- **No return to `created`:** no spec or code edge returns a session to `created`, so row-derived consumption is restart-safe and monotonic.
- **`UPLOAD_TOKEN_CONSUMED` meaning:** no row passes `finalizing` without `ready`, because failure reports on pre-running rows are no-ops and the watchdog is the exit, so it always means finalization committed.
- **Finalize retry:** a lost commit acknowledgement followed by a retry answers 410 only when the commit landed, and the finalizing watchdog owns the row otherwise.
- **Create-and-start:** it commits the row directly to `running` and never passes `ready`; SPEC-1's second sentence covers it.
- **Slot-level states:** `receiving_uploads` and `running_setup` never appear in the session row's State, so `UploadTokenConsumed` needs no arm for them.
- **§7.2 and `finalizing`:** §7.2 has no `finalizing` edges; the §15.1 finalize row is the spec statement of `finalizing` to `ready`.
- **MCP tools:** the MCP upload and finalize tools answer `VALIDATION_ERROR` for an absent `uploadToken` and never reach the REST check.
- **Peer finalize:** it needs no replica-local binding or coordinator, because `prepareAtFinalize` keys on `row.PodAssignment`, pre-running states hold no lease, and blobs live in the shared Artifact Store.
- **Post-Put recheck:** the `finalizeUpload` recheck reads through `s.readPrimary`, and a read error refuses with the `admitUpload` mapping (404 for a missing row, 500 otherwise) and the `UPLOAD_CHANNEL_CLOSED` unwind.
- **Recheck test home:** the lag and fault cases sit in Testing item 5, and item 10 stays unedited because its in-process store has neither.
- **Late peer upload:** each commit writes a new part_id, so a late peer upload leaves an orphan blob and cannot replace plan-named content.
- **`uploadAborts`:** it stays per process (out of scope), with a summary Defects entry filed by RECORDS-1 item 4.
- **Upload cap scope:** SPEC-1(f) states it in the §11.1 cells as `Per-session, global (each per replica)` and `Per-file, per-session (per replica)`; both fit the 39-character column, and the reason is summary decision 19.
- **Global cap was already per replica:** `upload_limits.go`, the flag help, and the chart comment already said so, so the §11.1 cell was wrong before this proposal.
- **`uploadLimits` non-goal:** it left the Non-goals line, and `closeSession` staleness goes in the Defects list, which RECORDS-1 item 4 files; the stale entry refuses nothing.
- **`closeSession` caller:** `s.uploadLimits.closeSession` has one non-test caller, the finalize handler; `upload_abort.go` holds an unrelated `closeSession`, so a bare-name grep overcounts.
- **`upload-max-bytes-per-session` help:** the replacement was widened past the DEFERRED wording to cover "The sum of all uploads in a session".
- **Chart comment edit:** it lands with CODE-2, and no CHART deliverable exists.
- **§11.2 backstop:** `/upload` reserves against the Redis-backed tenant counter with a durable fallback, so caps are bounded at tenant scope only when the tenant sets `storageQuotaBytes`.
- **§7.4 size wording:** "per-session upload limit" and "Per-file and total session size limits" stay true, and §11.1 holds the scope.
- **Other cap sites:** `uploadLimits` and the cap flags have no docs/, schema, or configuration page site.

Upload token, removal and performance:

- **Rotation surface:** `lenny-ctl admin keys rotate uploadToken` exists only in the §18.31 bullet SPEC-2(c) deletes, and `keys rotate` and `admin/keys` appear in no docs, cmd, pkg, §24, or admin table; "keys rotate" at §12.4 is Vault transit.
- **`uploadToken` spec sites:** it appears only in §7.1, §7.4, §14, §15.1, §18.11, §18.31, and §29; §29 steps 12 and 14 stay true, and `HMAC` hits outside the staged sites concern webhooks, adapter handshakes, or experiments.
- **S4 and S5 boundary:** S4 is CODE-1 plus CODE-2; CODE-1 deletes `sign`, `computeHMAC`, `parseAndVerifyHMAC`, `(*KeyRing).keys`, and the two rotator tests that call `Verify`; CODE-3 at S5 keeps the exported key material and the rest of `rotator_test.go`.
- **Rotator test file:** only those two tests use `NewIssuer`, `NewVerifier`, or `Verify`, and the rest compile at S4 with `context` and `fmt` still used.
- **Ring fields after S4:** `Issuer.ring` and `Verifier.ring` have no reader; `unused` default field-writes-are-uses is expected to pass, recalled from docs and not run.
- **Primary read cost:** the check adds no net-new Postgres write and one lock-free PK read per upload and finalize, bounded by `--session-primary-read-timeout` (default 5 s).
- **Primary read load:** Tier 3 is about 1,200 single-row transactions/s and Tier 4 about 12,000 PK reads/s assuming 5 uploads per session; no spec budget exists to break.
- **`GetPrimary`:** it shares `selectList` with `Get`, so it returns digest and expiry; no production wrapper hides it, and `ReadPrimary` is tenant-scoped with no replica fallback.
- **Failover:** uploads now answer 500 during a Postgres failover until promotion; the window is under 30 s against a 300 s token TTL, Edge cases records it, and §12.3 promises nothing contrary.
- **Idempotency and 500s:** `maybePersist` stores only responses below 500, so a primary-read 500 is not replayed; a stored 401, 403, or 410 replay stays F-11.5.27.
- **Anonymous reads:** upload and finalize sit behind the §10.2 ownership check, so a malformed token gives an anonymous caller no primary read.
- **Tenant scope:** the row read is tenant-scoped ahead of the token check.
- **Client surfaces:** openapi.json already lists all `UPLOAD_TOKEN_*` codes on upload, upload-archive, and finalize and needs no edit; no SDK names a code or parses the token, and `uploadToken` stays an opaque string in the schemas.
- **Tier-11 pins:** `tests/tier11_docs` pins none of the 409 strings DOCS-1 rewords.
- **spec-map:** `tests/spec-map.json` names no uploadtoken, rotator, or deleted consume test, so `validate-maps` holds between S4 and S9.
- **Tier-7a transitionStore:** without a forward it passes silently, because the fallback mutator returns errPrimaryReadOnly and `isFinalizeEntry` reads false; the forward stays in item 6 and S4 gains tier 7a.
- **Other embedding stores:** `credential_lookup_flapping_race_test.go` embeds the store but does not drive finalize.
- **Tier-7a race:** it is TEST-1 item 21 landing at S9, so the S5 sweep never rewrites a load_local file its gates do not run.

OAuth state and playground:

- **Expired `state`:** SPEC-3 states no per-backing redirect code, and the staged spec names `oidc_state_unavailable` alone among the `oidc_state_*` codes; the CODE-5 `Consume` table is the home of the rest.
- **Security of the code set:** no control depends on the code name, because every branch ends the flow before the token exchange.
- **`oidc_state_unavailable` namespace:** it does not collide with the reserved `tenant_*` query codes, and the playground UI does not branch on `oidc_state_*`.
- **§18.28 edit:** SPEC-3(h) reads "random `state`", the word §9.3 uses, and it sits in SPEC-3 because CODE-4 at S6 depends on S3 only.
- **No signed state left:** after SPEC-3(a), (e), and (h) no signed cookie or signed connector `state` statement remains in spec/ or docs/.
- **§9.3 and §12.4 homes:** §9.3 holds entropy and TTL (at least 128 bits, 10 minutes), and the §12.4 key row holds TTL and atomic consume; staged text cites them.
- **Entropy:** connector state is 16 bytes and playground `newOpaqueID` is 32 bytes.
- **§12.4 third site:** the wrapper paragraph says "opaque-id credential requirement" and stays true.
- **§12.4 failure table:** it does not enumerate every key, and **Shared Redis topology** accepts a fail-closed statement in the owning section, so §27.3.1 satisfies it with no row.
- **Plaintext verifier:** §12.4 app-layer encryption covers access tokens and credential lease references only, and §9.3 already stores `code_verifier` beside `state`.
- **Unknown `state`:** `consumeStateScript` returns at the GET miss and writes nothing.
- **Cross-flow entry:** it is refused both ways; a playground entry at the connector callback falls out at `connectors.Get` with an empty tenant and connector id.
- **Cookie contents:** the cookie carries only `state` and the verifier, so no OIDC nonce or return path moves.
- **`CONNECTOR_OAUTH_*`:** the codes appear only in `connector_oauth.go`, so retiring one touches no spec, OpenAPI, or SDK catalog, and openapi's callback row stays true.
- **Phase order:** Phase 4.5 ships OAuth state storage, the connector `StateStore` is Phase 12a, and the playground is Phase 17a, so reuse has no inversion.
- **Tier-11 key pin:** `redis_key_prefix_registry_test.go` pins only the literal key and category phrases, so SPEC-3(f) and (g) leave it green.
- **§15.1 callback row:** "Validates `state`" stays true.
- **§28 and §29:** neither has a playground or OAuth state site, so no register row is added.
- **`REDIS_UNAVAILABLE`:** it is absent from the §15.1 catalog and errorclassify, but it predates 0114 at §27.3.1, so SPEC-3(d) adds no new defect class.
- **Redis timeouts:** `RedisStateStore` uses `context.Background()`, and `pkg/redisconn` sets no override, so go-redis default dial, read, and retry limits bound `Put` and `Consume`.
- **Sweep wiring:** `buildCredentialSurface` and `buildHTTPSurface` run before `startBackgroundWorkers`, so the memoised helper sets `w.connectorStateStore` before the sweep's nil check, and a playground-only gateway is swept.
- **`connectorStateStore` field:** it stays, pinned by a test folded into item 13; deleting it for `w.oauthState` is the first alternative if two fields holding one store draws an objection.
- **`buildCredentialSurface` tests:** none exists today; the body needs `w.f` with connector, LLM proxy, dev-mode, and primary-read-timeout pointers, and dereferences `sessionSrv` only with a materializer.
- **Store-wiring fixture:** `gatewayWiring{f: parseFlags()}` plus `redistopology.Build` over miniredis, because the helper reads `w.concernRedis.For`.

Test plan, scope, and records:

- **Item numbering:** tier-8 and tier-9 tests are appended as items 22 and 23 after item 21, and the wiring test folds into item 13, so Red evidence and Files touched numbers hold.
- **Item 22 wording:** "the callback of another login begun before the cut" is deliberate, because the callback sent during the cut clears the cookie.
- **Tier-9 test:** it drives real gateway routes; the no-exchange assertion is the matching callback succeeding with the same code, and the connector case asserts no provider hit.
- **OIDC stub:** it deletes a code on redemption, including a failed one.
- **Existing OIDC pins:** `fakeOIDC.lastVerif` is written and never read, and the tier-4 IdP does not check `code_verifier`, so no existing test pins which verifier reaches Exchange.
- **Consume concurrency:** `redis_state_store_test.go` already has a single-winner test, so a playground concurrent test is not owed; the tier-4 connector flow runs on one gateway.
- **Tier 0 and tags:** tier 0 vets contract-tagged tests and has no equivalent for the `security` tag, so a tier-9 file is uncompiled unless tier 9 runs.
- **Plan scope:** 0114 claims the §10.2 "Shared gateway key-source proposal" entry (F-7.1.31, F-27.3.16) plus F-15.1.69; no R-step or §28 claim-register rule applies, and F-11.5.27 keeps its own routing.
- **Shared key absence:** it is a recorded divergence in summary decision 1 and Non-goals, and the plan qualifies F-7.1.31 with "where it fits".
- **Anonymous login write:** it is resolved as staged; neither Edge cases section gains a line, summary **Watch out for.**, Non-goals, and RECORDS-1 item 5 (F-27.3.17) hold it, and Open decisions reads "None."
- **Dead-letter receipt signer:** it stays out of scope with a Defects entry; no in-tree verifier checks a signature, and the separate defect is that §12.8 promises a public key a symmetric per-process key cannot provide.
- **F-11.5.26 and F-11.5.27:** they stay with their own proposal, with a Defects entry and no staged fix.
- **REST `/upload` in `running`:** it stays unresolved (F-15.1.57) with a Defects entry, and its Non-goals bullet was deleted, so Defects is the one record; the code route is `/v1/sessions/{id}/upload-to-session`, which no spec file names.
- **Chart callback flag:** the chart stays without `--connector-oauth-callback-url`; the Non-goals line and a Defects entry both stand, and the gateway Deployment has no extraEnv, envFrom, or extraArgs hook, so a deployer patches the rendered Deployment.
- **Deleted Non-goals:** F-11.5.26 and F-11.5.27, REST `/upload` in `running`, and the connector `Consume` fault mapping were deleted with no stubs; the spec-changes Edge cases §11.5 bullet stays.
- **Open findings:** F-11.5.26, F-11.5.27, and F-15.1.57 already exist; `uploadAborts`, the receipt signer, the chart callback flag, and `closeSession` have none.
- **Defects citations:** every file:line in the summary Defects section resolved at non-spec-recheck round 1, and upload.go:338 was a drifted line now cited directly.
- **Spec staging status:** spec-changes.md was byte-identical to snapshot spec-r6 through spec-recheck, and every quoted anchor matched its target once; the two two-hit strings are disambiguated by the staged wording.
- **Non-spec staging:** the staging had no delta against spec-recheck-r2 at the start of non-spec-recheck round 1.
- **Single-source status:** every staged rule has one stating site, and sites elsewhere cite §7.1 by bold label.
- **Filed-nothing decisions:** the per-replica §11.1 caps, the one-clause "one script call and no write" repeats, and the provider-outage reason were reviewed and refuted on materiality.
- **`problem-statement.md:17`:** "they do not refuse a token" understates peer stream reachability, and it stays unedited because the out-of-scope call stands.

### Traps

- **Keeping 409 on a `derive_failure` row:** it loses to the unconditional check order. A terminal-first check or a no-digest carve-out adds an exception, lets a tokenless request reach the precondition, and falsifies "401 UPLOAD_TOKEN_EXPIRED in every session state". Minting a digest on the audit row adds a write for a value no client holds.
- **Per-backing expired `state`:** do not restore that sentence to SPEC-3(b) or Edge cases. Writing both backings puts a dev-fallback difference into normative text, and one backing alone reads as true of all. The summary's `oidc_state_expired` **Watch out for.** bullet and Testing item 12's memory-versus-Redis cases are the remaining statement and pin, so keep them when reducing item 12.
- **§18.28 alternatives withdrawn:** moving the edit to SPEC-2 falsifies its "rotation items" description and lets CODE-4 lead the spec; "single-use `state`" is true but §9.3 does not say it.
- **Upload cap alternatives withdrawn:** a paragraph below the §11.1 table overclaims "the cross-replica bound"; Redis-backed counters add a key, Redis on the upload path, and crash-release for caps off by default; a defects entry alone leaves a spec-code disagreement this proposal makes reachable.
- **Problem statement line 17:** do not edit it for the caps. Sharing counters stays out of scope and the limiter refuses no token.
- **Searching for the signer:** a search for HMAC, StateSigner, cookie, or `uploadToken` misses "signed `state`" in the Phase 12a §18.28 bullet, outside §7, §12, §15, and §27. Search "signed" as well when retiring a signer. SPEC-3(h) now stages this site.
- **`derive_failure_audit_test.go` stale pass:** it stays green on the old 409 through the nil-verifier opt-out, so the Testing item 3 audit of verifier-wired tests never reaches it. Only item 4's explicit direction, dropping `finalize` from the loop, does.
- **§15.1 `UPLOAD_TOKEN_*` neighbours:** those rows predate the proposal and restate §7.1 conditions. That does not license the new row to list every disjunct.
- **SPEC-3(e) login row:** its three-clause summary with a See link matches every neighbouring row, and the entropy and TTL copies were refuted on materiality. Do not file it as a copy.
- **"Every write" claim:** SPEC-1 **Check order:** and the non-spec-changes text say the checks run ahead of "every write", while the idempotency Claim at `pkg/gateway/middleware/idempotency/pgstore/pgstore.go` inserts a pending row first on a keyed finalize. The accepted-failure line discloses only the stored-response replay. The summary entry records the Claim write, and a later firing that narrows the wording has this as input.
- **§11.5 carve-out:** "401 UPLOAD_TOKEN_EXPIRED in every session state" and the 410 "on every replica" rule carry no §11.5 carve-out in staged text; it stands only in spec-changes Edge cases. The proposal closing F-11.5.27 states that ordering in §7.4 or §11.5.
- **"governed by the `uploadToken`":** this is undefined for REST `/upload` in `running`, and §15.1 and §29 admit that arm while SPEC-1's single-use bullet answers 410 for a live token. The DOCS-1 "check the token first" sentence has no mid-session qualifier. Both describe the code as it stands, and the gap is the Defects entry; do not refile it as new.
- **Summary Decision 1 and the signer:** "each per-process secret is deleted" describes the repaired flows. The dead-letter signer keeps its per-process key, so an implementor must not read the sentence as covering it.
- **Anonymous login write:** `GET /playground/auth/login` becomes an anonymous Redis write held for 10 minutes, bounded only by the §11.1 global rate limit, which chart flags default off. The `maxmemory` failure under `noeviction` is stated only in RECORDS-1 item 5, and removing that item or the Non-goals bullet reopens the question.
- **`uploadAborts` Defects entry:** do not delete it as a follow-on to the recheck fix. A peer stream still runs to the recheck because the abort signal stays per process, and §7.4 "Upload channel closes after workspace finalization" depends on that recheck.
- **Recheck alternatives withdrawn:** a flat 500 gives one read two mappings; 410 on a read error claims a closed channel a read fault does not establish; 503 is no upload code; a Redis abort signal is new machinery; a store hook in item 10 is a second home for the primary-read rule.
- **Item 5 recheck store:** memstore `GetPrimary` is `Get`, so the cases need a store that switches by GetPrimary call count: first `created`, then the injected value, while `Get` keeps `created`. `accessRow` adds a `GetPrimary` only when replica `Get` fails, so a fixture that fails `Get` shifts the count by one.
- **Recheck position:** it stays ahead of the §11.1 `commitBytes` check, which is why its read-error branch has no byte count to undo. Moving it later breaks the unwind.
- **Moving key material into CODE-1:** do not move `SigningKey`, `KeyRing`, `ConsumedTracker`, `MemoryTracker`, `Consume`, or `ConsumeDigest`. They are exported, so `unused` does not flag them, and the constructors and `rotator.go` still reference the first three at S4.
- **S4 alternatives withdrawn:** merging S4 and S5 overturns decision 17 and puts the 29-file constructor sweep in the security fix; `nolint` is barred; deleting all of `rotator_test.go` leaves `rotator.go` untested while the gateway runs it.
- **Rewriting the rotator tests:** rewriting the two deleted tests against row-based `Verify` cannot work, because row verification reads no key, so overlap acceptance and swept-key rejection are not assertable.
- **`rotator_test.go` package:** it is `package uploadtoken`, and `uploadtoken_test.go` is `package uploadtoken_test`; one uncompilable file stops the whole package's test binary at S4.
- **`derive_upload_token_test.go`:** line 98 also calls two-argument `Verify` and uses `ParsedToken`. No Testing item names it, so the implementor meets it as a compile error at S4, covered only by **Commit sequence.**'s fixtures sentence.
- **Redis loss in tests:** `containers.Redis.Stop` terminates the container and a replacement gets a new port. Use `tcpfault.Start(t, rd.Addr)` with `Cut` and `Restore`, as `tests/tier8_chaos/delegation_budget_redis_loss_test.go` does. Testing item 16 stops Redis with no return step.
- **Callback during an outage:** it clears the state cookie before `Consume` faults, so a browser cannot retry it. The recovery case is a second login whose callback arrives after the restore.
- **Connector peer tests:** they must share Postgres as well as Redis. The callback calls `r.connectors.Get` after `Consume`, and the registry is per process without `--postgres-dsn`, so Redis alone fails with `CONNECTOR_OAUTH_INVALID_CALLBACK`. A tier-1 second router shares the fixture's connector and credential stores too.
- **Item 23 "on one Redis":** the wording is deliberate. `TestOAuthConnector` authorizes through dev-mode role headers and the live playground OIDC test runs `--dev-mode=false`, so the cross-flow case may need two gateways. That mode with a plaintext `--redis-url` needs `--redis-allow-insecure`.
- **Exchange counter alternative withdrawn:** the stub's code deletion already proves no exchange ran, and in-process tier-9 handlers duplicate Testing item 12.
- **Testing a per-replica cap:** `uploadLimiter` caps default to 0 (flag and chart), so a test must set `upload-max-bytes-per-session` explicitly.
- **Chart comment wrap:** the `maxBytesPerSession` first line grows past the block width after the replacement; rewrapping is the implementor's call.
- **`upload_limits.go` comment:** line 20 still says the per-session axis bounds "the sum of all uploads in one session"; CODE-2 aligns only the flag help and option comment.
- **`wire-format.md` absence check:** the page keeps "HMAC-SHA256" for the webhook signature, so a page-wide absence check on that string fails after DOCS-1.
- **`sessionrecord_test.go`:** its handlers need a wired store as well as the helper edit item 13 names; the Design's fixture sentence covers this.
- **RECORDS-1 item 4:** "the other entries" and "each entry" sweep in defects already filed (F-11.5.26, F-11.5.27, and the route half of F-15.1.57), so it must not refile them, and any Defects entry added for a filed finding becomes a duplicate.
- **RECORDS-1 item 3:** "the summary's `Consume` fault mapping entry" resolves only to the Defects entry "The connector callback reports a store fault as a bad state". Removing a Defects entry assuming a Non-goals twin carries it loses the record.
- **Non-goals twins left in place:** the `uploadAborts`, dead-letter signer, and `--connector-oauth-callback-url` bullets also have Defects twins and were left because the finding did not name them.
- **Plan wording:** F-27.3.16 "Sign it with a key every replica shares" and the "shared key" title are superseded by the later owner direction (Redis state, no chart HMAC Secret, no KMS alias), so neither is a conformance gap.
- **§12.3 label:** "**Read replicas:**" states no primary-read rule; SPEC-1(a) cites it only for the split, and the rule stands in the staged sentence.
- **No-`--redis-url` non-goal:** it is a non-chart posture, and §17.4 confines the in-memory backing to single-process dev mode, so it is not a missing Edge cases row.
- **§12.8 search:** the "External verifiers" sentence has a capital E, so a case-sensitive search misses it.
- **Fixer grant:** when the grant bars non-spec edits, record each as a DEFERRED line with the exact wording. A spec-lens fixer may open non-spec-changes.md only for a statement an edit falsified.

### Open

- **Web-playground docs bullet:** the DOCS-1 gap on `docs/operator-guide/web-playground.md` is carried under Deferred; [reconcile.1], [non-spec-recheck.1.review-docs-alignment.1] says this round files it.
- **Summary Goals overstates tier 5:** kind pins only the uploadToken path, and the design left this unedited as a separate finding; [non-spec-recheck.1.fix-G3.1], [non-spec-recheck.1.fix-design-G3.1].
- **`flake-budget.yml` pattern:** the tier-7a `--pattern` names only pod-coordination cases, so finalize race cases carry a stress budget CI never spends; [non-spec-recheck.1.fix-design-G2.1].
- **`UPLOAD_CHANNEL_CLOSED` undocumented:** it is emitted at upload.go and asserted by Testing items 5 and 10, is absent from spec/, docs/, schemas/, and errorclassify, and needs a Defects entry or a §15.1 row; [non-spec-recheck.1.fix-design-G2.1].
- **UNVERIFIED `translator.NewOpenResponsesHandler`:** the item 13 implementor confirms it accepts the test wiring; [non-spec-recheck.1.fix-design-G3.1].
- **UNVERIFIED RECORDS-1 duplicates:** whether item 4 files duplicates of F-11.5.26 and F-11.5.27 is for the records reviewer; [non-spec-recheck.1.review-plan-conformance.1].
- **UNVERIFIED `closeSession` size:** the summary's "The entry refuses nothing" gives no size for one entry per session per peer until restart, against 2,000 creations/s at Tier 4; [spec.4.review-performance.1].
- **UNVERIFIED "predates this change":** the summary's claim on the absent §12.4 OAuth state row is true for the connector use and not for the playground use; [spec.1.review-reliability.1].

### Deferred

- DEFERRED [proposals/0114_fix_upload-tokens-and-the-playground-oidc-state-depend-on/0114_fix_upload-tokens-and-the-playground-oidc-state-depend-on.non-spec-changes.md]: DOCS-1 has no target for docs/operator-guide/web-playground.md, whose `oidc` bullet (line 40) enumerates the login endpoint's 503 causes as unset `auth.oidc` values or an unreachable discovery document (`LENNY_PLAYGROUND_OIDC_UNAVAILABLE`). After SPEC-3(a) and (d), a store fault at login also answers `503 REDIS_UNAVAILABLE`, and a store fault at the callback redirects to `oidc_state_unavailable`. The docs loop should add that cause to the bullet and add the page to DOCS-1 Targets and Files touched. The remedy is docs-only, so this spec loop cannot close it. Authoring lands in non-spec-changes.md (DOCS-1 Targets, a new bullet, and Files touched) and then in that docs page. [spec.3.review-docs-alignment.1, spec.1.review-docs-alignment.1, reconcile.1]

## Ledger

### [non-spec-recheck.2.fix-G1.1]

DECISION: Testing item 16 gains one sentence requiring both gateway hops to carry one `Host` through a new `playgroundHop` host parameter, while each hop still dials its own gateway's `BaseURL()` — BECAUSE `callbackURL` builds `redirect_uri` from `r.Host` at login and at callback, and the stub compares it at redemption, so a correct implementation fails item 16 as written — ALTERNATIVES: relaxing the stub comparison drops the authorization-code pin; a fixed callback flag is a product change for a test defect; a reverse proxy is a larger fixture with the same effect as `req.Host`; deleting item 16 loses the only process-level login-on-A, callback-on-B proof.
WATCHOUT: set the override with `req.Host`, which is the field `client.Do` sends; a `Host` entry in `req.Header` is ignored by net/http on the client side. The authorization hop to the stub in the same test takes no override. — EVIDENCE: tests/tier4_integration/playground_oidc_login_test.go `playgroundHop`; pkg/gateway/mcpfabric/playground/auth.go `callbackURL`; tests/testinfra/stubs/oidc/oidc.go `redeemCode`
FACT: item 11 needs no `Host` condition because its callback URL is the fixed flag value, and items 12 and 22 stay within one process. A two-gateway cross-flow case in item 23 keeps a login and its matching callback on one gateway. — EVIDENCE: non-spec-changes.md Testing items 11, 12, 22, and 23

### [non-spec-recheck.2.fix-design-G1.1]
DECISION: item 16 gains one sentence requiring one `Host` value on both gateway hops through a `playgroundHop` host parameter — BECAUSE the failure is in the test staging and production replicas sit behind one ingress host — ALTERNATIVES: relaxing the stub's `redirect_uri` comparison (removes a pin of the OAuth contract), a fixed playground callback flag (a product change for a test defect), a reverse proxy in front of A and B (a larger fixture for the same effect), and deleting item 16 (drops the only process-level login-on-A, callback-on-B proof).
FACT: `callbackURL` is the only `r.Host` reader under pkg/gateway and cmd/lenny-gateway, so no gateway middleware refuses an overridden Host, and the stub stores any `redirect_uri` at /authorize and never dials it, so a value that resolves nowhere is safe. — EVIDENCE: pkg/gateway/mcpfabric/playground/auth.go:486; tests/testinfra/stubs/oidc/oidc.go:215-231
WATCHOUT: `playgroundHop` is private to tests/tier4_integration/playground_oidc_login_test.go, with four call sites there, so the parameter changes that file alone; item 23 sits in tier 9 and cannot reuse it. If item 23 uses two gateways for its cross-flow case, the matching callback stays on the gateway that took the login, so it needs no Host override. — EVIDENCE: tests/tier4_integration/playground_oidc_login_test.go:23,85,101,106,113

### [non-spec-recheck.2.review-applicability.1]
FACT: the playground redirect_uri is derived per request from r.Host, and oidcstub compares it at redemption, so two gateway processes on different ports cannot complete login on A and callback on B unless both hops carry one Host value. EVIDENCE: pkg/gateway/mcpfabric/playground/auth.go:93,147,479-486; tests/testinfra/stubs/oidc/oidc.go redeemCode; tests/tier4_integration/playground_oidc_login_test.go:23-45
WATCHOUT: playgroundHop has no Host parameter, so Testing item 16 needs a helper change as well as the second gateway. EVIDENCE: tests/tier4_integration/playground_oidc_login_test.go:23
FACT: item 11's connector flow is unaffected, because its callback URL is the fixed --connector-oauth-callback-url value. EVIDENCE: cmd/lenny-gateway/credentialsurface.go:210
FACT: CODE-1's four deletions lose their last callers as stated: rotator.go and every test file have no call to keys(), sign, computeHMAC, or parseAndVerifyHMAC, and only the two named rotator tests call Verify. EVIDENCE: pkg/uploadtoken/rotator.go; pkg/uploadtoken/rotator_test.go:46,64,78,102
FACT: only finalizeUpload holds a post-Put recheck; upload-archive shares it through runUpload, and upload_to_session.go:77 is the bearer route. EVIDENCE: pkg/gateway/sessionserver/upload.go:154,445,498

### [non-spec-recheck.2.review-citations.1]
FACT: The round-2 delta citations hold: finalize_handler.go:220, upload_abort.go:36, upload.go:498, the two rotator tests that call NewIssuer and NewVerifier, keySetReplicaArgs, tcpfault.StartURL/Cut/Restore, containers.Redis.Stop terminating the container, the oidc stub's single redemption, and the tier-7a header's "no upload-token verifier" sentence. EVIDENCE: tests/testinfra/containers/redis.go:98, tests/testinfra/stubs/oidc/oidc.go:286, tests/tier7a_load_local/finalize_admission_race_test.go:13
FACT: "HMAC" appears in session-lifecycle.md and glossary.md only on the uploadToken lines, so Testing item 19's absence check is satisfiable after DOCS-1. EVIDENCE: docs/client-guide/session-lifecycle.md:141, docs/reference/glossary.md:427

### [non-spec-recheck.2.review-mechanism.1]
FACT: requireSessionAccess reads through accessRow, which tries store.Get first and reaches GetPrimary only on a Get error, so with a healthy Get the first GetPrimary on an upload is admitUpload and the second is the recheck, as Testing item 5 counts them — EVIDENCE: pkg/gateway/sessionserver/rbac_gate.go:214-219
FACT: the Testing item 6 list matches every sessionserver test file that embeds sessionstore.Store and drives /upload or /finalize; laggingReplicaStore in launch_bound_test.go embeds *memstore.Store, drives /start only, and needs no forward — EVIDENCE: pkg/gateway/sessionserver/launch_bound_test.go:210,271
FACT: item 22's SPEC-3(b) and (d) labels are right: (b) is the callback store fault and (d) is the login row, and SPEC-3(a) only cites (d) — EVIDENCE: 0114 spec-changes.md SPEC-3(a), (b), and (d)

### [non-spec-recheck.2.review-reliability.1]
WATCHOUT: the new recheck read-error branch fires on a client disconnect, because `ReadPrimary` derives from r.Context(), and the Adjust release on the same context then fails and `Failover.Adjust` swallows it, so the Redis counter keeps bytes of a soft-deleted blob until a recovery edge or startup rehydrate. EVIDENCE: pkg/gateway/session/sessionstore/primary_read.go:48; pkg/gateway/quota/storagequota/failover.go:121; pkg/gateway/sessionserver/upload.go:506
FACT: tcpfault Cut closes proxied connections and the listener and keeps upstream data, so Testing item 22's pre-cut login callback can complete after Restore. EVIDENCE: tests/testinfra/tcpfault/tcpfault.go:55
FACT: the tier-4 connector fixture registers a public client, so a peer callback needs no shared client-secret key. EVIDENCE: tests/tier4_integration/connector_oauth_test.go:121
FACT: `/upload` is absent from the idempotency AllowedPaths, so a recheck 500 leaves no pending claim to block the re-send. EVIDENCE: cmd/lenny-gateway/httpsurface.go:731

### [non-spec-recheck.2.review-security.1]
FACT: the fix-round text checks out under the security lens. `finalizeUpload` has one caller, the recheck read error now fails closed through `readPrimary`'s bounded read, `(*KeyRing).keys` and `sign` have no reader in rotator.go or the surviving rotator tests, and SPEC-3(b) and (d) are the callback and login rows Testing item 22 names. EVIDENCE: pkg/gateway/sessionserver/upload.go:266; pkg/uploadtoken/rotator.go:168-202
FACT: tier-7a item 21's loser outcomes hold, because a refused compare-and-swap inside `Update` renders through `writePreconditionError` as 409. EVIDENCE: pkg/gateway/sessionserver/finalize_handler.go:141-160

### [non-spec-recheck.2.review-single-source.1]
FACT: The r1-to-r2 delta adds no rule stated in full at two sites. The recheck's outcome list lives in CODE-2 and Testing item 5 asserts it as a test oracle; summary decision 7 and the Edge cases bullet each give it in a clause. Item 22 cites SPEC-3(b) and (d) rather than restating them, and both labels are correct. EVIDENCE: non-spec-changes.md:82,244,266,273; summary.md:24; spec-changes.md SPEC-3(b),(d)
FACT: Removing the three Non-goals left no stale pointer: no proposal file cites a Non-goals bullet, and CODE-4 and RECORDS-1 cite the Defects section, which holds those entries. EVIDENCE: summary.md:92-103; non-spec-changes.md:117,229

### [non-spec-recheck.2.review-test-coverage.1]
FACT: a test wrapper embedding `*memstore.Store` promotes `GetPrimary`, so only wrappers embedding the `sessionstore.Store` interface hit the `ReadPrimary` fallback to `Update`; `vanishingStore` already reaches memstore's own `GetPrimary` on an unseeded store. — EVIDENCE: pkg/gateway/sessionserver/tracing_spans_test.go:202-212, pkg/gateway/session/sessionstore/memstore/memstore.go:238
FACT: `buildCredentialSurface` has no test caller today, but its only hard needs for Testing item 13 are the `gatewayFlags` pointers it dereferences; the flow branch is gated on `*f.connectorOAuthCallbackURL`. — EVIDENCE: cmd/lenny-gateway/credentialsurface.go:51-55,181
WATCHOUT: S4 also breaks `TestVerifyAcceptsOverlapKey`, `TestRotateOnlyOnePreviousKeyIsAdded`, and `TestVerifyRejectsTamperedHMAC`, which CODE-1 does not name beside the two rotator tests; the `Verify` signature change makes that a compile error, so the "each code step edits the fixtures it breaks" rule catches them. — EVIDENCE: pkg/uploadtoken/uploadtoken_test.go:94,129,157
FACT: concurrent single-use `Consume` is already pinned at the store level by `TestRedisStateStore_ConcurrentConsumeSingleWinner_spec_9_3_157`, and CODE-5 reuses that store unchanged. — EVIDENCE: pkg/connectoroauth/redis_state_store_test.go:149

### [non-spec-recheck.3.review-applicability.1]
FACT: the only delta since recheck round 2 is the Testing item 16 `Host` sentence. It applies cleanly: `playgroundHop` has one definition and four call sites in its own file, `callbackURL` reads `r.Host`, and oidcstub compares `redirect_uri` at redemption. — EVIDENCE: tests/tier4_integration/playground_oidc_login_test.go:23; pkg/gateway/mcpfabric/playground/auth.go:486; tests/testinfra/stubs/oidc/oidc.go:297

### [non-spec-recheck.4.review-applicability.1]
FACT: the UNVERIFIED `translator.NewOpenResponsesHandler` line is settled for Testing item 13: it panics only on a nil `HolderForwarder` or `Terminal`, and `buildCredentialSurface` always passes both from `sessionSrv`, so the test only needs a real `*sessionserver.Server`. EVIDENCE: pkg/gateway/environment/translator/open_responses.go:261-266; cmd/lenny-gateway/credentialsurface.go:75-82
FACT: a full applicability walk of the checklist found no forward reference: every deliverable sits in one step, the lanes are valid, spec steps lead, and each code step's spec statements land in a named dependency. The anchors in CODE-1 to CODE-5, DOCS-1, RECORDS-1, and Testing resolve in the current tree, and the Testing item 16 delta applies cleanly. EVIDENCE: implementation-checklist.md; .claude/skills/implement-proposal/SKILL.md "One execution sequence"
USEFUL [standing context: spec-map]: `tests/spec-map.json` names no deleted test, so no tier-0 map gate trips between S4 and S9.

### [non-spec-recheck.4.review-citations.1]
FACT: every file:line in the summary Defects section resolves at this tree, including finalize_handler.go:107 and :220, upload.go:308, :316, and :498, and idempotency.go:357, :424, and :472 — EVIDENCE: direct reads, this round.
FACT: item 16's delta is accurate: callbackURL derives from r.Host (auth.go:486), and the stub refuses a differing redirect_uri (oidc.go:297). playgroundHop has callers only in its own file, so the new parameter touches nothing else — EVIDENCE: tests/tier4_integration/playground_oidc_login_test.go:23.
CORRECTS [Standing context, Existing OIDC pins]: the tier-4 IdP does check code_verifier (tests/testinfra/stubs/oidc/oidc.go S256 check). Only fakeOIDC.lastVerif is unread.

### [non-spec-recheck.4.review-client-surface.1]
FACT: openapi.json has no /playground paths, so login 503 REDIS_UNAVAILABLE and oidc_state_unavailable touch no OpenAPI or SDK surface; the UI and error page do not branch on oidc_state_* codes. EVIDENCE: pkg/gateway/externalapi/openapi/openapi.json (grep playground: no hit); pkg/gateway/mcpfabric/playground/auth.go:462
FACT: the tier-3 parity test needs only the exemption drop plus the CODE-3 sweep; its tokens come from the create response or fail by digest, so its "foreign signing key" case stays 401 under row verification, though the case name goes stale. EVIDENCE: tests/tier3_contract/rest_mcp_consistency/finalize_upload_token_parity_test.go:105-147

### [non-spec-recheck.4.review-docs-alignment.1]
FACT: lookup and grep over docs/ show no page names `oidc_state_*`, `lenny_playground_oidc_state`, `conn:oauth:state`, or `CONNECTOR_OAUTH_*`, and no page calls a connector or playground `state` signed, so CODE-4 and CODE-5 owe no docs edit beyond the refuted web-playground bullet. EVIDENCE: exact lookup over docs, zero hits.
FACT: `hmac_hex` outside proposals sits only in glossary.md, wire-format.md, BUILD-GAPS.md, pkg/uploadtoken/uploadtoken.go, and spec/07, and each is staged or a record. EVIDENCE: grep over the tree.
FACT: the item 16 delta adds no docs site; docs/operator-guide/web-playground.md:40 already tells an operator to register one gateway host as the redirect URI.
WATCHOUT: docs/tutorials/agent-memory.md:37 and :96 and session-derive-replay.md:34 read `.uploadToken` from `GET /v1/sessions/{id}`, and only the create response struct carries that field. This predates 0114 and no staged edit changes it, so it was not filed as a finding. EVIDENCE: pkg/gateway/sessionserver/session_response.go:172
OPEN: the `UPLOAD_CHANNEL_CLOSED` line under Standing context Open is filed this round as a Defects-list finding.

### [non-spec-recheck.4.review-edit-sites.1]
FACT: the edit-site sweep finds no unstaged spec, docs, schemas, or charts surface. No token or state HMAC wording survives outside the staged sites, every OpenAPI playground or callback row stays true, and the uploadLimits caps appear in charts only. — EVIDENCE: pkg/gateway/externalapi/openapi/openapi.json /v1/admin/connectors/oauth/callback; charts/lenny/values.yaml:2534-2547
FACT: errorPageHTML escapes any code and has no allow-list, and playground metrics and audit carry no oidc_state label, so `oidc_state_unavailable` needs no companion entry. — EVIDENCE: pkg/gateway/mcpfabric/playground/assets.go:157
FACT: tests/tier3_contract/playground builds an OIDC handler with no store but never reaches login in oidc mode, so Testing item 13's fixture list needs no addition. — EVIDENCE: tests/tier3_contract/playground/playground_test.go:349
WATCHOUT: two records outside RECORDS-1 go stale, and neither makes spec or code wrong, so neither was filed: open T-7.1.12 asks for a rotation-overlap test on code CODE-3 deletes, and BUILD-PLAN.md still says "a signed `state`". — EVIDENCE: TEST-GAPS.md T-7.1.12; BUILD-PLAN.md:241

### [non-spec-recheck.4.review-fresh.1]
FACT: the tier-3 parity test stays green at S4 with `errorclassify` carrying `UPLOAD_TOKEN_INVALID`: its exemption only skips a comparison, and the "foreign signing key" case still answers 401 through a digest mismatch, so item 14 landing at S9 breaks no S4 tier-3 gate. EVIDENCE: tests/tier3_contract/rest_mcp_consistency/finalize_upload_token_parity_test.go:113-121,147
FACT: `tests/tier3_contract/playground` needs no `OAuthState` wiring: its only login call runs in non-oidc mode or stops at the nil-provider guard, so item 13's in-package list is complete for the login and callback paths. EVIDENCE: tests/tier3_contract/playground/playground_test.go:402-412
FACT: `UPLOAD_TOKEN_*` has one code catalog outside spec and docs, errorclassify; openapi.json already lists the codes, and no SDK or schema names them. EVIDENCE: grep over pkg cmd sdks schemas charts

### [non-spec-recheck.4.review-mechanism.1]
FACT: tests/tier3_contract/playground/playground_test.go needs no `OAuthState` wiring. `newPlayground` passes no `OIDC`, and `TestLoginEndpointIsOIDCModeOnly` asserts only the dev-mode 404, so the extended login guard cannot change its result. Item 13's fixture list stays complete without it. EVIDENCE: tests/tier3_contract/playground/playground_test.go:64, :407
FACT: S4 compiles with `Consume` and `ConsumeDigest` still in the package, because both call only `v.tracker`, and neither reaches `parseAndVerifyHMAC` or the old `Verify`. EVIDENCE: pkg/uploadtoken/uploadtoken.go:353, :363

### [non-spec-recheck.4.review-performance.1]
CORRECTS [Standing context, Primary read load]: "Tier 3 about 1,200 and Tier 4 about 12,000 reads/s" predates the recheck moving to the primary. At 5 uploads per session the count is 11 primary reads per session (2 per upload plus finalize): 2,200 transactions/s at Tier 3 and 22,000/s at Tier 4. The conclusion stands: a Tier 3 pool of 440 PgBouncer backends is well inside it at single-digit ms per transaction, the §17.8.2 Postgres table has no Tier 4 column, and the reads add no write. EVIDENCE: spec/17_deployment-topology.md §17.8.2 (Postgres and connection pooling), spec/16_observability.md §16.5 (Session creation rate), pkg/gateway/session/sessionstore/pgstore/pgstore.go:525
FACT: closes the `closeSession` size UNVERIFIED. An entry is one map slot of about 100 bytes, and only when `maxBytesPerSession` is set (default 0). The worst case, every session uploading on a peer, is about 20 KB/s fleet-wide at Tier 3, or about 58 MB per day per replica at 30 replicas, cleared by restart. It is disclosed in Defects and refuses nothing, so it is below the finding bar. EVIDENCE: pkg/gateway/sessionserver/upload_limits.go:70-85, spec/17_deployment-topology.md:973
FACT: a Redis loss removes no login the shipped design completed, because the shipped callback already wrote the session record to the same cache_pubsub Redis, so moving `state` there adds no new outage dependency for a finished login. EVIDENCE: cmd/lenny-gateway/httpsurface.go:403-404, cmd/lenny-gateway/credentialsurface.go:200

### [non-spec-recheck.4.review-plan-conformance.1]
FACT: The plan's only deliverable text for this proposal is the §10.2 "Shared gateway key-source proposal" entry, the §10.2 F-27.3.16 and F-15.1.69 items, and the owner decision that the finalize check stays until this lands; a search of the plan for state cookie, StateSigner, and upload token finds nothing else. EVIDENCE: gateway-runtime-comms-remediation.md:3702, :3939, :4214, :4523
FACT: "Sign it with a key every replica shares" in the plan's F-27.3.16 item is superseded by the owner direction in the key-source entry, and the summary Non-goals record the shared-key rejection, so it is not a conformance gap. EVIDENCE: gateway-runtime-comms-remediation.md:3941, :3707

### [non-spec-recheck.4.review-reliability.1]
FACT: A keyed finalize that answers 500 on the new primary read leaves no pending claim: the middleware releases the claim for any status at or above 500, so a same-key retry re-executes. EVIDENCE: pkg/gateway/middleware/idempotency/idempotency.go maybePersist
FACT: The memory store computes expiry from `fc.CreatedAt` and consumes against the passed `now`, so CODE-5's `CreatedAt: h.now()` and `Consume(state, h.now())` agree under a fake clock; the 1-minute Sweep can turn an expired entry into `oidc_state_invalid` rather than `oidc_state_expired`, which is the same refusal. EVIDENCE: pkg/connectoroauth/state.go MemoryStateStore.Put, Consume, Sweep
FACT: A primary recheck reading `created` proves the blob's Put finished before `finalizing` committed, so the recheck is the ordering guarantee a peer upload needs; `prepareAtFinalize` and the upload path keep no replica-local per-session state beyond `uploadAborts` and `uploadLimits`. EVIDENCE: pkg/gateway/sessionserver/upload.go finalizeUpload; pkg/gateway/sessionserver/finalize.go prepareAtFinalize

### [non-spec-recheck.4.review-security.1]
FACT: The only mint sites are `IssueDetailed` in create.go and create_and_start.go (`Issue` wraps it, and derive and replay reach create.go's `mintUploadToken`), and `buildTokenSigningStores` always sets the verifier, so the nil-verifier opt-out is unreachable through `cmd/lenny-gateway`. EVIDENCE: cmd/lenny-gateway/token_signing.go:49-51, cmd/lenny-gateway/sessionsrv.go:167-168
FACT: Replacing the signed cookie with an unsigned opaque cookie loses no login-CSRF control: an attacker's legitimately issued signed cookie passed the old check just as a matching opaque one passes the new one, and the store entry now supplies authenticity and single use. EVIDENCE: pkg/gateway/mcpfabric/playground/auth.go (handleCallback)
FACT: The connector callback's `Verify` block emits no audit or metric, so removing it drops no surfacing of a forged state. EVIDENCE: pkg/gateway/externalapi/admin/connector_oauth.go:315-320

### [non-spec-recheck.4.review-single-source.1]
FACT: the delta against non-spec-recheck-r2-prefix is the one Testing item 16 `Host` sentence. It states a test fixture requirement with a one-clause reason, has no second stating site in summary or staging, and adds no rule copy. — EVIDENCE: non-spec-changes.md Testing item 16
FACT: a full re-walk of summary, checklist, both stagings, and Testing found no rule with two stating sites beyond those already refuted on materiality. The S4 constructor rule (Commit sequence and CODE-1), the Watch-out bullets beside Testing items 6 and CODE-5, and the Non-goals beside Decisions are clause-length pairs, not copies. — EVIDENCE: non-spec-changes.md Commit sequence, CODE-1; summary.md Decisions, Watch out for, Non-goals

### [non-spec-recheck.4.review-test-coverage.1]
FACT: the only delta since the r2 prefix is the Testing item 16 `Host` sentence. It leaves the tier list and S9 target list true, and no other item needs the parameter. — EVIDENCE: non-spec-changes.md:260; tests/tier4_integration/playground_oidc_login_test.go:23
FACT: a tier-8 Postgres cut would not discriminate the CODE-2 primary reads, because `accessRow` also needs Postgres and fails ahead of `readPrimary`; only a live read pool with a lost primary reaches the new branch, so item 5's store-level fault injection is the pin. — EVIDENCE: pkg/gateway/sessionserver/rbac_gate.go:214-219
FACT: no per-process upload record links upload to finalize, so upload on one replica and finalize on another needs no extra mechanism; item 15 runs both on B. — EVIDENCE: pkg/gateway/sessionserver/upload.go:445-540
WATCHOUT: item 17 stages a sessiondriver upload method, but its test sentence says mint, finalize, and replay with no upload. Either the kind test uploads on the peer or the method goes; tier 4 item 15 carries the owner's upload-on-peer directive, so this was not filed. — EVIDENCE: non-spec-changes.md:261
WATCHOUT: `TestUploadTokenTrackerSweep_spec_7_4_9` must be deleted rather than swept, and no Testing item names it; the CODE-3 grep lists it and it fails to compile otherwise. — EVIDENCE: pkg/gateway/sessionserver/upload_token_ttl_internal_test.go:150
WATCHOUT: item 12's "burns no state at callback" on an unwired store pins only the `h.oauthState` half of CODE-5 step 4; a nil `h.sessions` with a wired store is unpinned. Production always wires `Sessions`, so this was not filed. — EVIDENCE: non-spec-changes.md:150,251; cmd/lenny-gateway/httpsurface.go:402-415,440

### [non-spec.1.fix-G1.1]

DECISION: record the `*cataloging.Store` unwind gap as a summary Defects entry, cut "and soft-deletes" from the `uploadAborts` entry, point decision 19 at the new entry, and make CODE-2 name `softDeleteStagedBlob` rather than the outcome — BECAUSE no staged rule depends on the fix, and RECORDS-1 item 4 files every Defects entry as an open finding — ALTERNATIVES: staging the fix in CODE-2 rewrites every unwind branch and needs tombstone retention in `Server`, and the over-headroom branch releases `reserved` while the catalog releases stored size.
WATCHOUT: giving `*cataloging.Store` a one-argument `SoftDelete` is the obvious repair and it is wrong, because `releaseBytes` then fires beside the branch's `Adjust` and the double release moves to the unwind. Dropping `Adjust` from the new read-error branch alone leaks the reservation with no catalog. The fix must name one releaser. — EVIDENCE: pkg/blobstore/cataloging/cataloging.go `SoftDelete` (reads the live row, then `releaseBytes`); pkg/gateway/sessionserver/upload.go `softDeleteStagedBlob` and its callers.
WATCHOUT: Testing item 5 "each case soft-deletes the blob" is true only on its tier-1 `blobstore.MemoryStore`, which is a Tombstoner. It pins that the branch makes the call. It does not show the blob goes away in a Postgres deployment, and no catalog-backed case belongs there until the filed finding is fixed. — EVIDENCE: non-spec-changes.md Testing item 5; pkg/blobstore/blobstore.go `SoftDelete` on `MemoryStore`.
FACT: `cataloging.go`'s package comment says Store forwards `blobstore.Tombstoner`, and that is false: the method takes `(u, retention)` and the only `var _` assertion in the package is `blobstore.Eraser`. Trust the signature over the comment. — EVIDENCE: pkg/blobstore/cataloging/cataloging.go `SoftDelete` and `var _ blobstore.Eraser`.
FACT: every `softDeleteStagedBlob` branch hits this gap, including `STORAGE_QUOTA_EXCEEDED`, so §7.4 **Inbound body hard cap** "any bytes already written to staging are removed" does not hold with Postgres today. The finding RECORDS-1 item 4 files should cite that sentence. — EVIDENCE: spec/07_session-lifecycle.md §7.4; pkg/gateway/sessionserver/upload.go.
FACT: a §11.2 rebuild clears the under-count whichever side of session GC it lands on, because it sums live rows: before GC the row is counted and GC's release is then right, and after GC the row is gone. — EVIDENCE: spec/12_storage-architecture.md §12.4 storage quota row.
USEFUL [non-spec.1.fix-design-G1]: the design's search of BUILD-GAPS, TEST-GAPS, and the plan held: my grep found closed resolutions only (F-4.5.6, the F-7.4.14 resolution), and no open finding names this defect.

### [non-spec.1.fix-G2.1]
DECISION: closed the tier-8 Postgres primary gap with Testing item 24 plus the TEST-1 Targets and Files touched entries, at the design's rung — BECAUSE only a gateway process reaches the real pool, reconnect, and token survival across a cut — ALTERNATIVES: the finding's in-cap 500 and no-blob assertions stay green on a replica read at admission; see [non-spec.1.fix-design-G2.1] for the rest.
FACT: the `GET /v1/sessions/{id}` 200 in item 24 holds by reading: `requireSessionAccess` takes `accessRow`, `handleGet` calls `store.Get`, which reads `s.read`, and `buildTaskRecord` swallows a transcript read error and returns the record. — EVIDENCE: pkg/gateway/sessionserver/rbac_gate.go `accessRow`, pkg/gateway/sessionserver/session_read.go `handleGet`, pkg/gateway/session/sessionstore/pgstore/pgstore.go `Get`, pkg/gateway/sessionserver/taskrecord.go `buildTaskRecord`.
WATCHOUT: `iwChaosEnv.start` points `--postgres-billing-audit-ddl-dsn` at the direct DSN and only `--postgres-dsn` at the proxy. Keep that split in item 24, and read the row for the `created` assertion over a direct pool. — EVIDENCE: tests/tier8_chaos/input_wait_recovery_chaos_test.go `iwChaosEnv.start`.
UNVERIFIED: no test has booted a gateway with `--postgres-read-dsn` (still true after this fix), so the S9 implementor confirms boot and the middleware chain under the cut. The GET 200 assertion fails first if something ahead of the handler reads the primary.
USEFUL [non-spec.1.fix-design-G2.1]: the `keySetReplicaArgs` watchout and the `wouldExceedBytes` ordering both re-verified, and the cap probe is the one store-free gate between admission and the tenant read.

### [non-spec.1.fix-G3.1]
DECISION: Testing item 1 pins the secret by the observable length of the third segment (64 hexadecimal characters) and keeps the two-tokens-differ assertion — BECAUSE no key exists, so the random secret is the only forgery barrier, and the sibling NewState test in CODE-4 already pins entropy and encoding — ALTERNATIVES: tightening CODE-1 Parse to a 64-character third segment was rejected, because Verify compares the digest of the whole token, so a Parse check adds a mechanism and leaves issuance unpinned.

### [non-spec.1.fix-design-G1.1]
DECISION: record the `*cataloging.Store` unwind defect as a summary Defects entry placed directly after the `uploadAborts` entry, cut that entry's sentence to "refuses", name `softDeleteStagedBlob` in the CODE-2 bullet, and point decision 19 at the new entry — BECAUSE no staged behaviour depends on a fix, and the remedy has to choose one releaser across every unwind branch — ALTERNATIVES: staging the fix in CODE-2, rejected because it needs the tombstone retention wired into `Server` and accounting that differs with and without a catalog; giving `cataloging.Store` a one-argument `SoftDelete`, rejected because `releaseBytes` then fires beside the branch's `Adjust`, a double release at the unwind itself.
FACT: the second release is healed by a counter rebuild. `storagequota.Rehydrate` at gateway startup and the `RecoveryReconciler` on the Redis recovery edge rebuild the counter from live `artifact_store` rows, so the under-count lasts until the next rebuild, and a rebuild between the unwind and session GC makes the GC decrement correct. EVIDENCE: spec/11_policy-and-controls.md:47, cmd/lenny-gateway/store_router.go:117, pkg/blobstore/cataloging/cataloging.go:575.
FACT: the over-headroom, hash-mismatch, recheck, and `commitBytes` branches all share this path, so a client with a valid token can reach it with a bad content hash and no peer. The refused blob stays live and uncharged until session GC. EVIDENCE: pkg/gateway/sessionserver/upload.go:455-456, :476-478, :506-508, :524-526.
WATCHOUT: Testing item 5's "soft-deletes the blob" stays correct only because its tier-1 store is a Tombstoner (`blobstore.MemoryStore`). It is not a test of the Postgres path, and no catalog-backed case can be added until a proposal fixes the defect, because it would be red or would pin the defect. EVIDENCE: pkg/blobstore/blobstore.go:660, pkg/gateway/sessionserver/upload_abort_test.go:37.
WATCHOUT: `cataloging.go`'s `Store` doc comment says it forwards `blobstore.Tombstoner`. Its `SoftDelete` and `HardPrune` signatures both differ from that interface, so the comment is false, and a search for that sentence suggests the unwind works. EVIDENCE: pkg/blobstore/cataloging/cataloging.go:53-57, :295, :487, pkg/blobstore/blobstore.go:417-433.

### [non-spec.1.fix-design-G2.1]
DECISION: add one tier-8 Testing item (next free number after item 23) for a primary cut under a live read pool, with an oversize probe upload as the discriminator — BECAUSE a status of 500 alone proves nothing for upload, since `reserveStorageQuota` reads the tenant from the primary after admission; a per-session cap sits ahead of that read, so a replica read at admission answers 429 and CODE-2 answers 500 — ALTERNATIVES: the finding's in-cap upload asserting 500 and no blob (green with or without CODE-2); asserting the absence of "storage-quota check failed" in the message (brittle negative string match); a MinIO listing (the blob is never written on either path, so it adds a container and discriminates nothing).
FACT: under a cut primary with a live read pool, the shipped upload already answers 500: `admitUpload` passes on the replica, then `reserveStorageQuota` fails at `s.tenants.Get`, which reads the primary pool. `w.storageCounter` defaults to `storagequota.NewMemory()` and `w.tenants` is always wired, so the nil guard never skips it. — EVIDENCE: pkg/gateway/sessionserver/upload.go:706-717, pkg/gateway/environment/tenantstore/pgstore/pgstore.go:267, cmd/lenny-gateway/redis_quota.go:118, cmd/lenny-gateway/sessionsrv.go:211-212
FACT: `wouldExceedBytes` runs in `runUpload` after `admitUpload` and ahead of `reserveStorageQuota`, needs no store, and refuses only when `upload-max-bytes-per-session` is positive and the declared `Content-Length` pushes past it, so a declared-size probe is the one store-free gate after admission. — EVIDENCE: pkg/gateway/sessionserver/upload.go:197-229, pkg/gateway/sessionserver/upload_limits.go:130
FACT: under a Postgres cut a finalize answers 500 with or without CODE-2, because a replica read at the token check still fails at `store.Update`. The finalize half of the test pins the outcome and the recovery rather than the read site; item 5 owns the site. — EVIDENCE: pkg/gateway/sessionserver/finalize_handler.go:141
FACT: the post-Put recheck cannot fail through a real cut at tier 8 on a Postgres-backed gateway: `cataloging.Store.Put` inserts the catalog row on the primary inside `Put`, so a cut before that insert answers 500 from `commitUploadBlob` ahead of the recheck, and a cut after it leaves only an unsteerable window. Item 5 stays the recheck pin. — EVIDENCE: pkg/blobstore/cataloging/cataloging.go:143-173, cmd/lenny-gateway/stores.go:494-503, pkg/gateway/sessionserver/upload.go:498
WATCHOUT: `keySetReplicaArgs` is the wrong startup for this test. `--multi-tenant=true` wires `bearerTenantRegistry`, whose `tenantstore.Get` reads the primary in the auth middleware, so a cut refuses ahead of the handler and never reaches CODE-2. Single-tenant `--dev-mode` with `X-Lenny-Tenant-ID` names the tenant with no store read. — EVIDENCE: cmd/lenny-gateway/tenancy.go:71-78, cmd/lenny-gateway/auth_seams.go:57-73, pkg/gateway/middleware/auth/auth.go:741
WATCHOUT: `tcpfault.Cut` gives refused dials and no stall, so "within `--session-primary-read-timeout`" passes quickly under a cut. A stalled-connection case needs a new proxy mode; the bound against a store that ignores its context is pinned in pkg/gateway/session/sessionstore/primary_read_test.go at tier 1, so no mode was added. — EVIDENCE: tests/testinfra/tcpfault/tcpfault.go:53
UNVERIFIED: no test has started a gateway process with `--postgres-read-dsn`, and the design did not run one. The S9 implementor confirms boot and that `accessRow` holds on the read pool during the cut, which the `GET /v1/sessions/{id}` 200 asserts. — EVIDENCE: grep of tests/ for postgres-read-dsn and LENNY_PG_READ_DSN
CORRECTS [standing context **Failover:**]: "uploads now answer 500 during a Postgres failover" overstates the change. The shipped tree answers 500 too, at the quota tenant read under `--postgres-read-dsn` and at `accessRow` without it. CODE-2 moves the refusal ahead of the cap and quota gates and leaves the status as it was. Non-spec Edge cases stays true as written.
CORRECTS [non-spec-recheck.4.review-test-coverage.1]: "a tier-8 Postgres cut would not discriminate the CODE-2 primary reads" holds for a cut with no read pool, and a read pool alone is not enough either, because of the quota tenant read. The upload site is discriminable with a live read pool and a per-session cap probe. Item 5 stays the pin for lag and for the recheck.

### [non-spec.1.fix-design-G3.1]
DECISION: replace the last sentence of non-spec-changes ## Testing item 1 with the reviewer's sentence, which pins the 64-character third segment and keeps the two-tokens-differ assertion — BECAUSE the secret is the token's only forgery barrier and CODE-1's Parse accepts any hex third segment, so the assertion has to be on the issued token — ALTERNATIVES: making Parse reject a wrong-length segment, rejected because the finding is a missing test and Verify already compares a digest; no CODE-1 or SPEC-1 change is owed.

### [non-spec.1.review-applicability.1]
FACT: a full applicability walk at non-spec round 1 found no forward reference, unstaged deliverable, lane fault, or unresolved anchor. The only delta since the r2 prefix is the Testing item 16 `Host` sentence, and it applies. — EVIDENCE: implementation-checklist.md; non-spec-changes.md:260; tests/tier4_integration/playground_oidc_login_test.go:23
FACT: at S4 the tier-3 parity test stays green with ring-based issuers, because the "foreign signing key" case now fails on the row digest and still answers `UPLOAD_TOKEN_INVALID`; item 14's exemption drop is safe at S9 because S4 adds the errorclassify entry. — EVIDENCE: tests/tier3_contract/rest_mcp_consistency/finalize_upload_token_parity_test.go:117,147
FACT: tests/tier3_contract/playground never drives login or callback in oidc mode, so CODE-5's new `h.oauthState` guard breaks no tier-3 fixture and S7 needs no tier 3. — EVIDENCE: tests/tier3_contract/playground/playground_test.go:349,407
WATCHOUT: `sessionrecord_test.go` constructs oidc handlers at lines 116 and 200 that also need a `MemoryStateStore`, although Testing item 13 names that file only for the cookie helper. The Design line "Each code step edits the test fixtures its change breaks" covers it, so it was not filed. — EVIDENCE: pkg/gateway/mcpfabric/playground/sessionrecord_test.go:116,200

### [non-spec.1.review-citations.1]
FACT: Every file:line in the summary Defects section, the CODE-1 to CODE-5 identifiers, the DOCS-1 anchors, and the SPEC-1 to SPEC-3 quoted anchors resolved against the tree at 56aebef74. EVIDENCE: finalize_handler.go:220, upload.go:498, signer.go:39, adminrouter.go:655, flags.go:1252, spec/27_web-playground.md:80-81,110,171
WATCHOUT: `completeOIDCLogin` has five callers across four files, and `sessionrecord_test.go` holds two of them with its own `Options` literals, so a list of fixtures needing `OAuthState` must include that file; the second literal shares `opts` across `replicaA` and `replicaB`. EVIDENCE: pkg/gateway/mcpfabric/playground/sessionrecord_test.go:120,131,200,212
FACT: `proposals/0111_*` is absent from this tree; it lives on `draft/0111-warm-workspace`, so an Impacts claim about it must be read with `git show draft/0111-warm-workspace:<path>`. EVIDENCE: proposals/ listing, git branch -a
FACT: The `sessionrecord_test.go` watch-out above is already in the standing context, and the Design sentence "Each code step edits the test fixtures its change breaks" covers it, so it was not filed. EVIDENCE: review-log.md:153, non-spec-changes.md:12

### [non-spec.1.review-client-surface.1]
FACT: `errorclassify.ClassifyStatus` serves both REST envelopes and the MCP projection, so the one catalog entry in CODE-2 closes the REST and MCP category parity Testing item 14 asserts. EVIDENCE: pkg/gateway/sessionserver/error_envelope.go:43
FACT: `CONNECTOR_OAUTH_STATE_INVALID` and the `oidc_state_*` redirect codes appear in no spec, docs, OpenAPI, SDK, or schema surface; only pkg/ handlers and tests carry them, so retiring or adding them owes no mirror edit. EVIDENCE: grep over the tree, pkg/gateway/externalapi/admin/connector_oauth.go:316, pkg/gateway/mcpfabric/playground/auth.go:126
FACT: the /playground/* routes are absent from openapi.json, and the connector callback's 400 description there ("Invalid or expired state") stays true after CODE-4. EVIDENCE: pkg/gateway/externalapi/openapi/openapi.json

### [non-spec.1.review-docs-alignment.1]
FACT: no docs/ page, spec section, or alert names `lenny_upload_token_overlap_keys`; the only site is the pkg/uploadtoken/rotator.go comment CODE-3 deletes, so the rotator removal owes no metrics-reference or runbook edit — EVIDENCE: pkg/uploadtoken/rotator.go:211
FACT: `oidc_state_*`, `CONNECTOR_OAUTH_*`, `conn:oauth`, `UPLOAD_CHANNEL_CLOSED`, and `keys rotate` have zero hits in docs/, and the non-webhook "HMAC" token hits are only session-lifecycle.md:141, wire-format.md:58, and glossary.md:427, all in DOCS-1 — EVIDENCE: docs/client-guide/session-lifecycle.md:141
WATCHOUT: the DOCS-1 409-row direction says "a session past finalization" returns 410, but an ended session is past finalization and answers 409; the SPEC-1 wording is "`ready` or any later non-terminal state" — EVIDENCE: docs/api/rest.md:158
FACT: Postgres-failover runbooks need no upload cause, because the cataloging Put already inserts a Postgres row on upload, so the primary read is not a new outage dependency in a Postgres deployment — EVIDENCE: docs/runbooks/postgres-failover.md:31

### [non-spec.1.review-edit-sites.1]
FACT: openapi.json has no `/playground/auth` path and no HMAC text, and `lenny_playground_oidc_state`, `oidc_state_`, `conn:oauth:state`, and `CONNECTOR_OAUTH_STATE` have no hit under docs/, schemas/, or charts/, so SPEC-3 has no docs or chart companion. — EVIDENCE: lookup exact results over docs, schemas, charts; pkg/gateway/externalapi/openapi/openapi.json:2451
FACT: `derive_failure` appears nowhere in docs/, so the SPEC-2(e) table change has no docs companion. — EVIDENCE: grep over docs/
FACT: `pkg/gateway/storage/rediskeys/rediskeys.go:29` names `conn:oauth:state:` with no HMAC or connector wording, so CODE-4's signature sweep correctly omits it. The tier-11 label "§12.4 connector-OAuth row" is a message string with no pin on spec text. — EVIDENCE: tests/tier11_docs/redis_key_prefix_registry_test.go:63
WATCHOUT: Testing item 13 names `sessionrecord_test.go` only for the cookie helper, and its two oidc handlers also need `OAuthState`, because the CODE-5 guard answers 503 at login without it. This fails loudly at S7 and the Commit sequence sentence on fixtures covers it, so it was not filed. — EVIDENCE: pkg/gateway/mcpfabric/playground/sessionrecord_test.go:116,196
WATCHOUT: "logged at startup" for no `--redis-url` holds for the playground session store only; the connector memory store path logs nothing. Not filed, because no behavior depends on the log. — EVIDENCE: cmd/lenny-gateway/httpsurface.go:425; cmd/lenny-gateway/credentialsurface.go:202-205

### [non-spec.1.review-fresh.1]
CORRECTS [Standing context, "Existing OIDC pins"]: the tier-4 IdP does check `code_verifier`. oidcstub compares the verifier's SHA-256 with the stored challenge and answers invalid_grant on a mismatch, so the flow in Testing item 16 pins the stored verifier. `fakeOIDC.lastVerif` having no reader stays true. EVIDENCE: tests/testinfra/stubs/oidc/oidc.go:289-295
WATCHOUT: `grep 'sessionstore\.Store$'` overcounts embedding stores, because it also matches named struct fields, which hold the concrete memstore and keep `GetPrimary`. `workspace_defaults_bind_internal_test.go` and the `reportFixture` in `failure_test.go` are such fields and do not belong in Testing item 6. EVIDENCE: pkg/gateway/sessionserver/workspace_defaults_bind_internal_test.go:61, pkg/gateway/sessionserver/failure_test.go:543
FACT: a `--dev-mode=false` gateway on a plaintext `redis://` URL needs the `redis-allow-insecure` opt-out unless the harness environment sets it, and I did not check the harness environment. This touches Testing items 16, 22, and 23, is a fixture detail, and was not filed. EVIDENCE: cmd/lenny-gateway/flags.go:537

### [non-spec.1.review-mechanism.1]
FACT: The upload path has one verification seam: `handleUpload` and `handleUploadArchive` both run `runUpload`, which holds the only `admitUpload` and `finalizeUpload` calls, so the archive route needs no separate edit. `handleUploadToSession` reads the row itself and never verifies a token. EVIDENCE: pkg/gateway/sessionserver/upload.go:146-154, pkg/gateway/sessionserver/upload.go:193, pkg/gateway/sessionserver/upload.go:266, pkg/gateway/sessionserver/upload_to_session.go:77
FACT: A client names uploaded blobs in the finalize plan through `uploadRef`, so a peer finalize needs no per-process upload list. EVIDENCE: docs/api/rest.md, finalize `workspacePlan` example; pkg/gateway/sessionserver/upload.go:261-271
FACT: pgstore persists both token columns on insert and full-row update and reads them back, so `Verify` never sees a zero expiry on a row that has a digest. EVIDENCE: pkg/gateway/session/sessionstore/pgstore/pgstore.go:270-271, :388-389, :603, :715, :1677, :1864
FACT: `lenny_upload_token_overlap_keys` exists only in a rotator.go comment. No gauge, spec row, or alert exists, so deleting the rotator orphans no metric. EVIDENCE: pkg/uploadtoken/rotator.go:209-214
WATCHOUT: "logged at startup" for the memory-store posture (summary Non-goals, non-spec Edge cases) holds for the playground SessionStore log only. The connector memory `StateStore` logs nothing on either the current or the staged path. Refuted as not worth a finding, because nothing depends on the log. EVIDENCE: cmd/lenny-gateway/httpsurface.go:425, cmd/lenny-gateway/credentialsurface.go:199-205
USEFUL [Peer finalize]: the `row.PodAssignment` entry held when traced through `prepareAtFinalize`. EVIDENCE: pkg/gateway/sessionserver/finalize.go:223

### [non-spec.1.review-performance.1]
CORRECTS [Standing context "Primary read load"]: the Tier 3 1,200/s and Tier 4 12,000/s figures predate the recheck moving to the primary. With 5 uploads per session each upload costs 2 primary reads (admit, recheck) plus 1 finalize, so 11 per session: 2,200 tx/s at Tier 3 (200/s) and 22,000/s at Tier 4 (2,000/s). Still under the Tier 3 pool (110 × 4 PgBouncer) at millisecond PK reads; no spec read budget exists. EVIDENCE: spec/16_observability.md:598, spec/17_deployment-topology.md:1185, pkg/gateway/session/sessionstore/pgstore/pgstore.go:525.
FACT: the `UNVERIFIED closeSession size` Open entry is now sized: about 120 bytes per entry, about 4 peers per session at 30 replicas with 5 uploads, so 800 entries/s and about 230 MB/day per replica at Tier 3 while `maxBytesPerSession` is set. EVIDENCE: pkg/gateway/sessionserver/upload_limits.go:63, spec/17_deployment-topology.md:973.
WATCHOUT: `uploadAbortRegistry.closeSession` keeps one closed entry per finalized session on the finalizing replica with no sweep, in every deployment, at the shipped rate. It predates 0114 and the rate does not change, but no Defects entry says so and the `uploadAborts` entry covers only the peer abort. EVIDENCE: pkg/gateway/sessionserver/upload_abort.go:96-99,120-123.
FACT: `--postgres-read-dsn` is what makes the primary-read change observable; with it unset `Get` already reads the primary, so a Postgres chaos test needs a primary path and a read path that fail separately. EVIDENCE: cmd/lenny-gateway/stores.go:160-166.
FACT: `conn:oauth:state` adds one SET per login and one EVAL per callback; no hot key, one hash slot, and login is opt-in, so the Redis budget is untouched apart from the F-27.3.17 anonymous write.

### [non-spec.1.review-plan-conformance.1]
USEFUL [non-spec-recheck.4.review-plan-conformance.1]: the plan scope held again. The §10.2 key-source entry, the F-27.3.16 and F-15.1.69 items, and the owner decision that the finalize check stays until this lands are still the only assigning text.
FACT: The last-round edit (Testing item 16 host parameter) touches no plan deliverable. Every deliverable in the assigning text is staged or recorded as a divergence. EVIDENCE: gateway-runtime-comms-remediation.md:3702, :3939, :4214, :4523

### [non-spec.1.review-reliability.1]
FACT: `*cataloging.Store` does not satisfy `blobstore.Tombstoner`, because its method is `SoftDelete(u, retention)`, so `softDeleteStagedBlob` does nothing in a Postgres deployment; a rejected upload's Adjust release is then repeated by the session GC through `SoftDeleteSession`. EVIDENCE: pkg/blobstore/cataloging/cataloging.go:295,535-576; pkg/gateway/sessionserver/upload.go:592-601; cmd/lenny-gateway/leader_sweeps.go:130
FACT: materialization resolves uploads by plan `uploadRef`, so an orphan blob from a refused upload is not materialized; a client re-send after a recheck 500 does not double-apply into the workspace. EVIDENCE: pkg/gateway/sessionserver/finalize.go:26,52
FACT: `maybePersist` releases the pending idempotency claim on a 5xx, so a finalize primary-read 500 retries rather than sticking on IDEMPOTENCY_KEY_IN_FLIGHT. EVIDENCE: pkg/gateway/middleware/idempotency/idempotency.go:462-475
FACT: `tcpfault.Cut` closes connections and refuses dials, so go-redis fails fast in Testing item 22, and go-redis retry of `Consume` after a lost reply yields ErrStateConsumed, which fails closed. EVIDENCE: tests/testinfra/tcpfault/tcpfault.go:55; pkg/connectoroauth/redis_state_store.go:66-73
USEFUL [Sweep wiring]: the memoised helper and worker nil check hold; no re-trace needed.

### [non-spec.1.review-security.1]
FACT: Every path a connector or playground callback takes on a store fault or unknown `state` ends before the token exchange: the connector default branch is a 400, the playground table redirects, and neither falls back to the cookie. EVIDENCE: pkg/gateway/externalapi/admin/connector_oauth.go Consume switch; non-spec-changes CODE-5 Consume table
FACT: A row with a digest but a zero expiry is refused, because `pgstore` reads a NULL expiry into the zero time and row-expiry comparison treats it as past. EVIDENCE: pkg/gateway/session/sessionstore/pgstore/pgstore.go `s.UploadTokenExpiry = *upExp`
FACT: `verifyUploadToken` has two production callers, `admitUpload` and `handleFinalize`; `/upload-archive` shares `admitUpload` and `finalizeUpload`, and `upload_to_session` calls no token check, so no third site stays on the read pool or on a fail-open recheck. EVIDENCE: pkg/gateway/sessionserver/upload.go:193,266,308; pkg/gateway/sessionserver/finalize_handler.go:107
FACT: `newOpaqueID` is base64url, so the bare `state` is a valid cookie value and Redis key. EVIDENCE: pkg/gateway/mcpfabric/playground/sessionrecord.go:287
CORRECTS [Existing OIDC pins]: the tier-4 IdP is `oidcstub`, and it does check `code_verifier` against the stored challenge, so a wrong stored verifier fails item 16 at the callback. EVIDENCE: tests/testinfra/stubs/oidc/oidc.go

### [non-spec.1.review-single-source.1]
FACT: the delta against non-spec-recheck-r2-prefix is still the one Testing item 16 `Host` sentence, and a re-walk across summary, checklist, both stagings, and Testing found no rule stated in full at two sites that the refuted list does not already cover. Multi-site pairs are clause length: the "one script call and no write" fact (summary decision 15, two Non-goals, non-spec Edge cases) and the no-`--redis-url` posture (summary Non-goals, non-spec Edge cases). — EVIDENCE: summary.md:32,77,84,85; non-spec-changes.md:275,276
USEFUL [non-spec-recheck.4.review-single-source.1]: it saved re-deriving the clause-length pairs beside Decisions and Watch-outs.

### [non-spec.1.review-test-coverage.1]
FACT: a primary-only outage is reachable in a test: `--postgres-read-dsn` (cmd/lenny-gateway/flags.go:517) can point at Postgres directly while `--postgres-dsn` goes through `tcpfault.StartURL` (tests/tier8_chaos/lease_extension_postgres_outage_test.go:121). EVIDENCE: those lines.
WATCHOUT: `TestHandleFinalizeEmitsAuditEvent_spec_7_4_17` asserts `tracker.IsConsumed` and goes red at S4 once CODE-2 drops ConsumeDigest; `TestFinalizeConsumesUploadToken` still accepts "410 or 409" with a stale comment. Testing item 7 names neither; only the Commit sequence fixtures sentence and the item 3 audit reach them. EVIDENCE: pkg/gateway/sessionserver/upload_finalize_audit_internal_test.go:132, pkg/gateway/sessionserver/security_test.go:82-148.
CORRECTS [Existing OIDC pins]: the tier-4 IdP does check `code_verifier`; the stub's S256 check is at tests/testinfra/stubs/oidc/oidc.go:289-295, per the refuted finding in the round history.

### [non-spec.2.fix-G1.1]
DECISION: applied Option B as designed: Testing item 24 serves `sessionserver.New` in process over `pgstore.New(proxiedPool, WithReadPool(directPool))`, and the summary Defects list gains the `environmentmw` primary-outage bullet — BECAUSE the binary answers 500 at `environmentmw` ahead of every session route during a cut, so no process-level assertion reaches the CODE-2 read — ALTERNATIVES: Option A (process level) loses the only real-pool check of the admission read, and a read pool or off switch for the middleware changes production code for a test's topology.
WATCHOUT: the Defects bullet names §12.3 alone, and §10.1.3 **Existing sessions continue** carries the same tension for the dual-store case. A fix of the defect has to reconcile both; the bullet needs no second citation because RECORDS-1 item 4 files it from the summary. — EVIDENCE: spec/10_gateway-internals.md §10.1.3 rule 1; spec/12_storage-architecture.md §12.3 **Behavior during Postgres failover**
WATCHOUT: do not read "§10.6 makes the filter an authorization boundary" as quoted spec text. §10.6 states the filtering and `noEnvironmentPolicy`; the phrase "authorization boundary" is the `environmentmw` code comment citing §10.6. — EVIDENCE: pkg/gateway/middleware/environment/environment.go `ServeHTTP`, `Resolve`
FACT: in process, `handleGet` reads only the read pool: `pgstore.Get` uses `s.read`, and `buildTaskRecord` returns nil with no transcript store, so the GET 200 holds with the primary cut. — EVIDENCE: pkg/gateway/session/sessionstore/pgstore/pgstore.go `Get`; pkg/gateway/sessionserver/taskrecord.go `buildTaskRecord`
USEFUL [non-spec.2.fix-design-G1.1]: its `reserveStorageQuota` and `verifyUploadToken` watch-outs, and its CORRECTS of [non-spec.1.fix-G2.1], held on re-check and removed the need to re-derive the fixture.

### [non-spec.2.fix-design-G1.1]
DECISION: rewrite Testing item 24 in process (the finding's Option B) and keep the cap probe as the discriminator — BECAUSE the session server can be mounted without `environmentmw`, the in-process test keeps a real pool, a real cut, and a real reconnect, and the probe still fails on a replica read at admission — ALTERNATIVES: Option A (process level, recovery only) was rejected because its cut phase asserts only the middleware's 500, so the whole test stays green on a replica read and on the shipped tree; removing `environments` from the gateway was rejected because the binary has no off switch and production code must not change for a test.
FACT: an in-process `Server` with no `Tenants` and no storage quota returns early from `reserveStorageQuota`, so the "in-cap upload answers 500 either way" clause does not apply in process and was deleted. The cap probe stays because it is a status assertion and needs no blob store introspection. — EVIDENCE: pkg/gateway/sessionserver/upload.go `reserveStorageQuota`
FACT: the external-package fixture for item 24 already exists: `sessionserver.New(store, sessionserver.Options{DevTenantHeader: true})` behind `ownerprincipal.Wrap(srv.Handler(), ownerprincipal.Alice)`, and `sessionserver` and `ownerprincipal` are already imported in tier 8 tests. — EVIDENCE: tests/tier7a_load_local/finalize_admission_race_test.go `finalizeRaceFixture`; tests/tier8_chaos/workspace_defaults_outage_test.go
CORRECTS [non-spec.1.fix-G2.1]: "only a gateway process reaches the real pool, reconnect, and token survival across a cut" is false. `pgstore.New(pool, WithReadPool(direct))` over a `tcpfault` DSN in process reaches all three, and a gateway process cannot reach the CODE-2 read at all during a cut.
CORRECTS [non-spec.1.fix-G2.1 WATCHOUT and UNVERIFIED]: the `iwChaosEnv.start` DSN split and the `--postgres-read-dsn` boot check no longer apply, because item 24 starts no gateway. The direct-pool read for the `created` assertion is the store's own `Get`, which reads the direct read pool.
WATCHOUT: item 24's GET 200 stays on purpose. It proves the read pool is live during the cut; without it the probe's 500 could come from `accessRow` falling back to `readPrimary`, and the test would pass vacuously. — EVIDENCE: pkg/gateway/sessionserver/rbac_gate.go `accessRow`
WATCHOUT: `verifyUploadToken` returns nil when `uploadVerifier` is nil, so item 24 must wire `UploadTokenVerifier`, or the replay assertions cannot hold and the token checks do not run. — EVIDENCE: non-spec-changes.md CODE-2 `verifyUploadToken`
FACT: §12.3 **Behavior during Postgres failover** says existing sessions with cached coordination state continue, and `environmentmw` answers 500 on every authenticated route during a primary outage with Redis healthy. BUILD-GAPS, TEST-GAPS, and the plan name no such entry, and §10.6 makes the filter an authorization boundary, so the remedy needs a spec decision. The design files it as a summary Defects entry. — EVIDENCE: pkg/gateway/middleware/environment/environment.go `ServeHTTP`; cmd/lenny-gateway/auxstores.go `buildAuxStores`; spec/12_storage-architecture.md §12.3

### [non-spec.2.review-performance.1]
FACT: every authenticated request passes `environmentmw`, which calls `environments.List` on the primary pool (`environmentpg.New(w.pgPool)`, no read-pool option) and fails closed with 500 "environment registry lookup failed". Under a cut primary no authenticated route reaches a sessionserver handler, whether or not `--postgres-read-dsn` is live and whatever the tenancy mode. — EVIDENCE: pkg/gateway/middleware/environment/environment.go:185, :234-243; cmd/lenny-gateway/auxstores.go:50-52; cmd/lenny-gateway/httpsurface.go:836-840; pkg/gateway/environment/environmentstore/pgstore/pgstore.go:213-215
CORRECTS [non-spec.1.fix-design-G2.1]: "a replica read at admission answers 429 and CODE-2 answers 500" is false through a gateway process; the 500 is the middleware's on both trees, so the probe discriminates nothing.
CORRECTS [non-spec.1.review-test-coverage.1 and the item-24 FACT on `GET /v1/sessions/{id}` 200]: the 200 was traced from `requireSessionAccess` inward and missed the middleware stack; a cut gives 500 there.
WATCHOUT: no process-level test can steer a primary-only loss to the CODE-2 reads, because `Options.Environments` has no off switch in the gateway binary. Item 5 is the only read-site pin. — EVIDENCE: pkg/gateway/middleware/environment/environment.go:219
USEFUL [non-spec.1.fix-design-G2.1 WATCHOUT on keySetReplicaArgs]: it pointed at the middleware stack as the place to look, though it stopped at auth.

### [non-spec.2.review-reliability.1]
FACT: on a Postgres gateway every authenticated route depends on the primary ahead of the session server, in single-tenant dev mode too: environment middleware calls `environments.List` on `pgPool` and answers 500 on a store error. `--postgres-read-dsn` does not cover it, because `environmentpg.New(w.pgPool)` takes no read pool. EVIDENCE: pkg/gateway/middleware/environment/environment.go:186 and :236-243; cmd/lenny-gateway/auxstores.go:52; cmd/lenny-gateway/httpsurface.go:836
WATCHOUT: a process-level test cannot reach a sessionserver handler while the primary is cut. Reach the CODE-2 read in process, with `sessionserver.New` over a pgstore on a proxied pool plus a direct read pool, and `Server.Handler()`. EVIDENCE: pkg/gateway/sessionserver/route_table.go:24; tests/tier8_chaos/lease_extension_postgres_outage_test.go header
MISTAKE: Testing item 24 as written in round 1 looked only at the tenant-resolution difference between `keySetReplicaArgs` and `iwChaosEnv.start`, and missed the environment middleware on the same request path.
FACT: `w.storageCounter` defaults to `storagequota.NewMemory()`, so `reserveStorageQuota` reads the tenant on a gateway with no Redis. EVIDENCE: cmd/lenny-gateway/redis_quota.go:118; pkg/gateway/sessionserver/upload.go:707-710

### [non-spec.2.review-test-coverage.1]
FACT: item 24's discriminator holds by reading the order in `runUpload`: `admitUpload` runs ahead of `wouldExceedBytes`, which runs ahead of `reserveStorageQuota`, so a probe above the cap answers 429 on a replica read and 500 only from the CODE-2 primary read. — EVIDENCE: pkg/gateway/sessionserver/upload.go `runUpload`.
FACT: item 5's "soft-deletes the blob" is assertable at tier 1 because the memory and filesystem blob stores implement `Tombstoner`; the `*cataloging.Store` Defects entry does not touch it. — EVIDENCE: pkg/blobstore/fsstore.go `SoftDelete`, pkg/blobstore/blobstore_test.go `TestMemoryStoreSoftDeleteContract`.

### [non-spec.3.review-performance.1]
FACT: The in-process rewrite of Testing item 24 traces clean. `handleGet` and `accessRow` read the replica (session_read.go:21, rbac_gate.go:210), `buildTaskRecord` returns nil with no transcripts (taskrecord.go:31), and `wouldExceedBytes` sits after `admitUpload` (upload.go:193, :222), so a replica read at admission answers 429 and a primary read answers 500. `reserveStorageQuota` is a no-op with no `Tenants` wired, so the in-cap upload needs no second pool. EVIDENCE: pkg/gateway/sessionserver/upload.go:707
FACT: The word "authorization boundary" appears in code comments only (environment.go:237); spec/ has no such phrase. §10.6 **Transparent filtering** is the nearest spec statement, so the summary's clause paraphrases §10.6. EVIDENCE: spec/10_gateway-internals.md, "Transparent filtering"
CORRECTS [standing context, "Failover"]: "§12.3 promises nothing contrary" is too strong now that the summary Defects entry records §12.3 **Behavior during Postgres failover**. Uploads are no worse than shipped, because `environmentmw` already answers 500 ahead of every tenant-principal route on a primary loss (auxstores.go:52, environment.go:234).

### [non-spec.3.review-reliability.1]
FACT: Testing item 24's in-process rebuild traces through: `requireSessionAccess` and `handleGet` read the read pool (`rbac_gate.go` `accessRow`, `session_read.go`), so GET and the access gate hold during a cut of the primary pool, and `buildTaskRecord` returns nil with no transcripts. `reserveStorageQuota` no longer enters the assertion. `commitBytes` runs after the recheck, so a retried in-cap upload burns no cap. EVIDENCE: pkg/gateway/sessionserver/upload.go:finalizeUpload; pkg/gateway/sessionserver/taskrecord.go:30
FACT: `tcpfault.Cut` closes the listener and live connections, so a primary pool fails fast rather than hanging; item 24's "within `Options.PrimaryReadTimeout`" is an upper bound, and `ReadPrimary` enforces it. EVIDENCE: tests/testinfra/tcpfault/tcpfault.go:55; pkg/gateway/session/sessionstore/primary_read.go
FACT: `tests/tier8_chaos/workspace_defaults_outage_test.go:145` is a tier-8 precedent for `sessionserver.New` in process, so item 24's fixture has a home in that tier.
FACT: the `environmentmw` Defects entry holds: `environmentpg.New(w.pgPool)` takes no read pool, and `Resolve` returns the `List` error, which `Wrap` answers with 500. EVIDENCE: cmd/lenny-gateway/auxstores.go:52; pkg/gateway/middleware/environment/environment.go:234
WATCHOUT: "authorization boundary" is the wording of the code comment at environment.go:237; §10.6 of the spec does not use that phrase. Treat the entry's "§10.6 makes the filter an authorization boundary" as a paraphrase of §10.6's "authorized runtimes" when filing the finding. EVIDENCE: spec/10_gateway-internals.md:598

### [non-spec.4.review-applicability.1]
FACT: The rewritten Testing item 24 applies as written: `Options` holds `Blobs`, `UploadTokenVerifier`, `MaxUploadBytesPerSession`, and `PrimaryReadTimeout`; `handleGet` and `accessRow` read only `s.store.Get`, which uses the read pool; `reserveStorageQuota` returns early when `storageQuota` or `tenants` is nil; a nil binder makes `prepareAtFinalize` a no-op. — EVIDENCE: pkg/gateway/sessionserver/sessionserver.go:871,886,1213,1530; pkg/gateway/sessionserver/session_read.go:21; pkg/gateway/sessionserver/upload.go:707
FACT: No deleted test or symbol is named in `tests/spec-map.json` or the registers, so `validate-maps` holds between S6 and S9 as well as S4 and S9. — EVIDENCE: git grep over the tree, excluding proposals
WATCHOUT: S4 leaves `consumedTracker` in finalize_exit_guard_internal_test.go and the `tracker.IsConsumed` assertion in upload_finalize_audit_internal_test.go:132 for the implementor; only the Design's "Each code step edits the test fixtures its change breaks" sentence covers them, and Testing item 7 names neither. — EVIDENCE: pkg/gateway/sessionserver/finalize_exit_guard_internal_test.go:477-493; pkg/gateway/sessionserver/upload_finalize_audit_internal_test.go:132
USEFUL [S4 and S5 boundary]: the settled boundary held when rechecked; `verifyUploadToken` has only the two call sites and `Verify` has one non-test caller at upload.go:834.

### [non-spec.4.review-citations.1]

FACT: Every file:line in the summary Defects section resolves at this round, including the new `environmentmw` entry (`cmd/lenny-gateway/auxstores.go` builds the store on `w.pgPool`; `pkg/gateway/middleware/environment/environment.go` answers 500 ahead of the inner handler). `upload_abort.go:36` is the registry type declaration rather than `closeSession`, which is a fair reading of "the registry". — EVIDENCE: pkg/gateway/sessionserver/upload_abort.go:36
FACT: Testing item 24 as rewritten holds: `handleGet` reads `s.store.Get`, `accessRow` holds on the read pool, `s.transcripts` is nil unless wired, and with `s.tenants` nil the in-cap upload never reaches `reserveStorageQuota`. — EVIDENCE: pkg/gateway/sessionserver/session_read.go:21, pkg/gateway/sessionserver/rbac_gate.go:210, pkg/gateway/sessionserver/upload.go:707
WATCHOUT: `spec/` never uses the phrase "authorization boundary" for §10.6; only the middleware comment does. §10.6 **Transparent filtering** describes a filtered view of authorized runtimes, so the summary's wording is a paraphrase and was not filed. — EVIDENCE: spec/10_gateway-internals.md:598
WATCHOUT: `grep -h ... | grep -v _test` does not filter test files, because `-h` drops the name. `pkg/connectoroauth`'s only Lenny import is in `redis_state_store_test.go`, so the CODE-5 "imports no other Lenny package" claim is true. — EVIDENCE: pkg/connectoroauth/redis_state_store_test.go
FACT: Proposal 0111 is absent from the draft-0114 tree and lives on `draft/0111-warm-workspace`; check Impacts claims there. 

### [non-spec.4.review-client-surface.1]
FACT: the client surfaces carrying `UPLOAD_TOKEN_*` are exactly openapi.json (401/403/410 on upload, upload-archive, and finalize), rest.md, mcp.md, error-catalog.md, and error-handling.md. No SDK, schema, or generated MCP schema names a code, and `CONNECTOR_OAUTH_STATE_INVALID` appears only in connector_oauth.go and its test. EVIDENCE: pkg/gateway/externalapi/openapi/openapi.json:766; docs/api/rest.md:110
FACT: no docs page, openapi entry, or SDK names `oidc_state_*` or the `/playground/auth/*` routes' error codes; `errorPageHTML` renders any code with no allowlist, so `oidc_state_unavailable` needs no UI edit. EVIDENCE: pkg/gateway/mcpfabric/playground/assets.go:157
FACT: `tests/tier3_contract/sdks/go_client_test.go` wires the ring constructors and only the CODE-3 sweep reaches it; it round-trips the create-response token, so it passes unchanged after the constructor edit. EVIDENCE: tests/tier3_contract/sdks/go_client_test.go:150
FACT: docs/ has no parallel to the §15.1 Derive-failure table, so SPEC-2(e) owes no docs edit.

### [non-spec.4.review-docs-alignment.1]
FACT: `docs/` names no `oidc_state_*` code, `REDIS_UNAVAILABLE`, `conn:oauth`, `CONNECTOR_OAUTH_*`, `uploadLimits`, or `--session-primary-read-timeout`, so none of those changes has a docs site beyond DOCS-1 — EVIDENCE: grep over docs/ this round.
FACT: the `lenny_upload_token_overlap_keys` gauge exists only in a comment at pkg/uploadtoken/rotator.go:211, so deleting the rotator owes no metrics-reference or spec §16.1 edit — EVIDENCE: grep over spec, docs, pkg, cmd, tests, and charts.
FACT: docs/tutorials/agent-memory.md:37 and docs/tutorials/session-derive-replay.md:34 read `.uploadToken` from `GET /v1/sessions/{id}`. That is a pre-existing doc oddity unrelated to this change, because the digest-only row storage predates it.

### [non-spec.4.review-edit-sites.1]
FACT: Item 24's discriminating 429 holds as rewritten: `wouldExceedBytes` checks Content-Length before the Put, so the replica-read variant answers 429 at admission without reaching the recheck — EVIDENCE: pkg/gateway/sessionserver/upload.go:222
FACT: No tier-11 test, register, or script pins any replaced spec string (HMAC nonce, signed cookie, §18 rotation bullets, `hmac_hex`); `tests/registers/change-graph-coverage.yaml` names only the package directories `pkg/uploadtoken/` and `pkg/connectoroauth/`, which survive — EVIDENCE: tests/registers/change-graph-coverage.yaml:69
FACT: `TestLoginEndpointIsOIDCModeOnly` in tier 3 drives dev mode only, and `cmd/lenny-gateway/playground_oidc_test.go` never drives the login route, so neither needs an `OAuthState` fixture and item 13's list is complete for them — EVIDENCE: tests/tier3_contract/playground/playground_test.go:407
WATCHOUT: docs/operator-guide/web-playground.md:40 names only `503 LENNY_PLAYGROUND_OIDC_UNAVAILABLE` for login and stays true after SPEC-3(d), so the new `503 REDIS_UNAVAILABLE` is an addition the page omits rather than a sentence that becomes wrong; below the edit-site bar.
USEFUL [Tier-11 pins]: the standing line saved re-deriving the 409 string pins; a fresh grep confirmed it.

### [non-spec.4.review-fresh.1]
FACT: Item 24 as rewritten holds against the tree: the 429 cap check runs after admitUpload (upload.go:193 then :226), so a primary-read 500 precedes it during a cut; GET reads store.Get only and buildTaskRecord returns nil with no transcripts wired; accessRow takes the read pool first; sessionserver Options.PrimaryReadTimeout and pgstore WithReadPool both exist. EVIDENCE: pkg/gateway/sessionserver/session_read.go:21, taskrecord.go:31, rbac_gate.go:210-215, sessionserver.go:1213
FACT: The import removals CODE-1 to CODE-5 state are exact: `rand.` has one use in each of sessionserver.go (:1935), token_signing.go (:45), and credentialsurface.go (:183), and `log.` has one use in finalize_handler.go (:212). The connector callback keeps its empty state and code check at connector_oauth.go:307 after the Verify block goes.
FACT: Only the Go files connector_oauth.go and its test name CONNECTOR_OAUTH_STATE_INVALID; `pkg/` carries no F-7.1.31, F-27.3.16, or F-15.1.69 reference, and tests/ carries one, the parity exemption item 14 removes. F-9.3.18 and F-27.3.17 are free in BUILD-GAPS.md.
OPEN: 0113 SPEC-5 and 0114 SPEC-3(d) both insert a row in the §27.3.1 failure-modes table, at different anchors (after the WebSocket row, and after the Redis revocation row), and both edit docs/api/mcp.md and rest.md in different tables. Neither anchor is disturbed, so I filed no finding, but the summary Impacts table has no 0113 row while the caller directive asks for one when an overlap appears. The owner of the Impacts table decides whether a textually clean overlap counts.

### [non-spec.4.review-mechanism.1]
FACT: Testing item 24's rewritten flow traces end to end: `accessRow` and `handleGet` read the read pool, `readPrimary` is the first primary touch in `admitUpload` and comes before the Content-Length cap at upload.go `wouldExceedBytes`, so the 429-to-500 probe discriminates, and tcpfault `Cut` refuses dials at once. The fixture row needs a UUID id, because pgstore casts `$1::uuid`, so the tier-7a `sess-fin-admission` constant cannot be copied. EVIDENCE: pkg/gateway/session/sessionstore/pgstore/pgstore.go:505; tests/tier7a_load_local/finalize_admission_race_test.go:44
FACT: `MemoryStateStore.Put` derives expiry from `fc.CreatedAt` rather than the wall clock, so CODE-5's `CreatedAt: h.now()` is what makes `oidc_state_expired` reachable under a test clock. EVIDENCE: pkg/connectoroauth/state.go `Put`
FACT: `sessionrecord_test.go:259` is the only test caller of `openState`; `tests/tier3_contract/playground` has no oidc login or callback flow, so CODE-5 breaks no tier-3 fixture and S7 omitting tier 3 is sound. EVIDENCE: tests/tier3_contract/playground/playground_test.go:349,412
USEFUL [non-spec.4.review-performance.1]: its item 24 feasibility facts matched this trace.

### [non-spec.4.review-performance.1]
FACT: The memory `StateStore` sweep holds for an oidc playground with the connector flow off, because `main` runs buildCredentialSurface and buildHTTPSurface before startBackgroundWorkers, so `w.connectorStateStore` is set before the `workers.go` nil check. EVIDENCE: cmd/lenny-gateway/main.go:201,232,280; cmd/lenny-gateway/workers.go:127
FACT: `admitUpload` and `finalizeUpload` are reached only from the token-governed upload handler, so CODE-2's primary reads do not extend to running sessions through `upload_to_session`. EVIDENCE: pkg/gateway/sessionserver/upload.go:193,266
FACT: A committed `ready` write cannot be lost on failover, because §12.3 requires synchronous replication, so row-derived consumption does not regress after promotion. EVIDENCE: spec/12_storage-architecture.md §12.3 Minimum topology
FACT: Item 24 is feasible in process: the access read and handleGet use the read pool, buildTaskRecord returns nil with no transcripts, the plain finalize runs with no binder, and tcpfault Cut refuses dials at once. EVIDENCE: pkg/gateway/sessionserver/rbac_gate.go:210; pkg/gateway/sessionserver/session_read.go:21; pkg/gateway/sessionserver/taskrecord.go:31; tests/testinfra/tcpfault/tcpfault.go:55

### [non-spec.4.review-plan-conformance.1]
USEFUL [non-spec.1.review-plan-conformance.1]: the assigning-text list held; the plan search for 0114, key-source, uploadToken, connectoroauth, and StateSigner found no new assigning text, and the last-round edits (Testing item 24 and the `environmentmw` Defects entry) touch no plan deliverable.

### [non-spec.4.review-reliability.1]
FACT: `tcpfault.Cut` closes every proxied connection, and a keyed finalize's primary-read 500 releases the idempotency claim through `maybePersist`, so a retry after `Restore` re-executes rather than answering IDEMPOTENCY_KEY_IN_FLIGHT. EVIDENCE: tests/testinfra/tcpfault/tcpfault.go:55; pkg/gateway/middleware/idempotency/idempotency.go (status >= 500 branch of maybePersist).
FACT: the item 24 in-process fixture needs no binder for finalize: `prepareAtFinalize` returns (nil, nil) when `s.podBinder == nil`, and `reserveStorageQuota` returns (nil, true) when `storageQuota` or `tenants` is unwired, so the probe's 500 comes from the CODE-2 read alone. EVIDENCE: pkg/gateway/sessionserver/finalize.go:215; pkg/gateway/sessionserver/upload.go (reserveStorageQuota).

### [non-spec.4.review-security.1]
FACT: `pkg/uploadtoken` has no caller outside `pkg/gateway/sessionserver` and `cmd/lenny-gateway`, and `admitUpload` plus `handleFinalize` are the only `verifyUploadToken` callers; `/upload-archive` shares `admitUpload` and `finalizeUpload`, so there is no second recheck to convert. EVIDENCE: pkg/gateway/sessionserver/upload.go:193, :266, :308; finalize_handler.go:107
FACT: The removed connector signature bound nothing but the nonce, so Consume alone preserves every binding. EVIDENCE: pkg/connectoroauth/state.go computeStateHMAC comment
FACT: A consumed `state` lost from Redis reads as unknown, so a tombstone loss on a store wipe still refuses; only the cookie check and tombstone guard single use, and every fault path ends before Exchange. EVIDENCE: pkg/connectoroauth/redis_state_store.go consumeStateScript

### [non-spec.4.review-single-source.1]
FACT: the delta since non-spec-r2-prefix is Testing item 24's rewrite and the `environmentmw` Defects entry. Item 24's one-clause `environmentmw` reason cites the Defects fact and states no second rule, so the delta adds no copy. EVIDENCE: non-spec-changes.md Testing item 24; summary.md Defects, last entry
FACT: a full re-walk of summary, checklist, both stagings, and Testing found only multi-site pairs the refuted list or Standing context already covers: RECORDS-1 item 4 refiling, items 2 and 12 as test copies, the "one script call and no write" clause, the no-`--redis-url` posture, the S4 constructor rule, and the provider-outage reason. EVIDENCE: non-spec.r4.history.md; review-log.md Filed-nothing decisions
USEFUL [non-spec.1.review-single-source.1]: its list of clause-length pairs matched this walk and saved re-arguing them.

### [non-spec.4.review-test-coverage.1]
FACT: Testing item 6's wrapper list is complete for stores that embed the `sessionstore.Store` interface and drive finalize or upload. A per-file grep finds those seven tier-1 files plus the two tier-4 and tier-7a race files, and `derive_failure_audit_test.go`, whose finalize case item 4 drops. Stores embedding `*memstore.Store` inherit `GetPrimary`. — EVIDENCE: grep of `^\s+sessionstore\.Store$` crossed with `/finalize|/upload` under pkg/gateway/sessionserver and tests
FACT: concurrent single-winner `Consume` is already pinned on the Redis store, so the playground's single-use claim needs no new race test. — EVIDENCE: pkg/connectoroauth/redis_state_store_test.go:149
FACT: no test asserts any `oidc_state_*` code by literal today; item 12 adds them, and `oidc_state_missing` is pinned only as "no session" by item 23. The behaviour is unchanged, so this is below the bar. — EVIDENCE: grep of `oidc_state_` over tests and playground test files
