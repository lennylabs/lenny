# Proposal: Correct the §10.7 eval, §8.3 messaging, and §8.6 auto-mode rate-limit wording from "sliding-window" to the fixed-window per-minute counter they share, and add the boundary tests

- **Status:** Approved (2026-07-17). Both Open decisions (§9) are resolved at sign-off: the bounded cross-boundary transient is documented explicitly at both rate-limit sites (SPEC-1 and SPEC-2), and no §8.6 auto-mode boundary test is added here (left to T-8.6.7). Verified (2026-07-17), converged after 2 adversarial review rounds.
- **Date:** 2026-07-17.
- **Scope:** A spec-first correction of three per-minute rate-limit descriptions in `spec/10_gateway-internals.md` (§10.7) and `spec/08_recursive-delegation.md` (§8.3, §8.6) from "sliding-window" to the fixed-window per-minute counter all three are enforced by, plus the two internal doc comments in `pkg/gateway/mcpfabric/mcptools/messaging.go` that carry the same imprecision. It re-points the existing skipped eval boundary test to assert the documented fixed-window contract and tracks the rename in `tests/spec-map.json`. It changes no rate-limit value, key, default, `429`/`Retry-After` behavior, or `RATE_LIMITED` receipt: the enforcement is already fixed-window, and this reconciles the descriptive wording and its one skipped test to what the code implements. It closes the Low-severity findings T-ADV.12 and T-8.3.16. The §11.2 token-BUDGET quota store (`pkg/gateway/quota/quotastore`) is a separate, genuinely sliding-window subsystem and is out of scope.

This document stages the proposed spec, code, and test changes. It does not modify any spec, code, or doc file. Apply the changes in the "Proposed changes" section after sign-off, spec edit first.

## 1. Problem

Three spec sites describe per-minute rate limits as sliding-window, but all three are enforced by the platform's single fixed-window per-minute counter primitive, which resets each key's count to zero at every wall-clock-minute boundary. The wording overclaims an evasion resistance the implementation does not provide. The findings are T-ADV.12 and T-8.3.16, both Low severity.

### The shared enforcement primitive is fixed-window

The `ratelimit` package is the §11.1 fixed-window request-rate counter and documents itself as such (`pkg/gateway/policy/ratelimit/ratelimit.go:3-12`, "the §11.1 fixed-window request-rate counter … Each key tracks one fixed one-minute window. The window rolls at the top of each wall-clock minute; a key's count starts again from zero when the minute advances"). Its in-memory implementation keys on `now.Unix() / 60` and drops the prior window's counts wholesale when the minute advances (`ratelimit.go:48-56`). The Redis implementation embeds the minute epoch in the key (`pkg/gateway/policy/ratelimit/redisstore/redisstore.go:48`, `windowKey := fmt.Sprintf("rl:%s:%d", key, now.Unix()/60)`). Both stores are fixed-window.

### (1) §10.7 describes the eval limit as sliding-window

The Eval Submission Contract's Rate limit row states the per-session and per-tenant limits (defaults 100/session/min, 10,000/tenant/min) are "enforced by the gateway via Redis sliding-window counters," with `429`/`Retry-After` on excess (`spec/10_gateway-internals.md:937`). The eval limit is enforced through `checkEvalRateLimit` (`pkg/gateway/sessionserver/eval.go:85`), which increments the same counter the §11.1 admission middleware uses; the server wires that counter as `EvalRateLimitCounter` from a `ratelimit.Counter` instance. The eval limit is the §11.1 fixed-window counter under a distinct key prefix.

### (2) §8.3 describes the messaging limits as sliding-window

The `messagingRateLimit` paragraph calls `maxPerMinute` a "per-session outbound sliding-window burst limit" and says the target "accepts at most `maxInboundPerMinute` messages per sliding window" (`spec/08_recursive-delegation.md:309`). Both per-minute caps go through the shared fixed-window counter: `allow` increments `msg:out:`+sender and `msg:in:`+target keys and compares each returned count to its limit (`pkg/gateway/mcpfabric/mcptools/messaging.go:97,105,109`), delegating all windowing to `ratelimit.Counter`. The type's own doc already states the two per-minute caps "share a fixed-window `ratelimit.Counter` under distinct key prefixes" (`messaging.go:59`), while the two struct-field docs still call them "sliding-window" (`messaging.go:30,37`), so the file contradicts itself.

