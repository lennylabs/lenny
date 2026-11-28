# Decisions: 0183 Redesign of the lenny-ops idempotency records

## Open decisions

Raised on 2026-11-27 when the change was created. Each entry states the question, the options, and a recommendation.

- **O1. Program and milestone.** The owner's answer to IP-21 named the timing in these words: "We can tackle that later as part of M3, I think". The change was created outside any program, as changes 0178 and 0182 are, and its membership is the owner's to confirm.
  - (a) The change joins the v1 program, and its steps are placed in M3 when M3 is sequenced. *Recommended*, because it follows the owner's words, and the defects it closes include a high-severity one (APICONV-I16).
  - (b) The change joins the v1 program in a later milestone of M3+.
  - (c) The change stays outside the v1 program, as 0178 and 0182 do, and is scheduled when the owner asks for it.

- **O2. One implementation, or a fix of `opsidem` in place.** The review recommended fixing `pkg/ops/opsidem` in place and folding it onto `idempotencymw` later, because the fixes were then step 0178.S6, which was not to wait on 0178.S3 and 0178.S4. As a change of its own that the owner places later, that reason no longer holds.
  - (a) Fold: after step 0178.S4 lands, `idempotencymw` gains the parameters of OI-9, `lenny-ops` runs it over its own table, and `pkg/ops/opsidem` is removed (steps 0183.S1 and 0183.S2 as written). *Recommended*: each rule of the gateway's design C is then built and tested once, which the code best practices ask for by reuse over duplication and the design principles ask for by keeping the core small, and `lenny-ops` inherits every later gateway fix, such as APICONV-I20.
  - (b) Fix `opsidem` in place, importing only the parts of `pkg/idempotency` and `idempotencymw` that the review found independent of 0178's rework (`HashBody`, the key limit, and the no-store gates), and fold as the last step. The fixes do not wait on 0178.S4, and two implementations remain until the fold.
  - (c) Fix `opsidem` in place and keep two implementations. APICONV-I26 closes as declined.

- **O3. `403 IDEMPOTENCY_KEY_OWNED_BY_OTHER_CALLER`.** §25.4 requires it when another caller holds the key. It tells the sender that some caller uses that key, and lets a caller that knows another caller's key block it (APICONV-I17, APICONV-I18). Removing it changes §25.4's error table.
  - (a) Remove the code. The same key from another caller is a separate record, as in the gateway under §11.5, and reveals nothing. *Recommended*.
  - (b) Keep the code within the new record scope, so it fires only for a key held by another subject or source of the same tenant.

- **O4. The backup exposure until the change lands.** Full backups copy `ops_idempotency_keys`, which holds the plaintext webhook signing secret and any impersonation bearer stored in the last 24 hours (APICONV-I16). No deployment of Lenny exists outside the project, so the exposure is limited to the project's own clusters and the backups they take.
  - (a) No interim fix. The change's no-store rule (OI-4 and OI-7) closes the exposure for every backup taken after it lands, and the table stays in backups. *Recommended*, because no operator outside the project holds such a backup and the owner chose a redesign over an urgent split in IP-21.
  - (b) An interim step ahead of the redesign adds `ops_idempotency_keys` to `defaultExcludedTables` in `cmd/lenny-backup` and §25.11, and the redesign removes it again.
  - (c) An interim step marks the credential responses not stored in `opsidem` now, the credential half of the cancelled step 0178.S6, which is the urgent split that IP-21 offered.

- **O5. The cleanup schedule.** §25.4 "Retention" says a daily delete at 02:30 UTC; the reconciler deletes expired rows every five minutes (APICONV-I24). `Claim` ignores expired rows, so neither schedule changes which records replay.
  - (a) §25.4 states the code's schedule as the setting `ops.idempotency.cleanupInterval`, default `5m`. *Recommended*: small frequent deletes hold no long lock, and the code and its tests stand.
  - (b) The reconciler runs daily at a configurable time, default 02:30 UTC, as §25.4 says.

- **O6. The headers `X-Lenny-Idempotent-Replay` and `X-Lenny-Idempotency-Degraded`.** `opsidem` sets them and §25.4 defines neither (APICONV-I23). The gateway sets no replay header.
  - (a) Remove both. The degradation stays in `degradation.warnings`, as §25.4 states. *Recommended*, because it aligns `lenny-ops` with the gateway and no client reads a header the specification does not define.
  - (b) Define both in §25.4 and add the replay header to the gateway's records as well.

- **O7. The inventory kind `idempotency_in_progress`.** §25.4 "Operations Inventory" lists it with the source `ops_idempotency_keys` where the state is in progress, and nothing produces it (OPSREM-I1).
  - (a) Add the source: the store lists the claims in progress, and the inventory reads it. *Recommended*: a long-running upgrade or restore claim is an operation in flight that on-call looks for.
  - (b) Remove the kind from §25.4 and from the inventory's filter.

- **O8. The in-flight answer.** `lenny-ops` answers `409 OPERATION_IN_PROGRESS` with `elapsed`; the gateway answers `409 IDEMPOTENCY_KEY_IN_FLIGHT` with `Retry-After: 1`. §25.4's outage outcomes name `OPERATION_IN_PROGRESS`.
  - (a) `lenny-ops` answers `409 IDEMPOTENCY_KEY_IN_FLIGHT` with `Retry-After`, and keeps the elapsed time in the error's details; §25.4 and its error table change. *Recommended*: the owner asked that the design align as much as possible with the gateway's, and no deployment exists that relies on the old code.
  - (b) `lenny-ops` keeps `409 OPERATION_IN_PROGRESS` with `elapsed`, through the error writer it injects.

## Decisions

None.
