# Steps: 0183 Redesign of the lenny-ops idempotency records

## Validation owed

None.

## Steps

The steps below follow the recommended answers to the open decisions. Under open decision O2, option (b), step 0183.S1 moves to the end and the other steps change `pkg/ops/opsidem` in place without waiting on 0178.S4; under O4, option (b) or (c), an interim step is added ahead of them.

### 0183.S1 One idempotency implementation for the gateway and lenny-ops
- status: Planned
- lanes: [code]
- depends-on: [0178.S4]
- closes: []

OI-9 in `change.md`. `pkg/idempotency` and `idempotencymw` gain an expiry carried by each record, a required-key classification, an error writer, and an outage policy that the caller injects, and a store over a platform-scoped table through `PlatformPostgres()`; the no-store gates and the list of refusals that can clear move into `pkg/idempotency`. The gateway passes its current behaviour as the defaults, so no gateway behaviour and no specification text changes. Unit tests of each parameter, and the gateway's idempotency tests unchanged and passing. The step depends on 0178.S4, because 0178.S3 and 0178.S4 rework `idempotency.Key`, its validation, the stored routes, and the refusals that are not stored on their branch.

### 0183.S2 lenny-ops records under design C
- status: Planned
- lanes: [specification, code, documentation]
- depends-on: [0183.S1]
- closes: [APICONV-I16 (part), APICONV-I17, APICONV-I18, APICONV-I19, APICONV-I21, APICONV-I22, APICONV-I23, APICONV-I25, APICONV-I26, APICONV-I15 (part)]

OI-1 (the keyed requests), OI-2, OI-3, OI-4, OI-5, OI-6, and OI-10 in `change.md`. First the specification: §25.4 "Idempotency" states the keyed requests, the record scope, the body digest and the `422`, the key limit, the request body limit and the `413`, the responses that are not stored with the refusals that can clear listed, the stored-response limit, the stored headers, the release of a pending claim, the in-flight answer, the settings with their defaults, the table, and an error table without `IDEMPOTENCY_KEY_OWNED_BY_OTHER_CALLER`; §25.5 states that the create and rotate-secret responses are not stored; §16.1 lists the `lenny-ops` metric of skipped stores. Then the code: `lenny-ops` runs the shared middleware inside its authenticated chain over a store of `ops_idempotency_keys`, with its TTL classes, its required-key routes, its `conventions` envelope, and its outage policy; the event-subscription handlers call the no-store gate for credentials; the migration of OI-10; the flags and chart values of the settings; and `pkg/ops/opsidem` is removed. The tests of "Tests" in `change.md` for these sections at the unit, static, component, contract, security, and load_local tiers. Then the "Idempotency" and "The operational event stream" sections of `docs/operator-guide/agent-operability.md`, `docs/reference/error-catalog.md`, `docs/reference/configuration.md`, `docs/reference/metrics.md`, and `docs/operator-guide/security.md`.

The step closes the match, credential, and backup parts of [APICONV-I16](../../issues/APICONV/APICONV-I16-ops-idempotency-replays-the-impersonation-bearer-by-subject.md): once no stored response carries a credential, a backup holds none. It closes the `lenny-ops` size part of [APICONV-I15](../../issues/APICONV/APICONV-I15-stored-idempotency-responses-outlive-what-they-describe-and-have-no-size-limit.md), whose gateway parts stay open.

### 0183.S3 Idempotency through the MCP bridge
- status: Planned
- lanes: [specification, code, documentation]
- depends-on: [0183.S2]
- closes: [OPSMCP-I1, APICONV-I16 (part)]

OI-7 in `change.md`. First the specification: §25.12 states the `idempotencyKey` argument, its forwarding as `Idempotency-Key` on the mapped REST call, `x-lenny-idempotency-key` from the §25.4 classification, and that `/mcp/management` is not a stored path; §25.4 states that `lenny-ops` stores nothing for a tool it proxies to the gateway. Then the bridge in `pkg/ops/opsserver/mcp.go` forwards the argument in `invokeLocal`, the generator in `pkg/ops/mcp` emits the argument and `x-lenny-idempotency-key`, and `/mcp/management` leaves the stored paths. The tests of "Tests" in `change.md` for the bridge at the unit, contract, and security tiers, among them a replay of `admin.start_impersonation` that executes again and writes its audit row, and a token without a tool's scope refused on a retry. Then the "MCP management catalog" section of `docs/operator-guide/agent-operability.md`. The step closes the MCP part of [APICONV-I16](../../issues/APICONV/APICONV-I16-ops-idempotency-replays-the-impersonation-bearer-by-subject.md), the stored impersonation bearer and the replay that skips the bridge's scope gate; APICONV-I16 closes when this step and 0183.S2 have landed.

### 0183.S4 The paths of the ops Ingress
- status: Planned
- lanes: [specification, code, documentation]
- depends-on: []
- closes: [LOPSKRES-I1]

The second paragraph of OI-1 in `change.md`. First the specification: §25.4 "Kubernetes Resources" states that the ops Ingress routes `/v1/` and `/mcp/management` only, and that the invalidate RPC and `/metrics` are reachable inside the cluster only. Then the Ingress template of `charts/lenny`, with a chart test that the rendered Ingress routes neither `/internal/` nor `/metrics`. Then `docs/operator-guide/ingress-and-tls.md`. The step depends on no other step.

### 0183.S5 Erasure, cleanup, and the inventory of the lenny-ops records
- status: Planned
- lanes: [specification, code, documentation]
- depends-on: [0183.S2]
- closes: [ERASE-I10, APICONV-I24, OPSREM-I1 (part)]

OI-8 in `change.md`. First the specification: §12.8 **Storage backends in erasure scope:** gains `ops_idempotency_keys`, deleted by `(tenant_id, subject)` through the `lenny-ops` store, with its step in the **`DeleteByUser` dependency-ordered deletion sequence** and in tenant deletion; §25.4 "Retention" states the cleanup interval setting; §25.4 "Operations Inventory" names the source of `idempotency_in_progress`. Then the `lenny-ops` store's deletion by user and by tenant and the call that reaches it from the erasure job, the reconciler's interval setting in `pkg/ops/opsservice/reconcile.go`, and the store's listing of claims in progress that the inventory reads. The tests of "Tests" in `change.md` for these sections at the unit, component, contract, and integration tiers. Then the erasure text of `docs/operator-guide/security.md` and the setting in `docs/reference/configuration.md`. The step depends on 0183.S2, because a record names no tenant until 0183.S2's table. It closes the `idempotency_in_progress` part of [OPSREM-I1](../../issues/OPSREM/OPSREM-I1-operations-inventory-has-no-source-for-idempotency-and-webhook-delivery-kinds.md); the `webhook_delivery_pending` part stays open.