### (3) §8.6 describes the auto-mode limit as sliding-window

The Auto-mode rate limit paragraph says the gateway "tracks the count of auto-approved extensions per task tree per sliding minute window" (`spec/08_recursive-delegation.md:714`). The `autoExtensionLimiter` reuses the same §11.1 counter keyed per tenant and tree (`pkg/gateway/mcpfabric/delegationtree/leasecontrol/ratelimit.go:21-24,49-50`), and its doc comment already names it "the per-minute fixed-window primitive."

### Consequence and severity

A fixed-window counter still bounds the sustained per-minute rate to the configured ceiling. The one inaccuracy is the classic fixed-window boundary transient: a caller can submit a full quota in the last seconds of one minute and another full quota in the first seconds of the next, transiently reaching up to twice the ceiling before the window resets. The §8.3 inbound anti-flood control keeps its N-independence under fixed-window, since the per-target aggregate cannot exceed `maxInboundPerMinute` per window regardless of sender count, and the cross-boundary transient is bounded to roughly twice the ceiling regardless of N. Both findings are Low severity. T-ADV.12 records a "Needs human input" note on whether to correct the wording or build a sliding-window primitive; this proposal resolves it in favor of correcting the wording.

## 2. Decisions

- **Correct the wording rather than build a second sliding-window primitive.** The platform has one intentional, tested rate-limit counter primitive (the §11.1 fixed-window counter) that these three limits deliberately share. "Sliding-window" is imprecise descriptive language rather than a per-feature algorithm mandate. A fixed-window counter bounds the sustained rate to the configured ceiling per minute; the only inaccuracy is the bounded up-to-2x transient across a boundary, which the spec should document honestly. Building a second decoupled sliding-window primitive for Low-severity limits is disproportionate. This is the human decision recorded in T-ADV.12's "Needs human input" note, resolved in favor of correcting the wording.
- **Spec-first, per `spec-driven-development.md`.** Land the three wording corrections, then add and re-point the tests that assert the now-documented fixed-window behavior.
- **The §8.3 inbound cap's security intent survives the correction.** Its actual property is N-independence of the per-target aggregate (`messaging.go:105` keys on target, not sender), which holds verbatim under fixed-window: N siblings collectively cannot exceed `maxInboundPerMinute` per window, and the cross-boundary transient is roughly 2x regardless of N, so the N × `maxPerMinute` flood the control prevents never reopens. Strict sliding-window inbound protection is a future enhancement rather than a v1 requirement.
- **Correct the two internal doc comments in `messaging.go` alongside the spec.** The struct-field docs at `messaging.go:30,37` call the caps "sliding-window," contradicting the same type's doc at `messaging.go:59` and `code-best-practices.md`'s accurate-comment rule. The `ratelimit` package and `redisstore` already say fixed-window, and the `leasecontrol` comment already says "per-minute fixed-window primitive," so no other code comment changes.
- **Pin the corrected behavior at tier 1.** The deterministic in-process fixed-window logic lives at the unit level, driven by an injected clock, and the existing tests already sit there (the skipped eval boundary test in `eval_test.go` and the limiter tests in `messaging_internal_test.go`). The boundary behavior is fully determined by the injected clock at the unit level, so a tier-4 integration test with clockstep adds infrastructure without a distinct behavioral assertion. Reuse the existing test file rather than adding a new one.
- **Rewrite the skipped eval boundary test to the documented fixed-window contract and rename it.** The test currently asserts the sliding-window behavior (`429` after the boundary) the code does not implement, and its name reads `SlidingWindowBoundaryEvasion`. It must run and assert that the per-minute ceiling holds within a window, that the count resets at the wall-clock-minute boundary and admits a fresh quota, and that the transient is bounded. Its `tests/spec-map.json` entry tracks the rename.
- **Leave the other "sliding" spec and code sites untouched.** They belong to the genuinely sliding-window subsystems (§11.2 `quota/quotastore`, §12.4 fail-open cumulative timer, §4.1 gcpause p99, §25.3 recommendation ring buffers) that this correction does not concern.

