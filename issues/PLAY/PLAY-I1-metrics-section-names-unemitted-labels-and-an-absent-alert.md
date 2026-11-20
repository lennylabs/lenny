---
id: PLAY-I1
status: open
kind: spec-defect
severity: low
requirements: []
legacy: T-27.8.4
---

# The playground metrics section names label values the gateway never emits and an alert that does not exist

## Specification

§27.8 (Metrics), the `lenny_playground_session_revocations_total` row: "`reason ∈ {user_logout, idle_timeout, admin_revoke, oidc_session_ended, user_invalidated}`".

§27.8 (Metrics), the `lenny_playground_session_revocation_propagation_seconds` row: "`outcome ∈ {pubsub_delivered, redis_authoritative, resubscribe}`. P99 alert threshold is the 500 ms logout propagation SLO defined in [§27.3.1]".

§27.3.1 (OIDC cookie-to-MCP-bearer exchange), **Revocation write.** "On logout, `user.invalidated` ([§27.3.1] step 4 "OIDC claim invalidation"), or explicit admin revocation, the gateway `DEL`s the `pg:sess:*` key".

## Evidence

- `pkg/gateway/playground/sessionrecord.go` defines `RevokeAdmin` (`admin_revoke`) and `RevokeOIDCSessionEnded` (`oidc_session_ended`). `rg -n "RevokeAdmin|RevokeOIDCSessionEnded" --type go -g '!*_test.go'` returns only the two definitions, so no production path passes either reason to the revocation primitive. The other reasons have callers: logout, the idle sweep, and the user invalidation fan-out.
- No admin endpoint revokes a single playground session. `BUILD-GAPS.md` F-27.6.4 (closed) left that surface open under F-27.3.7, and F-27.3.7 closed with only the idle sweep built. F-27.6.12 (closed) kept the unused `oidc_session_ended` constant as informational.
- The only Go mention of `redis_authoritative` is a comment in `pkg/gateway/playground/auth.go` that explains why the gateway stopped recording it. `BUILD-GAPS.md` F-27.6.6 (closed, commit `88a53d5d`) removed the emission on purpose and left the §27.8 enumeration unchanged.
- `rg -n "lenny_playground|Playground" pkg/alerting spec/16_observability.md` returns nothing. The §16.5 alert catalog has no playground row, and §27.8 gives no alert name, severity, window, or runbook.
- Recorded on branch `section-27` as T-27.8.4, T-27.8.8, and T-27.8.10. The `legacy` field carries T-27.8.4, and this issue covers all of them. The findings were checked against `main` at `092ef92f6` on 2026-11-19.

## Gap

Two `reason` values and one `outcome` value that §27.8 enumerates never appear on the metrics, so a dashboard or alert that selects them reads nothing. §27.3.1 names an admin revocation trigger that has no endpoint. §27.8 declares a P99 alert on the propagation SLO, and no alert rule watches any `lenny_playground_*` metric, so a breach of the 500 ms propagation bound fires nothing.

## Suggested resolution

Reconcile §27.8 with the code when the PLAY page lands. Either drop `admin_revoke`, `oidc_session_ended`, and `redis_authoritative` from the enumerations and remove "explicit admin revocation" from §27.3.1, or define the triggers that emit them and build those triggers. For the alert, either remove the sentence or add an alert with a name, severity, window, and runbook, which needs a row on the ALERTSLO page and in `pkg/alerting`.

Change 0138 (REST-only client API) and change 0149 (tenant scoping and identity) touch the playground, and neither closes this issue. 0138.S4 moves the playground client to REST and SSE, and 0138 edits §27.8 only to remove `lenny_playground_ws_connect_total`. 0138.S6 removes `GWSEAM-IsBearerRevoked` with the WebSocket, so the step that reconciles the propagation histogram should check which revocation check the playground's REST path consults after that step. 0149.S7 moves the playground's pages, cookie, and mint under the `/t/{tenantId}` prefix and leaves the revocation reasons and the metrics unchanged.
