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