## 3. The shared fixed-window primitive

All three limits route through `ratelimit.Counter.Incr(ctx, key, now)`, which returns the running count for `key` in the current fixed one-minute window and resets that count when the minute advances (`ratelimit.go:46-57`). Each limit uses a distinct key prefix over the same counter instance:

- §10.7 eval: `eval:s:<tenant>:<session>` and `eval:t:<tenant>` (`eval.go`), compared to `evalRateLimit.perSessionPerMinute` / `perTenantPerMinute`.
- §8.3 messaging: `msg:out:<tenant>:<sender>` and `msg:in:<tenant>:<target>` (`messaging.go:97,105`), compared to `MaxPerMinute` / `MaxInboundPerMinute`.
- §8.6 auto mode: `lease_ext_auto:<tenant>:<rootSession>` (`leasecontrol/ratelimit.go:49`), compared to `maxAutoExtensionsPerMinute`.

Because the primitive is fixed-window, each limit bounds its per-key count to the configured ceiling within a wall-clock minute and resets at the top of the next minute. The corrected spec text describes this one primitive at all three sites.

## 4. Proposed changes

### SPEC-1. Correct §10.7 eval rate-limit wording to fixed-window per-minute counters

**Target:** `spec/10_gateway-internals.md` §10.7, the Eval Submission Contract Rate limit row (`:937`).

**Rationale:** The row claims the eval limit is "enforced by the gateway via Redis sliding-window counters," but `checkEvalRateLimit` (`pkg/gateway/sessionserver/eval.go:85`) increments the same fixed-window `ratelimit.Counter` the §11.1 admission limiter uses (`ratelimit.go:48`, `redisstore.go:48`). The spec must describe the counter the code implements.

**Change (staged text):** In the Rate limit row, replace "enforced by the gateway via Redis sliding-window counters" with "enforced by the gateway via Redis fixed-window per-minute counters, each keyed count resetting at the top of every wall-clock minute". Leave the keys, the defaults (100/session/min via `evalRateLimit.perSessionPerMinute`; 10,000/tenant/min via `evalRateLimit.perTenantPerMinute`), and the `429`/`Retry-After` sentence unchanged. Append one sentence documenting the bounded cross-boundary transient (see Open decisions on whether to state it explicitly): "A caller can submit up to the configured ceiling in each wall-clock minute, so a burst straddling a minute boundary can transiently reach up to twice the ceiling before the window resets."

### SPEC-2. Correct §8.3 messagingRateLimit wording to fixed-window per-minute

**Target:** `spec/08_recursive-delegation.md` §8.3, the `messagingRateLimit` paragraph (`:309`).

**Rationale:** The paragraph calls `maxPerMinute` a "per-session outbound sliding-window burst limit" and says the inbound cap counts "per sliding window," but both per-minute caps go through the shared fixed-window counter (`messaging.go:97,105,109` calling `ratelimit.go:48`). The N-independent anti-flood property survives the correction and is preserved verbatim.

**Change (staged text):** Replace "`maxPerMinute` is a per-session outbound sliding-window burst limit" with "`maxPerMinute` is a per-session outbound fixed-window per-minute burst limit". Replace "the target accepts at most `maxInboundPerMinute` messages per sliding window" with "the target accepts at most `maxInboundPerMinute` messages per fixed one-minute window". Leave the anti-flood sentence ("This prevents N compromised siblings from flooding a single target at N × `maxPerMinute`; regardless of the number of senders …") and the `RATE_LIMITED` receipt sentences unchanged, since the N-independence property holds under fixed-window. Immediately after the anti-flood sentence, append one sentence documenting the bounded cross-boundary transient (the Open decision resolved in favor of explicit documentation at both rate-limit sites): "Because each window is fixed rather than sliding, a burst straddling a wall-clock-minute boundary can transiently reach up to twice a per-minute ceiling — twice `maxPerMinute` outbound, and twice `maxInboundPerMinute` in aggregate at a target regardless of sender count — before the window resets."

