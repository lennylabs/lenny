---
id: APICONV-I16
status: planned
kind: build-gap
severity: high
requirements: []
change: 0183.S3
---

# lenny-ops idempotency stores and replays the impersonation bearer to any caller with the same subject

## Specification

§25.4 The `lenny-ops` Service, **Idempotency**: `lenny-ops` keys a record by `(key, caller_id)`, where `caller_id` is the OIDC `sub` claim of the requesting service account, and returns the stored response without executing again. §11.5 **Stored credentials.** keeps credentials out of the gateway's stored responses; §25.4 states no such rule.

[§25.5](../../spec/25_agent-operability.md#255-operational-event-stream) (Operational Event Stream › Webhook Delivery): `POST /v1/admin/event-subscriptions` stores the SHA-256 hash of the subscription's secret in `ops_event_subscriptions.secret_hash` and returns the plaintext secret once; the rotation route returns the new secret once. [§25.11](../../spec/25_agent-operability.md#2511-backup-and-restore-api) (Backup and Restore API › Backup Execution › Sensitive Content Policy): unless `includeSensitiveTables` is `true`, `pg_dump` leaves out `platform_secrets`, `tenant_secrets`, `credential_pool_raw_secrets`, and any table matching `%_secret%` or `%_token%`.

## Evidence

- `opsidem` wraps every POST to `lenny-ops`, including `/mcp/management` (`pkg/ops/opsserver/opsserver.go`), and stores the response as JSONB keyed by `(key, caller_id)` (`pkg/ops/opsidem/opsidem.go`).
- The management MCP tool `admin.start_impersonation` (`pkg/ops/mcp/generated_tools.go`) proxies `POST /v1/admin/impersonation`, whose response carries `bearerToken` (`pkg/gateway/api/admin/impersonation.go`), and `invokeGateway` passes the body through unchanged (`pkg/ops/opsserver/mcp.go`).
- The `Claim` of each store keys a record by `(key, caller_id)` alone (`pkg/ops/opsidem/memory.go`, `pkg/ops/opsidem/pgstore/pgstore.go`). The middleware passes the endpoint, `method path`, and the store records it without comparing it, and no hash of the request body is computed (`pkg/ops/opsidem/opsidem.go`). A POST with the same key to any `lenny-ops` path therefore replays the stored response, the impersonation bearer included.
- `opsidem` wraps the whole mux (`opsserver.go`), and the MCP bridge's per-tool scope gate runs inside it (`pkg/ops/mcp/registry.go`), so a replay through `/mcp/management` returns before the scope gate runs, and a narrowed token of the same service account receives the stored bearer.
- The REST routes `POST /v1/admin/event-subscriptions` and `POST /v1/admin/event-subscriptions/{id}/rotate-secret` write `eventsubscription.Reveal`, whose `Secret` field holds the plaintext HMAC signing secret (`pkg/ops/eventsubscription/eventsubscription.go`, `handleCreateEventSubscription` and `handleRotateEventSubscriptionSecret` in `pkg/ops/opsserver/event_subscriptions.go`). `opsidem` stores a keyed `201` or `200` with the secret in it, base64-encoded in the `response` JSONB of `ops_idempotency_keys` (`Complete` in `pkg/ops/opsidem/pgstore/pgstore.go`), for the standard 24-hour window, and replays it to a retry with the same key. These routes do not go through the MCP bridge, so the MCP fixes above leave them storing the secret.
- `ops_idempotency_keys` matches neither `%_secret%` nor `%_token%`, and `defaultExcludedTables` in `cmd/lenny-backup/main.go` names only the three tables above. A full backup taken while a record is live therefore carries the plaintext webhook secret, and the impersonation bearer stored through `/mcp/management`, for the backup's retention period.
- Found by the security review of design C of change 0178 on 2026-11-27, and widened by the review of 0178 revision 11 on the same day. The defect predates 0178, and 0178 leaves the `lenny-ops` records out of its scope.

## Gap

Any holder of a token with the same service-account `sub` that knows the key replays a live impersonation bearer, through any `lenny-ops` path and whatever its scope, and no `admin.impersonation_started` audit row is written for the replay. The gateway's high-level rule that no stored response carries a credential, which change 0178 states, holds for the gateway only.

The same holds for the webhook signing secret: a caller that knows the key replays the plaintext secret of a subscription that §25.5 returns once, and every full backup taken within the record's window holds a copy of it and of any stored impersonation bearer.

## Suggested resolution

State in §25.4 that a record is matched on the key, the caller, the endpoint, and a SHA-256 of the request body, and that a mismatch answers `422 IDEMPOTENCY_KEY_REUSED`; that a response carrying a credential is not stored, as §11.5 does for the gateway; and that a replay through the MCP bridge runs the bridge's scope gate first. Have `opsidem` compare the endpoint and the body hash, skip storing a response that a handler marks as carrying a credential, with the impersonation proxy and any tool whose response carries a token marked, and run the scope gate before a replay. Add tests that a replay of `admin.start_impersonation` executes again and writes its audit row, that the same key on another path is refused, and that a token without the tool's scope is refused on a replay.

Mark the responses of the event-subscription create and rotate-secret handlers as carrying a credential, so `opsidem` releases the claim and a retry executes again. A backup taken before the fix keeps the records stored until then. State that in the operator documentation of §25.11, or add `ops_idempotency_keys` to `defaultExcludedTables`. Add a unit test that a keyed create and a keyed rotation store no record, and a static or contract test over the stored `lenny-ops` routes that no stored response carries a credential.

Open decision O20 of change 0178 asks whether a new step of 0178 fixes it, a change of its own does, or the issue stays unscheduled.

The owner answered O20 of change 0178 with option (a) on 2026-11-27 (0178 decision 19, IP-20): step 0178.S6 fixes the `lenny-ops` records and closes this issue. The owner asked for an in-depth review of `pkg/ops/opsidem` against the gateway's idempotency design, which started on 2026-11-27; the step starts after it reports.

Amended on 2026-11-27 with finding F1 of that review: the event-subscription routes store the plaintext webhook secret, and `ops_idempotency_keys` is copied into backups. The severity is raised from medium to high for the plaintext credentials that backups carry. Whether step 0178.S6 also took the backup exposure and the review's other findings was asked as IP-21 on the M3 readiness decision page.

The owner answered IP-21 on the M3 readiness decision page on 2026-11-27 with a redesign of the `lenny-ops` idempotency records as a change of its own (decision 20 of change 0178), which is change [0183](../../changes/0183-lenny-ops-idempotency/change.md). Step 0178.S6 is `Cancelled`, and the issue moves to that change. Step 0183.S2 closes the match on the endpoint and the body and the stored credentials of the `lenny-ops` routes, and with them the copy in backups taken after it lands; step 0183.S3 closes the stored impersonation bearer and the replay that skips the bridge's scope gate. The issue closes when step 0183.S3 lands. Open decision O4 of change 0183 asks whether an interim fix of the backup exposure lands ahead of the redesign, with no interim fix as its recommendation, because no deployment exists outside the project.