### SPEC-3. Correct §8.6 autoModeRateLimit wording to fixed one-minute window

**Target:** `spec/08_recursive-delegation.md` §8.6, the Auto-mode rate limit paragraph (`:714`).

**Rationale:** The text says the gateway "tracks the count of auto-approved extensions per task tree per sliding minute window," but `leasecontrol/ratelimit.go:21-24,49-50` reuses the same §11.1 fixed-window counter keyed per tenant and tree. This is the same mismatch and the same one-word correction, and it folds into this proposal because it is the same primitive.

**Change (staged text):** Replace "per task tree per sliding minute window" with "per task tree per fixed one-minute window". Leave the elicitation-fallback and audit-logging sentences unchanged; the fall-back-for-the-remainder-of-the-window behavior is unaffected by the fixed versus sliding wording.

### CODE-1. Correct the two internal sliding-window doc comments in messaging.go

**Target:** `pkg/gateway/mcpfabric/mcptools/messaging.go`, the `MaxPerMinute` field doc (`:30`) and the `MaxInboundPerMinute` field doc (`:37`).

**Rationale:** These struct-field comments call the caps "sliding-window," contradicting the same type's doc at `messaging.go:59` ("share a fixed-window `ratelimit.Counter` under distinct key prefixes") and `code-best-practices.md`'s accurate-comment rule. This is a comment-only change with no behavior effect. No other code comment needs it: `ratelimit.go` and `redisstore.go` already say fixed-window, and `leasecontrol/ratelimit.go:21-22` already says "per-minute fixed-window primitive."

**Change (staged text):** At `messaging.go:30`, change "per-sender outbound sliding-window burst limit" to "per-sender outbound fixed-window per-minute burst limit". At `messaging.go:37`, change "per-target inbound aggregate sliding-window limit" to "per-target inbound aggregate fixed-window per-minute limit". Keep the rest of both comments (defaults, the O(N²) storm-brake note) intact. Run `gofumpt` and `goimports`; no logic is touched.

### TEST-1. Un-skip and rewrite the eval boundary test to assert fixed-window behavior; rename it

**Target:** `pkg/gateway/sessionserver/eval_test.go`, `TestEvalRateLimitSlidingWindowBoundaryEvasion_spec_10_7_938` (`:197-241`).

**Rationale:** The test exists but is `t.Skip`-ped (`:207-217`) pending exactly the decision this proposal resolves, and its live assertion (`:235-236`, `429` after the boundary) encodes the sliding-window behavior the code does not implement. It must run and assert the documented fixed-window contract. It uses the existing `settableClock` (`:106`) and `evalServerRLClock` (`:115`) helpers.

**Change (staged text):** Remove the `t.Skip` call (`:207-217`). Rename the function to reflect fixed-window behavior (for example `TestEvalRateLimitFixedWindowBoundary_spec_10_7`). Keep the `settableClock` seeded at 12:00:58 and `perSession = 3`. Exhaust the ceiling in the tail of window 1 (three `201`s, then a fourth at the same clock returning `429` with a `Retry-After` header), asserting the sustained per-minute ceiling holds within a window. Advance across the wall-clock-minute boundary (`clock.add(3 * time.Second)`) and assert the next submission returns `201`, because the fixed window resets and admits a fresh quota, documenting the bounded up-to-2x cross-boundary transient. Update the doc comment and the in-body message strings that reference "sliding window" and "line 938" to describe the fixed-window reset. Keep a `// spec:` annotation naming §10.7 and the F-10.7.4 / F-11.2.19 tags.

### TEST-2. Re-point the §10.7 spec-map entry to the renamed eval boundary test

**Target:** `tests/spec-map.json` (`:1350`).

**Rationale:** The map lists `pkg/gateway/sessionserver/eval_test.go::TestEvalRateLimitSlidingWindowBoundaryEvasion_spec_10_7_938`, which TEST-1 renames. The spec-map validator strips the `::TestName` selector before stat-ing the file, so a stale function name is not machine-caught and would dangle the reference.

**Change (staged text):** Update the entry at `:1350` to the new function name chosen in TEST-1 (for example `pkg/gateway/sessionserver/eval_test.go::TestEvalRateLimitFixedWindowBoundary_spec_10_7`).

## 5. Non-goals

- **No new sliding-window Redis primitive.** The shared §11.1 fixed-window `ratelimit.Counter` is retained for all three limits; no new shared infrastructure is built.
- **No change to the §11.2 token-BUDGET quota store** (`pkg/gateway/quota/quotastore`), which is a separate, correct, tested sliding-window subsystem, nor to the other genuinely sliding-window sites (§12.4 fail-open cumulative timer, §4.1 gcpause p99, §25.3 recommendation ring buffers).
- **No change to rate-limit values, keys, defaults, `429`/`Retry-After` behavior, or the `RATE_LIMITED` receipt.** Behavior is unchanged; only the descriptive wording and its one skipped test move.
- **No fixed-window boundary test for the §8.3 messaging limiter.** The messaging limiter contains no window-boundary logic of its own to test: `allow` delegates all windowing to the shared counter (`messaging.go:97-111`), and the reset-at-wall-clock-minute behavior lives entirely in `ratelimit.Memory.Incr` (`ratelimit.go:47-56`), already pinned by `TestIncrResetsWhenTheMinuteAdvances` (`ratelimit_test.go:33-51`) plus per-key independence by `TestIncrIsPerKey` (`ratelimit_test.go:53-71`). TEST-1 re-exercises the identical counter boundary through the eval caller, so a messaging-layer boundary test would be a third test of one primitive's reset. The one messaging-specific property a boundary crossing could reveal, that the per-minute caps reset while the lifetime `maxPerSession` cap does not, is true by construction: `allowLifetime` (`messaging.go:120-125`) takes no time argument and cannot vary with the clock. The N-independent inbound aggregate within a window is already pinned by `TestMessagingLimiterInboundExceeded` (`messaging_internal_test.go`).
- **No auto-mode (§8.6) cross-replica or elicitation-fallback test.** That work is the separate, broader T-8.6.7 (Medium) finding; this proposal corrects only the §8.6:714 wording and leaves T-8.6.7's test scope to it (see Open decisions on whether to add a minimal auto-mode boundary test here).
- **No tier-4 integration test with clockstep.** The fixed-window boundary behavior is deterministic in-process logic pinned at tier 1; the tier-4 suggestion in T-ADV.12 and T-8.3.16 is declined as redundant.
- **No published-docs change.** A grep of `docs/` finds no reader-facing "sliding-window" description of these three limits, so no `docs/` edit is needed.

## 6. Testing

The change reaches tier 0 and tier 1 for `pkg/gateway/sessionserver` per `.claude/rules/test-coverage.md`. The spec edits (SPEC-1, SPEC-2, SPEC-3) and the comment edit (CODE-1) carry no runtime behavior and are covered by the tier-0 static suite (`go build`, `go vet`, `golangci-lint`) plus the spec-map validation that TEST-2 keeps green. The behavioral pin is the rewritten tier-1 boundary test.

- **tier-1 fixed-window boundary (boundary path, TEST-1):** In `pkg/gateway/sessionserver/eval_test.go`, the rewritten `TestEvalRateLimitFixedWindowBoundary_spec_10_7` drives the injected clock across a wall-clock-minute boundary. Within window 1 it exhausts the per-session ceiling (three `201`s at `perSession = 3`) and asserts the fourth submission at the same clock returns `429` with a `Retry-After` header, pinning that the sustained per-minute ceiling holds within a window. It then advances the clock past the minute boundary and asserts the next submission returns `201`, pinning that the fixed window resets and admits a fresh quota. The non-happy path is the boundary transient the fixed-window primitive documents: a full quota in the tail of one window followed by a full quota in the head of the next, bounded to roughly twice the ceiling. `// spec: 10.7 (eval-submission fixed-window per-minute rate limit)`.
- **tier-0 spec-map integrity (TEST-2):** `tests/spec-map.json` continues to resolve the §10.7 entry to an existing, current test function after the TEST-1 rename, so the spec-section coverage map does not dangle. The non-happy path is a stale `::TestName` selector that the validator does not stat-check.

## 7. Findings closed on application

This proposal closes T-ADV.12 (eval rate-limit window-boundary evasion, Low) and T-8.3.16 (messaging rate-limit sliding-window wording, Low). It resolves T-ADV.12's "Needs human input" note in favor of correcting the wording to fixed-window and re-points the skipped `TestEvalRateLimitSlidingWindowBoundaryEvasion_spec_10_7_938` to assert the documented fixed-window contract. The change runs at spec-edit and test time and needs no operator hardware.

## 8. Resolved in adversarial review

Subsequent adversarial review rounds populate this section. The challenge-round revisions carried in the draft dropped a fourth staged change, TEST-3 (a fixed-window boundary test for the §8.3 messaging limiter), on the grounds that the messaging limiter contains no window-boundary logic of its own: `allow` delegates all windowing to the shared `ratelimit.Counter` (`messaging.go:97-111`), the reset-at-wall-clock-minute behavior lives in `ratelimit.Memory.Incr` (`ratelimit.go:47-56`) and is already pinned by `TestIncrResetsWhenTheMinuteAdvances` (`ratelimit_test.go:33-51`) and `TestIncrIsPerKey` (`ratelimit_test.go:53-71`), and TEST-1 re-exercises the same counter boundary through the eval caller, so TEST-3 would be a third test of one primitive. The lifetime-cap-versus-per-minute-cap distinction the messaging boundary could reveal is true by construction, since `allowLifetime` (`messaging.go:120-125`) takes no time argument. TEST-3 was dropped and its reasoning recorded in §5 (Non-goals).

## 9. Open decisions for review

### Documenting the cross-boundary transient explicitly — RESOLVED (sign-off): explicit at both sites

The bounded up-to-2x cross-boundary transient is documented explicitly at both rate-limit sites: SPEC-1 (§10.7) carries the transient sentence, and SPEC-2 (§8.3) now appends the matching sentence after the anti-flood sentence (covering both `maxPerMinute` and the N-independent `maxInboundPerMinute` aggregate). The terse form was not chosen; explicit documentation of the security-relevant §8.3 control's actual bound was preferred.

### A minimal auto-mode boundary test — RESOLVED (sign-off): leave to T-8.6.7

No §8.6 auto-mode boundary test is added here. The auto-mode limiter routes through the same shared fixed-window primitive already pinned at tier 1, so a per-caller boundary test would re-exercise the shared primitive; the broader T-8.6.7 (Medium) finding owns auto-mode window testing, including the cross-replica and elicitation-fallback cases that carry the actual value. This proposal corrects only the §8.6 wording.

## 10. Files touched on application

- `spec/10_gateway-internals.md`: SPEC-1 (§10.7 Rate limit row, sliding-window → fixed-window per-minute, `:937`).
- `spec/08_recursive-delegation.md`: SPEC-2 (§8.3 `messagingRateLimit` paragraph, `:309`) and SPEC-3 (§8.6 Auto-mode rate limit paragraph, `:714`).
- `pkg/gateway/mcpfabric/mcptools/messaging.go`: CODE-1 (correct the `MaxPerMinute` and `MaxInboundPerMinute` field-doc comments, `:30,:37`).
- `pkg/gateway/sessionserver/eval_test.go`: TEST-1 (un-skip and rewrite the boundary test to the fixed-window contract; rename the function, `:197-241`).
- `tests/spec-map.json`: TEST-2 (re-point the §10.7 entry to the renamed test, `:1350`).
